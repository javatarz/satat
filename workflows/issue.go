// Package workflows defines the Hatchet workflow that takes an issue to a draft PR (ADR 0023).
//
// The workflow is built from a Spec, and a Spec is checked before it is registered: four caps and
// explicit timeouts, because Hatchet's defaults drop queued work silently.
package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// EventAgent starts a one-shot agent run on the same workflow, so it shares the four caps with
	// issue runs (deploy-targets.md decision 4).
	EventAgent = "satat:agent"

	// MinScheduleTimeout is the shortest wait behind a cap or a rate limit a task may allow. The
	// default is 5 minutes, after which queued work is cancelled without running (hatchet-caps-spike).
	MinScheduleTimeout = 24 * time.Hour
	// ScheduleTimeout is what Define sets. 720 h was accepted in the spike.
	ScheduleTimeout = 720 * time.Hour
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
	// Kind is KindIssue or KindAgent; empty means issue, so inputs from before agents still run.
	Kind string `json:"kind,omitempty"`
	// StepInput is the lane's input JSON for an agent run.
	StepInput json.RawMessage `json:"step_input,omitempty"`
	// Nonce is set by the agent CLI so it can find its own run; the worker ignores it.
	Nonce string `json:"nonce,omitempty"`
}

// Output is what a finished run reports in the dashboard.
type Output struct {
	Run      string `json:"run"`
	Status   string `json:"status"`
	Branch   string `json:"branch,omitempty"`
	PRURL    string `json:"pr_url,omitempty"`
	Reused   bool   `json:"reused,omitempty"`
	ExitCode int    `json:"exit_code"`
	Commits  int    `json:"commits"`

	// The rest is set by agent runs only.
	Kind      string         `json:"kind,omitempty"`
	Stdout    string         `json:"stdout,omitempty"`
	Stderr    string         `json:"stderr,omitempty"`
	Files     map[string]int `json:"files,omitempty"`
	TimedOut  bool           `json:"timed_out,omitempty"`
	OOMKilled bool           `json:"oom_killed,omitempty"`
	Cancelled bool           `json:"cancelled,omitempty"`
}

// MapIssue builds a run input from config. It is the only place caps are looked up.
func MapIssue(cfg config.Config, repo, base, lane string, issue runner.Issue) (Input, error) {
	account, ok := cfg.Accounts[lane]
	if !ok {
		return Input{}, fmt.Errorf("unknown lane %q", lane)
	}
	return Input{Repo: repo, Base: base, Lane: lane, Account: account, LaneCap: cfg.LaneMaxRuns[lane], Issue: issue}, nil
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
	Name             string
	Events           []string
	Caps             []Cap
	ScheduleTimeout  time.Duration
	ExecutionTimeout time.Duration
}

// The four caps, in the order ADR 0023 lists them.
var capExpressions = []string{"'global'", "input.lane", "input.repo", "input.account"}

// Define is the workflow Satat runs. executionTimeout bounds one run, and must cover the longest
// lane timeout plus publishing.
func Define(l config.Limits, executionTimeout time.Duration) Spec {
	return Spec{
		Name:   Name,
		Events: []string{EventIssue, EventAgent},
		Caps: []Cap{
			{Expression: capExpressions[0], Max: l.Global},
			{Expression: capExpressions[1], Max: 1, MaxExpression: "input.lane_cap"},
			{Expression: capExpressions[2], Max: l.PerRepo},
			{Expression: capExpressions[3], Max: l.PerAccount},
		},
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
	if s.ScheduleTimeout < MinScheduleTimeout {
		errs = append(errs, fmt.Errorf("schedule timeout %s is under %s: queued work would be cancelled", s.ScheduleTimeout, MinScheduleTimeout))
	}
	if s.ExecutionTimeout <= 0 {
		errs = append(errs, errors.New("no execution timeout"))
	}
	return errors.Join(errs...)
}

// Handler does the work of one run. It is the part of the workflow that does not depend on Hatchet.
type Handler struct {
	Launcher launcher.Launcher
	Pusher   pipeline.Publisher
	// Secrets are the values scrubbed from an agent run's output tails.
	Secrets []string
}

// Run takes one input to a result. A step that fails, or a change that cannot be published, is an
// error so the run shows as failed; a step that changes nothing is a normal result.
func (h Handler) Run(ctx context.Context, runID string, in Input) (Output, error) {
	if in.Kind == KindAgent {
		return h.runAgent(ctx, runID, in)
	}
	rep, err := pipeline.Run(ctx, pipeline.Options{
		Launcher: h.Launcher, Pusher: h.Pusher,
		Run: runID, Lane: in.Lane, Repo: in.Repo, Base: in.Base, Issue: in.Issue,
	})
	out := Output{Run: runID, ExitCode: rep.Launch.ExitCode}
	if rep.Runner != nil {
		out.Status, out.Branch, out.Commits = rep.Runner.Status, rep.Runner.Branch, rep.Runner.Commits
	}
	if err != nil {
		return out, err
	}
	if rep.Push != nil {
		out.PRURL, out.Reused = rep.Push.PRURL, rep.Push.Reused
	}
	if !rep.OK() {
		return out, failure(rep)
	}
	return out, nil
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
	wf := client.NewWorkflow(spec.Name,
		hatchet.WithWorkflowEvents(spec.Events...),
		hatchet.WithWorkflowConcurrency(caps...),
	)
	wf.NewTask("run", func(ctx hatchet.Context, in Input) (*Output, error) {
		out, err := h.Run(ctx, ctx.WorkflowRunId(), in)
		return &out, err
	},
		hatchet.WithScheduleTimeout(spec.ScheduleTimeout),
		hatchet.WithExecutionTimeout(spec.ExecutionTimeout),
		// A re-delivered task finds the earlier attempt's container (the launcher adopts it), so
		// Hatchet-level retries would only repeat a failure.
		hatchet.WithRetries(0),
	)
	return wf, nil
}
