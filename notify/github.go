package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Labels the GitHub channel manages.
const (
	// ReadyLabel is the label a person adds to let Satat run an issue (workflows.ReadyLabel).
	ReadyLabel = "ready-for-agent"
	// FailedLabel replaces ReadyLabel when a run fails, so the issue no longer looks queued.
	// Adding ReadyLabel again runs it again.
	FailedLabel = "satat:failed"
)

// GitHub posts a comment on the issue for every event, one after another, so everyone watching
// the issue is notified at each state. When a run fails it swaps ReadyLabel for FailedLabel; when
// a run starts it removes FailedLabel.
//
// Token needs Issues: write on each repo.
type GitHub struct {
	Token string
	// APIBase is the GitHub API root; overridden in tests. HTTP defaults to a 30 s client.
	APIBase string
	HTTP    *http.Client

	mu     sync.Mutex
	issues map[string]*sync.Mutex
}

// Notify implements Notifier.
func (g *GitHub) Notify(ctx context.Context, ev Event) error {
	if ev.Repo == "" || ev.Issue <= 0 {
		return nil
	}
	// One issue's comments go out in the order their events came, even from different goroutines.
	key := fmt.Sprintf("%s#%d", strings.ToLower(ev.Repo), ev.Issue)
	lock := g.issueLock(key)
	lock.Lock()
	defer lock.Unlock()

	var errs []error
	if err := g.api(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", ev.Repo, ev.Issue), map[string]string{"body": CommentBody(ev)}, nil); err != nil {
		errs = append(errs, fmt.Errorf("comment on %s: %w", key, err))
	}
	switch ev.State {
	case Started:
		if err := g.removeLabel(ctx, ev.Repo, ev.Issue, FailedLabel); err != nil {
			errs = append(errs, err)
		}
	case Failed:
		if err := g.removeLabel(ctx, ev.Repo, ev.Issue, ReadyLabel); err != nil {
			errs = append(errs, err)
		}
		if err := g.api(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", ev.Repo, ev.Issue), map[string][]string{"labels": {FailedLabel}}, nil); err != nil {
			errs = append(errs, fmt.Errorf("add %s to %s: %w", FailedLabel, key, err))
		}
	}
	return errors.Join(errs...)
}

func (g *GitHub) issueLock(key string) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.issues == nil {
		g.issues = map[string]*sync.Mutex{}
	}
	if g.issues[key] == nil {
		g.issues[key] = &sync.Mutex{}
	}
	return g.issues[key]
}

func (g *GitHub) removeLabel(ctx context.Context, repo string, issue int, label string) error {
	err := g.api(ctx, http.MethodDelete, fmt.Sprintf("/repos/%s/issues/%d/labels/%s", repo, issue, url.PathEscape(label)), nil, nil)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusNotFound {
		return nil // not on the issue
	}
	if err != nil {
		return fmt.Errorf("remove %s from %s#%d: %w", label, repo, issue, err)
	}
	return nil
}

// maxDetail keeps a comment well under GitHub's 65536-character limit.
const maxDetail = 20000

// CommentBody is the comment for one event (ADR 0036): the headline in bold, the body, the link,
// the full output collapsed, and the run in small print.
func CommentBody(ev Event) string {
	m := Compose(ev)
	var b strings.Builder
	fmt.Fprintf(&b, "**[%s] %s**", m.Level, m.Headline)
	if m.Body != "" {
		fmt.Fprintf(&b, "\n\n%s", m.Body)
	}
	if m.Link != "" {
		fmt.Fprintf(&b, "\n\n[%s](%s)", m.LinkText, m.Link)
		if m.LinkText != "Run log" && ev.RunURL != "" {
			fmt.Fprintf(&b, " · [Run log](%s)", ev.RunURL)
		}
	}
	if d := ev.Detail; d != "" {
		if len(d) > maxDetail {
			d = d[:maxDetail] + "\n… (cut)"
		}
		fmt.Fprintf(&b, "\n\n<details><summary>Details</summary>\n\n```\n%s\n```\n</details>", strings.ReplaceAll(d, "```", "'''"))
	}
	if ev.Run != "" {
		fmt.Fprintf(&b, "\n\n<sub>Satat run `%s` · %s</sub>", shortRun(ev.Run), ev.State)
	}
	return b.String()
}

type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

func (g *GitHub) api(ctx context.Context, method, path string, body, into any) error {
	base := g.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	hc := g.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, rd)
	if err != nil {
		return err
	}
	if g.Token != "" {
		r.Header.Set("Authorization", "Bearer "+g.Token)
	}
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &httpError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(into)
}
