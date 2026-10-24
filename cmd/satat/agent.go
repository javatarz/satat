package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/deploy"
	"github.com/javatarz/satat/workflows"
)

const agentUsage = "usage: satat agent run <lane> [--input file.json] [--wait] | ls | rm <run>   [--config satat.yaml]"

// The agent commands work only through Hatchet: they push the event, list runs and cancel runs,
// and never talk to Docker, so they keep working when the launcher behind the worker changes.

// RunRow is one run as the commands show it.
type RunRow struct {
	ID, Kind, Lane, State string
	Started               time.Time
	// Error is Hatchet's message for a failed run. A launch error makes the task return an error,
	// and Hatchet may then keep no output, so this is all a --wait caller has to show.
	Error string
	// Nonce is the correlation value agent run put in the run's input.
	Nonce string
}

// errNoSuchRun is what hatchetAPI.Get returns for an ID Hatchet does not know.
var errNoSuchRun = errors.New("no such run")

// ListFilter narrows a run listing. A zero Since means no time window.
type ListFilter struct {
	ActiveOnly bool // only QUEUED and RUNNING runs
	Since      time.Time
}

// hatchetAPI is the part of Hatchet the agent commands use.
type hatchetAPI interface {
	Push(ctx context.Context, event string, in any) error
	// List returns the workflow's runs matching the filter, all pages of them.
	List(ctx context.Context, f ListFilter) ([]RunRow, error)
	// Get returns the run and its output, nil when Hatchet kept none.
	Get(ctx context.Context, id string) (RunRow, *workflows.Output, error)
	Cancel(ctx context.Context, id string) error
}

// exitCode is returned by a command that wants the process to exit with this code.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// pollTimings are the polling intervals; tests shorten them.
type pollTimings struct {
	Lookup, LookupTimeout, Wait time.Duration
}

var defaultTimings = pollTimings{Lookup: time.Second, LookupTimeout: 30 * time.Second, Wait: 2 * time.Second}

func runAgentCmd(args []string) error {
	if len(args) == 0 {
		return errors.New(agentUsage)
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("agent "+sub, flag.ContinueOnError)
	path := fs.String("config", "satat.yaml", "path to satat.yaml")
	var input *string
	var wait *bool
	switch sub {
	case "run":
		input = fs.String("input", "", "JSON file handed to the lane as /etc/satat/input.json")
		wait = fs.Bool("wait", false, "wait for the run to finish and exit with the agent's exit code")
	case "ls", "rm":
	default:
		return fmt.Errorf("agent: unknown subcommand %q (%s)", sub, agentUsage)
	}
	// Go's flag package stops at the first positional, so take it off first to allow
	// `agent run smoke --wait` as well as `agent run --wait smoke`.
	var pos []string
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	want := map[string]int{"run": 1, "ls": 0, "rm": 1}[sub]
	if len(pos) != want {
		return fmt.Errorf("agent %s: want %d argument(s), got %d (%s)", sub, want, len(pos), agentUsage)
	}

	ctx, cancel := signalContext()
	defer cancel()
	// run needs the config for its lanes; ls and rm use it only to default the Hatchet client env,
	// so they still work without one when that env is set.
	cfg, err := config.Load(*path)
	if err != nil && (sub == "run" || !errors.Is(err, os.ErrNotExist)) {
		return err
	}
	var stepInput []byte
	if sub == "run" {
		if *input != "" {
			if stepInput, err = os.ReadFile(*input); err != nil {
				return err
			}
			if !json.Valid(stepInput) {
				return fmt.Errorf("%s is not valid JSON", *input)
			}
		}
	}
	api, err := newHatchetAPI(cfg, *path)
	if err != nil {
		return err
	}
	switch sub {
	case "run":
		return agentRun(ctx, api, cfg, pos[0], stepInput, *wait, os.Stdout, defaultTimings)
	case "ls":
		return agentLs(ctx, api, os.Stdout, time.Now())
	default:
		return agentRm(ctx, api, pos[0], os.Stdout)
	}
}

// agentRun pushes the agent event, prints the new run's ID once Hatchet lists it, and with wait
// follows it to the end.
func agentRun(ctx context.Context, api hatchetAPI, cfg config.Config, lane string, stepInput []byte, wait bool, w io.Writer, t pollTimings) error {
	in, err := workflows.MapAgent(cfg, lane, stepInput)
	if err != nil {
		return err
	}
	// The nonce lets this command find its own run among concurrent pushes to the same lane.
	nonce, err := newNonce()
	if err != nil {
		return err
	}
	in.Nonce = nonce
	// Hatchet's clock can run ahead of this machine's, so the window starts well before the push.
	since := time.Now().Add(-10 * time.Minute)
	if err := api.Push(ctx, workflows.EventAgent, in); err != nil {
		return fmt.Errorf("push event: %w", err)
	}
	fmt.Fprintf(w, "queued %s: run lookup pending\n", lane)
	id, err := findRun(ctx, api, nonce, since, t)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, id)
	if !wait {
		return nil
	}
	return followRun(ctx, api, id, w, t.Wait)
}

