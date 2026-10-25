package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (e *env) noClaudeSession(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(e.fake, "args")); err == nil {
		t.Error("claude ran a session after the toolchain step failed")
	}
}

func (e *env) miseMode(t *testing.T, mode string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.fake, "mise-mode"), []byte(mode), 0o644); err != nil {
		t.Fatal(err)
	}
}

func envLines(s string) map[string]string {
	return envMap(strings.Split(strings.TrimSpace(s), "\n"))
}

func TestToolchainIsInstalledBeforeClaude(t *testing.T) {
	e := setup(t, "commit")
	if err := Run(context.Background(), e.cfg); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(e.cfg.WorkDir, "repo")
	calls := e.read(t, filepath.Join(e.fake, "mise-calls"))
	want := "trust " + repo + "\nls --current --json\nls --current --missing --json\ninstall\n"
	if calls != want {
		t.Errorf("mise calls:\n got %q\nwant %q", calls, want)
	}
	if cwd := strings.TrimSpace(e.read(t, filepath.Join(e.fake, "mise-cwd"))); !strings.HasSuffix(cwd, "/work/repo") {
		t.Errorf("mise install ran in %s, want the clone", cwd)
	}
	tc := e.result(t).Toolchain
	if tc == nil || tc.Cache != "miss" || tc.Defaults != "skipped" || strings.Join(tc.Pinned, ",") != "go@1.25.1" || tc.Locked || tc.TimedOut || len(tc.Failed) != 0 ||
		strings.Join(tc.Missing, ",") != "go@1.25.1" || tc.InstallMS < 0 {
		t.Fatalf("toolchain: %+v", tc)
	}
}

func TestAWarmCacheIsRecordedAsAHit(t *testing.T) {
	e := setup(t, "commit")
	e.miseMode(t, "hit")
	if err := Run(context.Background(), e.cfg); err != nil {
		t.Fatal(err)
	}
	if tc := e.result(t).Toolchain; tc == nil || tc.Cache != "hit" || len(tc.Missing) != 0 {
		t.Fatalf("toolchain: %+v", tc)
	}
}

func TestAFailedMissingCheckOnlyLosesTheMeasurement(t *testing.T) {
	e := setup(t, "commit")
	e.miseMode(t, "lsfail")
	if err := Run(context.Background(), e.cfg); err != nil {
		t.Fatal(err)
	}
	if tc := e.result(t).Toolchain; tc == nil || tc.Cache != "unknown" {
		t.Fatalf("toolchain: %+v", tc)
	}
}

