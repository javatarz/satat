// Package eval grades what Satat produced for one issue against a case: checks a machine can run,
// with tests the agent never saw. It is a quick harness for the prototype, not a benchmark: one
// case, deterministic checks, no model judging yet.
package eval

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/runner"
)

// Case is one issue and how to judge the change made for it.
type Case struct {
	Name   string `yaml:"name"`
	Repo   string `yaml:"repo"`
	Base   string `yaml:"base"`
	Issue  int    `yaml:"issue"`
	Checks struct {
		Tests    *TestsCheck    `yaml:"tests"`
		Scope    *ScopeCheck    `yaml:"scope"`
		Commits  CommitsCheck   `yaml:"commits"`
		RanTests *RanTestsCheck `yaml:"ran_tests"`
	} `yaml:"checks"`

	Dir string `yaml:"-"`
}

// TestsCheck runs a command over the change plus held-out files, in a lane with no network.
type TestsCheck struct {
	Lane    string   `yaml:"lane"`
	Command []string `yaml:"command"`
	// Hidden files live beside case.yaml and are copied to the repo root before the run. The
	// agent never sees them.
	Hidden []string `yaml:"hidden"`
	// RequireOutput lists strings that must appear in the output (for example the held-out test
	// class names), so a runner that silently skips the hidden tests cannot pass.
	RequireOutput []string `yaml:"require_output"`
}

// ScopeCheck keeps the change to the files the issue is about.
type ScopeCheck struct {
	AllowedPaths []string `yaml:"allowed_paths"`
	MaxFiles     int      `yaml:"max_files"`
}

// CommitsCheck bounds the history. Subjects must also follow the repo's stated style: at most 50
// characters, capitalised, no trailing period.
type CommitsCheck struct {
	Max int `yaml:"max"`
}

// LoadCase reads <dir>/case.yaml.
func LoadCase(dir string) (Case, error) {
	var c Case
	b, err := os.ReadFile(filepath.Join(dir, "case.yaml"))
	if err != nil {
		return c, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", dir, err)
	}
	c.Dir = dir
	switch {
	case c.Name == "" || c.Repo == "" || c.Base == "" || c.Issue <= 0:
		return c, fmt.Errorf("%s: name, repo, base and issue are required", dir)
	case c.Checks.Tests != nil && (c.Checks.Tests.Lane == "" || len(c.Checks.Tests.Command) == 0):
		return c, fmt.Errorf("%s: tests needs a lane and a command", dir)
	case c.Checks.RanTests != nil && len(c.Checks.RanTests.Commands) == 0:
		return c, fmt.Errorf("%s: ran_tests needs at least one command pattern", dir)
	}
	if c.Checks.RanTests != nil {
		if _, err := compileAll(c.Checks.RanTests.Commands); err != nil {
			return c, fmt.Errorf("%s: %w", dir, err)
		}
	}
	if c.Checks.Tests != nil {
		for _, h := range c.Checks.Tests.Hidden {
			if h != filepath.Base(h) || strings.HasPrefix(h, ".") {
				return c, fmt.Errorf("%s: hidden file %q must be a plain file name beside case.yaml", dir, h)
			}
		}
	}
	return c, nil
}

// Subject is what a run produced.
type Subject struct {
	Run      string
	ExitCode int
	Runner   *runner.Result
	Bundle   []byte
	// Transcript is the agent's transcript.jsonl (stream-json); empty when the run left none.
	Transcript []byte
	// PR is set when the run published; the PR checks are skipped otherwise.
	PR *pusher.Outcome
}

// Check is one graded criterion.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Skip   bool   `json:"skip,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Result is the grade for one subject.
type Result struct {
	Case   string  `json:"case"`
	Run    string  `json:"run"`
	Checks []Check `json:"checks"`
}

// Passed is true when no check failed. Skipped checks do not count against it.
func (r Result) Passed() bool {
	for _, c := range r.Checks {
		if !c.Pass && !c.Skip {
			return false
		}
	}
	return len(r.Checks) > 0
}

