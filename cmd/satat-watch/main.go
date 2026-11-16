// Command satat-watch reports what the worker cannot report itself (#235, ADR 0035): it polls
// Hatchet and sends an ntfy message when no Satat worker has had a heartbeat for a while, and
// another when one is back. It runs beside the worker, never inside it.
//
// It reads notify.ntfy.server from satat.yaml ($SATAT_CONFIG, default satat.yaml) and the topic
// from SATAT_NTFY_TOPIC, and needs HATCHET_CLIENT_TOKEN (plus HATCHET_CLIENT_HOST_PORT and
// HATCHET_CLIENT_TLS_STRATEGY=none for the Compose stack). With no ntfy server it exits: there is
// no issue to comment on when no worker runs.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/notify"
	"github.com/javatarz/satat/watch"
)

const (
	interval = 30 * time.Second
	// grace covers a worker restart (start grace is 2 minutes) without an alert.
	grace = 3 * time.Minute
	// maxAge is five of the Hatchet SDK's 4 s heartbeats, plus slack.
	maxAge = time.Minute
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "satat-watch:", err)
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
	if cfg.Notify.NtfyServer == "" {
		return fmt.Errorf("notify.ntfy.server is not set in %s: satat-watch has nowhere to report", path)
	}
	topic := os.Getenv(config.NtfyTopicSecret)
	if topic == "" {
		return fmt.Errorf("%s is not set", config.NtfyTopicSecret)
	}
	hc, err := hatchet.NewClient()
	if err != nil {
		return fmt.Errorf("hatchet client: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	w := &watch.Watch{
		Workers: func(ctx context.Context) ([]rest.Worker, error) {
			list, err := hc.Workers().List(ctx)
			if err != nil || list == nil || list.Rows == nil {
				return nil, err
			}
			return *list.Rows, nil
		},
		Grace: grace, MaxAge: maxAge,
		Notify: &notify.Safe{Next: &notify.Ntfy{Server: cfg.Notify.NtfyServer, Topic: topic}, Log: log, Secrets: []string{topic}},
		Log:    log,
	}
	log.Info("watching for a Satat worker", "every", interval, "grace", grace)
	w.Run(ctx, interval)
	return nil
}
