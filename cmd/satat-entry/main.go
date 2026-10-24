// Command satat-entry is PID 1 of a Kubernetes step container. It waits until the pod's network
// policy is in force, loads the lane's secrets from mounted files, runs the lane command with
// stdout and stderr captured apart, and writes one result frame (package entry) as its only
// stdout. The worker reads that frame back through pods/log.
//
//	satat-entry --probe host:port [--probe-timeout 30s] [--pass-env NAME]... [--timeout 20m] -- cmd args...
//
// --probe names an address the lane's network policy blocks. Until a dial to it is refused or
// times out the policy is not yet applied, so the command does not start; if it still answers
// after --probe-timeout the wrapper fails closed with exit code 125.
//
// The exit code is the command's, 128+signal when a signal ended it, or 125 when the wrapper
// could not run it. Every exit path writes a frame; the wrapper's own diagnostics go into the
// frame's stderr.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/javatarz/satat/entry"
)

// Paths in the step pod. They are variables so tests can point them at temp dirs.
var (
	podinfoDir   = "/etc/podinfo"
	secretsDir   = "/run/satat/secrets"
	satatDir     = "/etc/satat"
	cgroupEvents = "/sys/fs/cgroup/memory.events"
)

const (
	failCode      = 125
	probeInterval = 200 * time.Millisecond
	dialTimeout   = time.Second
	killGrace     = 5 * time.Second
)

type options struct {
	probe        string
	probeTimeout time.Duration
	passEnv      []string
	timeout      time.Duration
	command      []string
}

type names []string

func (n *names) String() string     { return strings.Join(*n, ",") }
func (n *names) Set(v string) error { *n = append(*n, v); return nil }

func parse(args []string) (options, error) {
	o := options{}
	fs := flag.NewFlagSet("satat-entry", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.probe, "probe", "", "host:port the lane's network policy blocks")
	fs.DurationVar(&o.probeTimeout, "probe-timeout", 30*time.Second, "how long to wait for the network policy")
	fs.Var((*names)(&o.passEnv), "pass-env", "secret to load from the secrets dir (repeatable)")
	fs.DurationVar(&o.timeout, "timeout", 20*time.Minute, "bound on the command")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	o.command = fs.Args()
	if o.probe == "" {
		return o, errors.New("--probe is required")
	}
	if len(o.command) == 0 {
		return o, errors.New("no command after --")
	}
	return o, nil
}

func main() {
	o, err := parse(os.Args[1:])
	if err != nil {
		_ = entry.Write(os.Stdout, entry.Result{ExitCode: failCode, Stderr: diag(nil, "usage: %v", err)})
		os.Exit(failCode)
	}
	os.Exit(run(context.Background(), o, os.Stdout))
}

// run executes the step and writes its frame to stdout. It returns the wrapper's exit code.
func run(ctx context.Context, o options, stdout io.Writer) int {
	res := execute(ctx, o)
	if err := entry.Write(stdout, res); err != nil {
		return failCode
	}
	return res.ExitCode
}

func execute(ctx context.Context, o options) entry.Result {
	fail := func(format string, a ...any) entry.Result {
		return entry.Result{ExitCode: failCode, Stderr: diag(nil, format, a...)}
	}
	if err := waitForPolicy(ctx, o.probe, o.probeTimeout); err != nil {
		return fail("%v", err)
	}
	secrets, env, err := loadSecrets(o.passEnv)
	if err != nil {
		return fail("%v", err)
	}
	stepEnv, err := prepare()
	if err != nil {
		return fail("%v", err)
	}
	env = append(stepEnv, env...)

	oomBefore := oomKills()
	stdout := &capped{}
	stderr := &capped{}
	runCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, o.command[0], o.command[1:]...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = killGrace
	if err := cmd.Start(); err != nil {
		return fail("start %s: %v", o.command[0], err)
	}
	stop := forwardSIGTERM(cmd.Process)
	waitErr := cmd.Wait()
	stop()

	res := entry.Result{
		ExitCode:      exitCode(cmd.ProcessState),
		OOMKilled:     oomKills() > oomBefore,
		TimedOut:      errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil,
		Stdout:        entry.Scrub(stdout.buf.Bytes(), secrets),
		Stderr:        entry.Scrub(stderr.buf.Bytes(), secrets),
		LogsTruncated: stdout.truncated || stderr.truncated,
	}
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		res.Stderr = diag(res.Stderr, "output pipes still open %s after the command exited", killGrace)
	}
	if res.TimedOut {
		res.Stderr = diag(res.Stderr, "timed out after %s", o.timeout)
	}
	res.Output, res.FilesTruncated, err = entry.PackDir(filepath.Join(satatDir, "out"))
	if err != nil {
		res.Stderr = diag(res.Stderr, "collect output: %v", err)
	}
	return res
}