// Checker runs a command over a set of files in an isolated lane.
type Checker interface {
	Run(ctx context.Context, lane string, files map[string][]byte, command []string) (CheckRun, error)
}

type CheckRun struct {
	ExitCode int
	TimedOut bool
	Output   string
}

// Grader holds what grading needs. Token is optional: it lets a private repo be cloned and the
// PR be read back.
type Grader struct {
	Git      string
	CloneURL func(repo string) string
	APIBase  string
	HTTP     *http.Client
	Token    string
	Checker  Checker
	Work     string
}

const (
	maxTreeBytes = 4 << 20
	tokenVar     = "SATAT_EVAL_TOKEN"
)

var subjectRE = regexp.MustCompile(`^[A-Z]`)

// Grade runs every check the case defines.
func (g *Grader) Grade(ctx context.Context, c Case, s Subject) (Result, error) {
	if g.Git == "" {
		g.Git = "git"
	}
	if g.CloneURL == nil {
		g.CloneURL = func(repo string) string { return "https://github.com/" + repo + ".git" }
	}
	if g.APIBase == "" {
		g.APIBase = "https://api.github.com"
	}
	if g.HTTP == nil {
		g.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	res := Result{Case: c.Name, Run: s.Run}
	add := func(name string, pass bool, detail string) {
		res.Checks = append(res.Checks, Check{Name: name, Pass: pass, Detail: detail})
	}
	skip := func(name, why string) {
		res.Checks = append(res.Checks, Check{Name: name, Skip: true, Detail: why})
	}

	switch {
	case s.Runner == nil:
		add("run", false, "the step left no result.json")
	case s.ExitCode != 0:
		add("run", false, fmt.Sprintf("step exit code %d: %s", s.ExitCode, s.Runner.Error))
	case s.Runner.Status != "changes":
		add("run", false, fmt.Sprintf("status %q: %s", s.Runner.Status, s.Runner.Error))
	default:
		add("run", true, fmt.Sprintf("%d commit(s) on %s", s.Runner.Commits, s.Runner.Branch))
	}
	if !res.Checks[0].Pass {
		for _, n := range []string{"scope", "commits", "ran-tests", "tests", "pr"} {
			skip(n, "no change to grade")
		}
		return res, nil
	}

	dir, err := os.MkdirTemp(g.Work, "satat-eval-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(dir)
	w, err := g.fetch(ctx, dir, c, s)
	if err != nil {
		add("change", false, err.Error())
		return res, nil
	}

	if sc := c.Checks.Scope; sc != nil {
		names, err := w.names(ctx)
		if err != nil {
			return res, err
		}
		add("scope", scopeOK(sc, names) == "", orDefault(scopeOK(sc, names), fmt.Sprintf("%d file(s): %s", len(names), strings.Join(names, ", "))))
	}

	subjects, err := w.subjects(ctx)
	if err != nil {
		return res, err
	}
	add("commits", commitsOK(c.Checks.Commits, subjects) == "", orDefault(commitsOK(c.Checks.Commits, subjects), fmt.Sprintf("%d commit(s), subjects fit the repo style", len(subjects))))

	if rc := c.Checks.RanTests; rc != nil {
		pats, err := compileAll(rc.Commands)
		switch {
		case err != nil:
			return res, err
		case len(s.Transcript) == 0:
			skip("ran-tests", "no transcript to inspect")
		default:
			ok, detail := ranTests(s.Transcript, pats)
			add("ran-tests", ok, detail)
		}
	}

	if tc := c.Checks.Tests; tc != nil {
		files, err := w.tree(ctx)
		if err != nil {
			return res, err
		}
		for _, h := range tc.Hidden {
			b, err := os.ReadFile(filepath.Join(c.Dir, h))
			if err != nil {
				return res, fmt.Errorf("hidden file: %w", err)
			}
			files[h] = b
		}
		run, err := g.Checker.Run(ctx, tc.Lane, files, tc.Command)
		switch {
		case err != nil:
			return res, fmt.Errorf("run tests: %w", err)
		case run.TimedOut:
			add("tests", false, "timed out")
		case run.ExitCode != 0:
			add("tests", false, fmt.Sprintf("exit %d:\n%s", run.ExitCode, tail(run.Output, 1500)))
		default:
			var missing []string
			for _, want := range tc.RequireOutput {
				if !strings.Contains(run.Output, want) {
					missing = append(missing, want)
				}
			}
			if len(missing) > 0 {
				add("tests", false, "passed, but the output never mentions "+strings.Join(missing, ", ")+": the held-out tests did not run")
			} else {
				add("tests", true, lastLine(run.Output))
			}
		}
	}

	if s.PR == nil {
		skip("pr", "not published")
	} else if g.Token == "" {
		skip("pr", "no token to read the PR back")
	} else {
		detail, err := g.checkPR(ctx, c, s)
		if err != nil {
			return res, err
		}
		add("pr", detail == "", orDefault(detail, fmt.Sprintf("draft #%d on %s with the Satat-Run trailer", s.PR.PRNumber, s.PR.Branch)))
	}
	return res, nil
}

func orDefault(s, d string) string {
	if s != "" {
		return s
	}
	return d
}

func scopeOK(sc *ScopeCheck, names []string) string {
	var bad []string
	for _, n := range names {
		ok := false
		for _, a := range sc.AllowedPaths {
			if m, _ := path.Match(a, n); m || a == n {
				ok = true
			}
		}
		if !ok {
			bad = append(bad, n)
		}
	}
	switch {
	case len(bad) > 0:
		return "outside the issue's scope: " + strings.Join(bad, ", ")
	case sc.MaxFiles > 0 && len(names) > sc.MaxFiles:
		return fmt.Sprintf("%d files changed, limit %d", len(names), sc.MaxFiles)
	}
	return ""
}

func commitsOK(cc CommitsCheck, subjects []string) string {
	var problems []string
	if cc.Max > 0 && len(subjects) > cc.Max {
		problems = append(problems, fmt.Sprintf("%d commits, limit %d", len(subjects), cc.Max))
	}
	for _, s := range subjects {
		switch {
		case len([]rune(s)) > 50:
			problems = append(problems, fmt.Sprintf("subject over 50 characters: %q", s))
		case !subjectRE.MatchString(s):
			problems = append(problems, fmt.Sprintf("subject not capitalised: %q", s))
		case strings.HasSuffix(s, "."):
			problems = append(problems, fmt.Sprintf("subject ends with a period: %q", s))
		case strings.IndexFunc(s, unicode.IsLetter) < 0:
			problems = append(problems, fmt.Sprintf("subject has no words: %q", s))
		}
	}
	return strings.Join(problems, "; ")
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// worktree is a clone with the agent's commits fetched into it.
type worktree struct {
	g          *Grader
	dir        string
	base, head string
}

func (g *Grader) fetch(ctx context.Context, dir string, c Case, s Subject) (*worktree, error) {
	repoDir := filepath.Join(dir, "repo")
	bundle := filepath.Join(dir, "changes.bundle")
	if len(s.Bundle) == 0 {
		return nil, errors.New("the run left no bundle")
	}
	if err := os.WriteFile(bundle, s.Bundle, 0o600); err != nil {
		return nil, err
	}
	w := &worktree{g: g, dir: repoDir, base: s.Runner.BaseSHA, head: s.Runner.HeadSHA}
	if _, err := g.run(ctx, dir, true, "clone", "--quiet", "--no-checkout", "--depth", "100", "--branch", c.Base, "--", g.CloneURL(c.Repo), repoDir); err != nil {
		return nil, fmt.Errorf("clone %s: %w", c.Repo, err)
	}
	if _, err := g.run(ctx, repoDir, false, "fetch", "--quiet", bundle, "refs/heads/*:refs/satat-incoming/*"); err != nil {
		return nil, fmt.Errorf("the bundle does not apply to %s: %w", c.Base, err)
	}
	got, err := g.run(ctx, repoDir, false, "rev-parse", "--verify", w.head+"^{commit}")
	if err != nil || got != w.head {
		return nil, fmt.Errorf("the bundle does not contain head %s", w.head)
	}
	return w, nil
}

func (w *worktree) names(ctx context.Context) ([]string, error) {
	out, err := w.g.run(ctx, w.dir, false, "diff", "--name-only", "--no-renames", "-z", w.base, w.head)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, n := range strings.Split(out, "\x00") {
		if n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

func (w *worktree) subjects(ctx context.Context) ([]string, error) {
	out, err := w.g.run(ctx, w.dir, false, "log", "--reverse", "--format=%s", w.base+".."+w.head)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// tree returns the files at head, from git archive, bounded in size.
func (w *worktree) tree(ctx context.Context) (map[string][]byte, error) {
	cmd := exec.CommandContext(ctx, w.g.Git, "archive", "--format=tar", w.head)
	cmd.Dir = w.dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git archive: %w", err)
	}
	files := map[string][]byte{}
	var total int64
	tr := tar.NewReader(bytes.NewReader(out))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(h.Name)
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			continue
		}
		total += h.Size
		if total > maxTreeBytes {
			return nil, fmt.Errorf("the repo is over %d bytes; this harness only handles small repos", maxTreeBytes)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files[name] = b
	}
}

func (g *Grader) checkPR(ctx context.Context, c Case, s Subject) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/pulls/%d", strings.TrimRight(g.APIBase, "/"), c.Repo, s.PR.PRNumber), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("read PR #%d: %s", s.PR.PRNumber, resp.Status)
	}
	var pr struct {
		Draft  bool   `json:"draft"`
		Merged bool   `json:"merged"`
		Body   string `json:"body"`
		Head   struct{ Ref string }
		Base   struct{ Ref string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", err
	}
	var problems []string
	if !pr.Draft {
		problems = append(problems, "not a draft")
	}
	if pr.Merged {
		problems = append(problems, "already merged")
	}
	if pr.Head.Ref != "satat/"+s.Run {
		problems = append(problems, fmt.Sprintf("head branch %q", pr.Head.Ref))
	}
	if pr.Base.Ref != c.Base {
		problems = append(problems, fmt.Sprintf("base branch %q", pr.Base.Ref))
	}
	if !strings.Contains(pr.Body, "Satat-Run: "+s.Run) {
		problems = append(problems, "no Satat-Run trailer")
	}
	return strings.Join(problems, "; "), nil
}

func (g *Grader) run(ctx context.Context, dir string, auth bool, args ...string) (string, error) {
	if auth && g.Token != "" {
		helper := `!f() { echo username=x-access-token; echo "password=$` + tokenVar + `"; }; f`
		args = append([]string{"-c", "credential.helper=" + helper}, args...)
	}
	cmd := exec.CommandContext(ctx, g.Git, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if auth && g.Token != "" {
		cmd.Env = append(cmd.Env, tokenVar+"="+g.Token)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if g.Token != "" {
			msg = strings.ReplaceAll(msg, g.Token, "***")
		}
		return "", fmt.Errorf("git: %w: %s", err, msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// LauncherChecker runs checks through a launcher lane built for it: a language image with no
// network that reads {"files": {path: base64}, "command": [...]} from the step input.
type LauncherChecker struct {
	Launcher launcher.Launcher
	RunID    string
}

// Run implements Checker.
func (l LauncherChecker) Run(ctx context.Context, lane string, files map[string][]byte, command []string) (CheckRun, error) {
	enc := make(map[string]string, len(files))
	for name, b := range files {
		enc[name] = base64.StdEncoding.EncodeToString(b)
	}
	input, err := json.Marshal(map[string]any{"files": enc, "command": command})
	if err != nil {
		return CheckRun{}, err
	}
	res, err := l.Launcher.Launch(ctx, launcher.Step{Run: l.RunID, Name: "check", Lane: lane, Input: input})
	if err != nil {
		return CheckRun{}, err
	}
	return CheckRun{ExitCode: res.ExitCode, TimedOut: res.TimedOut || res.OOMKilled, Output: string(res.Stdout) + string(res.Stderr)}, nil
}
