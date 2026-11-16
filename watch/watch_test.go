package watch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"

	"github.com/javatarz/satat/notify"
)

type events struct{ got []notify.Event }

func (e *events) Notify(_ context.Context, ev notify.Event) error {
	e.got = append(e.got, ev)
	return nil
}

func worker(name string, status rest.WorkerStatus, beat time.Time) rest.Worker {
	return rest.Worker{Name: name, Status: &status, LastHeartbeatAt: &beat}
}

func TestWorkerDownIsReportedOnceAfterTheGraceAndThenBack(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	now := t0
	var rows []rest.Worker
	var err error
	e := &events{}
	w := &Watch{
		Workers: func(context.Context) ([]rest.Worker, error) { return rows, err },
		Grace:   3 * time.Minute, MaxAge: time.Minute,
		Notify: &notify.Safe{Next: e}, Now: func() time.Time { return now },
	}
	step := func(d time.Duration) { now = now.Add(d); w.Check(context.Background()) }

	rows = []rest.Worker{worker("satat-worker-a", rest.ACTIVE, t0)}
	step(0)
	rows = []rest.Worker{worker("satat-worker-a", rest.ACTIVE, t0), worker("other", rest.ACTIVE, t0.Add(10*time.Minute))}
	step(2 * time.Minute) // stale heartbeat, within the grace
	step(2 * time.Minute) // past the grace: reported
	step(time.Minute)     // still down: not again
	if len(e.got) != 1 || e.got[0].State != notify.WorkerDown || !strings.Contains(e.got[0].Reason, "fresh heartbeat") {
		t.Fatalf("events = %+v", e.got)
	}
	rows = []rest.Worker{worker("satat-worker-b", rest.ACTIVE, now)}
	step(time.Minute)
	step(time.Minute)
	if len(e.got) != 2 || e.got[1].State != notify.WorkerUp {
		t.Fatalf("events = %+v", e.got)
	}
}

func TestABriefGapIsNotReported(t *testing.T) {
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	var rows []rest.Worker
	e := &events{}
	w := &Watch{Workers: func(context.Context) ([]rest.Worker, error) { return rows, nil }, Grace: 3 * time.Minute, MaxAge: time.Minute,
		Notify: &notify.Safe{Next: e}, Now: func() time.Time { return now }}
	w.Check(context.Background()) // no worker registered
	now = now.Add(time.Minute)
	rows = []rest.Worker{worker("satat-worker-a", rest.ACTIVE, now)}
	w.Check(context.Background())
	if len(e.got) != 0 {
		t.Errorf("events = %+v, want none for a gap under the grace", e.got)
	}
}

func TestHatchetUnreachableCountsAsDown(t *testing.T) {
	now := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	e := &events{}
	w := &Watch{Workers: func(context.Context) ([]rest.Worker, error) { return nil, errors.New("connection refused") },
		Notify: &notify.Safe{Next: e}, Now: func() time.Time { return now }}
	w.Check(context.Background())
	if len(e.got) != 1 || !strings.Contains(e.got[0].Reason, "cannot reach Hatchet") {
		t.Errorf("events = %+v", e.got)
	}
}
