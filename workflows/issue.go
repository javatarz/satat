// Package workflows defines the Hatchet workflow that takes an issue to a draft PR (ADR 0023).
//
// The workflow is built from a Spec, and a Spec is checked before it is registered: four caps and
// explicit timeouts, because Hatchet's defaults drop queued work silently.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/pipeline"
	"github.com/javatarz/satat/runner"
)

const (
	// Name is the workflow's name in Hatchet.
	Name = "satat-issue"
	// EventIssue starts the workflow. Intake (slice 6) and the hand trigger push it.
	EventIssue = "satat:issue"

	// MinScheduleTimeout is the shortest wait behind a cap or a rate limit a task may allow. The
	// default is 5 minutes, after which queued work is cancelled without running (hatchet-caps-spike).
	MinScheduleTimeout = 24 * time.Hour
	// ScheduleTimeout is what Define sets. 720 h was accepted in the spike.
	ScheduleTimeout = 720 * time.Hour

	// ReadyLabel is the label a human puts on an issue to let Satat run it (the roadmap's triage
	// rule). The worker re-reads it at run start, so every way in is gated by it.
	ReadyLabel = "ready-for-agent"
	// StatusSkipped is the output status of a run that did not launch because the issue was not
	// ready. It is a normal result, not a failure, so it is not retried.
	StatusSkipped = "skipped"
)

// Input is the run input. It is also what the concurrency expressions read, so the caps that
// differ per lane travel in it (the mapper looks them up in satat.yaml).
type Input struct {
	Repo    string       `json:"repo"`
	Base    string       `json:"base"`
	Lane    string       `json:"lane"`
	Account string       `json:"account"`
	LaneCap int          `json:"lane_cap"`
	Issue   runner.Issue `json:"issue"`
}

// Output is what a finished run reports in the dashboard.
type Output struct {
	Run    string `json:"run"`
	Status string `json:"status"`
	Branch string `json:"branch,omitempty"`
	PRURL  string `json:"pr_url,omitempty"`
	Reused bool   `json:"reused,omitempty"`
	// Backoffs is how many times the run has been backed off for the Claude rate limit, set on
	// the attempt that hit it. Hatchet's attempt history shows the rest.
	Backoffs int `json:"backoffs,omitempty"`
	ExitCode int `json:"exit_code"`
	Commits  int `json:"commits"`
	// Reason says why a run was skipped.
	Reason string `json:"reason,omitempty"`
}

// MapIssue builds a run input from config. It is the only place caps are looked up. It lower-cases
// the repo (GitHub ignores case there) so the dedupe key in the input matches IssueKey.
func MapIssue(cfg config.Config, repo, base, lane string, issue runner.Issue) (Input, error) {
	account, ok := cfg.Accounts[lane]
	if !ok {
		return Input{}, fmt.Errorf("unknown lane %q", lane)
	}
	return Input{Repo: strings.ToLower(repo), Base: base, Lane: lane, Account: account, LaneCap: cfg.LaneMaxRuns[lane], Issue: issue}, nil
}

// Cap is one concurrency expression. Max is the static limit; MaxExpression, when set, computes
// each group's limit from the input instead.
type Cap struct {
	Expression    string
	Max           int
	MaxExpression string
}

// Spec is everything Register needs to know about the workflow's shape.
type Spec struct {
	Name   string
	Events []string
	Caps   []Cap
	// DedupeKey is a concurrency expression that names one issue. Hatchet cancels a run whose key
	// already has one in flight (CANCEL_NEWEST), so a duplicate event never starts a second run (#182).
	// It is registered first: Hatchet chains strategies, so a key registered after the caps only
	// sees runs that cleared them, and a duplicate waiting behind a cap would not be cancelled.
	DedupeKey        string
	ScheduleTimeout  time.Duration
	ExecutionTimeout time.Duration
}

// The four caps, in the order ADR 0023 lists them.
var capExpressions = []string{"'global'", "input.lane", "input.repo", "input.account"}

