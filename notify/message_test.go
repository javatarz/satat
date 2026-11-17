package notify

import (
	"strings"
	"testing"
	"time"
)

// TestEveryStateReads pins each state's headline and the parts of its body that carry the facts
// and the next step (ADR 0036). A change in wording should be a deliberate change here.
func TestEveryStateReads(t *testing.T) {
	reset := time.Date(2026, 11, 16, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		ev       Event
		headline string
		body     []string
		needsYou bool
		priority int
		level    Level
	}{
		{Event{State: Queued, Source: "poller", GlobalCap: 2, RepoCap: 1},
			"Satat queued this issue", []string{"Found by the poller.", "at most 2 at once, 1 per repo"}, false, 2, Info},
		{Event{State: Started, Run: "89d7dc38-9f43", Lane: "claude", Base: "main"},
			"Satat started work on this issue", []string{"Run `89d7dc38` on the `claude` lane, from `main`.", "Next: Satat clones"}, false, 3, Info},
		{Event{State: Started, Run: "r", Attempt: 2},
			"Satat started work on this issue", []string{"Retry 2 after the usage limit reset."}, false, 3, Info},
		{Event{State: Skipped, Reason: "o/r#1 is not labelled ready-for-agent"},
			"Satat skipped this issue", []string{"It no longer has `ready-for-agent`", "Nothing ran.", "Add `ready-for-agent` to queue it again."}, true, 4, Warn},
		{Event{State: Cloned, Repo: "o/r", Base: "main", SHA: "44f6ada2a7944529"},
			"Satat cloned o/r", []string{"`main` at `44f6ada`.", "Next: Claude Code starts"}, false, 2, Info},
		{Event{State: StepStarted},
			"Claude Code is working on this issue", []string{"title and body", "Next: it commits"}, false, 2, Info},
		{Event{State: StepFinished, Outcome: "changes", Commits: 2, Rehearsal: "passed", Rehearsals: 1, Took: 14 * time.Second},
			"Claude Code finished: 2 commits", []string{"Took 14 s.", "pre-push checks passed.", "Next: Satat checks the change and opens a draft PR."}, false, 2, Info},
		{Event{State: StepFinished, Outcome: "no_changes", Summary: "The issue is already done.\nMore.", Took: 75 * time.Second},
			"Claude Code finished: no changes", []string{"Took 1 min 15 s.", "> Claude Code said: The issue is already done.", "Nothing to publish."}, false, 2, Info},
		{Event{State: StepFinished, Outcome: "failed", Rehearsal: "failed", Rehearsals: 4},
			"Claude Code finished: it failed", []string{"still failed after 4 tries."}, false, 2, Warn},
		{Event{State: RateLimited, Attempt: 0, MaxAttempts: 20, ResetsAt: reset},
			"Satat paused: Claude usage limit reached", []string{"Retry 1 of 20 is queued for 15:00 UTC, when the limit resets.", "Nothing to do."}, false, 3, Warn},
		{Event{State: Published, PRNumber: 14, PRTitle: "Add greet function", Files: 2, Branch: "satat/89d7"},
			"Satat opened draft PR #14: Add greet function", []string{"2 files changed on `satat/89d7`.", "mark it ready for review", "Satat never merges."}, true, 4, Success},
		{Event{State: NotPublished, Reason: "refusing to publish: gitleaks found a possible secret in the change"},
			"Satat couldn't open a PR: the change looks like it holds a secret", []string{"nothing was pushed.", "Then add `ready-for-agent` again."}, true, 4, Error},
		{Event{State: Completed, PRNumber: 14, PRURL: "u", Took: 150 * time.Second},
			"Satat finished: draft PR #14 is ready for review", []string{"Took 2 min 30 s in all."}, false, 2, Success},
		{Event{State: Completed, Outcome: "no_changes"},
			"Satat finished without changes", []string{"Clarify the issue and add `ready-for-agent` again to retry."}, true, 3, Warn},
		{Event{State: Failed, Reason: "clone o/r: git: exit status 128: fatal: Remote branch nope not found in upstream origin", Took: 4 * time.Second},
			"Satat failed: the base branch `nope` doesn't exist", []string{"After 4 s.", "swapped for `satat:failed`", "Fix the base in `satat.yaml`", "again to re-run it."}, true, 4, Error},
		{Event{State: Failed, Reason: "step timed out", Agent: "Claude Code"},
			"Satat failed: Claude Code ran out of time", []string{"Split the issue"}, true, 4, Error},
		{Event{State: Failed, Reason: "boom"},
			"Satat failed: something went wrong", []string{"Read the details."}, true, 4, Error},
		{Event{State: WorkerDown, Reason: "no Satat worker has a fresh heartbeat since 08:35 UTC"},
			"Satat's worker is down", []string{"since 08:35 UTC.", "`just status`"}, true, 5, Error},
		{Event{State: WorkerUp},
			"Satat's worker is back", []string{"Queued issues start again."}, false, 3, Info},
	}
	for _, tc := range cases {
		tc.ev.Agent = "Claude Code" // every issue run's events name it
		m := Compose(tc.ev)
		if m.Headline != tc.headline {
			t.Errorf("%s: headline = %q, want %q", tc.ev.State, m.Headline, tc.headline)
		}
		for _, want := range tc.body {
			if !strings.Contains(m.Body, want) {
				t.Errorf("%s: body lacks %q:\n%s", tc.ev.State, want, m.Body)
			}
		}
		if m.NeedsYou != tc.needsYou || m.Priority != tc.priority {
			t.Errorf("%s: needs you %v priority %d, want %v %d", tc.ev.State, m.NeedsYou, m.Priority, tc.needsYou, tc.priority)
		}
		if m.Level != tc.level {
			t.Errorf("%s: level %s, want %s", tc.ev.State, m.Level, tc.level)
		}
		if len([]rune(m.Headline)) > 80 {
			t.Errorf("%s: headline is %d characters", tc.ev.State, len([]rune(m.Headline)))
		}
	}
}

func TestTheLinkIsThePRElseTheRun(t *testing.T) {
	if m := Compose(Event{State: Published, PRURL: "pr", RunURL: "run"}); m.Link != "pr" || m.LinkText != "Draft PR" {
		t.Errorf("link = %q %q", m.LinkText, m.Link)
	}
	if m := Compose(Event{State: Started, RunURL: "run"}); m.Link != "run" || m.LinkText != "Run log" {
		t.Errorf("link = %q %q", m.LinkText, m.Link)
	}
}
