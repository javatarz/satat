package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// fakeGitHub is the slice of the REST API the channel uses, for one issue.
type fakeGitHub struct {
	mu       sync.Mutex
	comments []comment
	labels   []string
	nextID   int64
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case r.Method == http.MethodPost && p == "/repos/o/r/issues/1/comments":
		var in struct{ Body string }
		json.NewDecoder(r.Body).Decode(&in)
		f.nextID++
		c := comment{ID: f.nextID, Body: in.Body}
		f.comments = append(f.comments, c)
		json.NewEncoder(w).Encode(c)
	case r.Method == http.MethodPost && p == "/repos/o/r/issues/1/labels":
		var in struct{ Labels []string }
		json.NewDecoder(r.Body).Decode(&in)
		for _, l := range in.Labels {
			if !slices.Contains(f.labels, l) {
				f.labels = append(f.labels, l)
			}
		}
		w.Write([]byte("[]"))
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/repos/o/r/issues/1/labels/"):
		l := strings.TrimPrefix(p, "/repos/o/r/issues/1/labels/")
		i := slices.Index(f.labels, l)
		if i < 0 {
			http.NotFound(w, r)
			return
		}
		f.labels = slices.Delete(f.labels, i, i+1)
		w.Write([]byte("[]"))
	default:
		http.Error(w, r.Method+" "+p, http.StatusTeapot)
	}
}

func newFake(t *testing.T) (*fakeGitHub, *GitHub) {
	t.Helper()
	f := &fakeGitHub{labels: []string{ReadyLabel}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &GitHub{Token: "t", APIBase: srv.URL}
}

func ev(s State, run string) Event {
	return Event{State: s, Run: run, Repo: "o/r", Issue: 1, At: time.Date(2026, 11, 16, 5, 0, 0, 0, time.UTC)}
}

func TestEveryStateIsANewCommentInOrder(t *testing.T) {
	f, g := newFake(t)
	ctx := context.Background()
	for _, e := range []Event{
		{State: Queued, Repo: "o/r", Issue: 1, Source: "poller"},
		ev(Started, "7cac965f-67b7"),
		{State: StepFinished, Run: "7cac965f-67b7", Repo: "o/r", Issue: 1, Agent: "Claude Code", Outcome: "changes", Commits: 1},
		{State: Published, Run: "7cac965f-67b7", Repo: "o/r", Issue: 1, PRNumber: 2, PRURL: "https://github.com/o/r/pull/2", RunURL: "http://hatchet/runs/7cac965f-67b7"},
	} {
		if err := g.Notify(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"⏳ **Satat queued this issue**", "▶️ **Satat started work on this issue**", "🏁 **Claude Code finished: 1 commit**", "✅ **Satat opened draft PR #2**"}
	if len(f.comments) != len(want) {
		t.Fatalf("%d comments, want one per state", len(f.comments))
	}
	for i, w := range want {
		if !strings.HasPrefix(f.comments[i].Body, w) {
			t.Errorf("comment %d = %q, want it to start %q", i, f.comments[i].Body, w)
		}
	}
	if pr := f.comments[3].Body; !strings.Contains(pr, "[Draft PR](https://github.com/o/r/pull/2) · [Run log](http://hatchet/runs/7cac965f-67b7)") || !strings.Contains(pr, "<sub>Satat run `7cac965f` · published</sub>") {
		t.Errorf("PR comment lacks its links or run:\n%s", pr)
	}
	if !slices.Equal(f.labels, []string{ReadyLabel}) {
		t.Errorf("labels = %v, want them untouched on success", f.labels)
	}
}

func TestAFailureSwapsTheLabelAndAStartClearsIt(t *testing.T) {
	f, g := newFake(t)
	ctx := context.Background()
	fail := ev(Failed, "r1")
	fail.Reason, fail.Detail = "step failed (exit 1): pre-push hook still failing after 3 retries", "pre-push hook still failing after 3 retries\n```\npython: not found"
	if err := g.Notify(ctx, fail); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.labels, []string{FailedLabel}) {
		t.Errorf("labels after a failure = %v, want only %s", f.labels, FailedLabel)
	}
	body := f.comments[0].Body
	for _, want := range []string{"❌ **Satat failed: the repo's pre-push checks kept failing**", "was swapped for `satat:failed`", "again to re-run it", "<details>"} {
		if !strings.Contains(body, want) {
			t.Errorf("failure comment lacks %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "```") != 2 {
		t.Errorf("the detail's own fence was not neutralised:\n%s", body)
	}
	f.labels = append(f.labels, ReadyLabel) // a person adds it again
	if err := g.Notify(ctx, ev(Started, "r2")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.labels, []string{ReadyLabel}) {
		t.Errorf("labels after a re-run starts = %v, want only %s", f.labels, ReadyLabel)
	}
}

func TestAnEventWithNoIssuePostsNothing(t *testing.T) {
	f, g := newFake(t)
	if err := g.Notify(context.Background(), Event{State: WorkerDown}); err != nil || len(f.comments) != 0 {
		t.Errorf("err %v, comments %d", err, len(f.comments))
	}
}

func TestALongDetailIsCut(t *testing.T) {
	e := ev(Failed, "r1")
	e.Detail = strings.Repeat("x", 100000)
	if body := CommentBody(e); len(body) > 30000 || !strings.Contains(body, "(cut)") {
		t.Errorf("body is %d bytes", len(body))
	}
}
