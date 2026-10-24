// Package health gives the worker a liveness and a readiness endpoint.
//
// Liveness says the process is up. Readiness says every dependency check passed on its last run.
// The checks run on a timer and the endpoints only read the stored results, so a probe never waits
// on Hatchet or Docker.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// checkTimeout bounds one run of one check, so a hung dependency shows up as a failure and not as
// a stuck loop.
const checkTimeout = 3 * time.Second

// Check is one named dependency probe. Run returns nil when the dependency is healthy.
type Check struct {
	Name string
	Run  func(ctx context.Context) error
}

// Checker runs checks on an interval and keeps their latest results.
type Checker struct {
	interval time.Duration
	checks   []Check

	mu      sync.Mutex
	ran     bool
	failing map[string]string
}

// New returns a Checker that runs checks every interval once started.
func New(interval time.Duration, checks ...Check) *Checker {
	return &Checker{interval: interval, checks: checks}
}

// Start runs every check once, waiting for the results, then keeps running them every interval
// until ctx ends. Doing the first run inline means Ready is never answered from an empty state
// afterwards.
func (c *Checker) Start(ctx context.Context) {
	c.runAll(ctx)
	go func() {
		t := time.NewTicker(c.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.runAll(ctx)
			}
		}
	}()
}

func (c *Checker) runAll(ctx context.Context) {
	failing := map[string]string{}
	for _, ch := range c.checks {
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		err := ch.Run(cctx)
		cancel()
		if err != nil {
			failing[ch.Name] = err.Error()
		}
	}
	c.mu.Lock()
	c.ran, c.failing = true, failing
	c.mu.Unlock()
}

// Ready reports whether every check passed on its last run, and the message of each one that
// failed. It is false until the first run has finished.
func (c *Checker) Ready() (ok bool, failing map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ran {
		return false, map[string]string{"health": "no check has run yet"}
	}
	failing = make(map[string]string, len(c.failing))
	for k, v := range c.failing {
		failing[k] = v
	}
	return len(failing) == 0, failing
}

// Handler serves GET /livez (always 200) and GET /readyz (200, or 503 with the failing checks).
func (c *Checker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "live"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ok, failing := c.Ready(); !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unready", "failing": failing})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// WaitReady blocks until c is ready. If it is still not ready after grace, it returns an error
// that names each failing check, so a worker that can never reach its dependencies exits with the
// reason. It also returns when ctx ends.
func WaitReady(ctx context.Context, c *Checker, grace time.Duration) error {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		ok, failing := c.Ready()
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			names := make([]string, 0, len(failing))
			for n, msg := range failing {
				names = append(names, fmt.Sprintf("%s: %s", n, msg))
			}
			sort.Strings(names)
			return fmt.Errorf("not ready after %s: %s", grace, strings.Join(names, "; "))
		case <-poll.C:
		}
	}
}
