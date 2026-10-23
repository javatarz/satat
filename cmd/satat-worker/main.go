// Command satat-worker is the Hatchet worker: it holds the Docker API (through the socket proxy,
// ADR 0027) and the GitHub write token, and runs the issue workflow.
//
// Environment: the Hatchet client settings (HATCHET_CLIENT_TOKEN, HATCHET_CLIENT_HOST_PORT,
// HATCHET_CLIENT_TLS_STRATEGY), DOCKER_HOST, SATAT_PUSH_TOKEN, and every name a lane lists in
// pass_env. SATAT_CONFIG overrides the path of satat.yaml. SATAT_HEALTH_ADDR (default
// 127.0.0.1:8001) is where /livez and /readyz are served.
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
	"net/http"
	"os"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	"github.com/hatchet-dev/hatchet/pkg/cmdutils"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/moby/moby/client"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/health"
	"github.com/javatarz/satat/launcher/docker"
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
	wf, err := workflows.Register(hc, spec, workflows.Handler{Launcher: l, Pusher: &pusher.Pusher{Token: token}})
	if err != nil {
		return err
	}
	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("hostname: %w", err)
	}
	// The hostname makes the name unique per container, so readiness can find this process's own
	// record and ignore the ones earlier containers left behind in Hatchet.
	name := "satat-worker-" + host
	w, err := hc.NewWorker(name, hatchet.WithWorkflows(wf), hatchet.WithSlots(cfg.Limits.Global))
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}

	checker := health.New(healthInterval,
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
