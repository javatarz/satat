// Command satat-worker is the Hatchet worker: it holds the Docker API (through the socket proxy,
// ADR 0027) and the GitHub write token, and runs the issue workflow.
//
// Environment: the Hatchet client settings (HATCHET_CLIENT_TOKEN, HATCHET_CLIENT_HOST_PORT,
// HATCHET_CLIENT_TLS_STRATEGY), DOCKER_HOST, SATAT_PUSH_TOKEN, SATAT_GITHUB_TOKEN (read-only, to
// check an issue is labelled ready-for-agent before running it), and every name a lane lists in
// pass_env. SATAT_CONFIG overrides the path of satat.yaml.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/cmdutils"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/moby/moby/client"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher/docker"
	"github.com/javatarz/satat/pipeline"
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
	// The ready-for-agent check only reads, so it uses the read-only token, not the write token.
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

	hc, err := hatchet.NewClient()
	if err != nil {
		return fmt.Errorf("hatchet client: %w", err)
	}
	spec := workflows.Define(cfg.Limits, longest+15*time.Minute)
	wf, err := workflows.Register(hc, spec, workflows.Handler{
		Launcher: l,
		Pusher:   &pusher.Pusher{Token: token},
		Issues:   pipeline.GitHubIssues{Token: readToken},
	})
	if err != nil {
		return err
	}
	w, err := hc.NewWorker("satat-worker", hatchet.WithWorkflows(wf), hatchet.WithSlots(cfg.Limits.Global))
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	log.Info("worker starting", "workflow", spec.Name, "global_cap", cfg.Limits.Global, "execution_timeout", spec.ExecutionTimeout)
	return w.StartBlocking(ctx)
}
