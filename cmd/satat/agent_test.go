package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	f.rows = append(f.rows, RunRow{ID: "run-new", Kind: "agent", Lane: "smoke", State: "QUEUED", Started: time.Now()})
	return nil
}

func (f *fakeHatchet) List(context.Context) ([]RunRow, error) { return f.rows, nil }

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
		LaneMaxRuns: map[string]int{"smoke": 1},
	}
}

func TestRunPushesAgentEvent(t *testing.T) {
	// An older run of the same lane is there already, even one stamped later than the push (Hatchet's
	// clock may run ahead): the new run is the one that was not listed before.
	f := &fakeHatchet{rows: []RunRow{{ID: "run-old", Kind: "agent", Lane: "smoke", State: "RUNNING", Started: time.Now().Add(time.Hour)}}}
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
