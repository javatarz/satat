// Package pusher turns a finished step's commits into a pushed branch and a draft PR (ADR 0025).
//
// It runs in the trusted worker, never in a step container. The agent only commits locally; this
// package takes the bundle the runner wrote, checks it, and is the only thing that holds a write
// token. It never merges and never force-pushes.
package pusher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Pusher holds the write token and the tools it needs. Zero values are filled by New.
type Pusher struct {
	// Token needs contents:write and pull_requests:write on the one repo.
	Token string
	// Git and Gitleaks are binary names or paths. A missing gitleaks is an error, not a skip.
	Git, Gitleaks string
	// APIBase is the GitHub API root. CloneURL builds the git remote. Both are overridden in tests.
	APIBase  string
	CloneURL func(repo string) string
	HTTP     *http.Client
	// WorkRoot is where per-push scratch clones live. Defaults to the OS temp dir.
	WorkRoot string
	// MaxCommits bounds a push. Defaults to 20.
	MaxCommits int
}

// Request is one finished step to publish.
type Request struct {
	Repo, Base, Run  string
	BaseSHA, HeadSHA string
	Bundle           []byte
	Issue            int
	// Summary is the agent's final message. It is untrusted text and only goes into the PR body.
	Summary string
}

// Outcome says what exists on GitHub afterwards.
type Outcome struct {
	Branch   string
	PRNumber int
	PRURL    string
	// Reused is true when the branch and PR were already there (a re-delivered step).
	Reused bool
}

