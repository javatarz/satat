package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/hatchet-dev/hatchet/sdks/go/features"

	"github.com/javatarz/satat/runner"
)

const (
	// StatusRateLimited is the output status of an attempt that hit the subscription limit. The
	// task is retried once the account's lane reopens.
	StatusRateLimited = "rate_limited"

	// MaxBackoffRetries bounds how often one run is re-queued for a rate limit. Each hit waits for
	// the reset the stream reported, so 20 outlasts several five-hour windows.
	MaxBackoffRetries = 20

	// OpenLimit is the per-second limit of an open account: far above what the caps allow, so the
	// rate limit only ever bites when it is closed to 0. The window is one second, not a minute,
	// because the Hatchet scheduler caches limits and re-reads them once per window: with a minute
	// window a close or reopen took up to a minute to bite, and the immediate retry of the step that
	// closed the lane ran straight through.
	OpenLimit = 1_000_000

	// resetMargin is added to the reported reset so the retry does not land on the boundary.
	resetMargin = 30 * time.Second
	// fallbackBackoff and maxBackoff bound the wait when the stream gave no usable reset time.
	fallbackBackoff = 5 * time.Minute
	maxBackoff      = 8 * 24 * time.Hour
)

// AccountKeyExpr is the Hatchet rate-limit key of a run: one per credential account.
const AccountKeyExpr = "'account-' + input.account"

// AccountKey is the rate-limit key of a credential account.
func AccountKey(account string) string { return "account-" + account }

// accountRateLimit is the task's rate limit: one unit of its account's key per run. The limit
// itself belongs to the Limiter. A dynamic key with no limit value is upserted with limit 0 each
// time a run is queued (Hatchet Lite v0.110.5), which closes the lane behind every run; -1 is
// Hatchet's placeholder for "do not upsert".
func accountRateLimit() *hatchet.RateLimit {
	key, units, limit, window := AccountKeyExpr, 1, "-1", hatchet.Second
	return &hatchet.RateLimit{Key: key, KeyExpr: &key, Units: &units, LimitValueExpr: &limit, Duration: &window}
}

// RateLimitError is returned by an attempt whose step hit the subscription limit. It is the only
// error Register leaves retryable.
type RateLimitError struct {
	// ResetsAt is when the limit resets; zero if the step did not say.
	ResetsAt time.Time
}

func (e *RateLimitError) Error() string {
	if e.ResetsAt.IsZero() {
		return "claude subscription rate limit hit; backing off until it resets"
	}
	return "claude subscription rate limit hit; backing off until " + e.ResetsAt.UTC().Format(time.RFC3339)
}

// limitText matches the wording of an error result from Claude itself.
var limitText = regexp.MustCompile(`(?i)usage limit|hit your limit|limit reached|rate[ _-]?limit|too many requests`)

// claudeAPI429 is the runner's own form for a Claude stage that ended on an HTTP 429
// ("claude: <exit>: <api error> (HTTP 429), ended with ..."). It is anchored on the "claude: "
// prefix because Result.Error also carries git and mise output, where a GitHub or download
// throttle must not close the Claude lane.
var claudeAPI429 = regexp.MustCompile(`^claude: .*\(HTTP 429\)`)

