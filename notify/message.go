package notify

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Message is what every channel says about an event (ADR 0036): a headline that says what
// happened, a body with the facts that matter and what happens next or what to do, and one link to
// the detail. Both channels render from it, so they always say the same thing.
type Message struct {
	// Headline is one sentence, outcome first, without the emoji.
	Headline string
	// Emoji marks the kind at a glance; Tag is the same as an ntfy short code. The headline says
	// the state in words, so neither is needed to understand it.
	Emoji, Tag string
	// Body is Markdown: the detail, then the next step.
	Body string
	// Link and LinkText point at the one place to look next (the PR, else the run).
	Link, LinkText string
	// NeedsYou is true when the reader has something to do.
	NeedsYou bool
	// Priority is ntfy's 1 to 5: quiet for information, loud when it needs the reader.
	Priority int
}

// Compose writes the message for ev.
func Compose(ev Event) Message {
	agent := or(ev.Agent, "the agent")
	m := Message{Priority: 2}
	var detail, next []string
	quote := ""
	switch ev.State {
	case Queued:
		m.Emoji, m.Tag, m.Priority = "⏳", "hourglass_flowing_sand", 2
		m.Headline = "Satat queued this issue"
		detail = append(detail, sentence("Found by the "+or(ev.Source, "queue")))
		if ev.GlobalCap > 0 {
			next = append(next, fmt.Sprintf("It starts when a slot is free (at most %d at once, %d per repo).", ev.GlobalCap, max(ev.RepoCap, 1)))
		} else {
			next = append(next, "It starts when a slot is free.")
		}
	case Started:
		m.Emoji, m.Tag, m.Priority = "▶️", "arrow_forward", 3
		m.Headline = "Satat started work on this issue"
		d := "Run `" + shortRun(ev.Run) + "`"
		if ev.Lane != "" {
			d += " on the `" + ev.Lane + "` lane"
		}
		if ev.Base != "" {
			d += ", from `" + ev.Base + "`"
		}
		detail = append(detail, d+".")
		if ev.Attempt > 0 {
			detail = append(detail, fmt.Sprintf("Retry %d after the usage limit reset.", ev.Attempt))
		}
		next = append(next, "Next: Satat clones the repo and starts the agent.")
	case Skipped:
		m.Emoji, m.Tag, m.Priority, m.NeedsYou = "⚠️", "warning", 4, true
		m.Headline = "Satat skipped this issue"
		detail = append(detail, sentence(plainSkip(ev.Reason)), "Nothing ran.")
		next = append(next, "Add `"+ReadyLabel+"` to queue it again.")
	case Cloned:
		m.Emoji, m.Tag = "📥", "inbox_tray"
		m.Headline = "Satat cloned " + or(ev.Repo, "the repo")
		if ev.Base != "" {
			d := "`" + ev.Base + "`"
			if ev.SHA != "" {
				d += " at `" + shortSHA(ev.SHA) + "`"
			}
			detail = append(detail, d+".")
		}
		next = append(next, "Next: "+agent+" starts on the issue.")
	case StepStarted:
		m.Emoji, m.Tag = "🤖", "robot"
		m.Headline = capital(agent) + " is working on this issue"
		detail = append(detail, "It has the issue's title and body and the repo's commands from `.satat/commands.yaml`.")
		next = append(next, "Next: it commits its change, then Satat runs the repo's pre-push checks.")
	case StepFinished:
		m.Emoji, m.Tag = "🏁", "checkered_flag"
		m.Headline = capital(agent) + " finished: " + outcomeWords(ev)
		var d []string
		if ev.Took > 0 {
			d = append(d, "Took "+duration(ev.Took)+".")
		}
		switch ev.Rehearsal {
		case "passed":
			if ev.Rehearsals > 1 {
				d = append(d, fmt.Sprintf("The repo's pre-push checks passed after %d tries.", ev.Rehearsals))
			} else {
				d = append(d, "The repo's pre-push checks passed.")
			}
		case "failed":
			d = append(d, fmt.Sprintf("The repo's pre-push checks still failed after %d tries.", ev.Rehearsals))
		}
		detail = append(detail, strings.Join(d, " "))
		if ev.Summary != "" && ev.Outcome != "changes" {
			quote = capital(agent) + " said: " + Short(ev.Summary)
		}
		switch ev.Outcome {
		case "changes":
			next = append(next, "Next: Satat checks the change and opens a draft PR.")
		case "no_changes":
			next = append(next, "Nothing to publish.")
		}
	case RateLimited:
		m.Emoji, m.Tag, m.Priority = "⏸️", "pause_button", 3
		m.Headline = "Satat paused: Claude usage limit reached"
		r := "A retry is queued"
		if ev.MaxAttempts > 0 {
			r = fmt.Sprintf("Retry %d of %d is queued", ev.Attempt+1, ev.MaxAttempts)
		}
		if !ev.ResetsAt.IsZero() {
			r += " for " + ev.ResetsAt.UTC().Format("15:04 MST") + ", when the limit resets"
		}
		detail = append(detail, r+".")
		next = append(next, "Nothing to do.")
	case Published:
		m.Emoji, m.Tag, m.Priority, m.NeedsYou = "✅", "white_check_mark", 4, true
		m.Headline = "Satat opened a draft PR"
		if ev.PRNumber > 0 {
			m.Headline = fmt.Sprintf("Satat opened draft PR #%d", ev.PRNumber)
		}
		if ev.PRTitle != "" {
			m.Headline += ": " + ev.PRTitle
		}
		var d []string
		if ev.Files > 0 {
			d = append(d, fmt.Sprintf("%d %s changed", ev.Files, plural(ev.Files, "file", "files")))
		}
		if ev.Branch != "" {
			d = append(d, "on `"+ev.Branch+"`")
		}
		if len(d) > 0 {
			detail = append(detail, sentence(strings.Join(d, " ")))
		}
		if ev.Reason != "" {
			detail = append(detail, sentence(ev.Reason))
		}
		next = append(next, "Review it and mark it ready for review when it's right. Satat never merges.")
	case NotPublished:
		m.Emoji, m.Tag, m.Priority, m.NeedsYou = "❌", "x", 4, true
		cause, fix := Explain(ev)
		m.Headline = "Satat couldn't open a PR: " + cause
		detail = append(detail, "The commits stay with the run; nothing was pushed.")
		next = append(next, sentence(fix)+" Then add `"+ReadyLabel+"` again.")
	case Completed:
		m.Emoji, m.Tag = "🏁", "checkered_flag"
		switch {
		case ev.PRNumber > 0:
			m.Headline = fmt.Sprintf("Satat finished: draft PR #%d is ready for review", ev.PRNumber)
		case ev.PRURL != "":
			m.Headline = "Satat finished: the draft PR is ready for review"
		case ev.Outcome == "no_changes":
			m.Headline, m.NeedsYou, m.Priority = "Satat finished without changes", true, 3
			next = append(next, "Clarify the issue and add `"+ReadyLabel+"` again to retry.")
		default:
			m.Headline = "Satat finished"
		}
		if ev.Took > 0 {
			detail = append(detail, "Took "+duration(ev.Took)+" in all.")
		}
	case Failed:
		m.Emoji, m.Tag, m.Priority, m.NeedsYou = "❌", "x", 4, true
		cause, fix := Explain(ev)
		m.Headline = "Satat failed: " + cause
		if ev.Took > 0 {
			detail = append(detail, "After "+duration(ev.Took)+".")
		}
		detail = append(detail, "`"+ReadyLabel+"` was swapped for `"+FailedLabel+"`.")
		next = append(next, sentence(fix)+" Then add `"+ReadyLabel+"` again to re-run it.")
	case WorkerDown:
		m.Emoji, m.Tag, m.Priority, m.NeedsYou = "🚨", "rotating_light", 5, true
		m.Headline = "Satat's worker is down"
		detail = append(detail, sentence(ev.Reason))
		next = append(next, "Queued issues wait until it's back. Check `just status` or the worker's logs.")
	case WorkerUp:
		m.Emoji, m.Tag, m.Priority = "✅", "white_check_mark", 3
		m.Headline = "Satat's worker is back"
		next = append(next, "Queued issues start again.")
	default:
		m.Headline = "Satat: " + string(ev.State)
		if ev.Reason != "" {
			detail = append(detail, sentence(ev.Reason))
		}
	}
	paras := []string{strings.Join(nonEmpty(detail), " ")}
	if quote != "" {
		paras = append(paras, "> "+quote)
	}
	paras = append(paras, strings.Join(next, " "))
	m.Body = strings.Join(nonEmpty(paras), "\n\n")
	switch {
	case ev.PRURL != "":
		m.Link, m.LinkText = ev.PRURL, "Draft PR"
	case ev.RunURL != "":
		m.Link, m.LinkText = ev.RunURL, "Run log"
	}
	return m
}

