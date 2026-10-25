// Package poller queues ready-for-agent issues: a Hatchet cron in the worker lists them on the
// repos named in satat.yaml and pushes the same satat:issue event satat-enqueue pushes (#135).
//
// Polling only decides what to queue. It does not gate: the workflow re-reads each issue at run
// start (workflows.Handler), so an issue that stops being ready is skipped whichever way it came in.
package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/runner"
	"github.com/javatarz/satat/workflows"
)

// Issues lists what a repo has ready and which issues an open pull request already closes.
// GitHub is the real one.
type Issues interface {
	ReadyIssues(ctx context.Context, repo, label string) ([]ReadyIssue, error)
	OpenPRIssues(ctx context.Context, repo string) (map[int]bool, error)
}

// Runs reports which issues have ever had a run, as "owner/name#N" keys (see Key). Any run counts,
// whatever its status: a failed run is not retried by the next poll, because that would spend the
// subscription again every interval. A person retries it with satat-enqueue.
type Runs interface {
	Issues(ctx context.Context) (map[string]bool, error)
}

// Queue pushes a run input as the satat:issue event.
type Queue interface {
	Push(ctx context.Context, in workflows.Input) error
}

// Poller is one poll of every configured repo. Poll is safe to call repeatedly.
type Poller struct {
	Config config.Config
	Issues Issues
	Runs   Runs
	Queue  Queue
	Log    *slog.Logger
	// Now defaults to time.Now.
	Now func() time.Time

	mu     sync.Mutex
	queued map[string]time.Time
}

// Pending is how long a pushed issue is remembered, so that the next poll does not queue it again
// before Hatchet has created its run from the event.
const Pending = 30 * time.Minute

// Key names an issue in Runs and in the poller's own memory.
func Key(repo string, number int) string { return workflows.IssueKey(repo, number) }

// Result counts what one poll did.
type Result struct {
	Queued []string
	// Skipped maps an issue to why it was not queued.
	Skipped map[string]string
}

// Poll queues every ready issue that has no run and no open PR yet. A failure on one repo does not
// stop the others; the errors are joined. If the runs cannot be listed it queues nothing: a
// duplicate run costs more than a late one.
func (p *Poller) Poll(ctx context.Context) (Result, error) {
	res := Result{Skipped: map[string]string{}}
	repos := p.Config.Poller.Repos
	if len(repos) == 0 {
		return res, nil
	}
	ran, err := p.Runs.Issues(ctx)
	if err != nil {
		return res, fmt.Errorf("list runs: %w", err)
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	p.forget(now())
	var errs []error
	for _, r := range repos {
		if err := p.pollRepo(ctx, r, ran, now, &res); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Name, err))
		}
	}
	return res, errors.Join(errs...)
}

func (p *Poller) pollRepo(ctx context.Context, r config.PollRepo, ran map[string]bool, now func() time.Time, res *Result) error {
	issues, err := p.Issues.ReadyIssues(ctx, r.Name, workflows.ReadyLabel)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		return nil
	}
	closing, err := p.Issues.OpenPRIssues(ctx, r.Name)
	if err != nil {
		return err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	var errs []error
	for _, is := range issues {
		key := Key(r.Name, is.Number)
		switch {
		case ran[key]:
			res.Skipped[key] = "already has a run"
			continue
		case closing[is.Number]:
			res.Skipped[key] = "an open pull request closes it"
			continue
		case p.isPending(key):
			res.Skipped[key] = "queued by an earlier poll"
			continue
		}
		in, err := workflows.MapIssue(p.Config, r.Name, r.Base, r.Lane, runner.Issue{Number: is.Number, Title: is.Title, Body: is.Body})
		if err == nil {
			err = p.Queue.Push(ctx, in)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("queue %s: %w", key, err))
			continue
		}
		p.remember(key, now())
		res.Queued = append(res.Queued, key)
		if p.Log != nil {
			p.Log.Info("queued issue", "issue", key, "lane", r.Lane)
		}
	}
	return errors.Join(errs...)
}

func (p *Poller) isPending(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.queued[key]
	return ok
}

func (p *Poller) remember(key string, t time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.queued == nil {
		p.queued = map[string]time.Time{}
	}
	p.queued[key] = t
}

// forget drops issues pushed longer than Pending ago: by then they show up in Runs.
func (p *Poller) forget(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, t := range p.queued {
		if now.Sub(t) > Pending {
			delete(p.queued, k)
		}
	}
}
