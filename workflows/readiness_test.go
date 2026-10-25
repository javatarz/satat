package workflows

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/readiness"
)

// passes is a readiness check every repo passes.
type passes struct{}

func (passes) Check(context.Context, readiness.Request) (readiness.Result, error) {
	return readiness.Result{}, nil
}

type fakeCheck struct {
	res readiness.Result
	err error
	got []readiness.Request
}

func (f *fakeCheck) Check(_ context.Context, r readiness.Request) (readiness.Result, error) {
	f.got = append(f.got, r)
	return f.res, f.err
}

type fakeReporter struct {
	got []pusher.NotReadyRequest
	out pusher.NotReadyOutcome
	err error
}

func (f *fakeReporter) NotReady(_ context.Context, r pusher.NotReadyRequest) (pusher.NotReadyOutcome, error) {
	f.got = append(f.got, r)
	return f.out, f.err
}

var failing = readiness.Result{Repo: "o/r", Failures: []readiness.Failure{
	{Check: "commands", Problem: "missing.", Fix: "add it."},
	{Check: "token reach", Problem: "too wide.", Fix: "narrow it."},
}}

func TestAFailingRepoIsNotStartedAndIsReportedOnce(t *testing.T) {
	l, p, rep := &fakeLauncher{res: result("changes")}, &fakePub{}, &fakeReporter{}
	chk := &fakeCheck{res: failing}
	out, err := Handler{Cloner: fakeCloner{}, Launcher: l, Pusher: p, Issues: ready(), Allowed: listed, Ready: chk, Reporter: rep}.Run(context.Background(), "r", input)
	if err != nil || out.Status != StatusNotReady || !strings.Contains(out.Reason, "2 blocking") {
		t.Fatalf("out %+v err %v", out, err)
	}
	if l.n != 0 || p.n != 0 {
		t.Errorf("launched %d, pushed %d; want neither", l.n, p.n)
	}
	if len(rep.got) != 1 {
		t.Fatalf("reported %d times; want once", len(rep.got))
	}
	r := rep.got[0]
	if r.Repo != input.Repo || r.Issue != input.Issue.Number || r.Key != failing.Key() || r.Body != failing.Comment() {
		t.Errorf("report: %+v", r)
	}
	if want := (readiness.Request{Repo: input.Repo, Base: input.Base, Issue: input.Issue.Number}); chk.got[0] != want {
		t.Errorf("checked %+v, want %+v", chk.got[0], want)
	}
}

func TestAReadyRepoLaunchesAndReportsNothing(t *testing.T) {
	l, rep := &fakeLauncher{res: result("no_changes")}, &fakeReporter{}
	out, err := Handler{Cloner: fakeCloner{}, Launcher: l, Pusher: &fakePub{}, Issues: ready(), Allowed: listed, Ready: passes{}, Reporter: rep}.Run(context.Background(), "r", input)
	if err != nil || out.Status != "no_changes" || l.n != 1 || len(rep.got) != 0 {
		t.Fatalf("out %+v err %v launches %d reports %d", out, err, l.n, len(rep.got))
	}
}

func TestTheCheckOnlyRunsForAnIssueThatPassedTheGate(t *testing.T) {
	gh := ready()
	gh.st.Labels = nil
	chk := &fakeCheck{res: failing}
	out, _ := Handler{Cloner: fakeCloner{}, Launcher: &fakeLauncher{}, Pusher: &fakePub{}, Issues: gh, Allowed: listed, Ready: chk, Reporter: &fakeReporter{}}.Run(context.Background(), "r", input)
	if out.Status != StatusSkipped || len(chk.got) != 0 {
		t.Fatalf("out %+v checks %d; an unlabelled issue is skipped before any repo check", out, len(chk.got))
	}
}

func TestACheckThatCannotRunFailsTheRunAndLeavesTheIssueAlone(t *testing.T) {
	l, rep := &fakeLauncher{res: result("changes")}, &fakeReporter{}
	out, err := Handler{Cloner: fakeCloner{}, Launcher: l, Pusher: &fakePub{}, Issues: ready(), Allowed: listed, Ready: &fakeCheck{err: errors.New("502")}, Reporter: rep}.Run(context.Background(), "r", input)
	if err == nil || !strings.Contains(err.Error(), "not launched") || out.Status == StatusNotReady {
		t.Fatalf("out %+v err %v", out, err)
	}
	if l.n != 0 || len(rep.got) != 0 {
		t.Errorf("launched %d, reported %d; want neither", l.n, len(rep.got))
	}
}

func TestAReportThatFailsFailsTheRun(t *testing.T) {
	l := &fakeLauncher{res: result("changes")}
	out, err := Handler{Cloner: fakeCloner{}, Launcher: l, Pusher: &fakePub{}, Issues: ready(), Allowed: listed, Ready: &fakeCheck{res: failing}, Reporter: &fakeReporter{err: errors.New("403")}}.Run(context.Background(), "r", input)
	if err == nil || !strings.Contains(err.Error(), "could not be updated") || l.n != 0 {
		t.Fatalf("out %+v err %v launches %d", out, err, l.n)
	}
}

func TestWithoutACheckNothingIsLaunched(t *testing.T) {
	for name, h := range map[string]Handler{
		"no checker":  {Ready: nil, Reporter: &fakeReporter{}},
		"no reporter": {Ready: passes{}},
	} {
		t.Run(name, func(t *testing.T) {
			l := &fakeLauncher{res: result("changes")}
			h.Launcher, h.Pusher, h.Issues, h.Allowed = l, &fakePub{}, ready(), listed
			_, err := h.Run(context.Background(), "r", input)
			if err == nil || l.n != 0 {
				t.Fatalf("err %v launches %d", err, l.n)
			}
		})
	}
}

func TestAStopCancelsAnEarlierAttemptsStep(t *testing.T) {
	l := &fakeLauncher{running: true}
	Handler{Cloner: fakeCloner{}, Launcher: l, Pusher: &fakePub{}, Issues: ready(), Allowed: listed, Ready: &fakeCheck{res: failing}, Reporter: &fakeReporter{}}.Run(context.Background(), "run-9", input)
	if l.running {
		t.Errorf("the step of a run stopped by the readiness check must be cancelled")
	}
}
