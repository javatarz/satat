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

// statusMarker starts Satat's status comment, so the channel finds its own comment again.
const statusMarker = "<!-- satat-status -->"

// GitHub keeps one status comment on each issue, edited in place with a row per event, so a
// person sees every state of every run in one place without a comment per state. When a run
// fails it swaps ReadyLabel for FailedLabel; when a run starts it removes FailedLabel.
//
// Token needs Issues: write on each repo. Only Satat's own comment is edited: one written by
// anyone else is never touched, even if it carries the marker.
type GitHub struct {
	Token string
	// APIBase is the GitHub API root; overridden in tests. HTTP defaults to a 30 s client.
	APIBase string
	HTTP    *http.Client

	mu       sync.Mutex
	issues   map[string]*sync.Mutex
	comments map[string]int64
	login    string
}

// Notify implements Notifier.
func (g *GitHub) Notify(ctx context.Context, ev Event) error {
	if ev.Repo == "" || ev.Issue <= 0 {
		return nil
	}
	key := fmt.Sprintf("%s#%d", strings.ToLower(ev.Repo), ev.Issue)
	lock := g.issueLock(key)
	lock.Lock()
	defer lock.Unlock()

	var errs []error
	if err := g.status(ctx, key, ev); err != nil {
		errs = append(errs, fmt.Errorf("status comment on %s: %w", key, err))
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
		g.comments = map[string]int64{}
	}
	if g.issues[key] == nil {
		g.issues[key] = &sync.Mutex{}
	}
	return g.issues[key]
}

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// status appends a row for ev to the issue's status comment, creating the comment if needed.
func (g *GitHub) status(ctx context.Context, key string, ev Event) error {
	id, body, err := g.findStatus(ctx, key, ev.Repo, ev.Issue)
	if err != nil {
		return err
	}
	if id == 0 {
		var c comment
		if err := g.api(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", ev.Repo, ev.Issue), map[string]string{"body": StatusBody("", ev)}, &c); err != nil {
			return err
		}
		g.setComment(key, c.ID)
		return nil
	}
	err = g.api(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/comments/%d", ev.Repo, id), map[string]string{"body": StatusBody(body, ev)}, nil)
	var he *httpError
	if errors.As(err, &he) && he.Status == http.StatusNotFound {
		// Deleted by someone since: start a new one.
		g.setComment(key, 0)
		return g.status(ctx, key, ev)
	}
	return err
}

func (g *GitHub) setComment(key string, id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.comments[key] = id
}

// findStatus returns Satat's status comment on the issue, or 0 if there is none. It reads the
// comment each time, so rows a restarted worker wrote are kept.
func (g *GitHub) findStatus(ctx context.Context, key, repo string, issue int) (int64, string, error) {
	login, err := g.viewer(ctx)
	if err != nil {
		return 0, "", err
	}
	g.mu.Lock()
	id := g.comments[key]
	g.mu.Unlock()
	if id != 0 {
		var c comment
		err := g.api(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/comments/%d", repo, id), nil, &c)
		if err == nil {
			return c.ID, c.Body, nil
		}
		g.setComment(key, 0)
	}
	for page := 1; ; page++ {
		var cs []comment
		q := url.Values{"per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if err := g.api(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/comments?%s", repo, issue, q.Encode()), nil, &cs); err != nil {
			return 0, "", err
		}
		for _, c := range cs {
			if strings.HasPrefix(c.Body, statusMarker) && strings.EqualFold(c.User.Login, login) {
				g.setComment(key, c.ID)
				return c.ID, c.Body, nil
			}
		}
		if len(cs) < 100 {
			return 0, "", nil
		}
	}
}

// viewer is the token's own login, so the channel edits only its own comment.
func (g *GitHub) viewer(ctx context.Context) (string, error) {
	g.mu.Lock()
	login := g.login
	g.mu.Unlock()
	if login != "" {
		return login, nil
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := g.api(ctx, http.MethodGet, "/user", nil, &u); err != nil {
		return "", fmt.Errorf("who is the notify token: %w", err)
	}
	g.mu.Lock()
	g.login = u.Login
	g.mu.Unlock()
	return u.Login, nil
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

// statusHeader is the top of a new status comment.
const statusHeader = statusMarker + "\n**Satat status.** Satat edits this comment as its runs on this issue move along, one row per state. " +
	"If a run fails, Satat swaps `" + ReadyLabel + "` for `" + FailedLabel + "`; add `" + ReadyLabel + "` again to run it again.\n\n" +
	"| Time (UTC) | Run | State | Detail |\n|---|---|---|---|\n"

// maxComment keeps the comment under GitHub's 65536-character limit; the oldest rows go first.
const maxComment = 60000

// StatusBody is the status comment after adding ev's row to body (empty for a new comment).
func StatusBody(body string, ev Event) string {
	if !strings.HasPrefix(body, statusMarker) {
		body = statusHeader
	}
	body = strings.TrimRight(body, "\n") + "\n" + row(ev) + "\n"
	for len(body) > maxComment {
		head, rows, ok := strings.Cut(body, "|---|---|---|---|\n")
		if !ok {
			break
		}
		_, rest, ok := strings.Cut(rows, "\n")
		if !ok {
			break
		}
		body = head + "|---|---|---|---|\n" + rest
	}
	return body
}

func row(ev Event) string {
	run := "—"
	if ev.Run != "" {
		run = "`" + ev.Run
		if len(ev.Run) > 8 {
			run = "`" + ev.Run[:8]
		}
		run += "`"
	}
	state := "**" + string(ev.State) + "**"
	if ev.Attempt > 0 && ev.State == Started {
		state += fmt.Sprintf(" (retry %d)", ev.Attempt)
	}
	var detail []string
	switch {
	case ev.PRURL != "":
		detail = append(detail, ev.PRURL)
	case ev.Source != "":
		detail = append(detail, "by the "+ev.Source)
	}
	if ev.Reason != "" {
		detail = append(detail, cell(ev.Reason))
	}
	d := strings.Join(detail, " · ")
	if ev.Detail != "" {
		d += " <details><summary>more</summary><pre>" + cell(htmlEscape(ev.Detail)) + "</pre></details>"
	}
	return fmt.Sprintf("| %s | %s | %s | %s |", ev.At.UTC().Format("2006-01-02 15:04:05"), run, state, d)
}

// cell keeps text inside one table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", "<br>")
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
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
