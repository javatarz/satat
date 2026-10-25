package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/workflows"
)

// fakeHatchet is an in-memory hatchetAPI. Push adds an agent run row, as Hatchet would.
type fakeHatchet struct {
	pushed    []any
	pushEvent string
	rows      []RunRow
	outputs   map[string]*workflows.Output
	cancelled []string
	errFor    string   // the error message Get reports on the run, as Hatchet does for a failed task
	states    []string // successive states Get reports for a pushed run, the last one repeating
	getCalls  int
}

func (f *fakeHatchet) Push(_ context.Context, event string, in any) error {
	f.pushEvent = event
	f.pushed = append(f.pushed, in)
	nonce := ""
	if in, ok := in.(workflows.Input); ok {
		nonce = in.Nonce
	}
	f.rows = append(f.rows, RunRow{ID: "run-new", Kind: "agent", Lane: "smoke", State: "QUEUED", Started: time.Now(), Nonce: nonce})
	return nil
}

func (f *fakeHatchet) List(context.Context, ListFilter) ([]RunRow, error) { return f.rows, nil }

func (f *fakeHatchet) Get(_ context.Context, id string) (RunRow, *workflows.Output, error) {
	for _, r := range f.rows {
		if r.ID != id {
			continue
		}
		if len(f.states) > 0 {
			i := f.getCalls
			if i >= len(f.states) {
				i = len(f.states) - 1
			}
			r.State = f.states[i]
			f.getCalls++
		}
		r.Error = f.errFor
		return r, f.outputs[id], nil
	}
	return RunRow{}, nil, errNoSuchRun
}

func (f *fakeHatchet) Cancel(_ context.Context, id string) error {
	f.cancelled = append(f.cancelled, id)
	return nil
}

var fast = pollTimings{Lookup: time.Millisecond, LookupTimeout: time.Second, Wait: time.Millisecond}

func testConfig() config.Config {
	return config.Config{
		Accounts:    map[string]string{"smoke": "none"},
		LaneMaxRuns: map[string]int{"smoke": 1, "claude": 1},
		NoAgent:     map[string]bool{"smoke": true},
	}
}

// The CLI refuses an agent lane before it pushes anything.
func TestRunRefusesAnAgentLaneWithoutPushing(t *testing.T) {
	f := &fakeHatchet{}
	cfg := testConfig()
	cfg.Accounts["claude"] = "claude-subscription"
	var out bytes.Buffer
	err := agentRun(context.Background(), f, cfg, "claude", nil, true, &out, fast)
	if err == nil || !strings.Contains(err.Error(), `"claude"`) || !strings.Contains(err.Error(), "agent: false") {
		t.Fatalf("err %v", err)
	}
	if len(f.pushed) != 0 {
		t.Errorf("pushed %d events", len(f.pushed))
	}
}

// A refusal recorded by the worker (an event pushed by something other than this CLI) is printed
// with its reason and exits non-zero.
func TestWaitPrintsARefusal(t *testing.T) {
	f := &fakeHatchet{rows: []RunRow{{ID: "r1", State: "COMPLETED"}}, outputs: map[string]*workflows.Output{
		"r1": {Run: "r1", Kind: workflows.KindAgent, Status: workflows.StatusRefused, Reason: "lane \"claude\" runs an agent"},
	}}
	var out bytes.Buffer
	err := followRun(context.Background(), f, "r1", &out, time.Millisecond)
	var code exitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err %v, want exit 1", err)
	}
	if !strings.Contains(out.String(), "status: refused") || !strings.Contains(out.String(), "reason: lane \"claude\" runs an agent") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestRunPushesAgentEvent(t *testing.T) {
	// Another run of the same lane is there already, even one stamped later than the push; only the
	// nonce says which run is ours.
	f := &fakeHatchet{rows: []RunRow{{ID: "run-old", Kind: "agent", Lane: "smoke", State: "RUNNING", Started: time.Now().Add(time.Hour), Nonce: "other"}}}
	var out bytes.Buffer
	err := agentRun(context.Background(), f, testConfig(), "smoke", []byte(`{"files":{"a":"b"}}`), false, &out, fast)
	if err != nil {
		t.Fatal(err)
	}
	if f.pushEvent != workflows.EventAgent {
		t.Fatalf("event = %q, want %q", f.pushEvent, workflows.EventAgent)
	}
	raw, _ := json.Marshal(f.pushed[0])
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if n, _ := got["nonce"].(string); len(n) != 32 {
		t.Fatalf("nonce = %q, want 32 hex characters", n)
	}
	if got["kind"] != "agent" || got["repo"] != "adhoc" || got["lane"] != "smoke" {
		t.Fatalf("pushed input = %s", raw)
	}
	step, _ := json.Marshal(got["step_input"])
	if string(step) != `{"files":{"a":"b"}}` {
		t.Fatalf("step_input = %s", step)
	}
	want := "queued smoke: run lookup pending\nrun-new\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

// twoRunsFake lists two same-lane runs, newest first, whose nonces match the pushed input only
// for the older one.
type twoRunsFake struct{ fakeHatchet }

func (f *twoRunsFake) Push(ctx context.Context, event string, in any) error {
	nonce := in.(workflows.Input).Nonce
	f.rows = []RunRow{
		{ID: "theirs", Kind: "agent", Lane: "smoke", State: "QUEUED", Started: time.Now().Add(time.Second), Nonce: "someone-else"},
		{ID: "ours", Kind: "agent", Lane: "smoke", State: "QUEUED", Started: time.Now(), Nonce: nonce},
	}
	return nil
}

func TestRunFindsOwnRunByNonce(t *testing.T) {
	f := &twoRunsFake{}
	var out bytes.Buffer
	if err := agentRun(context.Background(), f, testConfig(), "smoke", nil, false, &out, fast); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "ours\n") {
		t.Fatalf("output = %q, want the run with our nonce", out.String())
	}
}

