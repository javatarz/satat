package poller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestReadyIssuesFiltersPullRequestsAndPages(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RawQuery)
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/repos/me/a/issues" {
			http.Error(w, "bad", 400)
			return
		}
		if r.URL.Query().Get("labels") != "ready-for-agent" || r.URL.Query().Get("state") != "open" {
			http.Error(w, "bad query", 400)
			return
		}
		if r.URL.Query().Get("page") == "1" {
			var parts []string
			for i := 1; i <= perPage; i++ {
				if i == 2 {
					parts = append(parts, `{"number":2,"title":"a pr","pull_request":{}}`)
					continue
				}
				parts = append(parts, fmt.Sprintf(`{"number":%d,"title":"t%d","body":null}`, i, i))
			}
			fmt.Fprint(w, "["+strings.Join(parts, ",")+"]")
			return
		}
		fmt.Fprint(w, `[{"number":500,"title":"last","body":"text"}]`)
	}))
	defer srv.Close()
	got, err := GitHub{Token: "tok", APIBase: srv.URL}.ReadyIssues(context.Background(), "me/a", "ready-for-agent")
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || len(got) != perPage || got[0].Number != 1 || got[1].Number != 3 || got[len(got)-1] != (ReadyIssue{Number: 500, Title: "last", Body: "text"}) {
		t.Fatalf("asked %v, got %d issues, first %+v last %+v", asked, len(got), got[0], got[len(got)-1])
	}
}

func TestOpenPRIssuesReadsClosingKeywords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/me/a/pulls" || r.URL.Query().Get("state") != "open" {
			http.Error(w, "bad", 400)
			return
		}
		fmt.Fprint(w, `[{"body":"Resolves #12.\n\nSatat-Run: x"},{"body":"fixes: #3 and closes #4"},{"body":null},{"body":"mentions #99, resolves other/repo#7"}]`)
	}))
	defer srv.Close()
	got, err := GitHub{APIBase: srv.URL}.OpenPRIssues(context.Background(), "me/a")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int]bool{12: true, 3: true, 4: true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestErrorsNameTheStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := GitHub{APIBase: srv.URL}.ReadyIssues(context.Background(), "me/a", "x")
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "Issues: read") {
		t.Fatalf("got %v", err)
	}
}

func TestAFullLastPageIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := make([]string, perPage)
		for i := range parts {
			parts[i] = `{"body":"x"}`
		}
		fmt.Fprint(w, "["+strings.Join(parts, ",")+"]")
	}))
	defer srv.Close()
	if _, err := (GitHub{APIBase: srv.URL}).OpenPRIssues(context.Background(), "me/a"); err == nil || !strings.Contains(err.Error(), "partial") {
		t.Fatalf("got %v", err)
	}
}
