package readiness

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// server is a fake GitHub that records every request and refuses anything but GET.
func server(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) (HTTP, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet {
			t.Errorf("the check made a %s to %s; it must only GET", r.Method, r.URL.Path)
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		h, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return HTTP{Token: "tok", APIBase: srv.URL}, &seen
}

func body(s string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, s) }
}

func TestHTTPReadsEverythingWithGETOnly(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte(goodCommands))
	gh, seen := server(t, map[string]func(http.ResponseWriter, *http.Request){
		"/user": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-OAuth-Scopes", "repo, read:org")
			fmt.Fprint(w, `{"login":"satat-bot"}`)
		},
		"/repos/o/r": body(`{"full_name":"o/r","default_branch":"main","permissions":{"admin":false,"maintain":false,"push":true,"pull":true}}`),
		"/repos/o/r/contents/.satat/commands.yaml": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("ref") != "main" {
				t.Errorf("ref = %q", r.URL.Query().Get("ref"))
			}
			fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q}`, enc[:10]+"\n"+enc[10:])
		},
		"/repos/o/r/rules/branches/main": body(`[{"type":"pull_request","ruleset_id":7,"ruleset_source_type":"Repository"}]`),
		"/repos/o/r/rulesets/7":          body(`{"id":7,"name":"main","enforcement":"active","current_user_can_bypass":"never"}`),
		"/repos/o/r/issues/5/events": body(`[
			{"event":"labeled","label":{"name":"ready-for-agent"},"actor":{"login":"satat-bot"}},
			{"event":"unlabeled","label":{"name":"ready-for-agent"},"actor":{"login":"satat-bot"}},
			{"event":"labeled","label":{"name":"ready-for-agent"},"actor":{"login":"karun"}}]`),
		"/user/repos":              body(`[{"full_name":"o/r"}]`),
		"/repos/o/r/branches/main": body(`{"name":"main","protected":true}`),
	})
	ctx := context.Background()
	v, err := gh.Viewer(ctx)
	if err != nil || v.Login != "satat-bot" || !v.ScopesKnown || len(v.Scopes) != 2 {
		t.Fatalf("viewer %+v %v", v, err)
	}
	r, err := gh.Repo(ctx, "o/r")
	if err != nil || r.DefaultBranch != "main" || !r.Push || r.Admin {
		t.Fatalf("repo %+v %v", r, err)
	}
	raw, err := gh.File(ctx, "o/r", CommandsFile, "main")
	if err != nil || string(raw) != goodCommands {
		t.Fatalf("file %q %v", raw, err)
	}
	rules, err := gh.BranchRules(ctx, "o/r", "main")
	if err != nil || len(rules) != 1 || rules[0].RulesetID != 7 {
		t.Fatalf("rules %+v %v", rules, err)
	}
	rs, err := gh.Ruleset(ctx, "o/r", 7)
	if err != nil || rs.CurrentUserCanBypass != "never" {
		t.Fatalf("ruleset %+v %v", rs, err)
	}
	who, found, err := gh.LabeledBy(ctx, "o/r", 5, "ready-for-agent")
	if err != nil || !found || who != "karun" {
		t.Fatalf("labeled by %q %v %v; want the latest labelling", who, found, err)
	}
	if p, err := gh.BranchProtected(ctx, "o/r", "main"); err != nil || !p {
		t.Fatalf("protected %v %v", p, err)
	}
	repos, trunc, err := gh.AccessibleRepos(ctx)
	if err != nil || trunc || len(repos) != 1 {
		t.Fatalf("repos %v %v %v", repos, trunc, err)
	}
	for _, s := range *seen {
		if !strings.HasPrefix(s, "GET ") {
			t.Errorf("non-GET request %q", s)
		}
	}
}

func TestHTTPFineGrainedTokenHasNoScopesHeader(t *testing.T) {
	gh, _ := server(t, map[string]func(http.ResponseWriter, *http.Request){"/user": body(`{"login":"b"}`)})
	v, err := gh.Viewer(context.Background())
	if err != nil || v.ScopesKnown {
		t.Fatalf("%+v %v; a response without X-OAuth-Scopes is not a classic token", v, err)
	}
}

func TestHTTPMapsStatusToSentinels(t *testing.T) {
	gh, _ := server(t, map[string]func(http.ResponseWriter, *http.Request){
		"/repos/o/r":  func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusForbidden) },
		"/repos/o/r2": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusBadGateway) },
		"/repos/o/r4": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "You have exceeded a secondary rate limit", http.StatusForbidden)
		},
		"/repos/o/r5": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "API rate limit exceeded for user", http.StatusForbidden)
		},
		"/repos/o/r6": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "slow down", http.StatusTooManyRequests) },
		"/repos/o/r3": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, "limit", http.StatusForbidden)
		},
	})
	ctx := context.Background()
	if _, err := gh.Repo(ctx, "o/r"); !errors.Is(err, ErrForbidden) {
		t.Errorf("403: %v", err)
	}
	if _, err := gh.Repo(ctx, "o/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404: %v", err)
	}
	for _, r := range []string{"o/r2", "o/r3", "o/r4", "o/r5", "o/r6"} {
		if _, err := gh.Repo(ctx, r); err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
			t.Errorf("%s: %v; a 5xx or a rate limit is an error, not a finding", r, err)
		}
	}
}

func TestHTTPListsPagesAndSaysWhenTruncated(t *testing.T) {
	page := func(w http.ResponseWriter, r *http.Request) {
		var items []string
		for i := 0; i < perPage; i++ {
			items = append(items, fmt.Sprintf(`{"full_name":"o/p%s-%d"}`, r.URL.Query().Get("page"), i))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
	}
	gh, _ := server(t, map[string]func(http.ResponseWriter, *http.Request){"/user/repos": page})
	repos, trunc, err := gh.AccessibleRepos(context.Background())
	if err != nil || !trunc || len(repos) != perPage*maxPages {
		t.Fatalf("%d repos, truncated %v, %v", len(repos), trunc, err)
	}
}

func TestHTTPRefusesAnOddRepoName(t *testing.T) {
	gh, seen := server(t, nil)
	for _, r := range []string{"o/../x", "o", "o/r?x=1", ""} {
		if _, err := gh.Repo(context.Background(), r); err == nil {
			t.Errorf("%q accepted", r)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("requests made for bad names: %v", *seen)
	}
}

func TestHTTPNullActorReadsAsAnEmptyLogin(t *testing.T) {
	gh, _ := server(t, map[string]func(http.ResponseWriter, *http.Request){
		"/repos/o/r/issues/5/events": body(`[{"event":"labeled","label":{"name":"ready-for-agent"},"actor":null}]`),
	})
	who, found, err := gh.LabeledBy(context.Background(), "o/r", 5, "ready-for-agent")
	if err != nil || !found || who != "" {
		t.Fatalf("%q %v %v", who, found, err)
	}
}
