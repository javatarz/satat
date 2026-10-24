// Command satat-worker is the Hatchet worker: it holds the GitHub write token and the means to
// launch steps, and runs the issue workflow. deploy.target picks the launcher: on compose it holds
// the Docker API (through the socket proxy, ADR 0027); on kubernetes it uses the in-cluster
// config to create sandboxes, with no Docker client, reaper or Docker ping.
//
// Environment: the Hatchet client settings (HATCHET_CLIENT_TOKEN, HATCHET_CLIENT_HOST_PORT,
// HATCHET_CLIENT_TLS_STRATEGY), DOCKER_HOST (compose only), SATAT_PUSH_TOKEN, and on compose every
// name a lane lists in pass_env (on kubernetes lane secrets are not in the worker env). SATAT_CONFIG
// overrides the path of satat.yaml. SATAT_HEALTH_ADDR (default 127.0.0.1:8001; the Helm chart sets
// 0.0.0.0:8001 on kubernetes so the kubelet can probe) is where /livez and /readyz are served. The
// readiness checks are the lanes, the Hatchet worker record, and the Docker ping (compose) or the
// Kubernetes API (kubernetes). The server starts before lane setup, so
// a setup failure is reported by /readyz (the lanes check) for the start grace before the worker
// exits non-zero.
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

	hatchetrest "github.com/hatchet-dev/hatchet/pkg/client/rest"
	"github.com/hatchet-dev/hatchet/pkg/cmdutils"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/moby/moby/client"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/health"
	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/launcher/docker"
	k8slauncher "github.com/javatarz/satat/launcher/kubernetes"
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

	l, apiCheck, reap, closeLauncher, err := newLauncher(cfg, log)
	if err != nil {
		return err
	}
	defer closeLauncher()
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
		health.HatchetWorker(func(ctx context.Context) ([]hatchetrest.Worker, error) {
			list, err := hc.Workers().List(ctx)
			if err != nil {
				return nil, err
			}
			if list == nil || list.Rows == nil {
				return nil, nil
			}
			return *list.Rows, nil
		}, name, heartbeatMaxAge, time.Now),
		apiCheck,
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
	if reap != nil {
		go reap(ctx, 5*time.Minute, 2*longest+time.Hour)
	}

	spec := workflows.Define(cfg.Limits, longest+15*time.Minute)
	wf, err := workflows.Register(hc, spec, workflows.Handler{Launcher: l, Pusher: &pusher.Pusher{Token: token}, Secrets: secretValues(cfg)})
	if err != nil {
		return err
	}
	w, err := hc.NewWorker(name, hatchet.WithWorkflows(wf), hatchet.WithSlots(cfg.Limits.Global))
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

// stepLauncher is what the worker needs of either launcher.
type stepLauncher interface {
	launcher.Launcher
	Setup(ctx context.Context) error
}

// newLauncher builds the launcher for deploy.target with its API readiness check. reap is nil
// where there is no reaper (kubernetes); closeLauncher releases the launcher's client.
func newLauncher(cfg config.Config, log *slog.Logger) (l stepLauncher, apiCheck health.Check, reap func(ctx context.Context, interval, maxAge time.Duration), closeLauncher func(), err error) {
	if cfg.Deploy.Target == config.TargetKubernetes {
		rc, err := rest.InClusterConfig()
		if err != nil {
			return nil, health.Check{}, nil, nil, fmt.Errorf("in-cluster config: %w", err)
		}
		dyn, err := dynamic.NewForConfig(rc)
		if err != nil {
			return nil, health.Check{}, nil, nil, fmt.Errorf("dynamic client: %w", err)
		}
		core, err := kubernetes.NewForConfig(rc)
		if err != nil {
			return nil, health.Check{}, nil, nil, fmt.Errorf("kubernetes client: %w", err)
		}
		kl, err := k8slauncher.New(dyn, core, cfg.Lanes, k8slauncher.WithLogger(log))
		if err != nil {
			return nil, health.Check{}, nil, nil, err
		}
		return kl, health.KubeAPI(kl.Ping), nil, func() {}, nil
	}
	api, err := client.New(client.FromEnv)
	if err != nil {
		return nil, health.Check{}, nil, nil, err
	}
	dl, err := docker.New(api, cfg.Lanes, docker.WithLogger(log))
	if err != nil {
		api.Close()
		return nil, health.Check{}, nil, nil, err
	}
	ping := health.DockerPing(func(ctx context.Context) error {
		_, err := api.Ping(ctx, client.PingOptions{})
		return err
	})
	return dl, ping, dl.RunReaper, func() { api.Close() }, nil
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
