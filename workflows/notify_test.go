package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/notify"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/runner"
)

type eventLog struct{ got []notify.Event }

func (e *eventLog) Notify(_ context.Context, ev notify.Event) error {
	e.got = append(e.got, ev)
	return nil
}

func (e *eventLog) states() string {
	var s []string
	for _, ev := range e.got {
		s = append(s, string(ev.State))
	}
	return strings.Join(s, ", ")
}

func notifying(h Handler) (Handler, *eventLog) {
	e := &eventLog{}
	h.Notify = &notify.Safe{Next: e}
	return h, e
}

func TestEveryStateOfARunThatOpensAPRIsNotified(t *testing.T) {
	h, e := notifying(Handler{Cloner: fakeCloner{}, Launcher: &fakeLauncher{res: result("changes")}, Pusher: &fakePub{out: pusher.Outcome{PRURL: "https://x/pull/1"}}, Issues: ready(), Allowed: listed})
	if _, err := h.Attempt(context.Background(), "run-1", input, 0); err != nil {
		t.Fatal(err)
	}
	if got, want := e.states(), "started, cloned, step started, step finished, published, completed"; got != want {
		t.Errorf("states = %s, want %s", got, want)
	}
	for _, ev := range e.got {
		if ev.Run != "run-1" || ev.Repo != "a/b" || ev.Issue != 1 || ev.At.IsZero() {
			t.Errorf("event lacks the run's identity: %+v", ev)
		}
	}
	if last := e.got[len(e.got)-1]; last.PRURL != "https://x/pull/1" {
		t.Errorf("completed event = %+v, want the PR URL", last)
	}
}

func TestAFailedRunIsNotifiedWithItsCause(t *testing.T) {
	failed := result("failed")
	failed.ExitCode = 1
	h, e := notifying(Handler{Cloner: fakeCloner{}, Launcher: &fakeLauncher{res: failed}, Pusher: &fakePub{}, Issues: ready(), Allowed: listed})
	if _, err := h.Attempt(context.Background(), "r", input, 0); err == nil {
		t.Fatal("the run did not fail")
	}
	last := e.got[len(e.got)-1]
	if last.State != notify.Failed || !strings.Contains(last.Reason, "step failed") {
		t.Errorf("last event = %+v", last)
	}
}

func TestASkippedRunSaysWhy(t *testing.T) {
	h, e := notifying(Handler{Cloner: fakeCloner{}, Launcher: &fakeLauncher{}, Pusher: &fakePub{}, Issues: openIssue("bug"), Allowed: listed})
	if _, err := h.Attempt(context.Background(), "r", input, 0); err != nil {
		t.Fatal(err)
	}
	if got := e.states(); got != "started, skipped" || !strings.Contains(e.got[1].Reason, "not labelled") {
		t.Errorf("events = %+v", e.got)
	}
}

func TestARateLimitIsNotAFailure(t *testing.T) {
	h, e := notifying(Handler{Cloner: fakeCloner{}, Launcher: &fakeLauncher{res: rateLimited(t)}, Pusher: &fakePub{}, Issues: ready(), Allowed: listed})
	_, err := h.Attempt(context.Background(), "r", input, 2)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want a rate limit", err)
	}
	if e.got[0].Attempt != 2 || e.got[len(e.got)-1].State != notify.RateLimited {
		t.Errorf("events = %+v", e.got)
	}
}

func TestNoEventsForARepoThatIsNotAllowedOrAnAgentRun(t *testing.T) {
	h, e := notifying(Handler{Cloner: fakeCloner{}, Launcher: &fakeLauncher{}, Pusher: &fakePub{}, Issues: ready(), Allowed: []string{"other/repo"}})
	h.Attempt(context.Background(), "r", input, 0)
	agent := Input{Kind: KindAgent, Lane: "smoke"}
	h.Attempt(context.Background(), "r2", agent, 0)
	if len(e.got) != 0 {
		t.Errorf("events = %+v, want none", e.got)
	}
}

// rateLimited is a step result that hit the subscription limit.
func rateLimited(t *testing.T) launcher.Result {
	t.Helper()
	b, err := json.Marshal(runner.Result{Status: "failed", Error: "claude: exit status 1: rate_limit (HTTP 429), ended with \"x\"", BaseSHA: "b", HeadSHA: "b"})
	if err != nil {
		t.Fatal(err)
	}
	return launcher.Result{ExitCode: 1, Files: map[string][]byte{"result.json": b}}
}
