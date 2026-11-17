// Package watch reports what the worker cannot report itself (#235, ADR 0035): that no worker is
// running. A worker that crashed, or never registered, sends no events, and Hatchet keeps queuing
// work for it. Watch runs as its own process, polls Hatchet's worker list, and notifies once when
// no worker has a fresh heartbeat for Grace, then once when one is back.
package watch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"

	"github.com/javatarz/satat/notify"
)

// WorkerPrefix starts every Satat worker's name in Hatchet (cmd/satat-worker).
const WorkerPrefix = "satat-worker-"

// Watch tracks whether a worker is up. Check is called on an interval; it is not safe to call
// concurrently.
type Watch struct {
	// Workers lists Hatchet's workers.
	Workers func(ctx context.Context) ([]rest.Worker, error)
	// Grace is how long no worker may be up before it is reported. MaxAge is how old a heartbeat
	// may be for its worker to count as up.
	Grace, MaxAge time.Duration
	Notify        *notify.Safe
	Log           *slog.Logger
	// Now defaults to time.Now.
	Now func() time.Time

	downSince time.Time
	why       string
	reported  bool
}

// Check looks once and notifies on a change that has lasted long enough.
func (w *Watch) Check(ctx context.Context) {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	t := now()
	why := w.down(ctx, t)
	if why == "" {
		if w.reported {
			w.Notify.Send(ctx, notify.Event{State: notify.WorkerUp, Reason: "a Satat worker has a heartbeat again"})
			w.log("worker back")
		}
		w.downSince, w.reported = time.Time{}, false
		return
	}
	if w.downSince.IsZero() {
		w.downSince = t
	}
	w.why = why
	if !w.reported && t.Sub(w.downSince) >= w.Grace {
		w.Notify.Send(ctx, notify.Event{State: notify.WorkerDown,
			Reason: fmt.Sprintf("%s since %s: queued issues will wait", why, w.downSince.UTC().Format("15:04 MST"))})
		w.reported = true
		w.log("worker down", "why", why)
	}
}

// down says why no worker is up, or "" if one is.
func (w *Watch) down(ctx context.Context, now time.Time) string {
	rows, err := w.Workers(ctx)
	if err != nil {
		return "cannot reach Hatchet (" + notify.Short(err.Error()) + ")"
	}
	seen := false
	for _, r := range rows {
		if !strings.HasPrefix(r.Name, WorkerPrefix) {
			continue
		}
		seen = true
		if r.Status != nil && *r.Status == rest.ACTIVE && r.LastHeartbeatAt != nil && now.Sub(*r.LastHeartbeatAt) <= w.MaxAge {
			return ""
		}
	}
	if !seen {
		return "no Satat worker is registered with Hatchet"
	}
	return "no Satat worker has a fresh heartbeat"
}

// Run checks every interval until ctx is done.
func (w *Watch) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		w.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Watch) log(msg string, args ...any) {
	if w.Log != nil {
		w.Log.Info(msg, args...)
	}
}
