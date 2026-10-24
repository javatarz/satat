package config

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/launcher/docker"
)

const digest = "busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"

func lane(extra string) string {
	return "limits:\n  global: 2\n  per_repo: 1\n  per_account: 1\nlanes:\n  a:\n    image: " + digest + "\n    memory: 512MiB\n    cpus: 1\n    pids: 64\n    workspace: 64MiB\n" + extra
}

func TestParsesALane(t *testing.T) {
	c, err := Parse(strings.NewReader(lane("    timeout: 90s\n    internal: true\n    pass_env: [FOO]\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Lanes) != 1 {
		t.Fatalf("want 1 lane, got %d", len(c.Lanes))
	}
	l := c.Lanes[0]
	if l.Name != "a" || l.Image != digest || l.MemoryBytes != 512<<20 || l.WorkspaceBytes != 64<<20 ||
		l.CPUs != 1 || l.PidsLimit != 64 || l.Timeout != 90*time.Second || !l.Internal ||
		len(l.PassEnv) != 1 || l.PassEnv[0] != "FOO" {
		t.Fatalf("unexpected lane: %+v", l)
	}
}

func TestLimitsAndLaneCaps(t *testing.T) {
	c, err := Parse(strings.NewReader(lane("    account: shared\n    max_runs: 3\n")))
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits != (Limits{Global: 2, PerRepo: 1, PerAccount: 1}) || c.Accounts["a"] != "shared" || c.LaneMaxRuns["a"] != 3 {
		t.Fatalf("unexpected: %+v %v %v", c.Limits, c.Accounts, c.LaneMaxRuns)
	}
	c, err = Parse(strings.NewReader(lane("")))
	if err != nil || c.Accounts["a"] != "a" || c.LaneMaxRuns["a"] != 1 {
		t.Fatalf("defaults: %v %v %v err %v", c.Accounts, c.LaneMaxRuns, c.Limits, err)
	}
}

func TestLanesComeBackInNameOrder(t *testing.T) {
	y := lane("") + "  c:\n    image: " + digest + "\n    memory: 512MiB\n    cpus: 1\n    pids: 64\n    workspace: 64MiB\n" +
		"  b:\n    image: " + digest + "\n    memory: 512MiB\n    cpus: 1\n    pids: 64\n    workspace: 64MiB\n"
	c, err := Parse(strings.NewReader(y))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, l := range c.Lanes {
		got += l.Name
	}
	if got != "abc" {
		t.Fatalf("lanes out of order: %s", got)
	}
}

func TestRefusesBadFiles(t *testing.T) {
	cases := map[string]struct{ yaml, want string }{
		"empty":             {"", "empty file"},
		"no lanes":          {"lanes: {}\n", "no lanes"},
		"unknown key":       {lane("    memroy: 1GiB\n"), "memroy"},
		"unknown top key":   {lane("") + "extra: 1\n", "extra"},
		"missing memory":    {strings.Replace(lane(""), "    memory: 512MiB\n", "", 1), "memory: required"},
		"bare number size":  {strings.Replace(lane(""), "512MiB", "536870912", 1), "not a size"},
		"decimal unit":      {strings.Replace(lane(""), "512MiB", "512MB", 1), "not a size"},
		"zero size":         {strings.Replace(lane(""), "512MiB", "0MiB", 1), "positive"},
		"overflow size":     {strings.Replace(lane(""), "512MiB", "99999999999999GiB", 1), "too large"},
		"bad timeout":       {lane("    timeout: soon\n"), "timeout"},
		"negative timeout":  {lane("    timeout: -5m\n"), "timeout"},
		"not yaml":          {"lanes: [", "yaml"},
		"no limits":         {strings.Replace(lane(""), "limits:\n  global: 2\n  per_repo: 1\n  per_account: 1\n", "", 1), "limits"},
		"zero limit":        {strings.Replace(lane(""), "global: 2", "global: 0", 1), "limits"},
		"negative lane cap": {lane("    max_runs: -1\n"), "max_runs"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.yaml))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// Parsing accepts anything well-formed; the launcher is what refuses a lane that breaks the
// hardening floor. This pins that the two stay connected.
func TestLauncherRefusesLanesThatBreakTheFloor(t *testing.T) {
	cases := map[string]string{
		"root user":          lane("    user: \"0:0\"\n"),
		"named user":         lane("    user: root\n"),
		"unknown runtime":    lane("    runtime: kata\n"),
		"bad env name":       lane("    pass_env: [\"A B\"]\n"),
		"workspace ≥ memory": strings.Replace(lane(""), "workspace: 64MiB", "workspace: 600MiB", 1),
		"unpinned image":     strings.Replace(lane(""), digest, "busybox:latest", 1),
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := Parse(strings.NewReader(y))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := docker.New(nil, c.Lanes); err == nil {
				t.Fatal("launcher accepted a lane that breaks the floor")
			}
		})
	}
}

// The satat.yaml in the repo must load and pass the launcher's checks, so a bad edit fails CI
// instead of the first launch.
func TestRepoConfigLoadsAndPassesTheFloor(t *testing.T) {
	c, err := Load("../satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docker.New(nil, c.Lanes); err != nil {
		t.Fatal(err)
	}
}

// The same file, run for real: load it, set the lane up and launch a step in it.
func TestRepoLaneLaunches(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	api, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("no Docker client: %v", err)
	}
	defer api.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := api.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("Docker is not reachable: %v", err)
	}
	c, err := Load("../satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Only the smoke lane: the other lanes' images are built locally and may not exist here.
	var lanes []launcher.Lane
	for _, ln := range c.Lanes {
		if ln.Name == "smoke" {
			lanes = append(lanes, ln)
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
		_, _ = api.NetworkRemove(context.Background(), "satat-lane-smoke", client.NetworkRemoveOptions{})
	})
	res, err := l.Launch(ctx, launcher.Step{Run: "cfgtest", Name: "smoke", Lane: "smoke"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(string(res.Stdout), "uid=10001") || !strings.Contains(string(res.Stdout), "ok") {
		t.Fatalf("unexpected result: exit %d, stdout %q, stderr %q", res.ExitCode, res.Stdout, res.Stderr)
	}
}

// The real Claude lane image, under the real floor, with dummy credentials and an input the
// runner rejects: proves the image starts as a non-root user on a read-only root and that its
// result file comes back. Skipped when the image has not been built (scripts/build-claude-image.sh).
func TestClaudeImageStartsUnderTheFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	api, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("no Docker client: %v", err)
	}
	defer api.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := api.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("Docker is not reachable: %v", err)
	}
	c, err := Load("../satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var lanes []launcher.Lane
	for _, ln := range c.Lanes {
		if ln.Name == "claude" {
			lanes = append(lanes, ln)
		}
	}
	if _, err := api.ImageInspect(ctx, lanes[0].Image); err != nil {
		t.Skipf("claude image not built locally: %v", err)
	}
	dummy := func(string) (string, bool) { return "dummy-not-a-real-credential", true }
	l, err := docker.New(api, lanes, docker.WithEnv(dummy))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = api.NetworkRemove(context.Background(), "satat-lane-claude", client.NetworkRemoveOptions{})
	})
	res, err := l.Launch(ctx, launcher.Step{Run: "imgtest", Name: "claude", Lane: "claude", Input: []byte(`{"repo":"--bad","base":"main","issue":{"number":1,"title":"t"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 1 || !strings.Contains(string(res.Stderr), "not owner/name") {
		t.Fatalf("exit %d, stderr %q: want the runner's input refusal", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(string(res.Files["result.json"]), `"status": "failed"`) {
		t.Fatalf("result.json missing or wrong: %v", res.Files)
	}
}

func parseErr(t *testing.T, y string) string {
	t.Helper()
	_, err := Parse(strings.NewReader(y))
	if err == nil {
		t.Fatal("want an error")
	}
	return err.Error()
}

func TestDeployDefaultsWhenAbsent(t *testing.T) {
	c, err := Parse(strings.NewReader(lane("    pass_env: [FOO]\n")))
	if err != nil {
		t.Fatal(err)
	}
	if c.Deploy.Target != "compose" || c.Deploy.Secrets != nil {
		t.Fatalf("unexpected deploy: %+v", c.Deploy)
	}
}

func TestDeployRefusesUnknownTarget(t *testing.T) {
	msg := parseErr(t, lane("")+"deploy:\n  target: kubernetes\n  secrets:\n    SATAT_PUSH_TOKEN: env\n")
	if !strings.Contains(msg, "target") {
		t.Fatalf("error should name target: %s", msg)
	}
}

func TestDeployNeedsEverySecretMapped(t *testing.T) {
	y := lane("    pass_env: [CLAUDE_CODE_OAUTH_TOKEN]\n")
	msg := parseErr(t, y+"deploy:\n  target: compose\n  secrets:\n    SATAT_PUSH_TOKEN: env\n")
	if !strings.Contains(msg, "CLAUDE_CODE_OAUTH_TOKEN") || !strings.Contains(msg, `"a"`) {
		t.Fatalf("error should name the variable and the lane: %s", msg)
	}
	msg = parseErr(t, y+"deploy:\n  target: compose\n  secrets:\n    CLAUDE_CODE_OAUTH_TOKEN: env\n")
	if !strings.Contains(msg, "SATAT_PUSH_TOKEN") || !strings.Contains(msg, "worker") {
		t.Fatalf("error should name the variable and the worker: %s", msg)
	}
}

func TestDeployRefusesBadSecretRef(t *testing.T) {
	msg := parseErr(t, lane("")+"deploy:\n  target: compose\n  secrets:\n    SATAT_PUSH_TOKEN: plain-value\n")
	if !strings.Contains(msg, "op://") {
		t.Fatalf("error should mention op://: %s", msg)
	}
	if strings.Contains(msg, "plain-value") {
		t.Fatalf("error must not echo the value: %s", msg)
	}
}

func TestDeployRefusesUnusedSecret(t *testing.T) {
	msg := parseErr(t, lane("")+"deploy:\n  target: compose\n  secrets:\n    SATAT_PUSH_TOKEN: env\n    STRAY: env\n")
	if !strings.Contains(msg, "STRAY") {
		t.Fatalf("error should name the unused entry: %s", msg)
	}
}

func TestDeployParsesAndListsSecretNames(t *testing.T) {
	y := lane("    pass_env: [FOO, BAR]\n") + "deploy:\n  target: compose\n  secrets:\n    FOO: env\n    BAR: op://V/i\n    SATAT_PUSH_TOKEN: env\n"
	c, err := Parse(strings.NewReader(y))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.SecretNames(), ","); got != "BAR,FOO,SATAT_PUSH_TOKEN" {
		t.Fatalf("SecretNames = %s", got)
	}
	if c.Deploy.Secrets["BAR"] != "op://V/i" || c.Deploy.Secrets["FOO"] != SecretFromEnv {
		t.Fatalf("secrets: %v", c.Deploy.Secrets)
	}
}

// A lane must not pass a variable the worker sets for itself: it would hand the Hatchet token (or
// the worker's Docker or config settings) to an agent, and duplicate a key in compose.yaml.
func TestPassEnvRefusesWorkerEnv(t *testing.T) {
	for _, name := range []string{
		"HATCHET_CLIENT_TOKEN", "HATCHET_CLIENT_HOST_PORT", "HATCHET_CLIENT_TLS_STRATEGY",
		"HATCHET_CLIENT_SERVER_URL", "DOCKER_HOST", "SATAT_CONFIG", "HOME",
	} {
		msg := parseErr(t, lane("    pass_env: [FOO, "+name+"]\n"))
		if !strings.Contains(msg, name) || !strings.Contains(msg, `"a"`) || !strings.Contains(msg, "worker") {
			t.Errorf("%s: error should name the variable, the lane and the worker: %s", name, msg)
		}
	}
}