func TestRunWaitExitCode(t *testing.T) {
	cases := []struct {
		name     string
		out      *workflows.Output
		state    string
		errMsg   string
		wantCode int
		wantText string
	}{
		{"ok", &workflows.Output{Status: "ok", Stdout: "hello"}, "COMPLETED", "", 0, "status: ok"},
		{"agent exit code", &workflows.Output{Status: "failed", ExitCode: 3, Stderr: "boom"}, "COMPLETED", "", 3, "exit: 3"},
		{"oom", &workflows.Output{Status: "oom", ExitCode: 137, OOMKilled: true}, "COMPLETED", "", 137, "status: oom"},
		{"cancelled", &workflows.Output{Status: "cancelled", ExitCode: 137, Cancelled: true}, "COMPLETED", "", 1, "status: cancelled"},
		{"timed out", &workflows.Output{Status: "timed_out", ExitCode: 137, TimedOut: true}, "COMPLETED", "", 1, "status: timed_out"},
		{"launch error has no output", nil, "FAILED", "launching: image pull failed", 1, "image pull failed"},
		{"cancelled in Hatchet has no output", nil, "CANCELLED", "", 1, "CANCELLED"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeHatchet{outputs: map[string]*workflows.Output{"run-new": c.out}, states: []string{"RUNNING", c.state}}
			f.errFor = c.errMsg
			var out bytes.Buffer
			err := agentRun(context.Background(), f, testConfig(), "smoke", nil, true, &out, fast)
			code := 0
			var ec exitCode
			switch {
			case errors.As(err, &ec):
				code = int(ec)
			case err != nil:
				t.Fatal(err)
			}
			if code != c.wantCode {
				t.Fatalf("exit = %d, want %d\n%s", code, c.wantCode, out.String())
			}
			if !strings.Contains(out.String(), c.wantText) {
				t.Fatalf("output missing %q:\n%s", c.wantText, out.String())
			}
		})
	}
}

func TestLsShowsOnlyActive(t *testing.T) {
	now := time.Now()
	f := &fakeHatchet{rows: []RunRow{
		{ID: "r-run", Kind: "agent", Lane: "sleep", State: "RUNNING", Started: now.Add(-90 * time.Second)},
		{ID: "r-queued", Kind: "agent", Lane: "sleep", State: "QUEUED", Started: now.Add(-5 * time.Second)},
		{ID: "r-done", Kind: "agent", Lane: "smoke", State: "COMPLETED", Started: now.Add(-time.Hour)},
		{ID: "r-failed", Kind: "issue", Lane: "claude", State: "FAILED", Started: now.Add(-time.Hour)},
		{ID: "r-cancelled", Kind: "agent", Lane: "sleep", State: "CANCELLED", Started: now.Add(-time.Hour)},
	}}
	var out bytes.Buffer
	if err := agentLs(context.Background(), f, &out, now); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"ID", "KIND", "LANE", "STATE", "AGE", "r-run", "RUNNING", "1m", "r-queued", "QUEUED"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	for _, not := range []string{"r-done", "r-failed", "r-cancelled"} {
		if strings.Contains(s, not) {
			t.Errorf("output lists finished run %q:\n%s", not, s)
		}
	}
}

