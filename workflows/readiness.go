package workflows

import (
	"context"
	"errors"
	"fmt"

	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/readiness"
)

// ReadinessChecker checks a repo meets Satat's prerequisites (#175). readiness.Checker is the
// real one.
type ReadinessChecker interface {
	Check(ctx context.Context, req readiness.Request) (readiness.Result, error)
}

// NotReadyReporter stops an issue that failed the check: one comment and a label move. The
// pusher is the only component that writes to GitHub, so *pusher.Pusher is the real one.
type NotReadyReporter interface {
	NotReady(ctx context.Context, req pusher.NotReadyRequest) (pusher.NotReadyOutcome, error)
}

// StatusNotReady is the output status of a run that did not start because the repo failed the
// readiness check. Like StatusSkipped it is a normal result, so it is not retried.
const StatusNotReady = "not_ready"

// repoNotReady runs the readiness check once the issue itself has passed the gate. It returns
// stop true when the run must end here. A failed check is a normal result: nothing launches, the
// pusher posts one comment and moves the label to needs-info. A check that could not run (GitHub
// unreachable) fails the run without touching the issue, so a hiccup never relabels it. With no
// checker or no reporter configured nothing is launched.
func (h Handler) repoNotReady(ctx context.Context, runID string, in Input) (Output, bool, error) {
	if h.Ready == nil || h.Reporter == nil {
		return Output{Run: runID}, true, errors.Join(errors.New("not launched: no readiness check configured"), h.stopEarlierAttempt(ctx, runID))
	}
	res, err := h.Ready.Check(ctx, readiness.Request{Repo: in.Repo, Base: in.Base, Issue: in.Issue.Number})
	if err != nil {
		err = fmt.Errorf("not launched: readiness check of %s: %w", in.Repo, err)
		return Output{Run: runID}, true, errors.Join(err, h.stopEarlierAttempt(ctx, runID))
	}
	if res.Ready() {
		return Output{}, false, nil
	}
	done, rerr := h.Reporter.NotReady(ctx, pusher.NotReadyRequest{Repo: in.Repo, Issue: in.Issue.Number, Key: res.Key(), Body: res.Comment()})
	if rerr != nil {
		rerr = fmt.Errorf("not launched: %s failed the readiness check but the issue could not be updated: %w", in.Repo, rerr)
	}
	reason := fmt.Sprintf("%s is not ready: %d blocking problem(s), reported on the issue", in.Repo, len(res.Failures))
	if done.Skipped != "" {
		reason += " earlier (" + done.Skipped + ")"
	}
	return Output{Run: runID, Status: StatusNotReady, Reason: reason}, true, errors.Join(rerr, h.stopEarlierAttempt(ctx, runID))
}
