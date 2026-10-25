package pusher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIssueGitHub is an in-memory GitHub for one issue. These tests are the only exercise of
// the write path, so they never touch the real API.
type fakeIssueGitHub struct {
	mu       sync.Mutex
	t        *testing.T
	srv      *httptest.Server
	state    string
	labels   []string
	events   []map[string]any
	comments []map[string]any
	log      []string
	now      time.Time
	failPost string // a path suffix whose POST answers 500
}

func newFakeIssue(t *testing.T) *fakeIssueGitHub {
	f := &fakeIssueGitHub{t: t, state: "open", labels: []string{ReadyLabel}, now: time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)}
	f.event("labeled", ReadyLabel, "karun")
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssueGitHub) tick() string {
	f.now = f.now.Add(time.Minute)
	return f.now.Format(time.RFC3339)
}

func (f *fakeIssueGitHub) event(kind, label, actor string) {
	f.events = append(f.events, map[string]any{"event": kind, "created_at": f.tick(), "label": map[string]string{"name": label}, "actor": map[string]string{"login": actor}})
}

func (f *fakeIssueGitHub) hasLabel(l string) bool {
	for _, x := range f.labels {
		if x == l {
			return true
		}
	}
	return false
}

// humanRelabels is a person removing and re-adding the label.
func (f *fakeIssueGitHub) humanRelabels() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labels = []string{ReadyLabel}
	f.event("labeled", ReadyLabel, "karun")
}