func TestRmUnknownFinishedAndRunning(t *testing.T) {
	f := &fakeHatchet{rows: []RunRow{
		{ID: "done", State: "COMPLETED"},
		{ID: "live", State: "RUNNING"},
		{ID: "wait", State: "QUEUED"},
	}}
	var out bytes.Buffer

	// Unknown: an error that says so, and nothing is cancelled.
	err := agentRm(context.Background(), f, "nope", &out)
	if err == nil || !strings.Contains(err.Error(), "no such run") {
		t.Fatalf("unknown run: err = %v, want no such run", err)
	}

	// Finished: success, "not running (STATE)", nothing cancelled.
	out.Reset()
	if err := agentRm(context.Background(), f, "done", &out); err != nil {
		t.Fatalf("finished run: %v", err)
	}
	if strings.TrimSpace(out.String()) != "not running (COMPLETED)" {
		t.Fatalf("finished run output = %q", out.String())
	}
	if len(f.cancelled) != 0 {
		t.Fatalf("cancelled %v for unknown or finished runs", f.cancelled)
	}

	// Running and queued: cancelled.
	for _, id := range []string{"live", "wait"} {
		out.Reset()
		if err := agentRm(context.Background(), f, id, &out); err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out.String()) != "cancelled" {
			t.Fatalf("%s output = %q", id, out.String())
		}
	}
	if len(f.cancelled) != 2 || f.cancelled[0] != "live" || f.cancelled[1] != "wait" {
		t.Fatalf("cancelled = %v", f.cancelled)
	}
}

// The agent commands default the Hatchet client env for a Compose stack on this machine, reading
// the token from the env file deploy up wrote. No value is ever printed, here or by the code.
func TestHatchetEnvDefaults(t *testing.T) {
	const fileToken = "eyJ-from-env-file"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("POSTGRES_PASSWORD=p\nHATCHET_CLIENT_TOKEN="+fileToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "satat.yaml")
	compose := config.Config{Deploy: config.Deploy{Target: "compose"}}
	none := func(string) (string, bool) { return "", false }

	t.Run("compose with nothing set", func(t *testing.T) {
		got, err := hatchetEnv(compose, configPath, none)
		if err != nil {
			t.Fatal(err)
		}
		if got["HATCHET_CLIENT_TOKEN"] != fileToken {
			t.Error("HATCHET_CLIENT_TOKEN was not read from the env file")
		}
		for k, want := range map[string]string{
			"HATCHET_CLIENT_HOST_PORT":    "localhost:7077",
			"HATCHET_CLIENT_TLS_STRATEGY": "none",
			"HATCHET_CLIENT_SERVER_URL":   "http://localhost:8888",
		} {
			if got[k] != want {
				t.Errorf("%s = %q, want %q", k, got[k], want)
			}
		}
	})

	t.Run("set values win", func(t *testing.T) {
		set := map[string]string{
			"HATCHET_CLIENT_TOKEN":        "eyJ-from-shell",
			"HATCHET_CLIENT_HOST_PORT":    "h:1",
			"HATCHET_CLIENT_TLS_STRATEGY": "tls",
			"HATCHET_CLIENT_SERVER_URL":   "https://h",
		}
		got, err := hatchetEnv(compose, configPath, func(k string) (string, bool) { v, ok := set[k]; return v, ok })
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("overrides set env: %d keys", len(got))
		}
	})

	t.Run("other target gets only the token", func(t *testing.T) {
		got, err := hatchetEnv(config.Config{}, configPath, none)
		if err != nil {
			t.Fatal(err)
		}
		if got["HATCHET_CLIENT_TOKEN"] != fileToken {
			t.Error("HATCHET_CLIENT_TOKEN was not read from the env file")
		}
		if len(got) != 1 {
			t.Errorf("got %d keys, want only the token", len(got))
		}
	})

	t.Run("no env file", func(t *testing.T) {
		got, err := hatchetEnv(compose, filepath.Join(t.TempDir(), "satat.yaml"), none)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got["HATCHET_CLIENT_TOKEN"]; ok {
			t.Error("a token was set with no env file")
		}
	})
}