// cause is a known failure: what it says in the raw error, and what to tell the reader.
type cause struct {
	match *regexp.Regexp
	what  string
	fix   string
}

// causes turns raw failures into plain words (ADR 0036). The first match wins; a failure that
// matches none reads "something went wrong" and points at the details.
var causes = []cause{
	{regexp.MustCompile(`(?i)remote branch (\S+) not found`), "the base branch {1} doesn't exist", "Fix the base in `satat.yaml` or create the branch"},
	{regexp.MustCompile(`(?i)pre-push hook still failing`), "the repo's pre-push checks kept failing", "Read the details, then fix the checks or make the issue clearer"},
	{regexp.MustCompile(`(?i)timed out`), "{agent} ran out of time", "Split the issue into smaller ones, or raise the lane's timeout"},
	{regexp.MustCompile(`(?i)out of memory`), "{agent} ran out of memory", "Raise the lane's memory or split the issue"},
	{regexp.MustCompile(`(?i)gitleaks`), "the change looks like it holds a secret", "Look at the change in the details; Satat won't publish it"},
	{regexp.MustCompile(`(?i)\.github/workflows`), "the change edits CI workflows", "Make that change by hand; Satat won't publish it"},
	{regexp.MustCompile(`(?i)protected`), "the change touches a protected path", "Make that change by hand; Satat won't publish it"},
	{regexp.MustCompile(`(?i)cannot read .*#\d+`), "Satat couldn't read the issue", "Check the read token can see the repo's issues"},
	{regexp.MustCompile(`(?i)left no result`), "{agent} stopped without a result", "Read the details; it may have crashed"},
	{regexp.MustCompile(`(?i)claude auth|oauth|not logged in`), "{agent} isn't logged in", "Check the Claude token (or your login, in local mode)"},
}

