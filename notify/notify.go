// Package notify tells people where a run is (#235). The worker sends an Event at every state it
// knows in-process; each channel (the GitHub issue, ntfy) is a Notifier behind one interface.
//
// Notifying is best effort: a channel that fails is logged and never fails or delays a run beyond
// its timeout. Wrap channels with Safe before handing them to the worker.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

// State is where a run is.
type State string

// The states, in the order a run normally passes them.
const (
	// Queued: the poller or the webhook intake pushed the issue. There is no run ID yet.
	Queued State = "queued"
	// Started: a worker took the run from the queue (each attempt, after a rate-limit back-off too).
	Started State = "started"
	// Skipped: the issue was not ready at run start (closed, label gone). Reason says why.
	Skipped State = "skipped"
	// Cloned: the worker cloned the repo at the base.
	Cloned State = "cloned"
	// StepStarted: the agent's step is launching.
	StepStarted State = "step started"
	// StepFinished: the step exited. Reason carries its result status.
	StepFinished State = "step finished"
	// RateLimited: the step hit the subscription limit; the lane is closed and a retry is queued.
	RateLimited State = "rate limited"
	// Published: the branch is pushed and the draft PR is open (or was already).
	Published State = "published"
	// NotPublished: the step had changes but publishing failed or was refused. Reason says why.
	NotPublished State = "not published"
	// Completed: the run ended without error, with or without a PR.
	Completed State = "completed"
	// Failed: the run ended with an error. Reason is a one-line cause.
	Failed State = "failed"

	// WorkerDown and WorkerUp come from outside the worker (package watch): no worker has had a
	// heartbeat for a while, and then one has again. They name no repo or issue.
	WorkerDown State = "worker down"
	WorkerUp   State = "worker back"
)

// Event is one state of one run of one issue.
type Event struct {
	State State
	// Run is the Hatchet run ID; empty for Queued.
	Run   string
	Repo  string
	Issue int
	Lane  string
	// Attempt counts rate-limit back-offs before this attempt.
	Attempt int
	// Reason is a short, one-line explanation (a skip reason, a result status, a cause).
	Reason string
	// Detail is the full text behind Reason, if longer. Channels may show it collapsed.
	Detail string
	Branch string
	PRURL  string
	// Source says who queued the issue ("poller", "webhook"), for Queued.
	Source string
	At     time.Time

	// The rest fills in the message (ADR 0036); each is set where it is known, and a message
	// leaves out what is missing.

	// IssueTitle names the issue where the reader is not on it (ntfy).
	IssueTitle string
	// Agent is the coding agent's name, such as "Claude Code".
	Agent string
	// Base is the branch the run started from, SHA the commit cloned.
	Base, SHA string
	// GlobalCap and RepoCap are the run caps, for Queued.
	GlobalCap, RepoCap int
	// Outcome is the step's result: "changes", "no_changes", "failed", "timed_out",
	// "out_of_memory" or "cancelled".
	Outcome string
	// Commits the agent made; Rehearsal is the pre-push check's result ("passed", "failed") and
	// Rehearsals how many times it ran.
	Commits, Rehearsals int
	Rehearsal           string
	// Summary is the agent's own final message.
	Summary string
	// Took is how long the step (StepFinished) or the run (Completed, Failed) took.
	Took time.Duration
	// PRNumber, PRTitle and Files describe the draft PR.
	PRNumber int
	PRTitle  string
	Files    int
	// MaxAttempts is how many rate-limit retries a run gets; ResetsAt when the limit resets.
	MaxAttempts int
	ResetsAt    time.Time
	// RunURL links to the run in Hatchet's dashboard. Safe fills it from Run.
	RunURL string
}

// Notifier delivers events on one channel.
type Notifier interface {
	Notify(ctx context.Context, ev Event) error
}

// Multi sends each event to every notifier and joins their errors.
type Multi []Notifier

// Notify implements Notifier.
func (m Multi) Notify(ctx context.Context, ev Event) error {
	var errs []error
	for _, n := range m {
		if err := n.Notify(ctx, ev); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DefaultTimeout bounds one event's delivery on every channel together.
const DefaultTimeout = 15 * time.Second

// Safe wraps a notifier for the worker: each event gets its own timeout, detached from the run's
// context so that a cancelled or timed-out run is still reported, secrets are replaced with ***
// in Reason and Detail, and a delivery error is logged, never returned. A nil Notifier does nothing.
type Safe struct {
	Next    Notifier
	Log     *slog.Logger
	Secrets []string
	Timeout time.Duration
	// Now stamps events that have no time; nil means time.Now.
	Now func() time.Time
	// RunURL, when set, links an event with a Run to that run's page.
	RunURL func(run string) string
}

// Send delivers ev. It never fails.
func (s *Safe) Send(ctx context.Context, ev Event) {
	if s == nil || s.Next == nil {
		return
	}
	if ev.At.IsZero() {
		now := time.Now
		if s.Now != nil {
			now = s.Now
		}
		ev.At = now().UTC()
	}
	if ev.RunURL == "" && ev.Run != "" && s.RunURL != nil {
		ev.RunURL = s.RunURL(ev.Run)
	}
	// Detail stays even when it equals Reason: a failure's message shows the plain cause, not the
	// raw error, so Detail is the only place the reader sees it (ADR 0036).
	ev.Reason, ev.Detail, ev.Summary = Short(s.scrub(ev.Reason)), s.scrub(ev.Detail), s.scrub(ev.Summary)
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := s.Next.Notify(ctx, ev); err != nil && s.Log != nil {
		s.Log.Warn("notify failed", "state", string(ev.State), "run", ev.Run, "issue", fmt.Sprintf("%s#%d", ev.Repo, ev.Issue), "err", err)
	}
}

func (s *Safe) scrub(t string) string {
	for _, v := range s.Secrets {
		if v != "" {
			t = strings.ReplaceAll(t, v, "***")
		}
	}
	return t
}

// maxReason is the longest Reason a channel gets; the rest stays in Detail.
const maxReason = 200

// Short is the first non-blank line of t, cut to a notification's length.
func Short(t string) string {
	line := ""
	for _, l := range strings.Split(t, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	if utf8.RuneCountInString(line) <= maxReason {
		return line
	}
	r := []rune(line)
	return string(r[:maxReason-1]) + "…"
}
