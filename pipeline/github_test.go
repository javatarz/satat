package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubIssuesReadsTheIssue(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		w.Write([]byte(`{"number":7,"title":"T","body":null,"state":"open","labels":[{"name":"bug"},{"name":"ready-for-agent"}]}`))
	}))
	defer srv.Close()
	got, err := GitHubIssues{Token: "read", APIBase: srv.URL}.Issue(context.Background(), "a/b", 7)
	if err != nil || strings.Join(got.Labels, ",") != "bug,ready-for-agent" || !got.Open || got.PullRequest {
		t.Fatalf("issue %+v err %v", got, err)
	}
	if got.Title != "T" || got.Body != "" {
		t.Errorf("title %q body %q", got.Title, got.Body)
	}
	if path != "/repos/a/b/issues/7" || auth != "Bearer read" {
		t.Errorf("request: path %q auth %q", path, auth)
	}
}

func TestGitHubIssuesSeesClosedIssuesAndPullRequests(t *testing.T) {
	closed, err := GitHubIssues{APIBase: serve(t, 200, `{"state":"closed","labels":[]}`).URL}.Issue(context.Background(), "a/b", 1)
	if err != nil || closed.Open {
		t.Errorf("closed issue: %+v err %v", closed, err)
	}
	pr, err := GitHubIssues{APIBase: serve(t, 200, `{"state":"open","pull_request":{"url":"x"},"labels":[]}`).URL}.Issue(context.Background(), "a/b", 1)
	if err != nil || !pr.PullRequest {
		t.Errorf("pull request: %+v err %v", pr, err)
	}
}

func TestGitHubIssuesErrors(t *testing.T) {
	cases := map[int]string{404: "Issues: read", 403: "rate limit", 429: "rate limit", 500: "500"}
	for status, want := range cases {
		g := GitHubIssues{APIBase: serve(t, status, `{"message":"x"}`).URL}
		if _, err := g.Issue(context.Background(), "a/b", 7); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("status %d: error %v, want one mentioning %q", status, err, want)
		}
	}
	big := `{"state":"open","body":"` + strings.Repeat("x", maxIssueBody) + `","labels":[]}`
	if _, err := (GitHubIssues{APIBase: serve(t, 200, big).URL}).Issue(context.Background(), "a/b", 7); err == nil {
		t.Error("an answer over the size limit must be an error")
	}
	g := GitHubIssues{APIBase: "http://127.0.0.1:1"}
	for _, repo := range []string{"a", "a/../b", "/a/b", "a/b/c"} {
		if _, err := g.Issue(context.Background(), repo, 1); err == nil || !strings.Contains(err.Error(), "not an issue") {
			t.Errorf("repo %q must be refused, got %v", repo, err)
		}
	}
	if _, err := g.Issue(context.Background(), "a/b", 0); err == nil {
		t.Error("issue 0 must be refused")
	}
}
