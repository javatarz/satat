package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/runner"
)

const (
	// IntakeName is the intake workflow's name in Hatchet.
	IntakeName = "satat-intake"
	// EventWebhook is the event key a Hatchet GitHub webhook must emit (its event key expression
	// is the constant 'github:issues'). The webhook should deliver Issues events only; the mapper
	// still checks the payload, because the key does not say which action it was.
	EventWebhook = "github:issues"

	// DefaultBase is the branch a run starts from when the webhook does not say otherwise.
	DefaultBase = "main"
	// DefaultLane is the lane intake sends work to until satat.yaml routes by repo (#108).
	DefaultLane = "claude"
)

// WebhookPayload is the part of GitHub's `issues` webhook body that intake reads.
type WebhookPayload struct {
	Action string `json:"action"`
	Label  *struct {
		Name string `json:"name"`
	} `json:"label"`
	Issue *struct {
		Number      int             `json:"number"`
		Title       string          `json:"title"`
		Body        string          `json:"body"`
		State       string          `json:"state"`
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
	Repository *struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
}

// IntakeResult is what an intake run reports in the dashboard.
type IntakeResult struct {
	Queued bool   `json:"queued"`
	Reason string `json:"reason,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Issue  int    `json:"issue,omitempty"`
}

// MapWebhook turns an `issues` webhook into a run input. It returns ok=false with a reason when
// the delivery is not a request to run: another action, another label, a pull request, a closed
// issue or a repo outside allowed. allowed must be non-empty, so a missing list queues nothing.
// An error means the payload or the config is unusable.
func MapWebhook(cfg config.Config, allowed []string, lane string, p WebhookPayload) (in Input, ok bool, reason string, err error) {
	if len(allowed) == 0 {
		return Input{}, false, "", errors.New("no repos allowed for intake")
	}
	if p.Issue == nil || p.Repository == nil || p.Repository.FullName == "" || p.Issue.Number <= 0 {
		return Input{}, false, "", errors.New("payload has no issue or repository")
	}
	repo := p.Repository.FullName
	switch {
	case p.Action != "labeled":
		return Input{}, false, "action is " + p.Action + ", not labeled", nil
	case p.Label == nil || p.Label.Name != ReadyLabel:
		return Input{}, false, "label is not " + ReadyLabel, nil
	case len(p.Issue.PullRequest) > 0 && string(p.Issue.PullRequest) != "null":
		return Input{}, false, "a pull request, not an issue", nil
	case p.Issue.State != "open":
		return Input{}, false, "issue is " + p.Issue.State, nil
	case !slices.ContainsFunc(allowed, func(a string) bool { return strings.EqualFold(a, repo) }):
		return Input{}, false, repo + " is not an allowed repo", nil
	}
	base := p.Repository.DefaultBranch
	if base == "" {
		base = DefaultBase
	}
	in, err = MapIssue(cfg, repo, base, lane, runner.Issue{Number: p.Issue.Number, Title: p.Issue.Title, Body: p.Issue.Body})
	if err != nil {
		return Input{}, false, "", err
	}
	return in, true, "", nil
}

// EventPusher sends an event. Hatchet's events client satisfies it.
type EventPusher interface {
	Push(ctx context.Context, key string, payload any) error
}

// Intake maps webhook deliveries to issue events. The signature is checked by Hatchet's webhook,
// not here.
type Intake struct {
	Config  config.Config
	Allowed []string
	Lane    string
	Events  EventPusher
}

// Handle takes one delivery to a result. The worker re-checks the issue's labels at run start
// (Handler.Run), so a stale delivery cannot launch anything.
func (i Intake) Handle(ctx context.Context, p WebhookPayload) (IntakeResult, error) {
	lane := i.Lane
	if lane == "" {
		lane = DefaultLane
	}
	in, ok, reason, err := MapWebhook(i.Config, i.Allowed, lane, p)
	if err != nil {
		return IntakeResult{}, err
	}
	if !ok {
		return IntakeResult{Reason: reason}, nil
	}
	if err := i.Events.Push(ctx, EventIssue, in); err != nil {
		return IntakeResult{}, fmt.Errorf("queue %s#%d: %w", in.Repo, in.Issue.Number, err)
	}
	return IntakeResult{Queued: true, Repo: in.Repo, Issue: in.Issue.Number}, nil
}

// RegisterIntake builds the intake workflow: one cheap task that runs on each webhook event.
func RegisterIntake(client *hatchet.Client, i Intake) *hatchet.Workflow {
	wf := client.NewWorkflow(IntakeName, hatchet.WithWorkflowEvents(EventWebhook))
	wf.NewTask("map", func(ctx hatchet.Context, p WebhookPayload) (*IntakeResult, error) {
		r, err := i.Handle(ctx, p)
		return &r, err
	},
		// The default 5-minute wait would drop a delivery that arrives while the worker is down.
		hatchet.WithScheduleTimeout(ScheduleTimeout),
		hatchet.WithRetries(2),
	)
	return wf
}
