package health

import (
	"context"
	"fmt"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
)

// HatchetWorker checks that Hatchet lists a worker called name that is ACTIVE and has sent a
// heartbeat within maxAge. Only this process's own record counts: a stale record left by a
// previous container must not make a fresh worker look healthy or unhealthy.
func HatchetWorker(list func(ctx context.Context) ([]rest.Worker, error), name string, maxAge time.Duration, now func() time.Time) Check {
	return Check{Name: "hatchet", Run: func(ctx context.Context) error {
		rows, err := list(ctx)
		if err != nil {
			return fmt.Errorf("list hatchet workers: %w", err)
		}
		var own *rest.Worker
		for i := range rows {
			if rows[i].Name != name {
				continue
			}
			// A restarted container keeps its hostname, so more than one record can share the name;
			// the freshest heartbeat is the live one.
			if own == nil || beats(rows[i], own) {
				own = &rows[i]
			}
		}
		if own == nil {
			return fmt.Errorf("worker %q is not registered with hatchet", name)
		}
		if own.Status == nil || *own.Status != rest.ACTIVE {
			status := "unknown"
			if own.Status != nil {
				status = string(*own.Status)
			}
			return fmt.Errorf("worker %q is %s in hatchet, want ACTIVE", name, status)
		}
		if own.LastHeartbeatAt == nil {
			return fmt.Errorf("worker %q has no heartbeat in hatchet", name)
		}
		if age := now().Sub(*own.LastHeartbeatAt); age > maxAge {
			return fmt.Errorf("worker %q last sent a heartbeat %s ago, limit %s", name, age.Round(time.Second), maxAge)
		}
		return nil
	}}
}

// beats reports whether a has a later heartbeat than b.
func beats(a rest.Worker, b *rest.Worker) bool {
	if a.LastHeartbeatAt == nil {
		return false
	}
	return b.LastHeartbeatAt == nil || a.LastHeartbeatAt.After(*b.LastHeartbeatAt)
}
