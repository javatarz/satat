package poller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"

	"github.com/javatarz/satat/workflows"
)

// Name is the poll workflow's name in Hatchet.
const Name = "satat-poll"

// RunHistory is how far back HatchetRuns looks for an issue's earlier run. A run older than
// Hatchet's own retention is gone anyway; past this window a still-labelled issue is queued again.
const RunHistory = 30 * 24 * time.Hour

// HatchetQueue pushes the satat:issue event.
type HatchetQueue struct{ Client *hatchet.Client }

// Push implements Queue.
func (q HatchetQueue) Push(ctx context.Context, in workflows.Input) error {
	return q.Client.Events().Push(ctx, workflows.EventIssue, in)
}

// HatchetRuns lists the issues that have a satat-issue run in Hatchet.
type HatchetRuns struct {
	Client *hatchet.Client
	Now    func() time.Time
}

const runPage = 200

// Issues implements Runs. A run is recognised by its input (repo and issue number), which only
// the satat-issue workflow takes; the poll workflow's own runs carry none.
func (h HatchetRuns) Issues(ctx context.Context) (map[string]bool, error) {
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	since := now().Add(-RunHistory)
	set := map[string]bool{}
	for offset := int64(0); ; offset += runPage {
		limit, off := int64(runPage), offset
		list, err := h.Client.Runs().List(ctx, rest.V1WorkflowRunListParams{Since: since, Limit: &limit, Offset: &off, OnlyTasks: false})
		if err != nil {
			return nil, err
		}
		if err := collect(set, list.Rows); err != nil {
			return nil, err
		}
		if len(list.Rows) < runPage {
			return set, nil
		}
	}
}

// collect adds the issues of rows to set. It fails closed: a row it cannot read means a run the
// poller cannot see, and an issue it cannot see would be queued again every interval. So it is an
// error when payloads are hidden from the token, or when a satat-issue row has no readable input
// (for example an offloaded payload).
func collect(set map[string]bool, rows []rest.V1TaskSummary) error {
	for _, row := range rows {
		if row.PayloadsRestricted != nil && *row.PayloadsRestricted {
			return errors.New("hatchet hides run payloads from this token: cannot tell which issues have a run")
		}
		repo, n, ok := inputIssue(row.Input)
		switch {
		case ok:
			set[Key(repo, n)] = true
		case strings.HasPrefix(row.DisplayName, workflows.Name):
			return fmt.Errorf("run %q (%s) has no readable repo and issue in its input: cannot tell which issues have a run", row.DisplayName, row.TaskExternalId)
		}
	}
	return nil
}

// inputIssue reads the repo and issue number out of a satat-issue run's input.
func inputIssue(input map[string]any) (string, int, bool) {
	b, err := json.Marshal(input)
	if err != nil {
		return "", 0, false
	}
	var in struct {
		Repo  string `json:"repo"`
		Issue struct {
			Number int `json:"number"`
		} `json:"issue"`
	}
	if json.Unmarshal(b, &in) != nil || in.Repo == "" || in.Issue.Number <= 0 {
		return "", 0, false
	}
	return in.Repo, in.Issue.Number, true
}

// Register builds the cron workflow that runs p.Poll on cron. A failed poll fails the run, so it
// shows in the dashboard.
func Register(client *hatchet.Client, cron string, p *Poller) *hatchet.Workflow {
	wf := client.NewWorkflow(Name, hatchet.WithWorkflowCron(cron))
	wf.NewTask("poll", func(ctx hatchet.Context, _ struct{}) (*Output, error) {
		res, err := p.Poll(ctx)
		out := &Output{Queued: res.Queued, Skipped: len(res.Skipped)}
		if out.Queued == nil {
			out.Queued = []string{}
		}
		if err != nil {
			return out, fmt.Errorf("poll: %w", err)
		}
		return out, nil
	}, hatchet.WithExecutionTimeout(5*time.Minute), hatchet.WithRetries(0))
	return wf
}

// Output is what a poll run reports in the dashboard.
type Output struct {
	Queued  []string `json:"queued"`
	Skipped int      `json:"skipped"`
}
