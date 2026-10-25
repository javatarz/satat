package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ReadyIssue is an open issue that carries the ready label.
type ReadyIssue struct {
	Number int
	Title  string
	Body   string
}

// GitHub lists issues and pull requests with the REST API. It only reads, so Token is the
// read-only token (SATAT_GITHUB_TOKEN). A fine-grained token needs Issues: read and Pull requests:
// read on each polled repo; without them a private repo answers 404.
type GitHub struct {
	Token string
	// APIBase is the GitHub API root; overridden in tests. HTTP defaults to a 30 s client.
	APIBase string
	HTTP    *http.Client
}

const (
	perPage = 100
	// maxPages bounds a listing at 1000 items, so a huge backlog cannot make one poll unbounded.
	maxPages = 10
	maxBody  = 8 << 20
)

// ReadyIssues returns the open issues in repo (owner/name) labelled label, oldest first. Pull
// requests are left out: GitHub's issues endpoint lists them too.
func (g GitHub) ReadyIssues(ctx context.Context, repo, label string) ([]ReadyIssue, error) {
	var out []ReadyIssue
	q := url.Values{"state": {"open"}, "labels": {label}, "sort": {"created"}, "direction": {"asc"}}
	err := g.pages(ctx, "/repos/"+repo+"/issues", q, func(raw json.RawMessage) (int, error) {
		var items []struct {
			Number      int             `json:"number"`
			Title       string          `json:"title"`
			Body        *string         `json:"body"`
			PullRequest json.RawMessage `json:"pull_request"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return 0, err
		}
		for _, it := range items {
			if len(it.PullRequest) > 0 && string(it.PullRequest) != "null" {
				continue
			}
			ri := ReadyIssue{Number: it.Number, Title: it.Title}
			if it.Body != nil {
				ri.Body = *it.Body
			}
			out = append(out, ri)
		}
		return len(items), nil
	})
	return out, err
}

// closingRE matches GitHub's closing keywords followed by a same-repo issue reference. Satat's
// own PR bodies say "Resolves #N" (pusher.Body).
var closingRE = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s*:?\s*#(\d+)`)

// OpenPRIssues returns the set of issue numbers that an open pull request in repo says it
// closes, in the body's closing keywords.
func (g GitHub) OpenPRIssues(ctx context.Context, repo string) (map[int]bool, error) {
	set := map[int]bool{}
	q := url.Values{"state": {"open"}}
	err := g.pages(ctx, "/repos/"+repo+"/pulls", q, func(raw json.RawMessage) (int, error) {
		var prs []struct {
			Body *string `json:"body"`
		}
		if err := json.Unmarshal(raw, &prs); err != nil {
			return 0, err
		}
		for _, p := range prs {
			if p.Body == nil {
				continue
			}
			for _, m := range closingRE.FindAllStringSubmatch(*p.Body, -1) {
				if n, err := strconv.Atoi(m[1]); err == nil {
					set[n] = true
				}
			}
		}
		return len(prs), nil
	})
	return set, err
}

// pages GETs path page by page and hands each page to each, which returns how many items it
// held. It stops at a short page. A full page at maxPages is an error: an incomplete list of open
// PRs would let an issue with a PR be queued, and an incomplete list of issues hides some.
func (g GitHub) pages(ctx context.Context, path string, q url.Values, each func(json.RawMessage) (int, error)) error {
	base := g.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	hc := g.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	for page := 1; page <= maxPages; page++ {
		q.Set("per_page", strconv.Itoa(perPage))
		q.Set("page", strconv.Itoa(page))
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path+"?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		if g.Token != "" {
			r.Header.Set("Authorization", "Bearer "+g.Token)
		}
		r.Header.Set("Accept", "application/vnd.github+json")
		r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := hc.Do(r)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			hint := ""
			switch resp.StatusCode {
			case http.StatusForbidden, http.StatusTooManyRequests:
				hint = " (a rate limit, or a token without Issues: read and Pull requests: read)"
			case http.StatusNotFound:
				hint = " (no such repo, or a token without Issues: read and Pull requests: read on a private repo)"
			}
			return fmt.Errorf("GET %s: %s%s: %s", path, resp.Status, hint, strings.TrimSpace(string(body[:min(len(body), 512)])))
		}
		n, err := each(body)
		if err != nil {
			return fmt.Errorf("GET %s: %w", path, err)
		}
		if n < perPage {
			return nil
		}
	}
	return fmt.Errorf("GET %s: more than %d items; refusing to act on a partial list", path, maxPages*perPage)
}
