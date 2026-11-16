package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNtfyPublishesAMessageThatOpensThePR(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			http.Error(w, "bad", 400)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	n := &Ntfy{Server: srv.URL + "/", Topic: "secret-topic"}
	if err := n.Notify(context.Background(), Event{State: Published, Repo: "o/r", Issue: 1, IssueTitle: "Greet people", PRNumber: 2, PRTitle: "Add greet", PRURL: "https://github.com/o/r/pull/2"}); err != nil {
		t.Fatal(err)
	}
	if got["topic"] != "secret-topic" || got["click"] != "https://github.com/o/r/pull/2" || got["title"] != "Satat opened draft PR #2: Add greet" ||
		!strings.HasPrefix(got["message"].(string), "**o/r#1**: Greet people") || got["priority"].(float64) != 4 || got["markdown"] != true {
		t.Errorf("published %v", got)
	}
	if err := n.Notify(context.Background(), Event{State: Cloned, Repo: "o/r", Issue: 1, Base: "main", SHA: "44f6ada2a79"}); err != nil {
		t.Fatal(err)
	}
	if got["click"] != "https://github.com/o/r/issues/1" || got["priority"].(float64) != 2 || !strings.Contains(got["message"].(string), "`main` at `44f6ada`") {
		t.Errorf("a progress message should be quiet and open the issue: %v", got)
	}
}

func TestNtfyReportsAServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	err := (&Ntfy{Server: srv.URL, Topic: "secret-topic"}).Notify(context.Background(), Event{State: Started})
	if err == nil || strings.Contains(err.Error(), "secret-topic") {
		t.Errorf("err = %v, want an error without the topic", err)
	}
}
