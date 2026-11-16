// Package pipeline runs one issue through one lane and, optionally, publishes the result: the
// prototype's whole path from issue to draft PR, minus the queue (Hatchet, slice 5) and the
// webhook intake (slice 6). The hand trigger (cmd/satat-step) and the eval harness both use it.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/notify"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/runner"
)

// StepName is the name of the one step Run launches.
const StepName = "claude"

// Publisher turns a finished step's commits into a pushed branch and a draft PR.
// *pusher.Pusher is the real one.
type Publisher interface {
	Push(ctx context.Context, req pusher.Request) (pusher.Outcome, error)
}

// Options says what to run. A nil Pusher runs the step and stops before GitHub.
type Options struct {
	Launcher launcher.Launcher
	Pusher   Publisher
	// Cloner clones the repo before the step starts, so the step holds no GitHub credential. It
	// is required: with none, nothing is launched.
	Cloner Cloner

	Run, Lane  string
	Repo, Base string
	Issue      runner.Issue

	// Log, when set, gets one line per stage, so the worker's log shows where a run is.
	Log *slog.Logger
	// Notify, when set, gets an event per stage (#235).
	Notify *notify.Safe
}

// note sends ev with the run's identity filled in.
func (o Options) note(ctx context.Context, ev notify.Event) {
	ev.Run, ev.Repo, ev.Issue, ev.Lane = o.Run, o.Repo, o.Issue.Number, o.Lane
	o.Notify.Send(ctx, ev)
}

func (o Options) log(msg string, args ...any) {
	if o.Log != nil {
		o.Log.Info(msg, append([]any{"run", o.Run, "issue", fmt.Sprintf("%s#%d", o.Repo, o.Issue.Number)}, args...)...)
	}
}

// Report is everything the run produced. Runner is nil when the step left no result.json.
type Report struct {
	Run    string
	Launch launcher.Result
	Runner *runner.Result
	// Push is set when the change was published. PushErr is set when publishing was attempted
	// and failed, including a Refusal.
	Push    *pusher.Outcome
	PushErr error
}

// Run launches the step, then publishes if the runner reports changes and a Pusher is set. A
// step that fails or changes nothing is a Report, not an error; an error means the machinery
// itself failed.
func Run(ctx context.Context, o Options) (Report, error) {
	rep := Report{Run: o.Run}
	if o.Cloner == nil {
		return rep, errors.New("no cloner configured: the step holds no GitHub credential, so the worker must clone")
	}
	ck, err := o.Cloner.Clone(ctx, o.Repo, o.Base)
	if err != nil {
		return rep, err
	}
	defer ck.Close()
	o.log("cloned", "base_sha", ck.BaseSHA)
	o.note(ctx, notify.Event{State: notify.Cloned, Reason: "at " + short(ck.BaseSHA)})
	// The commands are the base commit's, read by the worker. The step gets them as input.
	input, err := json.Marshal(runner.Input{Repo: o.Repo, Base: o.Base, Issue: o.Issue, Commands: ck.Commands})
	if err != nil {
		return rep, err
	}
	o.log("step starting", "lane", o.Lane)
	o.note(ctx, notify.Event{State: notify.StepStarted})
	rep.Launch, err = o.Launcher.Launch(ctx, launcher.Step{
		Run: o.Run, Name: StepName, Lane: o.Lane, Input: input, Checkout: ck.Tar, CacheScope: launcher.CacheScope(o.Repo),
	})
	if err != nil {
		return rep, err
	}
	if b, ok := rep.Launch.Files["result.json"]; ok {
		var r runner.Result
		if err := json.Unmarshal(b, &r); err != nil {
			return rep, fmt.Errorf("result.json: %w", err)
		}
		rep.Runner = &r
	}
	status := "no result"
	if rep.Runner != nil {
		status = rep.Runner.Status
	}
	o.log("step finished", "exit_code", rep.Launch.ExitCode, "status", status, "timed_out", rep.Launch.TimedOut, "oom_killed", rep.Launch.OOMKilled, "cancelled", rep.Launch.Cancelled)
	o.note(ctx, notify.Event{State: notify.StepFinished, Reason: stepSummary(rep, status)})
	if o.Pusher == nil || rep.Runner == nil || rep.Launch.ExitCode != 0 || rep.Runner.Status != "changes" {
		return rep, nil
	}
	// The base is the commit the worker cloned, never what the step reports: the pusher reads the
	// protected-paths policy from it, and the step's result.json is the agent's own word.
	if rep.Runner.BaseSHA != ck.BaseSHA {
		rep.PushErr = &pusher.Refusal{Reason: fmt.Sprintf("the step reports base %q but the worker cloned %q", rep.Runner.BaseSHA, ck.BaseSHA)}
		o.log("not published", "err", rep.PushErr)
		o.note(ctx, notify.Event{State: notify.NotPublished, Reason: rep.PushErr.Error(), Detail: rep.PushErr.Error()})
		return rep, nil
	}
	out, err := o.Pusher.Push(ctx, pusher.Request{
		Repo: o.Repo, Base: o.Base, Run: o.Run,
		BaseSHA: ck.BaseSHA, HeadSHA: rep.Runner.HeadSHA,
		Bundle: rep.Launch.Files["changes.bundle"],
		Issue:  o.Issue.Number, Summary: Summary(rep.Runner),
	})
	if err != nil {
		rep.PushErr = err
		o.log("not published", "err", err)
		o.note(ctx, notify.Event{State: notify.NotPublished, Reason: err.Error(), Detail: err.Error()})
		return rep, nil
	}
	rep.Push = &out
	o.log("published", "branch", out.Branch, "pr_url", out.PRURL, "reused", out.Reused)
	pub := notify.Event{State: notify.Published, Branch: out.Branch, PRURL: out.PRURL}
	if out.Reused {
		pub.Reason = "the PR already existed (a re-delivered step)"
	}
	o.note(ctx, pub)
	return rep, nil
}

