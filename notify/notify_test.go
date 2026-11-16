package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type recorder struct {
	got []Event
	err error
	ctx error
}

func (r *recorder) Notify(ctx context.Context, ev Event) error {
	r.got = append(r.got, ev)
	r.ctx = ctx.Err()
	return r.err
}

func TestSafeScrubsShortensAndOutlivesTheRun(t *testing.T) {
	r := &recorder{err: errors.New("ntfy down")}
	var logs strings.Builder
	s := &Safe{Next: r, Secrets: []string{"tok123"}, Log: slog.New(slog.NewTextHandler(&logs, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the run is already cancelled
	s.Send(ctx, Event{State: Failed, Reason: "\n  push failed with tok123\nsecond line", Detail: "push failed with tok123\nsecond line"})
	if len(r.got) != 1 {
		t.Fatalf("got %d events", len(r.got))
	}
	e := r.got[0]
	if e.Reason != "push failed with ***" || strings.Contains(e.Detail, "tok123") || e.At.IsZero() {
		t.Errorf("event = %+v", e)
	}
	if r.ctx != nil {
		t.Errorf("the channel got a cancelled context: %v", r.ctx)
	}
	if !strings.Contains(logs.String(), "notify failed") {
		t.Errorf("a delivery error was not logged: %s", logs.String())
	}
}

func TestANilSafeOrChannelDoesNothing(t *testing.T) {
	var s *Safe
	s.Send(context.Background(), Event{State: Started})
	(&Safe{}).Send(context.Background(), Event{State: Started})
}

func TestShortCutsLongLines(t *testing.T) {
	if got := Short(strings.Repeat("é", 300)); len([]rune(got)) != maxReason || !strings.HasSuffix(got, "…") {
		t.Errorf("Short = %d runes", len([]rune(got)))
	}
}

func TestMultiReachesEveryChannel(t *testing.T) {
	a, b := &recorder{err: errors.New("a")}, &recorder{}
	if err := (Multi{a, b}).Notify(context.Background(), Event{State: Started}); err == nil || len(b.got) != 1 {
		t.Errorf("err %v, b got %d", err, len(b.got))
	}
}
