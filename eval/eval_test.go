package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher/docker"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/runner"
)

// hostChecker runs the command with the host's tools in a temp dir. It stands in for the
// no-network lane so the grading logic can be tested without Docker.
type hostChecker struct{}

func (hostChecker) Run(ctx context.Context, _ string, files map[string][]byte, command []string) (CheckRun, error) {
	dir, _ := os.MkdirTemp("", "eval-check-")
	defer os.RemoveAll(dir)
	for name, b := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, b, 0o644)
	}
	cmd := exec.CommandContext(ctx, pyName(command[0]), command[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		return CheckRun{}, err
	}
	return CheckRun{ExitCode: code, Output: string(out)}, nil
}

type fixture struct {
	t       *testing.T
	root    string
	remote  string
	clone   string
	baseSHA string
}

func git(t *testing.T, dir string, args ...string) string {
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
	for _, bin := range []string{"git", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	f := &fixture{t: t, root: root, remote: filepath.Join(root, "remote.git"), clone: filepath.Join(root, "clone")}
	git(t, root, "init", "-q", "--bare", "-b", "main", f.remote)
	git(t, root, "clone", "-q", f.remote, f.clone)
	git(t, f.clone, "checkout", "-q", "-b", "main")
	os.WriteFile(filepath.Join(f.clone, "hello.py"), []byte("def hello() -> str:\n    return \"Hello, world!\"\n"), 0o644)
	os.WriteFile(filepath.Join(f.clone, "test_hello.py"), []byte("import unittest\nfrom hello import hello\n\n\nclass T(unittest.TestCase):\n    def test_hello(self):\n        self.assertEqual(hello(), \"Hello, world!\")\n"), 0o644)
	git(t, f.clone, "add", "-A")
	git(t, f.clone, "commit", "-q", "-m", "Initial")
	git(t, f.clone, "push", "-q", "origin", "main")
	f.baseSHA = git(t, f.clone, "rev-parse", "HEAD")
	return f
}

const goodHello = "def hello() -> str:\n    return \"Hello, world!\"\n\n\ndef greet(name: str) -> str:\n    if not name:\n        raise ValueError(\"empty\")\n    return f\"Hello, {name}!\"\n"

func (f *fixture) subject(run string, files map[string]string, subjects ...string) Subject {
	f.t.Helper()
	git(f.t, f.clone, "checkout", "-q", "-B", "satat/"+run, f.baseSHA)
	if len(subjects) == 0 {
		subjects = []string{"Add greet function"}
	}
	for i, subj := range subjects {
		if i == 0 {
			for name, c := range files {
				p := filepath.Join(f.clone, name)
				os.MkdirAll(filepath.Dir(p), 0o755)
				os.WriteFile(p, []byte(c), 0o644)
			}
			git(f.t, f.clone, "add", "-A")
			git(f.t, f.clone, "commit", "-q", "-m", subj)
		} else {
			git(f.t, f.clone, "commit", "-q", "--allow-empty", "-m", subj)
		}
	}
	head := git(f.t, f.clone, "rev-parse", "HEAD")
	bundle := filepath.Join(f.root, run+".bundle")
	git(f.t, f.clone, "bundle", "create", "-q", bundle, f.baseSHA+"..satat/"+run)
	b, err := os.ReadFile(bundle)
	if err != nil {
		f.t.Fatal(err)
	}
	return Subject{Run: run, Bundle: b, Runner: &runner.Result{Status: "changes", Commits: len(subjects), Branch: "satat/" + run, BaseSHA: f.baseSHA, HeadSHA: head}}
}

func (f *fixture) grader() *Grader {
	return &Grader{CloneURL: func(string) string { return "file://" + f.remote }, Checker: hostChecker{}, Work: f.root}
}

func theCase(t *testing.T) Case {
	t.Helper()
	c, err := LoadCase("cases/sandbox-greet")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func byName(r Result) map[string]Check {
	m := map[string]Check{}
	for _, c := range r.Checks {
		m[c.Name] = c
	}
	return m
}

func TestAGoodChangePassesEveryCheck(t *testing.T) {
	f := newFixture(t)
	s := f.subject("r1", map[string]string{"hello.py": goodHello})
	res, err := f.grader().Grade(context.Background(), theCase(t), s)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed() {
		t.Fatalf("a good change failed: %+v", res.Checks)
	}
	got := byName(res)
	for _, n := range []string{"run", "scope", "commits", "tests"} {
		if !got[n].Pass {
			t.Errorf("check %s: %+v", n, got[n])
		}
	}
	if !got["pr"].Skip {
		t.Errorf("pr should be skipped when nothing was published: %+v", got["pr"])
	}
}

func TestBadChangesFailTheRightCheck(t *testing.T) {
	cases := map[string]struct {
		files    map[string]string
		subjects []string
		failing  string
		mention  string
	}{
		"missing the empty-name error": {
			files:   map[string]string{"hello.py": strings.Replace(goodHello, "    if not name:\n        raise ValueError(\"empty\")\n", "", 1)},
			failing: "tests", mention: "FAIL",
		},
		"breaks hello": {
			files:   map[string]string{"hello.py": strings.Replace(goodHello, "Hello, world!", "Hi", 1)},
			failing: "tests", mention: "FAIL",
		},
		"touches an unrelated file": {
			files:   map[string]string{"hello.py": goodHello, "notes.txt": "x\n"},
			failing: "scope", mention: "notes.txt",
		},
		"subject too long": {
			files: map[string]string{"hello.py": goodHello}, subjects: []string{"Add a greet function that returns a greeting for a name"},
			failing: "commits", mention: "over 50",
		},
		"subject ends in a period": {
			files: map[string]string{"hello.py": goodHello}, subjects: []string{"Add greet."},
			failing: "commits", mention: "period",
		},
		"subject not capitalised": {
			files: map[string]string{"hello.py": goodHello}, subjects: []string{"add greet"},
			failing: "commits", mention: "capitalised",
		},
		"too many commits": {
			files: map[string]string{"hello.py": goodHello}, subjects: []string{"One", "Two", "Three", "Four"},
			failing: "commits", mention: "limit 3",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			s := f.subject("r1", tc.files, tc.subjects...)
			res, err := f.grader().Grade(context.Background(), theCase(t), s)
			if err != nil {
				t.Fatal(err)
			}
			if res.Passed() {
				t.Fatal("a bad change passed")
			}
			c := byName(res)[tc.failing]
			if c.Pass || !strings.Contains(c.Detail, tc.mention) {
				t.Errorf("check %s = %+v, want a failure mentioning %q", tc.failing, c, tc.mention)
			}
		})
	}
}

func TestAFailedRunSkipsTheRestAndFails(t *testing.T) {
	f := newFixture(t)
	for name, s := range map[string]Subject{
		"no result":  {Run: "r1"},
		"bad exit":   {Run: "r1", ExitCode: 1, Runner: &runner.Result{Status: "failed", Error: "boom"}},
		"no changes": {Run: "r1", Runner: &runner.Result{Status: "no_changes"}},
	} {
		res, err := f.grader().Grade(context.Background(), theCase(t), s)
		if err != nil || res.Passed() {
			t.Fatalf("%s: passed=%v err=%v", name, res.Passed(), err)
		}
		if c := byName(res); c["run"].Pass || !c["tests"].Skip {
			t.Errorf("%s: %+v", name, res.Checks)
		}
	}
}

func TestHiddenTestsThatDoNotRunCannotPass(t *testing.T) {
	f := newFixture(t)
	c := theCase(t)
	c.Checks.Tests.Command = []string{"python3", "-m", "unittest", "discover", "-v"} // default pattern skips the hidden file
	res, err := f.grader().Grade(context.Background(), c, f.subject("r1", map[string]string{"hello.py": goodHello}))
	if err != nil || res.Passed() || !strings.Contains(byName(res)["tests"].Detail, "did not run") {
		t.Fatalf("passed=%v err=%v checks=%+v", res.Passed(), err, res.Checks)
	}
}

func TestTheHiddenTestsAreNotInTheRepo(t *testing.T) {
	// The agent works in a clone of the repo; the hidden file must not be there, and the case
	// directory is the only place it lives.
	f := newFixture(t)
	if _, err := os.Stat(filepath.Join(f.clone, "hidden_test_greet.py")); err == nil {
		t.Fatal("hidden test is in the repo")
	}
	if _, err := os.Stat("cases/sandbox-greet/hidden_test_greet.py"); err != nil {
		t.Fatal(err)
	}
}

func TestPRChecks(t *testing.T) {
	f := newFixture(t)
	s := f.subject("r1", map[string]string{"hello.py": goodHello})
	s.PR = &pusher.Outcome{Branch: "satat/r1", PRNumber: 2}
	serve := func(body map[string]any) *Grader {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" || !strings.HasSuffix(r.URL.Path, "/pulls/2") {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(body)
		}))
		t.Cleanup(srv.Close)
		g := f.grader()
		g.APIBase, g.Token = srv.URL, "tok"
		return g
	}
	good := map[string]any{"draft": true, "body": "x\n\nSatat-Run: r1\n", "head": map[string]any{"ref": "satat/r1"}, "base": map[string]any{"ref": "main"}}
	res, err := serve(good).Grade(context.Background(), theCase(t), s)
	if err != nil || !res.Passed() || !byName(res)["pr"].Pass {
		t.Fatalf("good PR: %v %+v", err, res.Checks)
	}
	for name, mod := range map[string]func(map[string]any){
		"not draft":  func(m map[string]any) { m["draft"] = false },
		"merged":     func(m map[string]any) { m["merged"] = true },
		"no trailer": func(m map[string]any) { m["body"] = "nothing" },
		"wrong head": func(m map[string]any) { m["head"] = map[string]any{"ref": "other"} },
	} {
		bad := map[string]any{}
		for k, v := range good {
			bad[k] = v
		}
		mod(bad)
		res, err := serve(bad).Grade(context.Background(), theCase(t), s)
		if err != nil || res.Passed() || byName(res)["pr"].Pass {
			t.Errorf("%s: should fail the pr check (err %v)", name, err)
		}
	}
}

