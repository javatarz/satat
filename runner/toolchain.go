package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Toolchain records how the runner prepared the repo's toolchains with mise (ADR 0033), so evals
// and capacity work (#109) can read install cost per run.
type Toolchain struct {
	Locked bool `json:"locked"` // mise.lock present, so `mise install --locked` ran
	// Missing is what mise had to install (tool@version), read before the install. Empty means
	// everything came from the /cache volume.
	Missing   []string `json:"missing,omitempty"`
	Cache     string   `json:"cache"` // "hit", "miss" or "unknown"
	InstallMS int64    `json:"install_ms"`
	Failed    []string `json:"failed,omitempty"` // tool@version mise reported as failed
	TimedOut  bool     `json:"timed_out,omitempty"`
}

const (
	// DefaultMiseTimeout covers trust, the missing-tools check and the install. A first install
	// on a cold /cache pulls hundreds of MB (a JDK, Go, Python and Node).
	DefaultMiseTimeout = 10 * time.Minute
	miseTimeoutVar     = "SATAT_MISE_TIMEOUT"
	idiomaticVar       = "MISE_IDIOMATIC_VERSION_FILE_ENABLE_TOOLS"
	// The languages whose idiomatic files (go.mod, .nvmrc, .python-version, .java-version, ...)
	// the lane honours, per ADR 0033. mise ignores them unless enabled.
	idiomaticTools = "go,node,python,java"
)

// miseEnvExact and miseEnvPrefixes are the only variables mise sees. It is an allowlist, not a
// denylist, so a credential added to the runner's environment later does not reach a process
// that runs code from the repo's config.
var (
	miseEnvExact = map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LANG": true, "TZ": true,
		"TMPDIR": true, "GOTMPDIR": true,
		"XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_STATE_HOME": true,
		// Language caches and toolchain behaviour (ADR 0033 build caches).
		"GOTOOLCHAIN": true, "GOMODCACHE": true, "GOCACHE": true, "GOPATH": true, "GOPROXY": true,
		"GOSUMDB": true, "GOPRIVATE": true, "GONOSUMDB": true, "GOFLAGS": true,
		"PIP_CACHE_DIR": true, "PIP_INDEX_URL": true, "npm_config_cache": true, "NPM_CONFIG_CACHE": true,
		"MAVEN_ARGS": true, "GRADLE_USER_HOME": true, "COREPACK_HOME": true,
		"COREPACK_ENABLE_DOWNLOAD_PROMPT": true, "JAVA_TOOL_OPTIONS": true,
		// Egress and TLS: the proxy of ADR 0025 and its CA, when they exist.
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
		"http_proxy": true, "https_proxy": true, "no_proxy": true, "all_proxy": true,
		"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	}
	miseEnvPrefixes = []string{"MISE_", "LC_"}

	miseFailedRE  = regexp.MustCompile(`(?m)^mise\s+✗\s+(\S+@\S+)`)
	miseErrToolRE = regexp.MustCompile(`(?m)Failed to install (?:[a-z0-9_-]+:)?(\S+@\S+?):`)
)

// miseEnv is the environment for every mise call: the allowlist above, minus anything carrying
// a secret value, plus the lane's defaults and the shims on PATH.
func miseEnv(env []string) []string {
	secrets := secretValues(env)
	var out []string
outer:
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == cloneVar || k == oauthVar || !allowedForMise(k) {
			continue
		}
		for _, s := range secrets {
			if strings.Contains(v, s) {
				continue outer
			}
		}
		out = append(out, kv)
	}
	return withToolchainEnv(out)
}

