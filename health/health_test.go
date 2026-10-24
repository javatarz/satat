package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
)

func ok() Check { return Check{Name: "a", Run: func(context.Context) error { return nil }} }
func down() Check {
	return Check{Name: "b", Run: func(context.Context) error { return errors.New("down") }}
}

func started(t *testing.T, checks ...Check) *Checker {
	t.Helper()
	c := New(time.Hour, checks...)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Start(ctx)
	return c
}

func TestReadyOnlyWhenEveryCheckPasses(t *testing.T) {
	c := started(t, ok(), down())
	got, failing := c.Ready()
	if got || failing["b"] != "down" {
		t.Fatalf("Ready() = %v, %v; want false with b=down", got, failing)
	}
	c = started(t, ok(), ok())
	if got, failing := c.Ready(); !got || len(failing) != 0 {
		t.Fatalf("Ready() = %v, %v; want true and empty", got, failing)
	}
}

func TestLivezIgnoresChecks(t *testing.T) {
	h := started(t, ok(), down()).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"live"`) {
		t.Fatalf("/livez = %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body struct {
		Status  string
		Failing map[string]string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 503 || body.Status != "unready" || body.Failing["b"] != "down" {
		t.Fatalf("/readyz = %d %s", rec.Code, rec.Body)
	}
}

func TestHatchetWorkerNeedsOwnFreshActiveRecord(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return t0 }
	rec := func(name string, st rest.WorkerStatus, age time.Duration) rest.Worker {
		hb := t0.Add(-age)
		return rest.Worker{Name: name, Status: &st, LastHeartbeatAt: &hb}
	}
	const me = "satat-worker-abc"
	cases := []struct {
		name    string
		rows    []rest.Worker
		listErr error
		want    string // empty means nil error
	}{
		{"fresh", []rest.Worker{rec(me, rest.ACTIVE, 5*time.Second)}, nil, ""},
		{"stale heartbeat", []rest.Worker{rec(me, rest.ACTIVE, 13*time.Second)}, nil, "heartbeat"},
		{"inactive", []rest.Worker{rec(me, rest.INACTIVE, time.Second)}, nil, "INACTIVE"},
		{"only an old container's record", []rest.Worker{rec("satat-worker-old", rest.ACTIVE, time.Second)}, nil, "not registered"},
		{"old inactive record beside a fresh own one", []rest.Worker{rec("satat-worker-old", rest.INACTIVE, time.Hour), rec(me, rest.ACTIVE, time.Second)}, nil, ""},
		{"list fails", nil, errors.New("connection refused"), "connection refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list := func(context.Context) ([]rest.Worker, error) { return tc.rows, tc.listErr }
			err := HatchetWorker(list, me, 12*time.Second, now).Run(context.Background())
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v; want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestDockerPingNamesItself(t *testing.T) {
	c := DockerPing(func(context.Context) error { return errors.New("refused") })
	if c.Name != "docker" || c.Run(context.Background()) == nil {
		t.Fatalf("DockerPing = %q, want a failing check named docker", c.Name)
	}
}

func TestWaitReadyFailsAfterGrace(t *testing.T) {
	c := New(10*time.Millisecond, down())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	err := WaitReady(ctx, c, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "b") || !strings.Contains(err.Error(), "down") {
		t.Fatalf("WaitReady = %v; want an error naming check b", err)
	}

	var runs atomic.Int32
	flaky := Check{Name: "flaky", Run: func(context.Context) error {
		if runs.Add(1) < 2 {
			return errors.New("warming up")
		}
		return nil
	}}
	c = New(10*time.Millisecond, flaky)
	c.Start(ctx)
	if err := WaitReady(ctx, c, 5*time.Second); err != nil {
		t.Fatalf("WaitReady = %v; want nil once the check passes", err)
	}
}