func TestLoadCaseRejectsBadCases(t *testing.T) {
	write := func(y string) string {
		d := t.TempDir()
		os.WriteFile(filepath.Join(d, "case.yaml"), []byte(y), 0o644)
		return d
	}
	base := "name: a\nrepo: o/r\nbase: main\nissue: 1\n"
	for name, y := range map[string]string{
		"unknown key":     base + "extra: 1\n",
		"no issue":        "name: a\nrepo: o/r\nbase: main\n",
		"tests no lane":   base + "checks:\n  tests:\n    command: [x]\n",
		"hidden with dir": base + "checks:\n  tests:\n    lane: l\n    command: [x]\n    hidden: [../x.py]\n",
		"hidden dotfile":  base + "checks:\n  tests:\n    lane: l\n    command: [x]\n    hidden: [.x]\n",
	} {
		if _, err := LoadCase(write(y)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// The no-network lane, for real: files and a command in, exit code and output out.
func TestCheckerLaneRunsInDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	api, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("no Docker client: %v", err)
	}
	defer api.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := api.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("Docker is not reachable: %v", err)
	}
	cfg, err := config.Load("../satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var lanes = cfg.Lanes[:0:0]
	for _, l := range cfg.Lanes {
		if l.Name == "check-python" {
			lanes = append(lanes, l)
		}
	}
	l, err := docker.New(api, lanes)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = api.NetworkRemove(context.Background(), "satat-lane-check-python", client.NetworkRemoveOptions{})
	})
	chk := LauncherChecker{Launcher: l, RunID: "evaltest"}

	files := map[string][]byte{
		"hello.py":      []byte(goodHello),
		"test_greet.py": []byte("import unittest\nfrom hello import greet\n\nclass T(unittest.TestCase):\n    def test_a(self):\n        self.assertEqual(greet('x'), 'Hello, x!')\n"),
	}
	run, err := chk.Run(ctx, "check-python", files, []string{"python", "-m", "unittest", "discover", "-v"})
	if err != nil || run.ExitCode != 0 || !strings.Contains(run.Output, "OK") {
		t.Fatalf("passing tests: %v %+v", err, run)
	}
	files["hello.py"] = []byte("def greet(name):\n    return 'nope'\n")
	run, err = chk.Run(ctx, "check-python", files, []string{"python", "-m", "unittest", "discover", "-v"})
	if err != nil || run.ExitCode == 0 || !strings.Contains(run.Output, "FAIL") {
		t.Fatalf("failing tests: %v %+v", err, run)
	}
	// No network: a command that needs one fails inside the lane.
	run, err = chk.Run(ctx, "check-python", files, []string{"python", "-c", "import urllib.request as u; u.urlopen('https://example.com', timeout=5)"})
	if err != nil || run.ExitCode == 0 {
		t.Fatalf("the check lane reached the network: %v %+v", err, run)
	}
}

// The host may only have python3; the lane's image has both.
func pyName(c string) string {
	if c == "python" {
		return "python3"
	}
	return c
}
