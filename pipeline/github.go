package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// IssueState is what the worker's readiness check needs to know about an issue right now.
type IssueState struct {
	// Title and Body are the issue's text as GitHub has it now. A run uses these, never the
	// text its trigger carried (#181).
	Title  string
	Body   string
	Labels []string
	// Open is false for a closed issue. PullRequest is true when the number is a PR, not an issue.
	Open        bool
	PullRequest bool
}

// GitHubIssues reads an issue's current state from the GitHub REST API. The worker uses it to
// check an issue is still ready before it runs (workflows.Handler). It only reads, so Token
// should be the read-only token (SATAT_GITHUB_TOKEN), never the pusher's write token. A
// fine-grained token needs Issues: read on the repo; without it a private repo answers 404.
type GitHubIssues struct {
	Token string
	// APIBase is the GitHub API root; overridden in tests. HTTP defaults to a 30 s client.
	APIBase string
	HTTP    *http.Client
}

var issueRepoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

// maxIssueBody bounds how much of the API's answer is read. An issue with a long body is well
// under it.
const maxIssueBody = 1 << 20

// Issue returns the current state of issue number in repo (owner/name).
func (g GitHubIssues) Issue(ctx context.Context, repo string, number int) (IssueState, error) {
	if !issueRepoRE.MatchString(repo) || strings.Contains(repo, "..") || number <= 0 {
		return IssueState{}, fmt.Errorf("not an issue: %q #%d", repo, number)
	}
	base := g.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	hc := g.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/issues/%d", strings.TrimRight(base, "/"), repo, number), nil)
	if err != nil {
		return IssueState{}, err
	}
	if g.Token != "" {
		r.Header.Set("Authorization", "Bearer "+g.Token)
	}
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := hc.Do(r)
	if err != nil {
		return IssueState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		hint := ""
		switch resp.StatusCode {
		case http.StatusForbidden, http.StatusTooManyRequests:
			hint = " (a rate limit, or a token without Issues: read)"
		case http.StatusNotFound:
			hint = " (no such issue, or a token without Issues: read on a private repo)"
		}
		return IssueState{}, fmt.Errorf("GET issue %s#%d: %s%s: %s", repo, number, resp.Status, hint, strings.TrimSpace(string(b)))
	}
	var iss struct {
		Title       string          `json:"title"`
		Body        *string         `json:"body"`
		State       string          `json:"state"`
		PullRequest json.RawMessage `json:"pull_request"`
		Labels      []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxIssueBody)).Decode(&iss); err != nil {
		return IssueState{}, fmt.Errorf("issue %s#%d: %w", repo, number, err)
	}
	st := IssueState{
		Title:       iss.Title,
		Open:        iss.State == "open",
		PullRequest: len(iss.PullRequest) > 0 && string(iss.PullRequest) != "null",
		Labels:      make([]string, 0, len(iss.Labels)),
	}
	if iss.Body != nil {
		st.Body = *iss.Body
	}
	for _, l := range iss.Labels {
		st.Labels = append(st.Labels, l.Name)
	}
	return st, nil
}
