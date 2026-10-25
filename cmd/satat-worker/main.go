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
// Pull requests: read on SATAT_GITHUB_TOKEN. SATAT_HEALTH_ADDR (default 127.0.0.1:8001) is where
// /livez and /readyz are served. The server starts before lane setup, so a setup failure is
// reported by /readyz (the lanes check) for the start grace before the worker exits non-zero.
//
// "satat-worker healthcheck" asks a running worker for /readyz and exits 0 only if it is ready; the
// compose healthcheck calls it.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	"github.com/hatchet-dev/hatchet/pkg/cmdutils"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/moby/moby/client"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/health"
	"github.com/javatarz/satat/launcher/docker"
	"github.com/javatarz/satat/pipeline"
	"github.com/javatarz/satat/poller"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/workflows"
)

const (
	healthInterval = 5 * time.Second
	// heartbeatMaxAge is three times the Hatchet SDK's 4 s heartbeat (pkg/client/action_listener.go),
	// so readiness turns false within 30 s of losing Hatchet but one late heartbeat is tolerated.
	heartbeatMaxAge = 12 * time.Second
	// startGrace is how long the worker may take to become ready the first time. After that it never
	// exits because Hatchet went away.
	startGrace        = 2 * time.Minute
	defaultHealthAddr = "127.0.0.1:8001"
)

func main() {
	// Before any config or env checks: the probe runs in a container where only the address matters.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "satat-worker:", err)
		os.Exit(1)
	}
}

func healthAddr() string {
	if a := os.Getenv("SATAT_HEALTH_ADDR"); a != "" {
		return a
	}
	return defaultHealthAddr
}

// healthcheck prints the body of /readyz and returns the process exit code.
func healthcheck() int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+healthAddr()+"/readyz", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "satat-worker healthcheck:", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "satat-worker healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	fmt.Print(string(body))
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
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

	hc, err := hatchet.NewClient()
	if err != nil {
		return fmt.Errorf("hatchet client: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("hostname: %w", err)
	}
	// The hostname makes the name unique per container, so readiness can find this process's own
	// record and ignore the ones earlier containers left behind in Hatchet.
	name := "satat-worker-" + host

	// The health server starts before lane setup, so a setup failure (a lane image that is not
	// pullable, say) shows in /readyz as the lanes check instead of as a container that restarts.
	var lanes health.LaneSetup
	checker := health.New(healthInterval,
		lanes.Check(),
		health.HatchetWorker(func(ctx context.Context) ([]rest.Worker, error) {
			list, err := hc.Workers().List(ctx)
			if err != nil {
				return nil, err
			}
			if list == nil || list.Rows == nil {
				return nil, nil
			}
			return *list.Rows, nil
		}, name, heartbeatMaxAge, time.Now),
		health.DockerPing(func(ctx context.Context) error {
			_, err := api.Ping(ctx, client.PingOptions{})
			return err
		}),
	)
	srv := &http.Server{Addr: healthAddr(), Handler: checker.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server stopped", "err", err)
		}
	}()
	defer srv.Close()
	checker.Start(ctx)

	setupStart := time.Now()
	if err := l.Setup(ctx); err != nil {
		lanes.Done(err)
		// Stay up for the rest of the start grace so the healthcheck can report why, then exit
		// non-zero as before and let restart: unless-stopped retry.
		log.Error("lane setup failed; exiting after the start grace", "err", err, "grace", startGrace)
		select {
		case <-ctx.Done():
		case <-time.After(startGrace - time.Since(setupStart)):
		}
		return fmt.Errorf("setup lanes: %w", err)
	}
	lanes.Done(nil)

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

	// A run starts only in a repo the intake or the poller lists.
	allowed := splitList(os.Getenv("SATAT_INTAKE_REPOS"))
	for _, r := range cfg.Poller.Repos {
		allowed = append(allowed, r.Name)
	}
	// Every account's lane starts open, which also creates the rate-limit keys the task names. A
	// step that hits the Claude subscription limit closes its account's lane until the reset.
	limiter := &workflows.Limiter{Put: workflows.HatchetPut(hc)}
	accounts := map[string]bool{}
	for _, a := range cfg.Accounts {
		accounts[a] = true
	}
	if err := limiter.Open(slices.Sorted(maps.Keys(accounts))...); err != nil {
		return fmt.Errorf("open lanes: %w", err)
	}
	spec := workflows.Define(cfg.Limits, longest+15*time.Minute)
	wf, err := workflows.Register(hc, spec, workflows.Handler{
		Launcher: l,
		Pusher:   &pusher.Pusher{Token: token},
		Cloner:   pipeline.GitCloner{Token: readToken},
		Issues:   pipeline.GitHubIssues{Token: readToken},
		Allowed:  allowed,
		Gate:     limiter,
		Secrets:  secretValues(cfg),
		NoAgent:  cfg.NoAgent,
	})
	if err != nil {
		return err
	}
	workflowsToRun := []hatchet.WorkflowBase{wf}
	// Webhook intake is on only when repos are named: it queues nothing for an unlisted repo.
	if repos := splitList(os.Getenv("SATAT_INTAKE_REPOS")); len(repos) > 0 {
		workflowsToRun = append(workflowsToRun, workflows.RegisterIntake(hc, workflows.Intake{
			Config: cfg, Allowed: repos, Events: eventPusher{hc}, Runs: poller.HatchetRuns{Client: hc}, Log: log,
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
	w, err := hc.NewWorker(name, hatchet.WithWorkflows(workflowsToRun...), hatchet.WithSlots(cfg.Limits.Global+2)) // Hatchet enforces the cap; spare slots keep intake and the poll from waiting behind steps
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}

	log.Info("worker starting", "name", name, "workflow", spec.Name, "global_cap", cfg.Limits.Global, "execution_timeout", spec.ExecutionTimeout, "health_addr", srv.Addr)
	done := make(chan error, 1)
	go func() { done <- w.StartBlocking(ctx) }()

	// Exit non-zero only if the worker never becomes ready; once it has been ready, losing Hatchet
	// shows up as unready and the worker waits for it to come back.
	if err := health.WaitReady(ctx, checker, startGrace); err != nil {
		return err
	}
	log.Info("worker ready")
	return <-done
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

// secretValues are the non-empty values, from this process's environment, of every secret name in
// the config. An agent run's output is scrubbed of them before it reaches the Hatchet history.
func secretValues(cfg config.Config) []string {
	var vals []string
	for _, n := range cfg.SecretNames() {
		if v := os.Getenv(n); v != "" {
			vals = append(vals, v)
		}
	}
	return vals
}
