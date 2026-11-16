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
		{State: StepFinished, Run: "7cac965f-67b7", Repo: "o/r", Issue: 1, Reason: "changes, exit 0, 1 commit(s)"},
		{State: Published, Run: "7cac965f-67b7", Repo: "o/r", Issue: 1, PRURL: "https://github.com/o/r/pull/2"},
	} {
		if err := g.Notify(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"**Satat: queued** · by the poller", "**Satat: started** · run `7cac965f`", "**Satat: step finished**", "**Satat: published**"}
	if len(f.comments) != len(want) {
		t.Fatalf("%d comments, want one per state", len(f.comments))
	}
	for i, w := range want {
		if !strings.HasPrefix(f.comments[i].Body, w) {
			t.Errorf("comment %d = %q, want it to start %q", i, f.comments[i].Body, w)
		}
	}
	if !strings.Contains(f.comments[3].Body, "https://github.com/o/r/pull/2") || !strings.Contains(f.comments[2].Body, "1 commit(s)") {
		t.Errorf("comments lack the PR or the reason: %+v", f.comments)
	}
	if !slices.Equal(f.labels, []string{ReadyLabel}) {
		t.Errorf("labels = %v, want them untouched on success", f.labels)
	}
}

func TestAFailureSwapsTheLabelAndAStartClearsIt(t *testing.T) {
	f, g := newFake(t)
	ctx := context.Background()
	fail := ev(Failed, "r1")
	fail.Reason, fail.Detail = "pre-push hook still failing", "pre-push hook still failing\n```\npython: not found"
	if err := g.Notify(ctx, fail); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.labels, []string{FailedLabel}) {
		t.Errorf("labels after a failure = %v, want only %s", f.labels, FailedLabel)
	}
	body := f.comments[0].Body
	if !strings.Contains(body, "pre-push hook still failing") || !strings.Contains(body, "<details>") || strings.Count(body, "```") != 2 || !strings.Contains(body, "again to run it again") {
		t.Errorf("failure comment:\n%s", body)
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
