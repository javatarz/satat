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
	ev.Reason, ev.Detail = Short(s.scrub(ev.Reason)), s.scrub(ev.Detail)
	if ev.Detail == ev.Reason {
		ev.Detail = ""
	}
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

// Text is a one-line, human summary of ev, for channels that show plain text.
func Text(ev Event) string {
	var b strings.Builder
	switch ev.State {
	case Published:
		if ev.PRURL != "" {
			fmt.Fprintf(&b, "Satat opened a draft PR: %s", ev.PRURL)
		} else {
			b.WriteString("Satat published the change")
		}
	case Failed:
		b.WriteString("Satat failed")
	default:
		fmt.Fprintf(&b, "Satat: %s", ev.State)
	}
	if ev.Reason != "" && ev.State != Published {
		fmt.Fprintf(&b, ": %s", ev.Reason)
	}
	return b.String()
}
