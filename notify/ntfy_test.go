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
	if err := n.Notify(context.Background(), Event{State: Published, Repo: "o/r", Issue: 1, PRURL: "https://github.com/o/r/pull/2"}); err != nil {
		t.Fatal(err)
	}
	if got["topic"] != "secret-topic" || got["click"] != "https://github.com/o/r/pull/2" || !strings.Contains(got["message"].(string), "draft PR") {
		t.Errorf("published %v", got)
	}
	if err := n.Notify(context.Background(), Event{State: Failed, Repo: "o/r", Issue: 1, Reason: "clone failed"}); err != nil {
		t.Fatal(err)
	}
	if got["click"] != "https://github.com/o/r/issues/1" || got["message"] != "Satat failed: clone failed" || got["priority"].(float64) != 4 {
		t.Errorf("published %v", got)
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
