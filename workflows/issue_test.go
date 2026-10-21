package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/runner"
)

var limits = config.Limits{Global: 2, PerRepo: 1, PerAccount: 1}

func TestDefineMeetsADR0023(t *testing.T) {
	s := Define(limits, 30*time.Minute)
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range s.Caps {
		got = append(got, c.Expression)
	}
	if strings.Join(got, ",") != "'global',input.lane,input.repo,input.account" {
		t.Errorf("caps: %v", got)
	}
	if s.Caps[0].Max != 2 || s.Caps[2].Max != 1 || s.Caps[3].Max != 1 || s.Caps[1].MaxExpression != "input.lane_cap" {
		t.Errorf("cap limits: %+v", s.Caps)
	}
	if s.ScheduleTimeout < MinScheduleTimeout || s.ExecutionTimeout != 30*time.Minute {
		t.Errorf("timeouts: schedule %s execution %s", s.ScheduleTimeout, s.ExecutionTimeout)
	}
	if len(s.Events) != 1 || s.Events[0] != EventIssue {
		t.Errorf("events: %v", s.Events)
	}
}

// A workflow with fewer caps or the 5-minute default drops queued work without telling anyone
// (ADR 0023), so Register must refuse every such spec before touching Hatchet.
func TestRegisterRefusesAnUnsafeSpec(t *testing.T) {
	good := func() Spec { return Define(limits, time.Hour) }
	cases := map[string]func(*Spec){
		"no caps":               func(s *Spec) { s.Caps = nil },
		"three caps":            func(s *Spec) { s.Caps = s.Caps[:3] },
		"five caps":             func(s *Spec) { s.Caps = append(s.Caps, Cap{Expression: "input.x", Max: 1}) },
		"wrong expression":      func(s *Spec) { s.Caps[2].Expression = "input.repository" },
		"swapped caps":          func(s *Spec) { s.Caps[1], s.Caps[2] = s.Caps[2], s.Caps[1] },
		"zero limit":            func(s *Spec) { s.Caps[0].Max = 0 },
		"no schedule timeout":   func(s *Spec) { s.ScheduleTimeout = 0 },
		"default 5 minute wait": func(s *Spec) { s.ScheduleTimeout = 5 * time.Minute },
		"no execution timeout":  func(s *Spec) { s.ExecutionTimeout = 0 },
		"no event":              func(s *Spec) { s.Events = nil },
		"no name":               func(s *Spec) { s.Name = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := good()
			mutate(&s)
			wf, err := Register(nil, s, Handler{}) // a nil client proves it is refused before any SDK call
			if err == nil || wf != nil || !strings.Contains(err.Error(), "refusing to register") {
				t.Fatalf("Register = %v, %v; want a refusal", wf, err)
			}
		})
	}
}

func cfg() config.Config {
	return config.Config{Accounts: map[string]string{"claude": "claude-subscription"}, LaneMaxRuns: map[string]int{"claude": 2}, Limits: limits}
}

func TestMapIssueLooksUpTheCaps(t *testing.T) {
	in, err := MapIssue(cfg(), "a/b", "main", "claude", runner.Issue{Number: 3, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if in.Account != "claude-subscription" || in.LaneCap != 2 || in.Repo != "a/b" || in.Lane != "claude" {
		t.Errorf("input: %+v", in)
	}
	if _, err := MapIssue(cfg(), "a/b", "main", "nope", runner.Issue{}); err == nil {
		t.Error("an unknown lane must be an error")
	}
	// The expressions read these exact JSON keys.
	b, _ := json.Marshal(in)
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"repo", "lane", "account", "lane_cap"} {
		if _, ok := m[k]; !ok {
			t.Errorf("input JSON lacks %q, which a cap expression reads: %s", k, b)
		}
	}
}

type fakeLauncher struct {
	res launcher.Result
	err error
	got launcher.Step
}

func (f *fakeLauncher) Launch(_ context.Context, s launcher.Step) (launcher.Result, error) {
	f.got = s
	return f.res, f.err
}
func (f *fakeLauncher) Cancel(context.Context, string, string) error { return nil }

type fakePub struct {
	out pusher.Outcome
	err error
	n   int
}

func (f *fakePub) Push(context.Context, pusher.Request) (pusher.Outcome, error) {
	f.n++
	return f.out, f.err
}

func result(status string) launcher.Result {
	b, _ := json.Marshal(runner.Result{Status: status, Branch: "satat/x", Commits: 1, BaseSHA: "b", HeadSHA: "h"})
	return launcher.Result{Files: map[string][]byte{"result.json": b, "changes.bundle": []byte("x")}}
}

var input = Input{Repo: "a/b", Base: "main", Lane: "claude", Issue: runner.Issue{Number: 1, Title: "t"}}

func TestHandlerPublishesAndReports(t *testing.T) {
	l, p := &fakeLauncher{res: result("changes")}, &fakePub{out: pusher.Outcome{PRURL: "https://x/pull/1"}}
	out, err := Handler{Launcher: l, Pusher: p}.Run(context.Background(), "run-1", input)
	if err != nil || out.PRURL != "https://x/pull/1" || out.Status != "changes" || out.Branch != "satat/x" || p.n != 1 {
		t.Fatalf("out %+v err %v pushes %d", out, err, p.n)
	}
	if l.got.Run != "run-1" || l.got.Lane != "claude" {
		t.Errorf("step identity: %+v", l.got)
	}
}

func TestNoChangesIsASuccess(t *testing.T) {
	out, err := Handler{Launcher: &fakeLauncher{res: result("no_changes")}, Pusher: &fakePub{}}.Run(context.Background(), "r", input)
	if err != nil || out.Status != "no_changes" {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestFailuresFailTheRun(t *testing.T) {
	timedOut := result("changes")
	timedOut.TimedOut = true
	oom := result("changes")
	oom.OOMKilled = true
	crashed := launcher.Result{ExitCode: 137}
	failed := result("failed")
	failed.ExitCode = 1
	cases := map[string]struct {
		l    *fakeLauncher
		p    *fakePub
		want string
	}{
		"refused by the pusher": {&fakeLauncher{res: result("changes")}, &fakePub{err: &pusher.Refusal{Reason: "workflow"}}, "not published"},
		"timed out":             {&fakeLauncher{res: timedOut}, &fakePub{}, "timed out"},
		"oom":                   {&fakeLauncher{res: oom}, &fakePub{}, "out of memory"},
		"no result":             {&fakeLauncher{res: crashed}, &fakePub{}, "left no result"},
		"step failed":           {&fakeLauncher{res: failed}, &fakePub{}, "step failed"},
		"launcher error":        {&fakeLauncher{err: errors.New("docker down")}, &fakePub{}, "docker down"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Handler{Launcher: tc.l, Pusher: tc.p}.Run(context.Background(), "r", input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}
