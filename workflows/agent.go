package workflows

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher"
)

const (
	// KindIssue and KindAgent are the values of Input.Kind. An empty Kind means KindIssue.
	KindIssue = "issue"
	KindAgent = "agent"
	// AdhocRepo is the repo key of every agent run. They have no repo, so they share the per-repo cap.
	AdhocRepo = "adhoc"
	// TailBytes is how much of stdout and stderr an agent run returns. It keeps the Hatchet payload
	// small; the full output is not kept.
	TailBytes = 4096

	agentStep = "agent"
)

// MapAgent builds an agent run input from config, looking up the lane's caps as MapIssue does. It
// refuses a lane that runs an agent, so the CLI fails before it pushes; the worker checks again.
func MapAgent(cfg config.Config, lane string, stepInput []byte) (Input, error) {
	account, ok := cfg.Accounts[lane]
	if !ok {
		return Input{}, fmt.Errorf("unknown lane %q", lane)
	}
	if !cfg.NoAgent[lane] {
		return Input{}, errors.New(agentLaneRefusal(lane))
	}
	return Input{Kind: KindAgent, Repo: AdhocRepo, Lane: lane, Account: account, LaneCap: cfg.LaneMaxRuns[lane], StepInput: stepInput}, nil
}

// agentLaneRefusal says why a lane-started run on an agent lane is refused.
func agentLaneRefusal(lane string) string {
	return fmt.Sprintf("lane %q runs an agent, so it takes no lane-started runs: agent work starts only from an issue labelled %s; only lanes with `agent: false` take lane-started runs", lane, ReadyLabel)
}

// runAgent launches one step and reports what it left. It never publishes anything. A non-zero
// exit, a timeout, an OOM kill and a cancel are results; only a launch error is an error. A lane
// that runs an agent (not in NoAgent) is refused before anything is launched: whoever pushed the
// event, agent work starts only from a triaged issue.
func (h Handler) runAgent(ctx context.Context, runID string, in Input) (Output, error) {
	out := Output{Run: runID, Kind: KindAgent}
	if !h.NoAgent[in.Lane] {
		out.Status, out.Reason = StatusRefused, agentLaneRefusal(in.Lane)
		return out, nil
	}
	res, err := h.Launcher.Launch(ctx, launcher.Step{Run: runID, Name: agentStep, Lane: in.Lane, Input: in.StepInput})
	if err != nil {
		return out, err
	}
	out.ExitCode = res.ExitCode
	out.TimedOut, out.OOMKilled, out.Cancelled = res.TimedOut, res.OOMKilled, res.Cancelled
	out.Stdout = scrubTail(res.Stdout, TailBytes, h.Secrets)
	out.Stderr = scrubTail(res.Stderr, TailBytes, h.Secrets)
	if len(res.Files) > 0 {
		out.Files = make(map[string]int, len(res.Files))
		for name, b := range res.Files {
			out.Files[name] = len(b)
		}
	}
	switch {
	case res.Cancelled:
		out.Status = "cancelled"
	case res.TimedOut:
		out.Status = "timed_out"
	case res.OOMKilled:
		out.Status = "oom"
	case res.ExitCode != 0:
		out.Status = "failed"
	default:
		out.Status = "ok"
	}
	return out, nil
}

// scrubTail returns the last max bytes of b with every secret replaced by "***". It scrubs the
// whole buffer first: cutting first could leave a fragment of a secret that straddles the cut,
// which no longer matches and so would survive.
func scrubTail(b []byte, max int, secrets []string) string {
	// Longest first, so a secret that contains another is not left half replaced.
	sorted := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			sorted = append(sorted, s)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	s := string(b)
	for _, sec := range sorted {
		s = strings.ReplaceAll(s, sec, "***")
	}
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}