func allowedForMise(k string) bool {
	if miseEnvExact[k] {
		return true
	}
	for _, p := range miseEnvPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func secretValues(env []string) []string {
	m := envMap(env)
	var out []string
	for _, k := range []string{cloneVar, oauthVar} {
		if v := m[k]; v != "" {
			out = append(out, v)
		}
	}
	return out
}

// withToolchainEnv adds what both mise and claude need to agree on which versions are current:
// idiomatic version files enabled (unless the image or operator chose otherwise), non-interactive
// mise, and the shims directory first on PATH. claude gets the same so that `go`, `node`,
// `python` and `java` in its Bash tool resolve to what the runner installed.
func withToolchainEnv(env []string) []string {
	m := envMap(env)
	var add []string
	if _, set := m[idiomaticVar]; !set {
		add = append(add, idiomaticVar+"="+idiomaticTools)
	}
	if _, set := m["MISE_YES"]; !set {
		add = append(add, "MISE_YES=1")
	}
	path := ToolchainPATH(m)
	return append(append(withoutKeys(env, "PATH"), add...), "PATH="+path)
}

// ShimsDir is where mise writes its shims: $MISE_DATA_DIR/shims, /cache/mise/shims in the lane.
func ShimsDir(env map[string]string) string {
	data := env["MISE_DATA_DIR"]
	switch {
	case data != "":
	case env["XDG_DATA_HOME"] != "":
		data = filepath.Join(env["XDG_DATA_HOME"], "mise")
	default:
		data = filepath.Join(env["HOME"], ".local", "share", "mise")
	}
	return filepath.Join(data, "shims")
}

// ToolchainPATH puts the mise shims first and keeps the rest of PATH in order, without a
// duplicate shims entry.
func ToolchainPATH(env map[string]string) string {
	shims := ShimsDir(env)
	parts := []string{shims}
	for _, p := range filepath.SplitList(env["PATH"]) {
		if p != "" && filepath.Clean(p) != shims {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

func miseTimeout(cfg Config, env map[string]string) (time.Duration, error) {
	if cfg.MiseTimeout > 0 {
		return cfg.MiseTimeout, nil
	}
	v := strings.TrimSpace(env[miseTimeoutVar])
	if v == "" {
		return DefaultMiseTimeout, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s=%q is not a positive duration (for example 10m)", miseTimeoutVar, v)
	}
	return d, nil
}

// prepareToolchain trusts the clone's mise config and installs what it pins (or the lane
// defaults from /etc/mise/config.toml). Any failure, including the timeout, fails the step
// before claude starts: an agent that cannot run the tests should not open a PR.
func prepareToolchain(ctx context.Context, cfg Config, dir string, timeout time.Duration, res *Result) error {
	tc := &Toolchain{Cache: "unknown"}
	res.Toolchain = tc
	env := miseEnv(cfg.Env)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	timedOut := func() bool { return errors.Is(ctx.Err(), context.DeadlineExceeded) }

	if out, err := mise(ctx, cfg, env, dir, "trust", dir); err != nil {
		if timedOut() {
			tc.TimedOut = true
			return fmt.Errorf("mise trust timed out after %s", timeout)
		}
		return fmt.Errorf("mise trust: %w: %s", err, lastLines(out, 5))
	}

	// What is missing before the install says whether /cache served this run. A failure here
	// only loses the measurement, so it does not fail the step.
	if out, err := mise(ctx, cfg, env, dir, "ls", "--current", "--missing", "--json"); err == nil {
		if missing, ok := parseMissing(out); ok {
			tc.Missing = missing
			tc.Cache = "miss"
			if len(missing) == 0 {
				tc.Cache = "hit"
			}
		}
	}

	args := []string{"install"}
	if _, err := os.Stat(filepath.Join(dir, "mise.lock")); err == nil {
		tc.Locked = true
		args = append(args, "--locked")
	}
	start := time.Now()
	out, err := mise(ctx, cfg, env, dir, args...)
	tc.InstallMS = time.Since(start).Milliseconds()
	if err == nil {
		return nil
	}
	if timedOut() {
		tc.TimedOut = true
		what := "the repo's tools"
		if len(tc.Missing) > 0 {
			what = strings.Join(tc.Missing, ", ")
		}
		return fmt.Errorf("mise install timed out after %s installing %s", timeout, what)
	}
	tc.Failed = failedTools(out)
	what := "a tool"
	if len(tc.Failed) > 0 {
		what = strings.Join(tc.Failed, ", ")
	}
	return fmt.Errorf("mise install failed for %s: %w: %s", what, err, lastLines(out, 5))
}

// mise runs one mise command and returns its combined output, also streamed to the log so an
// operator can watch a slow first install.
func mise(ctx context.Context, cfg Config, env []string, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, cfg.Mise, args...)
	cmd.Env, cmd.Dir = env, dir
	// A killed mise can leave children holding the pipes; do not wait on them forever.
	cmd.WaitDelay = 5 * time.Second
	var buf bytes.Buffer
	var w io.Writer = &buf
	if cfg.Log != nil && args[0] == "install" {
		w = io.MultiWriter(&buf, cfg.Log)
	}
	cmd.Stdout, cmd.Stderr = w, w
	if args[0] == "ls" {
		cmd.Stderr = io.Discard // keep warnings out of the JSON
	}
	err := cmd.Run()
	return scrub(buf.String(), cfg.Env), err
}

// parseMissing reads `mise ls --current --missing --json`: tool -> [{version}, ...].
func parseMissing(out string) ([]string, bool) {
	var m map[string][]struct {
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(out), &m) != nil {
		return nil, false
	}
	var tools []string
	for tool, vs := range m {
		for _, v := range vs {
			tools = append(tools, tool+"@"+v.Version)
		}
	}
	sort.Strings(tools)
	return tools, true
}

// failedTools names the tools mise reported as failed, from its per-tool "✗ tool@version" lines
// or, failing that, its "Failed to install backend:tool@version:" error.
func failedTools(out string) []string {
	seen := map[string]bool{}
	var tools []string
	for _, re := range []*regexp.Regexp{miseFailedRE, miseErrToolRE} {
		for _, m := range re.FindAllStringSubmatch(out, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				tools = append(tools, m[1])
			}
		}
		if len(tools) > 0 {
			break
		}
	}
	return tools
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
