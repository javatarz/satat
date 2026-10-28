package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/runner"
)

var t0 = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestDetectRateLimit(t *testing.T) {
	reset := t0.Add(2 * time.Hour)
	cases := map[string]struct {
		r      *runner.Result
		limit  bool
		resets time.Time
	}{
		"nil":    {nil, false, time.Time{}},
		"worked": {&runner.Result{Status: "changes", Error: "rate limit"}, false, time.Time{}},
		"rejected event, nested": {&runner.Result{Status: "failed", RateLimitEvent: []json.RawMessage{
			raw(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1}}`),
			raw(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":` + itoa(reset.Unix()) + `,"rateLimitType":"five_hour"}}`),
		}}, true, reset},
		"rejected event, flat milliseconds": {&runner.Result{Status: "failed", RateLimitEvent: []json.RawMessage{
			raw(`{"type":"rate_limit_event","status":"rejected","resetsAt":` + itoa(reset.UnixMilli()) + `}`),
		}}, true, reset},
		"warning only": {&runner.Result{Status: "failed", Error: "boom", RateLimitEvent: []json.RawMessage{
			raw(`{"rate_limit_info":{"status":"allowed_warning","resetsAt":5}}`),
		}}, false, time.Time{}},
		"429 from the api retry":            {&runner.Result{Status: "failed", Error: "claude: exit status 1: rate_limit (HTTP 429), ended with \"x\""}, true, time.Time{}},
		"usage limit result":                {&runner.Result{Status: "failed", ClaudeResult: raw(`{"type":"result","is_error":true,"result":"Claude AI usage limit reached|1761400000"}`)}, true, time.Time{}},
		"api status 429":                    {&runner.Result{Status: "failed", ClaudeResult: raw(`{"is_error":true,"api_error_status":429}`)}, true, time.Time{}},
		"git throttle":                      {&runner.Result{Status: "failed", Error: "git push: fatal: unable to access: The requested URL returned error: 429 Too Many Requests, rate limit exceeded"}, false, time.Time{}},
		"mise download throttle":            {&runner.Result{Status: "failed", Error: "mise install go: download failed: HTTP 429 rate limit reached"}, false, time.Time{}},
		"unrelated failure":                 {&runner.Result{Status: "failed", Error: "claude: exit status 1", ClaudeResult: raw(`{"is_error":true,"result":"tests failed"}`)}, false, time.Time{}},
		"limit text in a non-error summary": {&runner.Result{Status: "failed", Error: "x", ClaudeResult: raw(`{"is_error":false,"result":"added a rate limit"}`)}, false, time.Time{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resets, limited := DetectRateLimit(tc.r)
			if limited != tc.limit || !resets.Equal(tc.resets) {
				t.Fatalf("got (%v, %v), want (%v, %v)", resets, limited, tc.resets, tc.limit)
			}
		})
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestBackoffUntil(t *testing.T) {
	cases := map[string]struct {
		resets  time.Time
		attempt int
		want    time.Duration
	}{
		"waits for the reset plus a margin": {t0.Add(2 * time.Hour), 0, 2*time.Hour + resetMargin},
		"unknown reset starts at five min":  {time.Time{}, 0, 5 * time.Minute},
		"unknown reset doubles":             {time.Time{}, 2, 20 * time.Minute},
		"unknown reset stops at an hour":    {time.Time{}, 10, time.Hour},
		"a reset in the past falls back":    {t0.Add(-time.Hour), 0, 5 * time.Minute},
		"a far reset is capped":             {t0.Add(400 * 24 * time.Hour), 0, maxBackoff},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := BackoffUntil(t0, tc.resets, tc.attempt).Sub(t0); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// fakeClock lets a test fire the reopen timer by hand.
type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
}
type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (c *fakeClock) after(d time.Duration, f func()) func() {
	t := &fakeTimer{d: d, f: f}
	c.timers = append(c.timers, t)
	return func() { t.stopped = true }
}

type puts struct {
	mu   sync.Mutex
	log  [][2]any
	fail bool
}

func (p *puts) put(key string, limit int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return errors.New("hatchet down")
	}
	p.log = append(p.log, [2]any{key, limit})
	return nil
}

func newLimiter() (*Limiter, *puts, *fakeClock) {
	p, c := &puts{}, &fakeClock{now: t0}
	return &Limiter{Put: p.put, Now: func() time.Time { return c.now }, AfterFunc: c.after}, p, c
}

