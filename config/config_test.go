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
	return "lanes:\n  a:\n    image: " + digest + "\n    memory: 512MiB\n    cpus: 1\n    pids: 64\n    workspace: 64MiB\n" + extra
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
		"empty":            {"", "empty file"},
		"no lanes":         {"lanes: {}\n", "no lanes"},
		"unknown key":      {lane("    memroy: 1GiB\n"), "memroy"},
		"unknown top key":  {lane("") + "extra: 1\n", "extra"},
		"missing memory":   {strings.Replace(lane(""), "    memory: 512MiB\n", "", 1), "memory: required"},
		"bare number size": {strings.Replace(lane(""), "512MiB", "536870912", 1), "not a size"},
		"decimal unit":     {strings.Replace(lane(""), "512MiB", "512MB", 1), "not a size"},
		"zero size":        {strings.Replace(lane(""), "512MiB", "0MiB", 1), "positive"},
		"overflow size":    {strings.Replace(lane(""), "512MiB", "99999999999999GiB", 1), "too large"},
		"bad timeout":      {lane("    timeout: soon\n"), "timeout"},
		"negative timeout": {lane("    timeout: -5m\n"), "timeout"},
		"not yaml":         {"lanes: [", "yaml"},
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
	l, err := docker.New(api, c.Lanes)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Reap(context.Background(), 0)
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