// Explain gives the plain cause of a failure and what the reader can do about it.
func Explain(ev Event) (what, fix string) {
	text := ev.Reason + "\n" + ev.Detail
	agent := or(ev.Agent, "the agent")
	for _, c := range causes {
		if m := c.match.FindStringSubmatch(text); m != nil {
			what = strings.ReplaceAll(c.what, "{agent}", agent)
			if len(m) > 1 {
				what = strings.ReplaceAll(what, "{1}", "`"+m[1]+"`")
			}
			return what, c.fix
		}
	}
	return "something went wrong", "Read the details"
}

// plainSkip rewords the handler's skip reasons for someone on the issue.
func plainSkip(reason string) string {
	switch {
	case strings.Contains(reason, "not labelled"):
		return "It no longer has `" + ReadyLabel + "` (it was removed while the run waited)"
	case strings.Contains(reason, "is closed"):
		return "The issue was closed"
	case strings.Contains(reason, "pull request"):
		return "It's a pull request, not an issue"
	case reason == "":
		return "It wasn't ready when the run started"
	}
	return reason
}

func outcomeWords(ev Event) string {
	switch ev.Outcome {
	case "changes":
		return fmt.Sprintf("%d %s", max(ev.Commits, 1), plural(max(ev.Commits, 1), "commit", "commits"))
	case "no_changes":
		return "no changes"
	case "timed_out":
		return "ran out of time"
	case "out_of_memory":
		return "ran out of memory"
	case "cancelled":
		return "cancelled"
	case "failed":
		return "it failed"
	}
	return "done"
}

func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute).Round(time.Second).Seconds())
		if s == 0 {
			return fmt.Sprintf("%d min", m)
		}
		return fmt.Sprintf("%d min %d s", m, s)
	}
	return d.Round(time.Minute).String()
}

func shortRun(run string) string {
	if len(run) > 8 {
		return run[:8]
	}
	return run
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// sentence ends s with a full stop unless it already ends a sentence.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

func nonEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