func (f *fakeIssueGitHub) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, l := range f.log {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeIssueGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer write-token" {
		f.t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
	}
	b, _ := io.ReadAll(r.Body)
	const base = "/repos/o/r/issues/5"
	switch {
	case r.Method == "GET" && r.URL.Path == "/user":
		fmt.Fprint(w, `{"login":"satat-bot"}`)
	case r.Method == "GET" && r.URL.Path == base:
		var ls []map[string]string
		for _, l := range f.labels {
			ls = append(ls, map[string]string{"name": l})
		}
		json.NewEncoder(w).Encode(map[string]any{"state": f.state, "labels": ls})
	case r.Method == "GET" && r.URL.Path == base+"/events":
		json.NewEncoder(w).Encode(f.events)
	case r.Method == "GET" && r.URL.Path == base+"/comments":
		json.NewEncoder(w).Encode(f.comments)
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, f.failPost) && f.failPost != "":
		http.Error(w, "boom", http.StatusInternalServerError)
	case r.Method == "POST" && r.URL.Path == base+"/comments":
		var in struct{ Body string }
		json.Unmarshal(b, &in)
		f.comments = append(f.comments, map[string]any{"body": in.Body, "created_at": f.tick(), "user": map[string]string{"login": "satat-bot"}})
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{}`)
	case r.Method == "POST" && r.URL.Path == base+"/labels":
		var in struct{ Labels []string }
		json.Unmarshal(b, &in)
		for _, l := range in.Labels {
			if !f.hasLabel(l) {
				f.labels = append(f.labels, l)
			}
		}
		fmt.Fprint(w, `[]`)
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, base+"/labels/"):
		name := strings.TrimPrefix(r.URL.Path, base+"/labels/")
		if !f.hasLabel(name) {
			http.NotFound(w, r)
			return
		}
		var keep []string
		for _, l := range f.labels {
			if l != name {
				keep = append(keep, l)
			}
		}
		f.labels = keep
		fmt.Fprint(w, `[]`)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func (f *fakeIssueGitHub) pusher() *Pusher {
	return &Pusher{Token: "write-token", APIBase: f.srv.URL}
}

var notReady = NotReadyRequest{Repo: "o/r", Issue: 5, Key: "abc123def456", Body: "Satat did not start this issue.\n- **commands**: missing."}

func TestNotReadyCommentsOnceAndMovesTheLabel(t *testing.T) {
	f := newFakeIssue(t)
	out, err := f.pusher().NotReady(context.Background(), notReady)
	if err != nil || !out.Commented || !out.Relabeled {
		t.Fatalf("out %+v err %v", out, err)
	}
	if len(f.comments) != 1 || !strings.HasPrefix(f.comments[0]["body"].(string), marker(notReady.Key)) || !strings.Contains(f.comments[0]["body"].(string), "missing") {
		t.Errorf("comments: %v", f.comments)
	}
	if f.hasLabel(ReadyLabel) || !f.hasLabel(NeedsInfoLabel) {
		t.Errorf("labels: %v", f.labels)
	}
}

func TestNotReadyIsIdempotent(t *testing.T) {
	f := newFakeIssue(t)
	p := f.pusher()
	if _, err := p.NotReady(context.Background(), notReady); err != nil {
		t.Fatal(err)
	}
	out, err := p.NotReady(context.Background(), notReady)
	if err != nil || out.Commented || out.Relabeled || out.Skipped == "" {
		t.Fatalf("second call: %+v %v; want a skip", out, err)
	}
	if len(f.comments) != 1 {
		t.Errorf("%d comments; want one", len(f.comments))
	}
}

func TestNotReadyRetryAfterACrashOnlyFinishesTheRelabel(t *testing.T) {
	f := newFakeIssue(t)
	f.failPost = "/labels" // the relabel fails after the comment is posted
	p := f.pusher()
	if _, err := p.NotReady(context.Background(), notReady); err == nil {
		t.Fatal("want the relabel error")
	}
	f.failPost = ""
	out, err := p.NotReady(context.Background(), notReady)
	if err != nil || out.Commented || !out.Relabeled {
		t.Fatalf("retry: %+v %v", out, err)
	}
	if len(f.comments) != 1 || f.hasLabel(ReadyLabel) || !f.hasLabel(NeedsInfoLabel) {
		t.Errorf("comments %d labels %v", len(f.comments), f.labels)
	}
}

func TestNotReadyNeverLeavesTheIssueWithNeitherLabel(t *testing.T) {
	f := newFakeIssue(t)
	f.failPost = "/comments"
	if _, err := f.pusher().NotReady(context.Background(), notReady); err == nil {
		t.Fatal("want the comment error")
	}
	if !f.hasLabel(ReadyLabel) || f.hasLabel(NeedsInfoLabel) {
		t.Errorf("a failed comment must change nothing; labels %v", f.labels)
	}
}

func TestNotReadyCommentsAgainWhenAHumanRelabelsWithoutAFix(t *testing.T) {
	f := newFakeIssue(t)
	p := f.pusher()
	p.NotReady(context.Background(), notReady)
	f.humanRelabels()
	out, err := p.NotReady(context.Background(), notReady)
	if err != nil || !out.Commented || len(f.comments) != 2 {
		t.Fatalf("%+v %v comments %d; the second labelling gets its own answer", out, err, len(f.comments))
	}
}

func TestNotReadyDifferentFailuresGetTheirOwnComment(t *testing.T) {
	f := newFakeIssue(t)
	p := f.pusher()
	p.NotReady(context.Background(), notReady)
	f.humanRelabels()
	other := notReady
	other.Key = "000000000000"
	if out, _ := p.NotReady(context.Background(), other); !out.Commented {
		t.Fatal("a different failure set must be reported")
	}
}

func TestNotReadyIgnoresAMarkerFromSomeoneElse(t *testing.T) {
	f := newFakeIssue(t)
	f.comments = append(f.comments, map[string]any{"body": marker(notReady.Key), "created_at": f.tick(), "user": map[string]string{"login": "mallory"}})
	if out, err := f.pusher().NotReady(context.Background(), notReady); err != nil || !out.Commented {
		t.Fatalf("%+v %v; a pasted marker must not suppress the comment", out, err)
	}
}

func TestNotReadyDoesNothingWhenTheIssueMovedOn(t *testing.T) {
	for name, mutate := range map[string]func(*fakeIssueGitHub){
		"label removed": func(f *fakeIssueGitHub) { f.labels = []string{NeedsInfoLabel} },
		"closed":        func(f *fakeIssueGitHub) { f.state = "closed" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeIssue(t)
			mutate(f)
			out, err := f.pusher().NotReady(context.Background(), notReady)
			if err != nil || out.Skipped == "" || out.Commented || out.Relabeled {
				t.Fatalf("%+v %v", out, err)
			}
			if f.count("POST") != 0 || f.count("DELETE") != 0 {
				t.Errorf("wrote after the issue moved on: %v", f.log)
			}
		})
	}
}

func TestNotReadyRefusesBadInput(t *testing.T) {
	f := newFakeIssue(t)
	for name, mutate := range map[string]func(*NotReadyRequest){
		"repo":  func(r *NotReadyRequest) { r.Repo = "o/../x" },
		"issue": func(r *NotReadyRequest) { r.Issue = 0 },
		"key":   func(r *NotReadyRequest) { r.Key = "--> <!--" },
		"body":  func(r *NotReadyRequest) { r.Body = " " },
	} {
		r := notReady
		mutate(&r)
		if _, err := f.pusher().NotReady(context.Background(), r); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if len(f.log) != 0 {
		t.Errorf("requests made for bad input: %v", f.log)
	}
}