// issueKeyExpression names one issue in a concurrency expression: "owner/name#N". It is plain CEL
// (no extension functions), so registration cannot fail on a function Hatchet lacks. Unlike
// IssueKey it does not lower-case, because MapIssue already did, so it sees the same value.
const issueKeyExpression = "input.repo + '#' + string(input.issue.number)"

// Define is the workflow Satat runs. executionTimeout bounds one run, and must cover the longest
// lane timeout plus publishing.
func Define(l config.Limits, executionTimeout time.Duration) Spec {
	return Spec{
		Name:   Name,
		Events: []string{EventIssue},
		Caps: []Cap{
			{Expression: capExpressions[0], Max: l.Global},
			{Expression: capExpressions[1], Max: 1, MaxExpression: "input.lane_cap"},
			{Expression: capExpressions[2], Max: l.PerRepo},
			{Expression: capExpressions[3], Max: l.PerAccount},
		},
		DedupeKey:        issueKeyExpression,
		ScheduleTimeout:  ScheduleTimeout,
		ExecutionTimeout: executionTimeout,
	}
}

// Validate refuses a spec that would register a workflow Hatchet could quietly break.
func (s Spec) Validate() error {
	var errs []error
	if s.Name == "" {
		errs = append(errs, errors.New("no name"))
	}
	if len(s.Events) == 0 {
		errs = append(errs, errors.New("no trigger event"))
	}
	if len(s.Caps) != len(capExpressions) {
		errs = append(errs, fmt.Errorf("%d caps, want the four of ADR 0023", len(s.Caps)))
	} else {
		for i, want := range capExpressions {
			c := s.Caps[i]
			if c.Expression != want {
				errs = append(errs, fmt.Errorf("cap %d is %q, want %q", i+1, c.Expression, want))
			}
			if c.Max <= 0 {
				errs = append(errs, fmt.Errorf("cap %q has no positive limit", c.Expression))
			}
		}
	}
	if s.DedupeKey != issueKeyExpression {
		errs = append(errs, fmt.Errorf("dedupe key is %q, want %q: a duplicate event would start a second run", s.DedupeKey, issueKeyExpression))
	}
	if s.ScheduleTimeout < MinScheduleTimeout {
		errs = append(errs, fmt.Errorf("schedule timeout %s is under %s: queued work would be cancelled", s.ScheduleTimeout, MinScheduleTimeout))
	}
	if s.ExecutionTimeout <= 0 {
		errs = append(errs, errors.New("no execution timeout"))
	}
	return errors.Join(errs...)
}

// IssueReader reads an issue's current state. pipeline.GitHubIssues is the real one.
type IssueReader interface {
	Issue(ctx context.Context, repo string, number int) (pipeline.IssueState, error)
}