var (
	repoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
	baseRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]{0,99}$`)
	runRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	shaRE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Refusal is returned when the pusher declines to publish. Nothing is pushed or opened.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return "refusing to publish: " + r.Reason }

func refuse(format string, a ...any) error { return &Refusal{fmt.Sprintf(format, a...)} }

func (p *Pusher) defaults() {
	if p.Git == "" {
		p.Git = "git"
	}
	if p.Gitleaks == "" {
		p.Gitleaks = "gitleaks"
	}
	if p.APIBase == "" {
		p.APIBase = "https://api.github.com"
	}
	if p.CloneURL == nil {
		p.CloneURL = func(repo string) string { return "https://github.com/" + repo + ".git" }
	}
	if p.HTTP == nil {
		p.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if p.MaxCommits == 0 {
		p.MaxCommits = 20
	}
}

// Branch is the only branch name the pusher will create for a run.
func Branch(run string) string { return "satat/" + run }

// Push checks the bundle, pushes satat/<run> and opens (or finds) the draft PR.
func (p *Pusher) Push(ctx context.Context, req Request) (Outcome, error) {
	p.defaults()
	branch := Branch(req.Run)
	out := Outcome{Branch: branch}
	switch {
	case p.Token == "":
		return out, errors.New("no write token configured")
	case !repoRE.MatchString(req.Repo) || strings.Contains(req.Repo, ".."):
		return out, refuse("repo %q is not owner/name", req.Repo)
	case !baseRE.MatchString(req.Base) || strings.Contains(req.Base, ".."):
		return out, refuse("base %q is not a plain branch name", req.Base)
	case !runRE.MatchString(req.Run):
		return out, refuse("run %q is not a simple identifier", req.Run)
	case !shaRE.MatchString(req.BaseSHA) || !shaRE.MatchString(req.HeadSHA):
		return out, refuse("base and head must be full commit SHAs")
	case len(req.Bundle) == 0:
		return out, refuse("there is nothing to publish (empty bundle)")
	}

	dir, err := os.MkdirTemp(p.WorkRoot, "satat-push-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)
	repoDir := filepath.Join(dir, "repo")
	bundle := filepath.Join(dir, "changes.bundle")
	if err := os.WriteFile(bundle, req.Bundle, 0o600); err != nil {
		return out, err
	}

	if err := p.git(ctx, dir, true, "clone", "--quiet", "--no-checkout", "--depth", "100", "--branch", req.Base, "--", p.CloneURL(req.Repo), repoDir); err != nil {
		return out, fmt.Errorf("clone: %w", err)
	}
	// Fetch from the bundle into the one ref name the pusher allows, whatever the bundle calls it.
	if err := p.git(ctx, repoDir, false, "fetch", "--quiet", bundle, "refs/heads/*:refs/satat-incoming/*"); err != nil {
		return out, refuse("the bundle does not apply to %s: %v", req.Base, err)
	}
	refs, err := p.gitOut(ctx, repoDir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/satat-incoming/")
	if err != nil {
		return out, err
	}
	lines := strings.Fields(refs)
	if len(lines) != 2 || lines[1] != req.HeadSHA {
		return out, refuse("the bundle must hold exactly one branch ending at %s, got %q", req.HeadSHA, refs)
	}
	if err := p.git(ctx, repoDir, false, "merge-base", "--is-ancestor", req.BaseSHA, req.HeadSHA); err != nil {
		return out, refuse("head %s does not descend from base %s", req.HeadSHA, req.BaseSHA)
	}
	n, err := p.gitOut(ctx, repoDir, "rev-list", "--count", req.BaseSHA+".."+req.HeadSHA)
	if err != nil {
		return out, err
	}
	var commits int
	fmt.Sscan(n, &commits)
	if commits == 0 || commits > p.MaxCommits {
		return out, refuse("%d commits; must be between 1 and %d", commits, p.MaxCommits)
	}

	// Content checks, on exactly what would be pushed.
	names, err := p.gitOut(ctx, repoDir, "diff", "--name-only", "--no-renames", "-z", req.BaseSHA, req.HeadSHA)
	if err != nil {
		return out, err
	}
	for _, name := range strings.Split(names, "\x00") {
		if isWorkflowPath(name) {
			return out, refuse("the change touches %s; Satat does not edit workflows", name)
		}
	}
	if err := p.scan(ctx, repoDir, req); err != nil {
		return out, err
	}

	// Idempotence: a re-delivered step finds what the first attempt did.
	existing, err := p.remoteBranch(ctx, repoDir, branch)
	if err != nil {
		return out, err
	}
	switch {
	case existing == req.HeadSHA:
		out.Reused = true
	case existing != "":
		return out, refuse("%s already exists on the remote at %s, not %s; not overwriting it", branch, existing, req.HeadSHA)
	default:
		if err := p.git(ctx, repoDir, true, "push", "--quiet", "origin", req.HeadSHA+":refs/heads/"+branch); err != nil {
			return out, fmt.Errorf("push: %w", err)
		}
	}

	title, err := p.title(ctx, repoDir, req)
	if err != nil {
		return out, err
	}
	pr, found, err := p.findPR(ctx, req.Repo, branch)
	if err != nil {
		return out, err
	}
	if found {
		out.PRNumber, out.PRURL, out.Reused = pr.Number, pr.URL, true
		return out, nil
	}
	pr, err = p.openPR(ctx, req, branch, title)
	if err != nil {
		return out, err
	}
	out.PRNumber, out.PRURL = pr.Number, pr.URL
	return out, nil
}

func isWorkflowPath(name string) bool {
	n := strings.ToLower(strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+name)), "/"))
	return n == ".github/workflows" || strings.HasPrefix(n, ".github/workflows/")
}

// scan runs gitleaks over the diff the push would publish.
func (p *Pusher) scan(ctx context.Context, repoDir string, req Request) error {
	diff, err := p.gitOut(ctx, repoDir, "diff", "--no-color", "--unified=0", req.BaseSHA, req.HeadSHA)
	if err != nil {
		return err
	}
	msgs, err := p.gitOut(ctx, repoDir, "log", "--format=%B", req.BaseSHA+".."+req.HeadSHA)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, p.Gitleaks, "stdin", "--no-banner", "--redact", "--exit-code", "7", "--log-level", "error")
	cmd.Stdin = strings.NewReader(diff + "\n" + msgs)
	var stderr bytes.Buffer
	cmd.Stderr, cmd.Stdout = &stderr, &stderr
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee) && ee.ExitCode() == 7:
		return refuse("gitleaks found a possible secret in the change: %s", firstLines(stderr.String(), 12))
	default:
		return fmt.Errorf("gitleaks did not run (%v); not publishing unscanned changes: %s", err, firstLines(stderr.String(), 5))
	}
}

func (p *Pusher) title(ctx context.Context, repoDir string, req Request) (string, error) {
	subjects, err := p.gitOut(ctx, repoDir, "log", "--reverse", "--format=%s", req.BaseSHA+".."+req.HeadSHA)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(subjects, "\n")
	first = strings.TrimSpace(first)
	if first == "" {
		first = fmt.Sprintf("Resolve #%d", req.Issue)
	}
	if len(first) > 200 {
		first = first[:200]
	}
	return first, nil
}

func (p *Pusher) remoteBranch(ctx context.Context, repoDir, branch string) (string, error) {
	out, err := p.gitOutAuth(ctx, repoDir, "ls-remote", "--heads", "origin", "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("ls-remote: %w", err)
	}
	if f := strings.Fields(out); len(f) >= 1 {
		return f[0], nil
	}
	return "", nil
}

type pull struct {
	Number int    `json:"number"`
	URL    string `json:"html_url"`
}

func (p *Pusher) findPR(ctx context.Context, repo, branch string) (pull, bool, error) {
	owner, _, _ := strings.Cut(repo, "/")
	q := url.Values{"head": {owner + ":" + branch}, "state": {"all"}}
	var list []pull
	if err := p.api(ctx, http.MethodGet, "/repos/"+repo+"/pulls?"+q.Encode(), nil, &list); err != nil {
		return pull{}, false, err
	}
	if len(list) == 0 {
		return pull{}, false, nil
	}
	return list[0], true, nil
}

func (p *Pusher) openPR(ctx context.Context, req Request, branch, title string) (pull, error) {
	var pr pull
	err := p.api(ctx, http.MethodPost, "/repos/"+req.Repo+"/pulls", map[string]any{
		"title": title, "head": branch, "base": req.Base, "body": Body(req), "draft": true,
		"maintainer_can_modify": false,
	}, &pr)
	return pr, err
}

// Body is the PR description. The Satat-Run line is how a PR is traced back to its run.
func Body(req Request) string {
	var b strings.Builder
	if req.Issue > 0 {
		fmt.Fprintf(&b, "Resolves #%d.\n\n", req.Issue)
	}
	if s := strings.TrimSpace(req.Summary); s != "" {
		if len(s) > 3000 {
			s = s[:3000] + "\n\n[truncated]"
		}
		b.WriteString("Summary from the agent:\n\n")
		for _, l := range strings.Split(s, "\n") {
			b.WriteString("> " + l + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Opened as a draft by Satat. Review it like any other change; Satat never merges.\n\n")
	fmt.Fprintf(&b, "Satat-Run: %s\n", req.Run)
	return b.String()
}

func (p *Pusher) api(ctx context.Context, method, path string, body, into any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.APIBase, "/")+path, rd)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+p.Token)
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.HTTP.Do(r)
	if err != nil {
		return fmt.Errorf("github %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("github %s %s: %s: %s", method, path, resp.Status, p.scrub(firstLines(string(data), 5)))
	}
	if into != nil {
		return json.Unmarshal(data, into)
	}
	return nil
}

// git runs git. When auth is set the write token is made available to git's credential helper
// through the environment only: it is not in argv, the remote URL or .git/config.
func (p *Pusher) git(ctx context.Context, dir string, auth bool, args ...string) error {
	_, err := p.run(ctx, dir, auth, args...)
	return err
}

func (p *Pusher) gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	return p.run(ctx, dir, false, args...)
}

func (p *Pusher) gitOutAuth(ctx context.Context, dir string, args ...string) (string, error) {
	return p.run(ctx, dir, true, args...)
}

const tokenVar = "SATAT_PUSH_TOKEN"

func (p *Pusher) run(ctx context.Context, dir string, auth bool, args ...string) (string, error) {
	if auth {
		helper := `!f() { echo username=x-access-token; echo "password=$` + tokenVar + `"; }; f`
		args = append([]string{"-c", "credential.helper=" + helper}, args...)
	}
	cmd := exec.CommandContext(ctx, p.Git, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if auth {
		cmd.Env = append(cmd.Env, tokenVar+"="+p.Token)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		name := args[0]
		for i, a := range args {
			if a == "-c" || strings.HasPrefix(a, "credential.helper=") {
				continue
			}
			if i > 0 && args[i-1] == "-c" {
				continue
			}
			name = a
			break
		}
		return "", fmt.Errorf("git %s: %w: %s", name, err, p.scrub(firstLines(stderr.String(), 5)))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func (p *Pusher) scrub(s string) string {
	if p.Token != "" {
		s = strings.ReplaceAll(s, p.Token, "***")
	}
	return s
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
