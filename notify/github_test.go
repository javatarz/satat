package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	case r.Method == http.MethodGet && p == "/user":
		json.NewEncoder(w).Encode(map[string]string{"login": "satat-bot"})
	case r.Method == http.MethodGet && p == "/repos/o/r/issues/1/comments":
		json.NewEncoder(w).Encode(f.comments)
	case r.Method == http.MethodPost && p == "/repos/o/r/issues/1/comments":
		var in struct{ Body string }
		json.NewDecoder(r.Body).Decode(&in)
		f.nextID++
		c := comment{ID: f.nextID, Body: in.Body}
		c.User.Login = "satat-bot"
		f.comments = append(f.comments, c)
		json.NewEncoder(w).Encode(c)
	case strings.HasPrefix(p, "/repos/o/r/issues/comments/"):
		var id int64
		fmt.Sscan(strings.TrimPrefix(p, "/repos/o/r/issues/comments/"), &id)
		i := slices.IndexFunc(f.comments, func(c comment) bool { return c.ID == id })
		if i < 0 {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPatch {
			if f.comments[i].User.Login != "satat-bot" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			var in struct{ Body string }
			json.NewDecoder(r.Body).Decode(&in)
			f.comments[i].Body = in.Body
		}
		json.NewEncoder(w).Encode(f.comments[i])
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

func TestOneStatusCommentGetsARowPerState(t *testing.T) {
	f, g := newFake(t)
	ctx := context.Background()
	for _, e := range []Event{
		{State: Queued, Repo: "o/r", Issue: 1, Source: "poller"},
		ev(Started, "7cac965f-67b7"),
		ev(StepFinished, "7cac965f-67b7"),
		{State: Published, Run: "7cac965f-67b7", Repo: "o/r", Issue: 1, PRURL: "https://github.com/o/r/pull/2"},
	} {
		if err := g.Notify(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.comments) != 1 {
		t.Fatalf("%d comments, want one status comment", len(f.comments))
	}
	body := f.comments[0].Body
	for _, want := range []string{statusMarker, "**queued** | by the poller", "`7cac965f`", "**started**", "**step finished**", "https://github.com/o/r/pull/2"} {
		if !strings.Contains(body, want) {
			t.Errorf("status comment lacks %q:\n%s", want, body)
		}
	}
	if !slices.Equal(f.labels, []string{ReadyLabel}) {
		t.Errorf("labels = %v, want them untouched on success", f.labels)
	}
}

func TestAFailureSwapsTheLabelAndAStartClearsIt(t *testing.T) {
	f, g := newFake(t)
	ctx := context.Background()
	fail := ev(Failed, "r1")
	fail.Reason = "pre-push hook still failing | after 3 retries"
	if err := g.Notify(ctx, fail); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.labels, []string{FailedLabel}) {
		t.Errorf("labels after a failure = %v, want only %s", f.labels, FailedLabel)
	}
	if !strings.Contains(f.comments[0].Body, `pre-push hook still failing \| after 3 retries`) {
		t.Errorf("the cause is not in one cell:\n%s", f.comments[0].Body)
	}
	f.labels = append(f.labels, ReadyLabel) // a person adds it again
	if err := g.Notify(ctx, ev(Started, "r2")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.labels, []string{ReadyLabel}) {
		t.Errorf("labels after a re-run starts = %v, want only %s", f.labels, ReadyLabel)
	}
}

func TestARestartedWorkerFindsItsCommentAndKeepsTheRows(t *testing.T) {
	f, g := newFake(t)
	ctx := context.Background()
	if err := g.Notify(ctx, ev(Started, "r1")); err != nil {
		t.Fatal(err)
	}
	g2 := &GitHub{Token: "t", APIBase: g.APIBase}
	if err := g2.Notify(ctx, ev(Completed, "r1")); err != nil {
		t.Fatal(err)
	}
	if len(f.comments) != 1 || !strings.Contains(f.comments[0].Body, "**started**") || !strings.Contains(f.comments[0].Body, "**completed**") {
		t.Errorf("comments after a restart: %+v", f.comments)
	}
}

func TestSomeoneElsesCommentWithTheMarkerIsLeftAlone(t *testing.T) {
	f, g := newFake(t)
	forged := comment{ID: 99, Body: statusMarker + "\nnot Satat"}
	forged.User.Login = "mallory"
	f.comments = append(f.comments, forged)
	if err := g.Notify(context.Background(), ev(Started, "r1")); err != nil {
		t.Fatal(err)
	}
	if len(f.comments) != 2 || f.comments[0].Body != forged.Body {
		t.Errorf("comments: %+v", f.comments)
	}
}

func TestTheOldestRowsGoWhenTheCommentGetsLong(t *testing.T) {
	body := ""
	e := ev(Failed, "r1")
	e.Reason = strings.Repeat("x", 190)
	for range 400 {
		body = StatusBody(body, e)
	}
	if len(body) > maxComment || !strings.HasPrefix(body, statusMarker) {
		t.Errorf("body is %d bytes, header kept: %v", len(body), strings.HasPrefix(body, statusMarker))
	}
}