// Handler does the work of one run. It is the part of the workflow that does not depend on Hatchet.
type Handler struct {
	Launcher launcher.Launcher
	Pusher   pipeline.Publisher
	// Cloner clones the repo before each step starts (pipeline.GitCloner, with the read token).
	Cloner pipeline.Cloner
	// Issues gates each run: only an open issue labelled ReadyLabel runs. A nil reader fails
	// closed: nothing is launched.
	Issues IssueReader
	// Allowed lists the repos a run may start in (the intake and poller lists). It is checked at
	// run start, so an event for a repo no longer listed, or a forged one, launches nothing. Empty
	// allows no repo.
	Allowed []string
	// Gate closes the account's lane when a step hits the subscription rate limit. With a nil
	// Gate the limit is reported but the lane is not paused.
	Gate Gate
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Run takes one input to a result. The issue's title and body are read from GitHub at run start;
// the text in the input is ignored, so a forged event cannot choose what the agent is told (#181).
// An issue that is not ready at run start (closed, a pull request, without ReadyLabel, or in a
// repo that is no longer allowed) is skipped, which is a normal result; if it cannot be read,
// nothing is launched and the run fails. A step that fails, or a change that cannot be published,
// is an error so the run shows as failed; a step that changes nothing is a normal result.
//
// A step that hit the subscription rate limit closes its account's lane until the reset and
// returns a *RateLimitError, which Register leaves retryable.
func (h Handler) Run(ctx context.Context, runID string, in Input) (Output, error) {
	return h.RunAttempt(ctx, runID, in, 0)
}

// RunAttempt is Run for a run that has already been backed off attempt times; the count sets how
// long to wait when the step reports no reset time.
func (h Handler) RunAttempt(ctx context.Context, runID string, in Input, attempt int) (Output, error) {
	st, reason, err := h.notReady(ctx, in)
	if err != nil {
		err = fmt.Errorf("not launched: cannot read %s#%d: %w", in.Repo, in.Issue.Number, err)
		return Output{Run: runID}, errors.Join(err, h.stopEarlierAttempt(ctx, runID))
	}
	if reason != "" {
		return Output{Run: runID, Status: StatusSkipped, Reason: reason}, h.stopEarlierAttempt(ctx, runID)
	}
	in.Issue.Title, in.Issue.Body = st.Title, st.Body
	rep, err := pipeline.Run(ctx, pipeline.Options{
		Launcher: h.Launcher, Pusher: h.Pusher, Cloner: h.Cloner,
		Run: runID, Lane: in.Lane, Repo: in.Repo, Base: in.Base, Issue: in.Issue,
	})
	out := Output{Run: runID, ExitCode: rep.Launch.ExitCode}
	if rep.Runner != nil {
		out.Status, out.Branch, out.Commits = rep.Runner.Status, rep.Runner.Branch, rep.Runner.Commits
	}
	if err != nil {
		return out, err
	}
	if resets, limited := DetectRateLimit(rep.Runner); limited {
		return h.backOff(ctx, out, in, resets, attempt)
	}
	if rep.Push != nil {
		out.PRURL, out.Reused = rep.Push.PRURL, rep.Push.Reused
	}
	if !rep.OK() {
		return out, failure(rep)
	}
	return out, nil
}

// backOff closes the lane and reports the attempt as rate limited.
func (h Handler) backOff(ctx context.Context, out Output, in Input, resets time.Time, attempt int) (Output, error) {
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	out.Status = StatusRateLimited
	rl := &RateLimitError{ResetsAt: resets}
	if h.Gate == nil {
		return out, rl
	}
	if err := h.Gate.Close(ctx, in.Account, BackoffUntil(now(), resets, attempt)); err != nil {
		// Without the pause the retry would run straight into the limit again, so do not retry.
		// %v, not %w: the error must not be a *RateLimitError, which Register would retry.
		return out, fmt.Errorf("claude rate limit hit but the lane could not be paused: %v", err)
	}
	return out, rl
}

// notReady re-reads the issue, because it can change while the run waits behind a cap. It returns
// why the issue must not run, or "" when it may, along with the state it read.
func (h Handler) notReady(ctx context.Context, in Input) (pipeline.IssueState, string, error) {
	if h.Issues == nil {
		return pipeline.IssueState{}, "", errors.New("no issue reader configured")
	}
	ref := fmt.Sprintf("%s#%d", in.Repo, in.Issue.Number)
	if !slices.ContainsFunc(h.Allowed, func(a string) bool { return strings.EqualFold(a, in.Repo) }) {
		return pipeline.IssueState{}, in.Repo + " is not an allowed repo", nil
	}
	st, err := h.Issues.Issue(ctx, in.Repo, in.Issue.Number)
	if err != nil {
		return pipeline.IssueState{}, "", err
	}
	switch {
	case st.PullRequest:
		return st, ref + " is a pull request, not an issue", nil
	case !st.Open:
		return st, ref + " is closed", nil
	case !slices.Contains(st.Labels, ReadyLabel):
		return st, fmt.Sprintf("%s is not labelled %s", ref, ReadyLabel), nil
	}
	return st, "", nil
}

// stopEarlierAttempt cancels this run's step when the gate stops the run. A re-delivered task
// (the worker restarted mid-step) would otherwise never reach Launch to adopt the container an
// earlier attempt started, and it would keep running, holding tokens and outside the caps. With
// no such container it does nothing.
func (h Handler) stopEarlierAttempt(ctx context.Context, runID string) error {
	if err := h.Launcher.Cancel(ctx, runID, pipeline.StepName); err != nil {
		return fmt.Errorf("could not stop an earlier attempt's step: %w", err)
	}
	return nil
}

func failure(rep pipeline.Report) error {
	switch {
	case rep.PushErr != nil:
		return fmt.Errorf("step succeeded but was not published: %w", rep.PushErr)
	case rep.Launch.TimedOut:
		return errors.New("step timed out")
	case rep.Launch.OOMKilled:
		return errors.New("step ran out of memory")
	case rep.Runner == nil:
		return fmt.Errorf("step exited %d and left no result", rep.Launch.ExitCode)
	default:
		return fmt.Errorf("step failed (exit %d): %s", rep.Launch.ExitCode, rep.Runner.Error)
	}
}

// Register builds the workflow in Hatchet from a validated Spec.
func Register(client *hatchet.Client, spec Spec, h Handler) (*hatchet.Workflow, error) {
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("refusing to register %q: %w", spec.Name, err)
	}
	strategy := hatchet.GroupRoundRobin
	caps := make([]hatchet.Concurrency, 0, len(spec.Caps))
	for _, c := range spec.Caps {
		max := int32(c.Max)
		hc := hatchet.Concurrency{Expression: c.Expression, MaxRuns: &max, LimitStrategy: &strategy}
		if c.MaxExpression != "" {
			expr := c.MaxExpression
			hc.MaxRunsExpression = &expr
		}
		caps = append(caps, hc)
	}
	// One run per issue: the newest of a duplicate pair is cancelled, never the one in flight.
	// First in the chain, so it also counts runs still waiting behind the other caps: checked live,
	// a duplicate queued behind a cap is cancelled at once, whereas with the key last both ran in turn.
	one, cancelNewest := int32(1), hatchet.CancelNewest
	caps = append([]hatchet.Concurrency{{Expression: spec.DedupeKey, MaxRuns: &one, LimitStrategy: &cancelNewest}}, caps...)
	keyExpr, units, window := AccountKeyExpr, 1, hatchet.Second
	wf := client.NewWorkflow(spec.Name,
		hatchet.WithWorkflowEvents(spec.Events...),
		hatchet.WithWorkflowConcurrency(caps...),
	)
	wf.NewTask("run", func(ctx hatchet.Context, in Input) (*Output, error) {
		attempt := ctx.RetryCount()
		out, err := h.RunAttempt(ctx, ctx.WorkflowRunId(), in, attempt)
		var rl *RateLimitError
		switch {
		case errors.As(err, &rl):
			// The step did not fail: its lane is closed until the limit resets, and the retry
			// queues behind it. Hatchet shows each attempt, so the wait is visible.
			// Counts only rate-limit back-offs: this attempt is the (attempt+1)th, since every
			// other failure is non-retryable.
			out.Backoffs = attempt + 1
			ctx.Log("rate limited: " + err.Error())
		case err != nil:
			// Any other failure repeats on a retry (the launcher would adopt the same step).
			err = hatchet.NewNonRetryableError(err)
		}
		return &out, err
	},
		hatchet.WithScheduleTimeout(spec.ScheduleTimeout),
		hatchet.WithExecutionTimeout(spec.ExecutionTimeout),
		// Only a rate limit is retried (everything else is made non-retryable above): a
		// re-delivered task finds the earlier attempt's container (the launcher adopts it), so
		// retrying other failures would only repeat them.
		hatchet.WithRetries(MaxBackoffRetries),
		// A few seconds between attempts: the scheduler applies a closed lane up to a second after
		// Close, so an instant retry could still run. Verified against Hatchet Lite v0.110.5.
		hatchet.WithRetryBackoff(5, 5),
		// The retry waits on the account's rate limit, which the Gate closes until the reset.
		hatchet.WithRateLimits(&hatchet.RateLimit{Key: keyExpr, KeyExpr: &keyExpr, Units: &units, Duration: &window}),
	)
	return wf, nil
}
