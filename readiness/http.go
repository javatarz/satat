package readiness

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// HTTP is the real GitHub: the REST API, GET only. Token is the push token, because the check is
// about what that token can do and reach.
type HTTP struct {
	Token string
	// APIBase is the GitHub API root; overridden in tests. Client defaults to a 30 s client.
	APIBase string
	Client  *http.Client
}

const (
	perPage  = 100
	maxPages = 10
	maxBody  = 4 << 20
)

var repoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

// ValidRepo reports whether s is a plain owner/name.
func ValidRepo(s string) bool { return repoRE.MatchString(s) && !strings.Contains(s, "..") }

// get makes one GET and decodes the JSON answer into into. It returns the response headers.
func (h HTTP) get(ctx context.Context, path string, into any) (http.Header, error) {
	base := h.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	c := h.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+h.Token)
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.Do(r)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return resp.Header, fmt.Errorf("GET %s: %w", path, ErrNotFound)
	case resp.StatusCode == http.StatusForbidden && !rateLimited(resp.Header, data):
		return resp.Header, fmt.Errorf("GET %s: %w", path, ErrForbidden)
	case resp.StatusCode != http.StatusOK:
		return resp.Header, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	if into != nil {
		if err := json.Unmarshal(data, into); err != nil {
			return resp.Header, fmt.Errorf("GET %s: %w", path, err)
		}
	}
	return resp.Header, nil
}

func checkRepo(repo string) error {
	if !ValidRepo(repo) {
		return fmt.Errorf("not owner/name: %q", repo)
	}
	return nil
}

// Viewer reads GET /user. A 401 or 403 here means the token is bad, which is an error, not a
// finding.
func (h HTTP) Viewer(ctx context.Context) (Viewer, error) {
	var u struct {
		Login string `json:"login"`
	}
	hdr, err := h.get(ctx, "/user", &u)
	if err != nil {
		return Viewer{}, err
	}
	v := Viewer{Login: u.Login}
	if _, ok := hdr["X-Oauth-Scopes"]; ok {
		v.ScopesKnown = true
		for _, s := range strings.Split(hdr.Get("X-OAuth-Scopes"), ",") {
			if s = strings.TrimSpace(s); s != "" {
				v.Scopes = append(v.Scopes, s)
			}
		}
	}
	return v, nil
}

func (h HTTP) Repo(ctx context.Context, repo string) (Repo, error) {
	if err := checkRepo(repo); err != nil {
		return Repo{}, err
	}
	var r struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		Archived      bool   `json:"archived"`
		Permissions   struct {
			Admin, Maintain, Push, Pull bool
		} `json:"permissions"`
	}
	if _, err := h.get(ctx, "/repos/"+repo, &r); err != nil {
		return Repo{}, err
	}
	p := r.Permissions
	return Repo{FullName: r.FullName, DefaultBranch: r.DefaultBranch, Archived: r.Archived,
		Admin: p.Admin, Maintain: p.Maintain, Push: p.Push, Pull: p.Pull}, nil
}

func (h HTTP) File(ctx context.Context, repo, path, ref string) ([]byte, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	var f struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	q := url.Values{"ref": {ref}}
	if _, err := h.get(ctx, "/repos/"+repo+"/contents/"+path+"?"+q.Encode(), &f); err != nil {
		return nil, err
	}
	if f.Type != "file" || f.Encoding != "base64" {
		return nil, fmt.Errorf("%s is not a plain file (type %q): %w", path, f.Type, ErrNotFound)
	}
	return decodeBase64(f.Content)
}

func (h HTTP) BranchRules(ctx context.Context, repo, branch string) ([]Rule, error) {
	if err := checkRepo(repo); err != nil {
		return nil, err
	}
	var rules []struct {
		Type              string `json:"type"`
		RulesetID         int    `json:"ruleset_id"`
		RulesetSourceType string `json:"ruleset_source_type"`
	}
	if _, err := h.get(ctx, "/repos/"+repo+"/rules/branches/"+url.PathEscape(branch)+"?per_page=100", &rules); err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		out = append(out, Rule{Type: r.Type, RulesetID: r.RulesetID})
	}
	return out, nil
}

func (h HTTP) Ruleset(ctx context.Context, repo string, id int) (Ruleset, error) {
	if err := checkRepo(repo); err != nil {
		return Ruleset{}, err
	}
	var r struct {
		ID                   int    `json:"id"`
		Name                 string `json:"name"`
		Enforcement          string `json:"enforcement"`
		CurrentUserCanBypass string `json:"current_user_can_bypass"`
	}
	if _, err := h.get(ctx, fmt.Sprintf("/repos/%s/rulesets/%d?includes_parents=true", repo, id), &r); err != nil {
		return Ruleset{}, err
	}
	return Ruleset{ID: r.ID, Name: r.Name, Enforcement: r.Enforcement, CurrentUserCanBypass: r.CurrentUserCanBypass}, nil
}

func (h HTTP) LabeledBy(ctx context.Context, repo string, issue int, label string) (string, bool, error) {
	if err := checkRepo(repo); err != nil {
		return "", false, err
	}
	login, found := "", false
	for page := 1; page <= maxPages; page++ {
		var evs []struct {
			Event string `json:"event"`
			Label struct {
				Name string `json:"name"`
			} `json:"label"`
			Actor struct {
				Login string `json:"login"`
			} `json:"actor"`
		}
		if _, err := h.get(ctx, fmt.Sprintf("/repos/%s/issues/%d/events?per_page=%d&page=%d", repo, issue, perPage, page), &evs); err != nil {
			return "", false, err
		}
		for _, e := range evs { // oldest first, so the last match is the latest labelling
			if e.Event == "labeled" && strings.EqualFold(e.Label.Name, label) {
				login, found = e.Actor.Login, true
			}
		}
		if len(evs) < perPage {
			return login, found, nil
		}
	}
	// More than maxPages of events: the latest labelling may be past what was read.
	return "", false, nil
}

func (h HTTP) AccessibleRepos(ctx context.Context) ([]string, bool, error) {
	var out []string
	for page := 1; page <= maxPages; page++ {
		var rs []struct {
			FullName string `json:"full_name"`
		}
		path := fmt.Sprintf("/user/repos?affiliation=owner,collaborator,organization_member&per_page=%d&page=%d", perPage, page)
		if _, err := h.get(ctx, path, &rs); err != nil {
			return nil, false, err
		}
		for _, r := range rs {
			out = append(out, r.FullName)
		}
		if len(rs) < perPage {
			return out, false, nil
		}
	}
	return out, true, nil
}

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(s, "\n", ""))
}

// rateLimited recognises GitHub's primary and secondary rate limits, which answer 403: a 403 that
// is a limit is a plain error to retry, never a finding that would relabel the issue.
func rateLimited(h http.Header, body []byte) bool {
	return h.Get("X-RateLimit-Remaining") == "0" || h.Get("Retry-After") != "" ||
		strings.Contains(strings.ToLower(string(body)), "rate limit")
}

func (h HTTP) BranchProtected(ctx context.Context, repo, branch string) (bool, error) {
	if err := checkRepo(repo); err != nil {
		return false, err
	}
	var b struct {
		Protected bool `json:"protected"`
	}
	if _, err := h.get(ctx, "/repos/"+repo+"/branches/"+url.PathEscape(branch), &b); err != nil {
		return false, err
	}
	return b.Protected, nil
}