// waitForPolicy dials addr until the dial is refused or times out, the sign that the pod's
// network policy now blocks it. A dial that connects or fails to resolve means keep waiting.
func waitForPolicy(ctx context.Context, addr string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	d := net.Dialer{Timeout: dialTimeout}
	for {
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			c.Close()
		} else if blocked(err) {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("network policy probe of %s: %w", addr, ctx.Err())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("network policy not in force: %s still reachable after %s", addr, limit)
		}
		time.Sleep(probeInterval)
	}
}

func blocked(err error) bool {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// loadSecrets reads each named secret from its file. It returns the values, for scrubbing, and
// the NAME=value entries for the command's environment. A missing secret is an error that names
// it and never shows a value.
func loadSecrets(names []string) (values, env []string, err error) {
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(secretsDir, n))
		if err != nil {
			return nil, nil, fmt.Errorf("secret %s: not readable from %s", n, secretsDir)
		}
		values = append(values, string(b))
		env = append(env, n+"="+string(b))
	}
	return values, env, nil
}

// prepare copies the Downward API input to input.json, creates the output directory and returns
// the step's SATAT_* environment.
func prepare() ([]string, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(podinfoDir, name))
		if err != nil {
			return "", fmt.Errorf("pod info %s: %w", name, err)
		}
		return string(b), nil
	}
	run, err := read("run")
	if err != nil {
		return nil, err
	}
	step, err := read("step")
	if err != nil {
		return nil, err
	}
	input, err := read("input")
	if err != nil {
		return nil, err
	}
	in := filepath.Join(satatDir, "input.json")
	if err := os.Remove(in); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("write input: %w", err)
	}
	if err := os.WriteFile(in, []byte(input), 0o444); err != nil {
		return nil, fmt.Errorf("write input: %w", err)
	}
	if err := os.Chmod(in, 0o444); err != nil {
		return nil, fmt.Errorf("write input: %w", err)
	}
	out := filepath.Join(satatDir, "out")
	if err := os.MkdirAll(out, 0o700); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}
	if err := os.Chmod(out, 0o700); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}
	return []string{
		"SATAT_RUN=" + run,
		"SATAT_STEP=" + step,
		"SATAT_INPUT=" + in,
		"SATAT_OUTPUT=" + out,
	}, nil
}

// forwardSIGTERM passes a SIGTERM sent to the wrapper on to the command until stop is called.
func forwardSIGTERM(p *os.Process) (stop func()) {
	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigs, syscall.SIGTERM)
	go func() {
		for {
			select {
			case s := <-sigs:
				_ = p.Signal(s)
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
	}
}

func exitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

// oomKills reads the cgroup's oom_kill count, or 0 when it cannot be read.
func oomKills() int {
	b, err := os.ReadFile(cgroupEvents)
	if err != nil {
		return 0
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "oom_kill "); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

// diag appends one wrapper diagnostic line to stderr.
func diag(stderr []byte, format string, a ...any) []byte {
	return fmt.Appendf(stderr, "satat-entry: "+format+"\n", a...)
}

// capped keeps the first entry.MaxStream bytes written to it and drops the rest.
type capped struct {
	buf       bytes.Buffer
	truncated bool
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	room := entry.MaxStream - c.buf.Len()
	if room < len(p) {
		c.truncated = true
		p = p[:max(room, 0)]
	}
	c.buf.Write(p)
	return n, nil
}
