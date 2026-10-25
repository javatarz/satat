package poller

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/workflows"
)

type fakeIssues struct {
	ready   map[string][]ReadyIssue
	closing map[string]map[int]bool
	err     map[string]error
}

func (f fakeIssues) ReadyIssues(_ context.Context, repo, label string) ([]ReadyIssue, error) {
	if label != workflows.ReadyLabel {
		return nil, errors.New("wrong label " + label)
	}
	return f.ready[repo], f.err[repo]
}

func (f fakeIssues) OpenPRIssues(_ context.Context, repo string) (map[int]bool, error) {
	return f.closing[repo], nil
}

type fakeRuns struct {
	set map[string]bool
	err error
}

func (f fakeRuns) Issues(context.Context) (map[string]bool, error) { return f.set, f.err }

type fakeQueue struct{ pushed []workflows.Input }

func (q *fakeQueue) Push(_ context.Context, in workflows.Input) error {
	q.pushed = append(q.pushed, in)
	return nil
}

func cfg() config.Config {
	return config.Config{
		Lanes:       []launcher.Lane{{Name: "claude"}},
		Accounts:    map[string]string{"claude": "sub"},
		LaneMaxRuns: map[string]int{"claude": 1},
		Poller: config.Poller{Repos: []config.PollRepo{
			{Name: "me/a", Base: "main", Lane: "claude"},
			{Name: "me/b", Base: "dev", Lane: "claude"},
		}},
	}
}

func nums(q *fakeQueue) []string {
	var out []string
	for _, in := range q.pushed {
		out = append(out, Key(in.Repo, in.Issue.Number))
	}
	return out
}

func TestQueuesReadyIssuesOldestFirstWithConfig(t *testing.T) {
	q := &fakeQueue{}
	p := &Poller{Config: cfg(), Runs: fakeRuns{}, Queue: q, Issues: fakeIssues{ready: map[string][]ReadyIssue{
		"me/a": {{Number: 9, Title: "nine", Body: "b"}, {Number: 3}},
		"me/b": {{Number: 1}},
	}}}
	res, err := p.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"me/a#3", "me/a#9", "me/b#1"}; !reflect.DeepEqual(nums(q), want) || !reflect.DeepEqual(res.Queued, want) {
		t.Fatalf("queued %v, want %v", nums(q), want)
	}
	in := q.pushed[1]
	if in.Base != "main" || in.Lane != "claude" || in.Account != "sub" || in.LaneCap != 1 || in.Issue.Title != "nine" || in.Issue.Body != "b" {
		t.Fatalf("unexpected input %+v", in)
	}
	if q.pushed[2].Base != "dev" {
		t.Fatalf("base not taken from config: %+v", q.pushed[2])
	}
}

func TestSkipsIssuesWithARunOrAnOpenPR(t *testing.T) {
	q := &fakeQueue{}
	p := &Poller{Config: cfg(), Queue: q,
		Runs: fakeRuns{set: map[string]bool{"me/a#1": true}},
		Issues: fakeIssues{
			ready:   map[string][]ReadyIssue{"me/a": {{Number: 1}, {Number: 2}, {Number: 3}}},
			closing: map[string]map[int]bool{"me/a": {2: true}},
		}}
	res, err := p.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"me/a#3"}; !reflect.DeepEqual(nums(q), want) {
		t.Fatalf("queued %v, want %v", nums(q), want)
	}
	if res.Skipped["me/a#1"] == "" || res.Skipped["me/a#2"] == "" {
		t.Fatalf("skips not reported: %v", res.Skipped)
	}
}

