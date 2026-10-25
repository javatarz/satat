package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/runner"
)

type fakeLauncher struct {
	res launcher.Result
	got launcher.Step
}

func (f *fakeLauncher) Launch(_ context.Context, s launcher.Step) (launcher.Result, error) {
	f.got = s
	return f.res, nil
}
func (f *fakeLauncher) Cancel(context.Context, string, string) error { return nil }

type fakePublisher struct {
	req pusher.Request
	n   int
	out pusher.Outcome
	err error
}

func (f *fakePublisher) Push(_ context.Context, r pusher.Request) (pusher.Outcome, error) {
	f.n++
	f.req = r
	return f.out, f.err
}

func result(status string) launcher.Result {
	b, _ := json.Marshal(runner.Result{
		Status: status, BaseSHA: "b", HeadSHA: "h",
		ClaudeResult: json.RawMessage(`{"type":"result","result":"all done"}`),
	})
	return launcher.Result{Files: map[string][]byte{"result.json": b, "changes.bundle": []byte("bundle")}}
}

type fakeCloner struct {
	err    error
	closed bool
}

func (f *fakeCloner) Clone(context.Context, string, string) (*Checkout, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &Checkout{
		Tar: strings.NewReader("tar"), BaseSHA: "b",
		Commands: map[string]string{"setup": "just setup", "test": "just test"},
		closeFn:  func() { f.closed = true },
	}, nil
}

func opts(l launcher.Launcher, p Publisher) Options {
	return Options{Launcher: l, Pusher: p, Cloner: &fakeCloner{}, Run: "r1", Lane: "claude", Repo: "a/b", Base: "main", Issue: runner.Issue{Number: 3, Title: "t", Body: "b"}}
}

func TestChangesArePublished(t *testing.T) {
	l := &fakeLauncher{res: result("changes")}
	p := &fakePublisher{out: pusher.Outcome{Branch: "satat/r1", PRURL: "u"}}
	rep, err := Run(context.Background(), opts(l, p))
	if err != nil || !rep.OK() || rep.Push == nil || rep.Push.PRURL != "u" {
		t.Fatalf("report %+v, err %v", rep, err)
	}
	if p.req.Summary != "all done" || p.req.HeadSHA != "h" || p.req.Issue != 3 || string(p.req.Bundle) != "bundle" {
		t.Errorf("push request: %+v", p.req)
	}
	in, err := runner.ParseInput(l.got.Input)
	if err != nil || in.Repo != "a/b" || in.Base != "main" || in.Commands["test"] != "just test" {
		t.Errorf("step input: %s (%v)", l.got.Input, err)
	}
	if b, _ := io.ReadAll(l.got.Checkout); string(b) != "tar" {
		t.Errorf("the step did not get the worker's checkout: %q", b)
	}
	if l.got.CacheScope != launcher.CacheScope("a/b") {
		t.Errorf("cache scope = %q, want the repo's", l.got.CacheScope)
	}
}

func TestNothingIsPublishedUnlessThereAreChanges(t *testing.T) {
	for _, status := range []string{"no_changes", "failed"} {
		p := &fakePublisher{}
		rep, err := Run(context.Background(), opts(&fakeLauncher{res: result(status)}, p))
		if err != nil || p.n != 0 || rep.Push != nil {
			t.Errorf("%s: pushed %d times, report %+v, err %v", status, p.n, rep, err)
		}
	}
	// A failing exit code stops it even when result.json claims changes.
	bad := result("changes")
	bad.ExitCode = 1
	p := &fakePublisher{}
	if _, _ = Run(context.Background(), opts(&fakeLauncher{res: bad}, p)); p.n != 0 {
		t.Error("published after a non-zero exit")
	}
}

func TestNoPublisherStopsBeforeGitHub(t *testing.T) {
	rep, err := Run(context.Background(), opts(&fakeLauncher{res: result("changes")}, nil))
	if err != nil || !rep.OK() || rep.Push != nil {
		t.Fatalf("report %+v, err %v", rep, err)
	}
}

func TestARefusalIsReportedNotHidden(t *testing.T) {
	p := &fakePublisher{err: &pusher.Refusal{Reason: "touches a workflow"}}
	rep, err := Run(context.Background(), opts(&fakeLauncher{res: result("changes")}, p))
	var ref *pusher.Refusal
	if err != nil || rep.OK() || !errors.As(rep.PushErr, &ref) {
		t.Fatalf("report %+v, err %v: want PushErr set and OK false", rep, err)
	}
}

// A step that cannot be given a checkout is not launched, and no cloner means no launch.
func TestNothingLaunchesWithoutACheckout(t *testing.T) {
	l := &fakeLauncher{res: result("changes")}
	o := opts(l, nil)
	o.Cloner = &fakeCloner{err: errors.New("no such branch")}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "no such branch") {
		t.Fatalf("err = %v", err)
	}
	o.Cloner = nil
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("a run with no cloner launched")
	}
	if l.got.Run != "" {
		t.Error("the launcher was called")
	}
}

func TestTheCheckoutIsReleasedAfterTheStep(t *testing.T) {
	c := &fakeCloner{}
	o := opts(&fakeLauncher{res: result("no_changes")}, nil)
	o.Cloner = c
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if !c.closed {
		t.Error("the clone was not removed")
	}
}