func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// findRun polls for the run whose input carries the nonce. Pushing an event returns no run ID, so
// this is how the run is found.
func findRun(ctx context.Context, api hatchetAPI, nonce string, since time.Time, t pollTimings) (string, error) {
	deadline := time.Now().Add(t.LookupTimeout)
	for {
		rows, err := api.List(ctx, ListFilter{Since: since})
		if err != nil {
			return "", err
		}
		for _, r := range rows {
			if r.Nonce == nonce {
				return r.ID, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("the run was queued but not found after %s: check `satat agent ls`", t.LookupTimeout)
		}
		if err := sleep(ctx, t.Lookup); err != nil {
			return "", err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func terminal(state string) bool {
	return state == "COMPLETED" || state == "FAILED" || state == "CANCELLED"
}

// followRun waits for the run to end, prints what it left, and returns the exit the caller should
// make: the agent's own exit code, or 1 when it was cancelled, timed out or never produced a result.
func followRun(ctx context.Context, api hatchetAPI, id string, w io.Writer, every time.Duration) error {
	for {
		row, out, err := api.Get(ctx, id)
		if err != nil {
			return err
		}
		if !terminal(row.State) {
			if err := sleep(ctx, every); err != nil {
				return err
			}
			continue
		}
		if out == nil {
			// A launch error makes the task fail, and Hatchet may drop the output.
			fmt.Fprintf(w, "state: %s\n", row.State)
			if row.Error != "" {
				fmt.Fprintf(w, "error: %s\n", row.Error)
			}
			return exitCode(1)
		}
		printOutput(w, out)
		code := out.ExitCode
		if out.Status == "cancelled" || out.Status == "timed_out" || (out.Status != "ok" && code == 0) {
			code = 1
		}
		if code != 0 {
			return exitCode(code)
		}
		return nil
	}
}

func printOutput(w io.Writer, o *workflows.Output) {
	fmt.Fprintf(w, "status: %s\nexit: %d\n", o.Status, o.ExitCode)
	if len(o.Files) > 0 {
		names := make([]string, 0, len(o.Files))
		for n := range o.Files {
			names = append(names, n)
		}
		sort.Strings(names)
		parts := make([]string, len(names))
		for i, n := range names {
			parts[i] = fmt.Sprintf("%s (%d bytes)", n, o.Files[n])
		}
		fmt.Fprintf(w, "files: %s\n", strings.Join(parts, ", "))
	}
	if o.Stdout != "" {
		fmt.Fprintf(w, "stdout (tail):\n%s\n", strings.TrimRight(o.Stdout, "\n"))
	}
	if o.Stderr != "" {
		fmt.Fprintf(w, "stderr (tail):\n%s\n", strings.TrimRight(o.Stderr, "\n"))
	}
}

func active(state string) bool { return state == "QUEUED" || state == "RUNNING" }

// agentLs lists the runs that are queued or running.
func agentLs(ctx context.Context, api hatchetAPI, w io.Writer, now time.Time) error {
	rows, err := api.List(ctx, ListFilter{ActiveOnly: true})
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tLANE\tSTATE\tAGE")
	for _, r := range rows {
		if active(r.State) {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.Kind, r.Lane, r.State, age(now.Sub(r.Started)))
		}
	}
	return tw.Flush()
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// agentRm cancels a run. An unknown ID is an error; a finished run is not, since the goal (it is
// not running) already holds.
func agentRm(ctx context.Context, api hatchetAPI, id string, w io.Writer) error {
	row, _, err := api.Get(ctx, id)
	if err != nil {
		return err
	}
	if !active(row.State) {
		fmt.Fprintf(w, "not running (%s)\n", row.State)
		return nil
	}
	if err := api.Cancel(ctx, id); err != nil {
		return err
	}
	fmt.Fprintln(w, "cancelled")
	return nil
}

// sdkAPI is hatchetAPI over the Hatchet Go SDK.
type sdkAPI struct {
	hc *hatchet.Client
	// workflow is the ID of workflows.Name; the run list takes IDs, not names.
	workflow uuid.UUID
}

// newHatchetAPI connects with the SDK's HATCHET_CLIENT_* environment, defaulted by hatchetEnv.
func newHatchetAPI(cfg config.Config, configPath string) (hatchetAPI, error) {
	// The SDK logs at debug level by default, which would mix into --wait output.
	if _, ok := os.LookupEnv("HATCHET_CLIENT_LOG_LEVEL"); !ok {
		os.Setenv("HATCHET_CLIENT_LOG_LEVEL", "error")
	}
	defaults, err := hatchetEnv(cfg, configPath, os.LookupEnv)
	if err != nil {
		return nil, err
	}
	for k, v := range defaults {
		os.Setenv(k, v)
	}
	hc, err := hatchet.NewClient()
	if err != nil {
		// Never wrap the token or any env value into the message.
		return nil, fmt.Errorf("hatchet client (needs HATCHET_CLIENT_TOKEN, set or in %s): %w", envFileFor(configPath), err)
	}
	return &sdkAPI{hc: hc}, nil
}

// composeClientEnv is how a CLI on the host reaches a Compose stack: the ports compose.yaml
// publishes on 127.0.0.1, without TLS. A kubernetes target on k3d maps the same ports. The worker
// uses the in-network names instead.
var composeClientEnv = map[string]string{
	"HATCHET_CLIENT_HOST_PORT":    "localhost:7077",
	"HATCHET_CLIENT_TLS_STRATEGY": "none",
	"HATCHET_CLIENT_SERVER_URL":   "http://localhost:8888",
}

// hatchetEnv returns the Hatchet client variables to set because lookup has none: the token from
// the env file deploy up writes next to the config, and for a Compose or kubernetes target the local
// endpoints.
// A variable already set always wins. The values are secrets and are never printed.
func hatchetEnv(cfg config.Config, configPath string, lookup func(string) (string, bool)) (map[string]string, error) {
	out := map[string]string{}
	if _, ok := lookup("HATCHET_CLIENT_TOKEN"); !ok {
		token, found, err := deploy.ReadKey(envFileFor(configPath), "HATCHET_CLIENT_TOKEN")
		if err != nil {
			return nil, fmt.Errorf("reading HATCHET_CLIENT_TOKEN: %w", err)
		}
		if found && token != "" {
			out["HATCHET_CLIENT_TOKEN"] = token
		}
	}
	if cfg.Deploy.Target == config.TargetCompose || cfg.Deploy.Target == config.TargetKubernetes {
		for k, v := range composeClientEnv {
			if _, ok := lookup(k); !ok {
				out[k] = v
			}
		}
	}
	return out, nil
}

// envFileFor is the env file deploy up keeps beside the config.
func envFileFor(configPath string) string { return filepath.Join(filepath.Dir(configPath), ".env") }

func (a *sdkAPI) workflowID(ctx context.Context) (uuid.UUID, error) {
	if a.workflow == uuid.Nil {
		id, err := a.hc.Workflows().GetId(ctx, workflows.Name)
		if err != nil {
			return uuid.Nil, fmt.Errorf("finding workflow %s (is the worker up?): %w", workflows.Name, err)
		}
		a.workflow = id
	}
	return a.workflow, nil
}

func (a *sdkAPI) Push(ctx context.Context, event string, in any) error {
	return a.hc.Events().Push(ctx, event, in)
}

func (a *sdkAPI) List(ctx context.Context, f ListFilter) ([]RunRow, error) {
	wf, err := a.workflowID(ctx)
	if err != nil {
		return nil, err
	}
	const page = int64(200)
	payloads := true
	// Since is required by the API; without a window, ask for everything Hatchet could hold.
	since := f.Since
	if since.IsZero() {
		since = time.Unix(0, 0)
	}
	params := rest.V1WorkflowRunListParams{
		Since:           since,
		WorkflowIds:     &[]uuid.UUID{wf},
		Limit:           func() *int64 { l := page; return &l }(),
		IncludePayloads: &payloads,
	}
	if f.ActiveOnly {
		params.Statuses = &[]rest.V1TaskStatus{rest.V1TaskStatusQUEUED, rest.V1TaskStatusRUNNING}
	}
	var rows []RunRow
	for offset := int64(0); ; offset += page {
		o := offset
		params.Offset = &o
		list, err := a.hc.Runs().List(ctx, params)
		if err != nil {
			return nil, err
		}
		for _, r := range list.Rows {
			kind, lane, nonce := inputFields(r.Input)
			rows = append(rows, RunRow{ID: r.TaskExternalId.String(), Kind: kind, Lane: lane, Nonce: nonce, State: string(r.Status), Started: r.CreatedAt})
		}
		if int64(len(list.Rows)) < page {
			return rows, nil
		}
	}
}

func (a *sdkAPI) Get(ctx context.Context, id string) (RunRow, *workflows.Output, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return RunRow{}, nil, errNoSuchRun
	}
	// The SDK has no typed not-found, and the ID could be any run in the tenant, so check it is
	// one of this workflow's before trusting it.
	wf, err := a.workflowID(ctx)
	if err != nil {
		return RunRow{}, nil, err
	}
	d, err := a.hc.Runs().Get(ctx, u.String())
	if err != nil {
		if strings.Contains(err.Error(), "status 404") || strings.Contains(err.Error(), "status 400") {
			return RunRow{}, nil, errNoSuchRun
		}
		return RunRow{}, nil, err
	}
	if d.Run.WorkflowId != wf {
		return RunRow{}, nil, errNoSuchRun
	}
	kind, lane, nonce := inputFields(d.Run.Input)
	row := RunRow{ID: id, Kind: kind, Lane: lane, Nonce: nonce, State: string(d.Run.Status)}
	if d.Run.CreatedAt != nil {
		row.Started = *d.Run.CreatedAt
	}
	if d.Run.ErrorMessage != nil {
		row.Error = *d.Run.ErrorMessage
	}
	return row, decodeOutput(d.Run.Output), nil
}

func (a *sdkAPI) Cancel(ctx context.Context, id string) error {
	u, err := uuid.Parse(id)
	if err != nil {
		return errNoSuchRun
	}
	_, err = a.hc.Runs().Cancel(ctx, rest.V1CancelTaskRequest{ExternalIds: &[]uuid.UUID{u}})
	return err
}

// inputFields reads a run's input. The workflow input may arrive as the run's input itself or
// nested under "input"; an input with no kind is an issue run (see workflows.Input).
func inputFields(in map[string]any) (kind, lane, nonce string) {
	if nested, ok := in["input"].(map[string]any); ok && in["lane"] == nil {
		in = nested
	}
	kind, _ = in["kind"].(string)
	lane, _ = in["lane"].(string)
	nonce, _ = in["nonce"].(string)
	if kind == "" {
		kind = workflows.KindIssue
	}
	return kind, lane, nonce
}

// decodeOutput returns the run's Output, or nil when Hatchet kept none. A run's output may be
// the task's result or keyed by task name.
func decodeOutput(m map[string]any) *workflows.Output {
	if len(m) == 0 {
		return nil
	}
	if _, ok := m["status"]; !ok {
		for _, v := range m {
			if inner, ok := v.(map[string]any); ok {
				m = inner
				break
			}
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	var out workflows.Output
	if json.Unmarshal(raw, &out) != nil || out.Status == "" {
		return nil
	}
	return &out
}