func TestALockfileMeansLockedInstall(t *testing.T) {
	e := setup(t, "commit")
	if err := os.WriteFile(filepath.Join(e.repo, "mise.lock"), []byte("[tools]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", e.repo, "add", "mise.lock"},
		{"-C", e.repo, "-c", "user.name=a", "-c", "user.email=a@b", "commit", "-q", "-m", "Lock"},
		{"-C", e.checkout, "pull", "-q"}, // the worker clones after the commit
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := Run(context.Background(), e.cfg); err != nil {
		t.Fatal(err)
	}
	if calls := e.read(t, filepath.Join(e.fake, "mise-calls")); !strings.Contains(calls, "install --locked\n") {
		t.Errorf("install was not locked:\n%s", calls)
	}
	if tc := e.result(t).Toolchain; tc == nil || !tc.Locked {
		t.Fatalf("toolchain: %+v", tc)
	}
}

func TestAFailedInstallStopsTheStep(t *testing.T) {
	e := setup(t, "commit")
	e.miseMode(t, "fail")
	err := Run(context.Background(), e.cfg)
	if err == nil || !strings.Contains(err.Error(), "mise install failed for go@1.25.1") {
		t.Fatalf("error = %v, want it to name mise and go", err)
	}
	r := e.result(t)
	if r.Status != "failed" || !strings.Contains(r.Error, "mise install failed for go@1.25.1") || !strings.Contains(r.Error, "404") {
		t.Errorf("result: status %q error %q", r.Status, r.Error)
	}
	if r.Toolchain == nil || strings.Join(r.Toolchain.Failed, ",") != "go@1.25.1" {
		t.Errorf("toolchain: %+v", r.Toolchain)
	}
	e.noClaudeSession(t)
}

func TestATimedOutInstallStopsTheStep(t *testing.T) {
	e := setup(t, "commit")
	e.miseMode(t, "slow")
	e.cfg.MiseTimeout = 300 * time.Millisecond
	start := time.Now()
	err := Run(context.Background(), e.cfg)
	if err == nil || !strings.Contains(err.Error(), "mise install timed out after 300ms installing go@1.25.1") {
		t.Fatalf("error = %v, want a mise timeout naming go", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("the timeout took %s to land", d)
	}
	r := e.result(t)
	if r.Status != "failed" || r.Toolchain == nil || !r.Toolchain.TimedOut || !strings.Contains(r.Error, "timed out") {
		t.Errorf("result: %+v toolchain %+v", r, r.Toolchain)
	}
	e.noClaudeSession(t)
}

func TestMiseTimeoutComesFromTheEnvironment(t *testing.T) {
	e := setup(t, "commit", "SATAT_MISE_TIMEOUT=200ms")
	e.miseMode(t, "slow")
	if err := Run(context.Background(), e.cfg); err == nil || !strings.Contains(err.Error(), "timed out after 200ms") {
		t.Fatalf("error = %v", err)
	}
	e = setup(t, "commit", "SATAT_MISE_TIMEOUT=soon")
	if err := Run(context.Background(), e.cfg); err == nil || !strings.Contains(err.Error(), "SATAT_MISE_TIMEOUT") {
		t.Fatalf("error = %v, want a bad-timeout refusal", err)
	}
	if got, _ := miseTimeout(Config{}, nil); got != 10*time.Minute {
		t.Errorf("default timeout = %s", got)
	}
}

// mise runs code from the repo's config, so it gets an allowlisted environment: no clone token,
// no OAuth token, nothing the lane did not mean to pass.
func TestMiseGetsOnlyTheAllowlistedEnvironment(t *testing.T) {
	e := setup(t, "commit",
		"GITHUB_TOKEN=gh-secret",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"MISE_GITHUB_TOKEN=oauth-secret-value", // allowed name, secret value: dropped
		"HTTPS_PROXY=http://proxy:3128",
		"GOMODCACHE=/cache/go-mod",
		"TMPDIR=/cache/tmp",
		"MISE_CACHE_DIR=/cache/mise-cache",
	)
	if err := Run(context.Background(), e.cfg); err != nil {
		t.Fatal(err)
	}
	raw := e.read(t, filepath.Join(e.fake, "mise-env"))
	for _, s := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "oauth-secret-value",
		"GITHUB_TOKEN", "aws-secret", "FAKE_MODE", "GIT_AUTHOR_NAME"} {
		if strings.Contains(raw, s) {
			t.Errorf("mise's environment contains %s:\n%s", s, raw)
		}
	}
	m := envLines(raw)
	for k, v := range map[string]string{
		"HTTPS_PROXY": "http://proxy:3128", "GOMODCACHE": "/cache/go-mod", "TMPDIR": "/cache/tmp",
		"MISE_CACHE_DIR": "/cache/mise-cache", "MISE_YES": "1", idiomaticVar: "go,node,python,java",
		"HOME": filepath.Join(e.dir, "home"),
	} {
		if m[k] != v {
			t.Errorf("mise env %s = %q, want %q", k, m[k], v)
		}
	}
	shims := filepath.Join(e.dir, "cache", "mise", "shims")
	if !strings.HasPrefix(m["PATH"], shims+string(os.PathListSeparator)) {
		t.Errorf("mise PATH %q does not start with the shims", m["PATH"])
	}
}

func TestClaudeGetsTheToolchainOnPath(t *testing.T) {
	e := setup(t, "commit")
	if err := Run(context.Background(), e.cfg); err != nil {
		t.Fatal(err)
	}
	m := envLines(e.read(t, filepath.Join(e.fake, "env")))
	shims := filepath.Join(e.dir, "cache", "mise", "shims")
	if want := shims + string(os.PathListSeparator) + os.Getenv("PATH"); m["PATH"] != want {
		t.Errorf("claude PATH:\n got %q\nwant %q", m["PATH"], want)
	}
	if m[idiomaticVar] != idiomaticTools {
		t.Errorf("claude %s = %q; the shims would ignore go.mod and .nvmrc", idiomaticVar, m[idiomaticVar])
	}
}

func TestToolchainPATH(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"data dir":        {map[string]string{"MISE_DATA_DIR": "/cache/mise", "PATH": "/usr/local/bin:/usr/bin"}, "/cache/mise/shims:/usr/local/bin:/usr/bin"},
		"no duplicate":    {map[string]string{"MISE_DATA_DIR": "/cache/mise", "PATH": "/usr/bin:/cache/mise/shims/:/bin"}, "/cache/mise/shims:/usr/bin:/bin"},
		"xdg":             {map[string]string{"XDG_DATA_HOME": "/x", "PATH": "/bin"}, "/x/mise/shims:/bin"},
		"home default":    {map[string]string{"HOME": "/workspace", "PATH": "/bin"}, "/workspace/.local/share/mise/shims:/bin"},
		"empty path bits": {map[string]string{"MISE_DATA_DIR": "/m", "PATH": ":/bin::"}, "/m/shims:/bin"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ToolchainPATH(tc.env); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// claude and mise are run by absolute path, so a shim or a repo-installed binary named claude
// on PATH cannot stand in for them.
func TestBinariesMustBeAbsolute(t *testing.T) {
	for name, mut := range map[string]func(*Config){
		"claude": func(c *Config) { c.Claude = "claude" },
		"mise":   func(c *Config) { c.Mise = "mise" },
	} {
		t.Run(name, func(t *testing.T) {
			e := setup(t, "commit")
			mut(&e.cfg)
			if err := Run(context.Background(), e.cfg); err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
				t.Fatalf("error = %v", err)
			}
			e.noClaudeSession(t)
		})
	}
}

func TestFailedTools(t *testing.T) {
	out := "mise ⇢ jq@1.7.1  1ms · already installed\n" +
		"mise ✗ go@1.99.99  215ms · failed: 404\n" +
		"mise ERROR Failed to install core:go@1.99.99: 404\n"
	if got := strings.Join(failedTools(out), ","); got != "go@1.99.99" {
		t.Errorf("got %q", got)
	}
	if got := strings.Join(failedTools("mise ERROR Failed to install aqua:jqlang/jq@1.7.1: boom\n"), ","); got != "jqlang/jq@1.7.1" {
		t.Errorf("fallback got %q", got)
	}
	if got := failedTools("mise ERROR something else\n"); len(got) != 0 {
		t.Errorf("got %q", got)
	}
}

func (e *env) unpinned(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.fake, "mise-pins"), []byte("none"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *env) defaultsFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(e.dir, "mise-defaults.toml")
	if err := os.WriteFile(p, []byte("[tools]\ngo = \"latest\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A repo that pins its own tools gets only those: the lane defaults would add a JDK, Python and
// Node it never asked for.
func TestAPinnedRepoSkipsTheLaneDefaults(t *testing.T) {
	e := setup(t, "commit")
	e.cfg.Env = append(e.cfg.Env, "SATAT_MISE_DEFAULTS="+e.defaultsFile(t))
	if err := Run(context.Background(), e.cfg); err != nil {
		t.Fatal(err)
	}
	if tc := e.result(t).Toolchain; tc == nil || tc.Defaults != "skipped" {
		t.Fatalf("toolchain: %+v", tc)
	}
	for _, f := range []string{"mise-env", "env"} {
		if v, set := envLines(e.read(t, filepath.Join(e.fake, f)))[globalConfigVar]; set {
			t.Errorf("%s has %s=%s for a pinned repo", f, globalConfigVar, v)
		}
	}
}

func TestAnUnpinnedRepoGetsTheLaneDefaults(t *testing.T) {
	e := setup(t, "commit")
	e.unpinned(t)
	defaults := e.defaultsFile(t)
	e.cfg.Env = append(e.cfg.Env, "SATAT_MISE_DEFAULTS="+defaults)
	if err := Run(context.Background(), e.cfg); err != nil {
		t.Fatal(err)
	}
	tc := e.result(t).Toolchain
	if tc == nil || tc.Defaults != "applied" || len(tc.Pinned) != 0 || strings.Join(tc.Missing, ",") != "go@1.25.1" {
		t.Fatalf("toolchain: %+v", tc)
	}
	// Both the install and claude's shims must see the defaults, or `go` would not resolve.
	for _, f := range []string{"mise-env", "env"} {
		if got := envLines(e.read(t, filepath.Join(e.fake, f)))[globalConfigVar]; got != defaults {
			t.Errorf("%s: %s = %q, want %q", f, globalConfigVar, got, defaults)
		}
	}
}

func TestMissingLaneDefaultsAreAWarningNotAFailure(t *testing.T) {
	e := setup(t, "commit")
	e.unpinned(t)
	e.cfg.Env = append(e.cfg.Env, "SATAT_MISE_DEFAULTS="+filepath.Join(e.dir, "nope.toml"))
	var log strings.Builder
	e.cfg.Log = &log
	if err := Run(context.Background(), e.cfg); err != nil {
		t.Fatal(err)
	}
	if r := e.result(t); r.Status != "changes" || r.Toolchain == nil || r.Toolchain.Defaults != "missing" {
		t.Fatalf("result: %+v toolchain %+v", r, r.Toolchain)
	}
	if !strings.Contains(log.String(), "nope.toml are missing") {
		t.Errorf("no warning in the log:\n%s", log.String())
	}
	if _, set := envLines(e.read(t, filepath.Join(e.fake, "env")))[globalConfigVar]; set {
		t.Error("claude got a defaults file that does not exist")
	}
}