func TestDoesNotRequeueBeforeTheRunAppears(t *testing.T) {
	q := &fakeQueue{}
	now := time.Now()
	p := &Poller{Config: cfg(), Runs: fakeRuns{}, Queue: q, Now: func() time.Time { return now },
		Issues: fakeIssues{ready: map[string][]ReadyIssue{"me/a": {{Number: 1}}}}}
	for i := 0; i < 2; i++ {
		if _, err := p.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(q.pushed) != 1 {
		t.Fatalf("queued %d times, want 1", len(q.pushed))
	}
	// Once the memory expires the run is expected to be listed; if it is not, the issue is queued again.
	now = now.Add(Pending + time.Minute)
	if _, err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(q.pushed) != 2 {
		t.Fatalf("queued %d times after the memory expired, want 2", len(q.pushed))
	}
}

func TestQueuesNothingWhenRunsCannotBeListed(t *testing.T) {
	q := &fakeQueue{}
	p := &Poller{Config: cfg(), Runs: fakeRuns{err: errors.New("hatchet down")}, Queue: q,
		Issues: fakeIssues{ready: map[string][]ReadyIssue{"me/a": {{Number: 1}}}}}
	if _, err := p.Poll(context.Background()); err == nil || len(q.pushed) != 0 {
		t.Fatalf("want an error and no pushes, got %v and %v", err, nums(q))
	}
}

func TestOneRepoFailingDoesNotStopTheOthers(t *testing.T) {
	q := &fakeQueue{}
	p := &Poller{Config: cfg(), Runs: fakeRuns{}, Queue: q, Issues: fakeIssues{
		err:   map[string]error{"me/a": errors.New("404")},
		ready: map[string][]ReadyIssue{"me/b": {{Number: 5}}},
	}}
	if _, err := p.Poll(context.Background()); err == nil {
		t.Fatal("want the failing repo reported")
	}
	if want := []string{"me/b#5"}; !reflect.DeepEqual(nums(q), want) {
		t.Fatalf("queued %v, want %v", nums(q), want)
	}
}

func TestNoReposDoesNothing(t *testing.T) {
	c := cfg()
	c.Poller.Repos = nil
	q := &fakeQueue{}
	p := &Poller{Config: c, Runs: fakeRuns{err: errors.New("must not be called")}, Queue: q, Issues: fakeIssues{}}
	if _, err := p.Poll(context.Background()); err != nil || len(q.pushed) != 0 {
		t.Fatalf("got %v, %v", err, q.pushed)
	}
}

func TestInputIssue(t *testing.T) {
	repo, n, ok := inputIssue(map[string]any{"repo": "me/a", "issue": map[string]any{"number": float64(7)}})
	if !ok || repo != "me/a" || n != 7 {
		t.Fatalf("got %q %d %v", repo, n, ok)
	}
	if _, _, ok := inputIssue(map[string]any{}); ok {
		t.Fatal("a poll run's empty input must not count")
	}
}

func TestCollectFailsClosed(t *testing.T) {
	yes := true
	good := rest.V1TaskSummary{DisplayName: "satat-issue-abc", Input: map[string]interface{}{"repo": "me/a", "issue": map[string]interface{}{"number": float64(4)}}}
	set := map[string]bool{}
	// The live listing wraps the input and names runs "run-<ms>"; the workflow name is separate.
	wfName := workflows.Name
	wrapped := rest.V1TaskSummary{DisplayName: "run-1", WorkflowName: &wfName, Input: map[string]interface{}{"input": map[string]interface{}{"repo": "me/w", "issue": map[string]interface{}{"number": float64(9)}}, "triggered_by": "event"}}
	if err := collect(set, []rest.V1TaskSummary{wrapped}); err != nil || !set["me/w#9"] {
		t.Errorf("wrapped live row: set %v, err %v", set, err)
	}
	if err := collect(map[string]bool{}, []rest.V1TaskSummary{{DisplayName: "run-2", WorkflowName: &wfName}}); err == nil {
		t.Error("a satat-issue row with no readable input must fail closed")
	}
	if err := collect(set, []rest.V1TaskSummary{good, {DisplayName: "satat-poll-x"}}); err != nil || !set["me/a#4"] {
		t.Fatalf("got %v, %v", err, set)
	}
	for name, row := range map[string]rest.V1TaskSummary{
		"restricted":    {DisplayName: "satat-poll-x", PayloadsRestricted: &yes},
		"missing input": {DisplayName: "satat-issue-def"},
		"no issue":      {DisplayName: "satat-issue-def", Input: map[string]interface{}{"repo": "me/a"}},
	} {
		if err := collect(map[string]bool{}, []rest.V1TaskSummary{row}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
