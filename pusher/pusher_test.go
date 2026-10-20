package pusher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeGitHub records the API calls the pusher makes and answers the two it needs.
type fakeGitHub struct {
	mu       sync.Mutex
	srv      *httptest.Server
	existing []pull           // returned by the PR list call
	posted   []map[string]any // bodies of PR creates
	auths    []string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls"):
			json.NewEncoder(w).Encode(f.existing)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			f.posted = append(f.posted, b)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(pull{Number: 2, URL: "https://github.com/acme/widgets/pull/2"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type fixture struct {
	t       *testing.T
	root    string
	remote  string // bare repo standing in for GitHub
	clone   string // the "agent's" working clone
	baseSHA string
	gh      *fakeGitHub
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=agent", "GIT_AUTHOR_EMAIL=a@b", "GIT_COMMITTER_NAME=agent", "GIT_COMMITTER_EMAIL=a@b")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	for _, bin := range []string{"git", "gitleaks"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	f := &fixture{t: t, root: root, remote: filepath.Join(root, "remote.git"), clone: filepath.Join(root, "clone"), gh: newFakeGitHub(t)}
	sh(t, root, "init", "-q", "--bare", "-b", "main", f.remote)
	sh(t, root, "clone", "-q", f.remote, f.clone)
	sh(t, f.clone, "checkout", "-q", "-b", "main")
	os.WriteFile(filepath.Join(f.clone, "hello.py"), []byte("print('hi')\n"), 0o644)
	sh(t, f.clone, "add", "-A")
	sh(t, f.clone, "commit", "-q", "-m", "Initial")
	sh(t, f.clone, "push", "-q", "origin", "main")
	f.baseSHA = sh(t, f.clone, "rev-parse", "HEAD")
	return f
}

// commit makes the agent's branch with one commit that writes the given files, and returns a
// request for it.
func (f *fixture) commit(run string, files map[string]string) Request {
	f.t.Helper()
	sh(f.t, f.clone, "checkout", "-q", "-B", "satat/"+run, f.baseSHA)
	for name, content := range files {
		p := filepath.Join(f.clone, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	sh(f.t, f.clone, "add", "-A")
	sh(f.t, f.clone, "commit", "-q", "-m", "Add greet function")
	head := sh(f.t, f.clone, "rev-parse", "HEAD")
	bundle := filepath.Join(f.root, run+".bundle")
	sh(f.t, f.clone, "bundle", "create", "-q", bundle, f.baseSHA+"..satat/"+run)
	b, err := os.ReadFile(bundle)
	if err != nil {
		f.t.Fatal(err)
	}
	return Request{Repo: "acme/widgets", Base: "main", Run: run, BaseSHA: f.baseSHA, HeadSHA: head, Bundle: b, Issue: 7, Summary: "Added greet.\nTests unverified."}
}

func (f *fixture) pusher() *Pusher {
	return &Pusher{
		Token:    "write-secret-value",
		APIBase:  f.gh.srv.URL,
		CloneURL: func(string) string { return "file://" + f.remote },
		WorkRoot: f.root,
	}
}

func (f *fixture) remoteHas(branch string) string {
	cmd := exec.Command("git", "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	cmd.Dir = f.remote
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

func TestPushesTheBranchAndOpensADraftPR(t *testing.T) {
	f := newFixture(t)
	req := f.commit("run1", map[string]string{"hello.py": "print('hi')\n\ndef greet(n):\n    return n\n"})
	out, err := f.pusher().Push(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if out.Branch != "satat/run1" || out.PRNumber != 2 || out.Reused {
		t.Fatalf("unexpected outcome %+v", out)
	}
	if got := f.remoteHas("satat/run1"); got != req.HeadSHA {
		t.Errorf("remote branch at %q, want the agent's commit %s", got, req.HeadSHA)
	}
	if len(f.gh.posted) != 1 {
		t.Fatalf("want one PR create, got %d", len(f.gh.posted))
	}
	pr := f.gh.posted[0]
	if pr["draft"] != true || pr["head"] != "satat/run1" || pr["base"] != "main" || pr["title"] != "Add greet function" {
		t.Errorf("PR payload: %v", pr)
	}
	body := pr["body"].(string)
	for _, want := range []string{"Resolves #7.", "> Added greet.", "Satat-Run: run1", "Satat never merges"} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body lacks %q:\n%s", want, body)
		}
	}
	for _, a := range f.gh.auths {
		if a != "Bearer write-secret-value" {
			t.Errorf("API call used auth %q", a)
		}
	}
	// The token is in no remote URL or git config in the bare remote.
	cfg, _ := os.ReadFile(filepath.Join(f.remote, "config"))
	if strings.Contains(string(cfg), "write-secret-value") {
		t.Error("token written to git config")
	}
}

func TestRefusals(t *testing.T) {
	secret := "gh" + "p_" + "R8dK2mQz7LxV4nBw9TyHs3JfCu6PaEe1GoXi" // built at run time so this file does not trip gitleaks itself
	cases := map[string]struct {
		files map[string]string
		edit  func(*Request)
		want  string
	}{
		"workflow file":       {files: map[string]string{".github/workflows/ci.yml": "on: push\n"}, want: "does not edit workflows"},
		"workflow file cased": {files: map[string]string{".GitHub/Workflows/x.yml": "on: push\n"}, want: "does not edit workflows"},
		"secret in the diff":  {files: map[string]string{"config.py": "TOKEN = '" + secret + "'\n"}, want: "gitleaks found"},
		"head sha mismatch":   {files: map[string]string{"a.txt": "a\n"}, edit: func(r *Request) { r.HeadSHA = strings.Repeat("a", 40) }, want: "must hold exactly one branch ending at"},
		"bad run id":          {files: map[string]string{"a.txt": "a\n"}, edit: func(r *Request) { r.Run = "../x" }, want: "not a simple identifier"},
		"bad repo":            {files: map[string]string{"a.txt": "a\n"}, edit: func(r *Request) { r.Repo = "--x/y" }, want: "not owner/name"},
		"empty bundle":        {files: map[string]string{"a.txt": "a\n"}, edit: func(r *Request) { r.Bundle = nil }, want: "empty bundle"},
		"short sha":           {files: map[string]string{"a.txt": "a\n"}, edit: func(r *Request) { r.BaseSHA = "abc" }, want: "full commit SHAs"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			req := f.commit("run1", tc.files)
			if tc.edit != nil {
				tc.edit(&req)
			}
			_, err := f.pusher().Push(context.Background(), req)
			var ref *Refusal
			if !errors.As(err, &ref) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want a refusal mentioning %q", err, tc.want)
			}
			if got := f.remoteHas("satat/run1"); got != "" {
				t.Errorf("a refused change was pushed (%s)", got)
			}
			if len(f.gh.posted) != 0 {
				t.Error("a refused change opened a PR")
			}
		})
	}
}

func TestRefusalDoesNotLeakTheSecretItFound(t *testing.T) {
	f := newFixture(t)
	secret := "gh" + "p_" + "R8dK2mQz7LxV4nBw9TyHs3JfCu6PaEe1GoXi"
	req := f.commit("run1", map[string]string{"config.py": "TOKEN = '" + secret + "'\n"})
	_, err := f.pusher().Push(context.Background(), req)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error must exist and must not repeat the secret: %v", err)
	}
}

func TestMissingGitleaksIsAnErrorNotASkip(t *testing.T) {
	f := newFixture(t)
	req := f.commit("run1", map[string]string{"a.txt": "a\n"})
	p := f.pusher()
	p.Gitleaks = "gitleaks-does-not-exist"
	_, err := p.Push(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "unscanned") {
		t.Fatalf("error = %v, want a refusal to publish unscanned changes", err)
	}
	if f.remoteHas("satat/run1") != "" {
		t.Error("pushed without scanning")
	}
}

func TestTooManyCommitsAreRefused(t *testing.T) {
	f := newFixture(t)
	req := f.commit("run1", map[string]string{"a.txt": "a\n"})
	p := f.pusher()
	p.MaxCommits = 1
	if _, err := p.Push(context.Background(), req); err != nil {
		t.Fatalf("one commit within a limit of 1 must pass: %v", err)
	}
	// Two commits against a limit of one.
	f2 := newFixture(t)
	r2 := f2.commit("run2", map[string]string{"a.txt": "a\n"})
	sh(t, f2.clone, "commit", "-q", "--allow-empty", "-m", "Second")
	r2.HeadSHA = sh(t, f2.clone, "rev-parse", "HEAD")
	sh(t, f2.clone, "bundle", "create", "-q", filepath.Join(f2.root, "two.bundle"), f2.baseSHA+"..satat/run2")
	r2.Bundle, _ = os.ReadFile(filepath.Join(f2.root, "two.bundle"))
	p2 := f2.pusher()
	p2.MaxCommits = 1
	if _, err := p2.Push(context.Background(), r2); err == nil || !strings.Contains(err.Error(), "2 commits") {
		t.Fatalf("error = %v, want a refusal for 2 commits", err)
	}
}

func TestRedeliveryReusesTheBranchAndPR(t *testing.T) {
	f := newFixture(t)
	req := f.commit("run1", map[string]string{"a.txt": "a\n"})
	if _, err := f.pusher().Push(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	f.gh.existing = []pull{{Number: 2, URL: "https://github.com/acme/widgets/pull/2"}}
	out, err := f.pusher().Push(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Reused || out.PRNumber != 2 {
		t.Errorf("outcome %+v, want the existing PR reused", out)
	}
	if len(f.gh.posted) != 1 {
		t.Errorf("a second delivery opened %d PRs, want 1 in total", len(f.gh.posted))
	}
}

func TestDoesNotOverwriteADifferentBranch(t *testing.T) {
	f := newFixture(t)
	// Someone already pushed satat/run1 with different content.
	sh(t, f.clone, "checkout", "-q", "-B", "satat/run1", f.baseSHA)
	os.WriteFile(filepath.Join(f.clone, "other.txt"), []byte("x\n"), 0o644)
	sh(t, f.clone, "add", "-A")
	sh(t, f.clone, "commit", "-q", "-m", "Other")
	sh(t, f.clone, "push", "-q", "origin", "satat/run1")
	other := f.remoteHas("satat/run1")
	req := f.commit("run1", map[string]string{"a.txt": "a\n"})
	_, err := f.pusher().Push(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "not overwriting") {
		t.Fatalf("error = %v, want a refusal to overwrite", err)
	}
	if f.remoteHas("satat/run1") != other {
		t.Error("the existing branch was changed")
	}
}

func TestGitHubErrorsDoNotLeakTheToken(t *testing.T) {
	f := newFixture(t)
	f.gh.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad credentials write-secret-value", http.StatusUnauthorized)
	})
	req := f.commit("run1", map[string]string{"a.txt": "a\n"})
	_, err := f.pusher().Push(context.Background(), req)
	if err == nil || strings.Contains(err.Error(), "write-secret-value") {
		t.Fatalf("want an error without the token: %v", err)
	}
}

func TestBodyShape(t *testing.T) {
	b := Body(Request{Run: "r9", Issue: 0, Summary: strings.Repeat("x", 4000)})
	if strings.Contains(b, "Resolves") || !strings.Contains(b, "[truncated]") || !strings.HasSuffix(b, "Satat-Run: r9\n") {
		t.Errorf("unexpected body:\n%s", b)
	}
}