// DetectRateLimit reports whether a failed step died on the subscription rate or usage limit, and
// when the limit resets (zero if unknown). A step that succeeded is never rate limited, whatever
// its summary says.
func DetectRateLimit(r *runner.Result) (resetsAt time.Time, limited bool) {
	if r == nil || r.Status != "failed" {
		return time.Time{}, false
	}
	// The stream's own verdict: a rate_limit_event with status "rejected".
	for _, raw := range r.RateLimitEvent {
		var ev struct {
			Status   string      `json:"status"`
			ResetsAt json.Number `json:"resetsAt"`
			Info     *struct {
				Status   string      `json:"status"`
				ResetsAt json.Number `json:"resetsAt"`
			} `json:"rate_limit_info"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		status, reset := ev.Status, ev.ResetsAt
		if ev.Info != nil {
			status, reset = ev.Info.Status, ev.Info.ResetsAt
		}
		if status == "rejected" {
			limited = true
			if t := parseReset(reset); t.After(resetsAt) {
				resetsAt = t
			}
		}
	}
	if limited {
		return resetsAt, true
	}
	// Otherwise Claude's own error result, or the runner's Claude-stage error.
	var res struct {
		IsError  bool   `json:"is_error"`
		Result   string `json:"result"`
		APIError int    `json:"api_error_status"`
	}
	_ = json.Unmarshal(r.ClaudeResult, &res)
	if res.APIError == 429 || claudeAPI429.MatchString(r.Error) || (res.IsError && limitText.MatchString(res.Result)) {
		return time.Time{}, true
	}
	return time.Time{}, false
}

// parseReset reads a reset as Unix seconds (or milliseconds). It returns the zero time if absent.
func parseReset(n json.Number) time.Time {
	f, err := n.Float64()
	if err != nil || f <= 0 {
		return time.Time{}
	}
	if f > 1e12 {
		return time.UnixMilli(int64(f))
	}
	return time.Unix(int64(f), 0)
}

// BackoffUntil is when the lane should reopen: the reset plus a margin, or, with no usable reset,
// a wait that doubles from five minutes with each earlier back-off of the run. It never exceeds
// eight days.
func BackoffUntil(now, resetsAt time.Time, attempt int) time.Time {
	var wait time.Duration
	if resetsAt.After(now) {
		wait = resetsAt.Sub(now) + resetMargin
	} else {
		wait = fallbackBackoff
		for i := 0; i < attempt && wait < time.Hour; i++ {
			wait *= 2
		}
		wait = min(wait, time.Hour)
	}
	return now.Add(min(wait, maxBackoff))
}

// Gate closes and reopens an account's lane. Close is called by the attempt that hit the limit,
// before it fails, so the retry queues behind a closed lane.
type Gate interface {
	Close(ctx context.Context, account string, until time.Time) error
}

// Limiter is the Gate backed by a Hatchet rate limit per account. Closing sets the limit to 0 and
// reopens it by timer at the reset. The timers live in the worker: Open must run at start, so a
// restart never leaves a lane closed (the next rate-limited step closes it again).
type Limiter struct {
	// Put sets the rate limit of a key per second. HatchetPut is the real one.
	Put func(key string, limit int) error
	// Now and AfterFunc exist for tests.
	Now       func() time.Time
	AfterFunc func(d time.Duration, f func()) (stop func())

	mu     sync.Mutex
	closed map[string]closure
}

type closure struct {
	until time.Time
	stop  func()
}

// HatchetPut returns a Put that upserts the limit through the Hatchet client.
func HatchetPut(c *hatchet.Client) func(string, int) error {
	return func(key string, limit int) error {
		return c.RateLimits().Upsert(features.CreateRatelimitOpts{Key: key, Limit: limit, Duration: hatchet.Second})
	}
}

// Open sets every account's limit open. The worker calls it before registering the workflow, which
// also creates the keys the task's rate limit names.
func (l *Limiter) Open(accounts ...string) error {
	var errs []error
	for _, a := range accounts {
		if err := l.Put(AccountKey(a), OpenLimit); err != nil {
			errs = append(errs, fmt.Errorf("open %s: %w", a, err))
		}
	}
	return errors.Join(errs...)
}

// Close closes the account's lane until the given time. A later close extends the pause; an
// earlier one does not shorten it.
func (l *Limiter) Close(_ context.Context, account string, until time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cur, ok := l.closed[account]; ok && !until.After(cur.until) {
		return nil
	}
	if err := l.Put(AccountKey(account), 0); err != nil {
		return fmt.Errorf("close %s: %w", account, err)
	}
	if l.closed == nil {
		l.closed = map[string]closure{}
	}
	if cur, ok := l.closed[account]; ok {
		cur.stop()
	}
	now, after := l.Now, l.AfterFunc
	if now == nil {
		now = time.Now
	}
	if after == nil {
		after = func(d time.Duration, f func()) func() { return stopFunc(time.AfterFunc(d, f)) }
	}
	l.closed[account] = closure{until: until, stop: after(until.Sub(now()), func() { l.reopen(account, until) })}
	return nil
}

func stopFunc(t *time.Timer) func() { return func() { t.Stop() } }

// reopen restores the lane. If the Put fails it tries again a minute later, because a lane left
// closed would hold queued work forever.
func (l *Limiter) reopen(account string, until time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cur, ok := l.closed[account]
	if !ok || !cur.until.Equal(until) {
		return
	}
	if err := l.Put(AccountKey(account), OpenLimit); err != nil {
		after := l.AfterFunc
		if after == nil {
			after = func(d time.Duration, f func()) func() { return stopFunc(time.AfterFunc(d, f)) }
		}
		cur.stop = after(time.Minute, func() { l.reopen(account, until) })
		l.closed[account] = cur
		return
	}
	delete(l.closed, account)
}