// stepSummary is the step's outcome in a few words, for a notification.
func stepSummary(rep Report, status string) string {
	l := rep.Launch
	switch {
	case l.TimedOut:
		return "timed out"
	case l.OOMKilled:
		return "ran out of memory"
	case l.Cancelled:
		return "cancelled"
	}
	s := fmt.Sprintf("%s, exit %d", strings.ReplaceAll(status, "_", " "), l.ExitCode)
	if rep.Runner != nil && rep.Runner.Commits > 0 {
		s += fmt.Sprintf(", %d commit(s)", rep.Runner.Commits)
	}
	return s
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// Summary is the agent's final message from the runner's result event.
func Summary(r *runner.Result) string {
	var ev struct {
		Result string `json:"result"`
	}
	_ = json.Unmarshal(r.ClaudeResult, &ev)
	return ev.Result
}

// FetchIssue reads an issue with the gh CLI, using the owner's own login on the host.
func FetchIssue(repo string, number int) (runner.Issue, error) {
	out, err := exec.Command("gh", "issue", "view", fmt.Sprint(number), "-R", repo, "--json", "number,title,body").Output()
	if err != nil {
		return runner.Issue{}, fmt.Errorf("gh issue view: %w", err)
	}
	var iss runner.Issue
	return iss, json.Unmarshal(out, &iss)
}

// Save writes what a run left behind into dir and returns the names written.
func Save(dir string, rep Report) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	files := map[string][]byte{"stdout.log": rep.Launch.Stdout, "stderr.log": rep.Launch.Stderr}
	for n, b := range rep.Launch.Files {
		files[n] = b
	}
	var names []string
	var errs []error
	for name, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			errs = append(errs, err)
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, errors.Join(errs...)
}

// OK reports whether the run did what it was asked: the step succeeded, and if a push was
// attempted it succeeded.
func (r Report) OK() bool {
	l := r.Launch
	return l.ExitCode == 0 && !l.TimedOut && !l.OOMKilled && !l.Cancelled && r.PushErr == nil && r.Runner != nil && r.Runner.Status != "failed"
}