func TestLimiterClosesThenReopensAtTheReset(t *testing.T) {
	l, p, c := newLimiter()
	if err := l.Close(context.Background(), "claude-subscription", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(c.timers) != 1 || c.timers[0].d != time.Hour {
		t.Fatalf("timers: %+v", c.timers)
	}
	c.timers[0].f()
	want := [][2]any{{"account-claude-subscription", 0}, {"account-claude-subscription", OpenLimit}}
	if len(p.log) != 2 || p.log[0] != want[0] || p.log[1] != want[1] {
		t.Fatalf("puts %v, want %v", p.log, want)
	}
}

func TestLimiterOnlyExtendsAPause(t *testing.T) {
	l, p, c := newLimiter()
	ctx := context.Background()
	l.Close(ctx, "a", t0.Add(time.Hour))
	l.Close(ctx, "a", t0.Add(30*time.Minute)) // earlier: ignored
	if len(p.log) != 1 || len(c.timers) != 1 {
		t.Fatalf("earlier close was not ignored: %v, %d timers", p.log, len(c.timers))
	}
	l.Close(ctx, "a", t0.Add(2*time.Hour)) // later: replaces
	if !c.timers[0].stopped || len(c.timers) != 2 {
		t.Fatal("later close did not replace the timer")
	}
	// The stale timer firing must not reopen the lane early.
	c.timers[0].f()
	if got := p.log[len(p.log)-1]; got[1] != 0 {
		t.Fatalf("stale timer reopened the lane: %v", p.log)
	}
	c.timers[1].f()
	if got := p.log[len(p.log)-1]; got[1] != OpenLimit {
		t.Fatalf("lane not reopened: %v", p.log)
	}
}

func TestLimiterRetriesAFailedReopen(t *testing.T) {
	l, p, c := newLimiter()
	l.Close(context.Background(), "a", t0.Add(time.Hour))
	p.fail = true
	c.timers[0].f()
	if len(c.timers) != 2 || c.timers[1].d != time.Minute {
		t.Fatalf("no retry scheduled: %+v", c.timers)
	}
	p.fail = false
	c.timers[1].f()
	if got := p.log[len(p.log)-1]; got[1] != OpenLimit {
		t.Fatalf("lane not reopened: %v", p.log)
	}
}

func TestLimiterCloseFailureIsReported(t *testing.T) {
	l, p, c := newLimiter()
	p.fail = true
	if err := l.Close(context.Background(), "a", t0.Add(time.Hour)); err == nil {
		t.Fatal("want an error")
	}
	if len(c.timers) != 0 {
		t.Fatal("a timer was set for a lane that never closed")
	}
}

func TestOpenSetsEveryAccount(t *testing.T) {
	l, p, _ := newLimiter()
	if err := l.Open("x", "y"); err != nil {
		t.Fatal(err)
	}
	if len(p.log) != 2 || p.log[1] != [2]any{"account-y", OpenLimit} {
		t.Fatalf("%v", p.log)
	}
}

// With a dynamic key and no limit value, Hatchet takes the limit as 0 and upserts it whenever a run
// is queued, closing the account's lane behind every run until the worker reopens it. -1 is
// Hatchet's "do not upsert" placeholder, so the limit stays what the Limiter set.
func TestAccountRateLimitLeavesTheLimitToTheLimiter(t *testing.T) {
	rl := accountRateLimit()
	if rl.LimitValueExpr == nil || *rl.LimitValueExpr != "-1" {
		t.Fatalf("LimitValueExpr = %v, want -1 so Hatchet never upserts the account's limit", rl.LimitValueExpr)
	}
	if rl.Key != AccountKeyExpr || rl.KeyExpr == nil || *rl.KeyExpr != AccountKeyExpr {
		t.Errorf("key = %q / %v, want %q", rl.Key, rl.KeyExpr, AccountKeyExpr)
	}
	if rl.Units == nil || *rl.Units != 1 {
		t.Errorf("units = %v, want 1", rl.Units)
	}
}

type fakeGate struct {
	account string
	until   time.Time
	err     error
	n       int
}

func (g *fakeGate) Close(_ context.Context, a string, u time.Time) error {
	g.n++
	g.account, g.until = a, u
	return g.err
}

func limitedResult() launcher.Result {
	b, _ := json.Marshal(runner.Result{
		Status: "failed", Error: "claude: exit status 1: rate_limit (HTTP 429)",
		RateLimitEvent: []json.RawMessage{raw(`{"rate_limit_info":{"status":"rejected","resetsAt":` + itoa(t0.Add(time.Hour).Unix()) + `}}`)},
	})
	return launcher.Result{ExitCode: 1, Files: map[string][]byte{"result.json": b}}
}

func TestARateLimitedStepClosesTheLaneAndRetries(t *testing.T) {
	l := &fakeLauncher{res: limitedResult()}
	p, g := &fakePub{}, &fakeGate{}
	in := input
	in.Account = "claude-subscription"
	out, err := Handler{Cloner: fakeCloner{}, Launcher: l, Pusher: p, Issues: ready(), Allowed: listed, Gate: g, Now: func() time.Time { return t0 }}.Run(context.Background(), "r", in)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("want a RateLimitError, got %v", err)
	}
	if out.Status != StatusRateLimited || p.n != 0 {
		t.Fatalf("status %q, pushes %d", out.Status, p.n)
	}
	if g.account != "claude-subscription" || g.until != t0.Add(time.Hour+resetMargin) {
		t.Fatalf("gate closed %q until %v", g.account, g.until)
	}
}

func TestACloseFailureIsNotAPlainRateLimit(t *testing.T) {
	l := &fakeLauncher{res: limitedResult()}
	g := &fakeGate{err: errors.New("hatchet down")}
	_, err := Handler{Cloner: fakeCloner{}, Launcher: l, Issues: ready(), Allowed: listed, Gate: g, Now: func() time.Time { return t0 }}.Run(context.Background(), "r", input)
	var rl *RateLimitError
	if err == nil || errors.As(err, &rl) {
		t.Fatalf("want a non-retryable plain error, got %v", err)
	}
}

func TestAnOrdinaryFailureDoesNotCloseTheLane(t *testing.T) {
	b, _ := json.Marshal(runner.Result{Status: "failed", Error: "claude reported an error result"})
	l := &fakeLauncher{res: launcher.Result{ExitCode: 1, Files: map[string][]byte{"result.json": b}}}
	g := &fakeGate{}
	_, err := Handler{Cloner: fakeCloner{}, Launcher: l, Issues: ready(), Allowed: listed, Gate: g}.Run(context.Background(), "r", input)
	var rl *RateLimitError
	if err == nil || errors.As(err, &rl) || g.n != 0 {
		t.Fatalf("err %v, gate calls %d", err, g.n)
	}
}
