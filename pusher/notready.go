package pusher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ReadyLabel is what a human puts on an issue to let Satat run it; NeedsInfoLabel is where
	// NotReady moves it (#175).
	ReadyLabel     = "ready-for-agent"
	NeedsInfoLabel = "needs-info"

	maxListPages = 10
)

// NotReadyRequest says an issue's repo failed the readiness check. Body is the comment text the
// check wrote; Key identifies the set of failures it lists.
type NotReadyRequest struct {
	Repo  string
	Issue int
	Key   string
	Body  string
}

// NotReadyOutcome says what NotReady did. Skipped is set, with the reason, when it did nothing.
type NotReadyOutcome struct {
	Commented, Relabeled bool
	Skipped              string
}

func marker(key string) string { return "<!-- satat-not-ready:" + key + " -->" }

// NotReady stops an issue that failed the readiness check: it posts one comment and moves the
// label from ready-for-agent to needs-info, so the poller never queues it again. This is the
// only place Satat writes to an issue, and it uses the pusher's own token, which needs
// issues: write.
//
// It is idempotent. It does nothing when the issue is no longer open and labelled ready-for-agent
// (an earlier delivery already moved it). It does not comment again when this token's user has
// already left a comment with the same Key since the label was last added, so a retry after a
// crash between the comment and the relabel only finishes the relabel. A human who re-adds the
// label without fixing the repo gets a fresh comment, because the label is newer than the old one.
// The label is added before the other is removed, so a crash never leaves the issue with neither.
func (p *Pusher) NotReady(ctx context.Context, req NotReadyRequest) (NotReadyOutcome, error) {
	p.defaults()
	var out NotReadyOutcome
	switch {
	case p.Token == "":
		return out, errors.New("no write token configured")
	case !repoRE.MatchString(req.Repo) || strings.Contains(req.Repo, ".."):
		return out, refuse("repo %q is not owner/name", req.Repo)
	case req.Issue <= 0:
		return out, refuse("issue %d is not an issue number", req.Issue)
	case !keyRE.MatchString(req.Key):
		return out, refuse("key %q is not a short hex string", req.Key)
	case strings.TrimSpace(req.Body) == "" || len(req.Body) > 10000:
		return out, refuse("the comment must be between 1 and 10000 bytes")
	}
	issue := fmt.Sprintf("/repos/%s/issues/%d", req.Repo, req.Issue)

	var st struct {
		State       string `json:"state"`
		PullRequest any    `json:"pull_request"`
		Labels      []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := p.api(ctx, http.MethodGet, issue, nil, &st); err != nil {
		return out, err
	}
	ready := false
	for _, l := range st.Labels {
		ready = ready || l.Name == ReadyLabel
	}
	switch {
	case st.PullRequest != nil:
		out.Skipped = "it is a pull request"
		return out, nil
	case st.State != "open":
		out.Skipped = "the issue is closed"
		return out, nil
	case !ready:
		out.Skipped = "the issue is no longer labelled " + ReadyLabel
		return out, nil
	}

	dup, err := p.alreadyCommented(ctx, issue, req.Key)
	if err != nil {
		return out, err
	}
	if !dup {
		if err := p.api(ctx, http.MethodPost, issue+"/comments", map[string]string{"body": marker(req.Key) + "\n" + req.Body}, nil); err != nil {
			return out, err
		}
		out.Commented = true
	}
	if err := p.api(ctx, http.MethodPost, issue+"/labels", map[string][]string{"labels": {NeedsInfoLabel}}, nil); err != nil {
		return out, err
	}
	err = p.api(ctx, http.MethodDelete, issue+"/labels/"+url.PathEscape(ReadyLabel), nil, nil)
	var ae *apiError
	if err != nil && !(errors.As(err, &ae) && ae.Status == http.StatusNotFound) {
		return out, err
	}
	out.Relabeled = true
	return out, nil
}

// alreadyCommented reports whether the token's own user left a comment with this key after the
// label was last added.
func (p *Pusher) alreadyCommented(ctx context.Context, issue, key string) (bool, error) {
	var me struct {
		Login string `json:"login"`
	}
	if err := p.api(ctx, http.MethodGet, "/user", nil, &me); err != nil {
		return false, err
	}
	var since time.Time
	for page := 1; page <= maxListPages; page++ {
		var evs []struct {
			Event     string    `json:"event"`
			CreatedAt time.Time `json:"created_at"`
			Label     struct {
				Name string `json:"name"`
			} `json:"label"`
		}
		if err := p.api(ctx, http.MethodGet, fmt.Sprintf("%s/events?per_page=100&page=%d", issue, page), nil, &evs); err != nil {
			return false, err
		}
		for _, e := range evs {
			if e.Event == "labeled" && e.Label.Name == ReadyLabel && e.CreatedAt.After(since) {
				since = e.CreatedAt
			}
		}
		if len(evs) < 100 {
			break
		}
	}
	want := marker(key)
	for page := 1; page <= maxListPages; page++ {
		var cs []struct {
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"created_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := p.api(ctx, http.MethodGet, fmt.Sprintf("%s/comments?per_page=100&page=%d", issue, page), nil, &cs); err != nil {
			return false, err
		}
		for _, c := range cs {
			if strings.EqualFold(c.User.Login, me.Login) && strings.HasPrefix(c.Body, want) && !c.CreatedAt.Before(since) {
				return true, nil
			}
		}
		if len(cs) < 100 {
			break
		}
	}
	return false, nil
}
