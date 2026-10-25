// Command satat-worker is the Hatchet worker: it holds the Docker API (through the socket proxy,
// ADR 0027) and the GitHub write token, and runs the issue workflow.
//
// Environment: the Hatchet client settings (HATCHET_CLIENT_TOKEN, HATCHET_CLIENT_HOST_PORT,
// HATCHET_CLIENT_TLS_STRATEGY), DOCKER_HOST, SATAT_PUSH_TOKEN, SATAT_GITHUB_TOKEN (read-only, to
// check an issue is labelled ready-for-agent and to clone the repo before each step; it is not
// in any lane's pass_env, so no step holds it), and every name a lane lists in
// pass_env. SATAT_CONFIG overrides the path of satat.yaml. SATAT_INTAKE_REPOS (comma-separated
// owner/name) turns on the GitHub webhook intake for those repos only. With repos under poller: in
// satat.yaml the worker also polls them for ready-for-agent issues, which needs Issues: read and
// Pull requests: read on SATAT_GITHUB_TOKEN.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/cmdutils"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/moby/moby/client"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher/docker"
	"github.com/javatarz/satat/pipeline"
	"github.com/javatarz/satat/poller"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/workflows"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "satat-worker:", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	path := os.Getenv("SATAT_CONFIG")
	if path == "" {
		path = "satat.yaml"
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	token := os.Getenv("SATAT_PUSH_TOKEN")
	if token == "" {
		return fmt.Errorf("SATAT_PUSH_TOKEN is not set: the worker publishes the PR, so it needs the write token")
	}
	// The read-only token (ADR 0032: github-read-token) is the worker's alone: it checks the issue,
	// polls, and clones the repo before each step starts. No step receives it.
	readToken := os.Getenv("SATAT_GITHUB_TOKEN")
	if readToken == "" {
		return fmt.Errorf("SATAT_GITHUB_TOKEN is not set: the worker reads each issue's labels before it runs, so it needs the read token")
	}

	api, err := client.New(client.FromEnv)
	if err != nil {
		return err
	}
	defer api.Close()
	l, err := docker.New(api, cfg.Lanes, docker.WithLogger(log))
	if err != nil {
		return err
	}
	ctx, cancel := cmdutils.NewInterruptContext()
	defer cancel()
	if err := l.Setup(ctx); err != nil {
		return fmt.Errorf("setup lanes: %w", err)
	}

	// Anything a lane's step left behind longer than twice its timeout is the debris of a crash.
	var longest time.Duration
	for _, lane := range cfg.Lanes {
		longest = max(longest, lane.Timeout)
	}
	if longest == 0 {
		longest = 30 * time.Minute
	}
	go l.RunReaper(ctx, 5*time.Minute, 2*longest+time.Hour)
	// Caches outlive steps and have their own reaper (ADR 0033). Sizing walks every volume, so it
	// runs hourly.
	go l.RunCacheReaper(ctx, time.Hour, docker.CachePolicy{
		MaxUnused: cfg.CacheReaper.MaxUnused,
		MaxBytes:  cfg.CacheReaper.MaxBytes,
		StepAge:   2*longest + time.Hour,
	})

	hc, err := hatchet.NewClient()
	if err != nil {
		return fmt.Errorf("hatchet client: %w", err)
	}
	// A run starts only in a repo the intake or the poller lists.
	allowed := splitList(os.Getenv("SATAT_INTAKE_REPOS"))
	for _, r := range cfg.Poller.Repos {
		allowed = append(allowed, r.Name)
	}
	spec := workflows.Define(cfg.Limits, longest+15*time.Minute)
	wf, err := workflows.Register(hc, spec, workflows.Handler{
		Launcher: l,
		Pusher:   &pusher.Pusher{Token: token},
		Cloner:   pipeline.GitCloner{Token: readToken},
		Issues:   pipeline.GitHubIssues{Token: readToken},
		Allowed:  allowed,
	})
	if err != nil {
		return err
	}
	workflowsToRun := []hatchet.WorkflowBase{wf}
	// Webhook intake is on only when repos are named: it queues nothing for an unlisted repo.
	if repos := splitList(os.Getenv("SATAT_INTAKE_REPOS")); len(repos) > 0 {
		workflowsToRun = append(workflowsToRun, workflows.RegisterIntake(hc, workflows.Intake{
			Config: cfg, Allowed: repos, Events: eventPusher{hc},
		}))
		log.Info("webhook intake on", "repos", repos)
	} else {
		log.Info("webhook intake off: SATAT_INTAKE_REPOS is empty")
	}
	// The poller is on only when satat.yaml lists repos.
	if repos := cfg.Poller.Repos; len(repos) > 0 {
		p := &poller.Poller{
			Config: cfg,
			Issues: poller.GitHub{Token: readToken},
			Runs:   poller.HatchetRuns{Client: hc},
			Queue:  poller.HatchetQueue{Client: hc},
			Log:    log,
		}
		workflowsToRun = append(workflowsToRun, poller.Register(hc, cfg.Poller.Cron, p))
		log.Info("poller on", "repos", len(repos), "cron", cfg.Poller.Cron)
	}
	w, err := hc.NewWorker("satat-worker", hatchet.WithWorkflows(workflowsToRun...), hatchet.WithSlots(cfg.Limits.Global+2)) // Hatchet enforces the cap; spare slots keep intake and the poll from waiting behind steps
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	log.Info("worker starting", "workflow", spec.Name, "global_cap", cfg.Limits.Global, "execution_timeout", spec.ExecutionTimeout)
	return w.StartBlocking(ctx)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// eventPusher adapts Hatchet's events client (whose Push takes options) to workflows.EventPusher.
type eventPusher struct{ hc *hatchet.Client }

func (e eventPusher) Push(ctx context.Context, key string, payload any) error {
	return e.hc.Events().Push(ctx, key, payload)
}
