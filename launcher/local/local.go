// Package local runs a step as a process on the worker's own machine, with no container. It is
// for trying the flow on a laptop with the operator's own Claude login instead of
// CLAUDE_CODE_OAUTH_TOKEN.
//
// mise is stopped at Dir (MISE_CEILING_PATHS), so a config above it, such as the Satat checkout's
// own mise.toml, cannot pin the step's tools or load its .env. Point SATAT_MISE_DEFAULTS at
// images/claude/mise-config.toml to get the lane's default toolchains.
//
// It is not a sandbox: the agent runs as the operator, with permissions bypassed, and can reach
// everything the operator can. Use it only on repos you trust. None of the ADR 0026 hardening
// floor applies, and lane limits other than the timeout are ignored. A step does not outlive the
// worker, so there is nothing to adopt after a restart.
package local

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/runner"
)

// defaultTimeout matches the launcher package's documented default.
const defaultTimeout = 30 * time.Minute

// Launcher runs steps in directories under Dir with runner.Run, in this process.
type Launcher struct {
	// Dir holds one directory per step. Each step's out/ is kept for inspection; the rest is
	// removed when the step ends.
	Dir string
	Log *slog.Logger

	lanes             map[string]launcher.Lane
	claude, mise, git string

	mu      sync.Mutex
	running map[string]*step
}

type step struct {
	done      chan struct{}
	cancel    context.CancelFunc
	cancelled bool
	res       launcher.Result
	err       error
}

// New builds a launcher for lanes. Steps go under dir.
func New(lanes []launcher.Lane, dir string, log *slog.Logger) *Launcher {
	m := make(map[string]launcher.Lane, len(lanes))
	for _, l := range lanes {
		m[l.Name] = l
	}
	return &Launcher{Dir: dir, Log: log, lanes: m, running: map[string]*step{}}
}

// Setup finds claude, mise and git on PATH. The runner needs absolute paths.
func (l *Launcher) Setup(context.Context) error {
	var errs []error
	for _, b := range []struct {
		name string
		dst  *string
	}{{"claude", &l.claude}, {"mise", &l.mise}, {"git", &l.git}} {
		p, err := exec.LookPath(b.name)
		if err == nil {
			p, err = filepath.Abs(p)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", b.name, err))
			continue
		}
		*b.dst = p
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return err
	}
	// mise matches MISE_CEILING_PATHS against the real path, so resolve it (macOS's $TMPDIR is
	// under the /var symlink and ends in a slash).
	dir, err := filepath.Abs(l.Dir)
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		return err
	}
	l.Dir = dir
	return nil
}

// Launch runs the step and waits for it. A second call for the same step while it runs waits for
// the same result.
func (l *Launcher) Launch(ctx context.Context, s launcher.Step) (launcher.Result, error) {
	key := s.Run + "/" + s.Name
	l.mu.Lock()
	if st, ok := l.running[key]; ok {
		l.mu.Unlock()
		select {
		case <-st.done:
		case <-ctx.Done():
			return launcher.Result{}, ctx.Err()
		}
		res := st.res
		res.Adopted = true
		return res, st.err
	}
	lane, ok := l.lanes[s.Lane]
	if !ok {
		l.mu.Unlock()
		return launcher.Result{}, fmt.Errorf("unknown lane %q", s.Lane)
	}
	timeout := lane.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	st := &step{done: make(chan struct{}), cancel: cancel}
	l.running[key] = st
	l.mu.Unlock()

	st.res, st.err = l.run(runCtx, s)
	cancel()
	l.mu.Lock()
	if st.cancelled {
		st.res.Cancelled = true
	}
	delete(l.running, key)
	l.mu.Unlock()
	close(st.done)
	return st.res, st.err
}

// Cancel stops a running step. A step that is not running is not an error.
func (l *Launcher) Cancel(_ context.Context, run, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st, ok := l.running[run+"/"+name]; ok {
		st.cancelled = true
		st.cancel()
	}
	return nil
}

func (l *Launcher) run(ctx context.Context, s launcher.Step) (launcher.Result, error) {
	if l.claude == "" {
		return launcher.Result{}, errors.New("local launcher: Setup was not run")
	}
	dir := filepath.Join(l.Dir, s.Run+"-"+s.Name)
	if err := os.RemoveAll(dir); err != nil {
		return launcher.Result{}, err
	}
	in, out, work, repo := filepath.Join(dir, "input.json"), filepath.Join(dir, "out"), filepath.Join(dir, "work"), filepath.Join(dir, "repo")
	for _, d := range []string{out, work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return launcher.Result{}, err
		}
	}
	defer func() {
		os.RemoveAll(work)
		os.RemoveAll(repo)
	}()
	if err := os.WriteFile(in, s.Input, 0o600); err != nil {
		return launcher.Result{}, err
	}
	if s.Checkout != nil {
		if err := extract(s.Checkout, repo); err != nil {
			return launcher.Result{}, fmt.Errorf("checkout: %w", err)
		}
	}

	var stderr bytes.Buffer
	err := runner.Run(ctx, runner.Config{
		Run: s.Run, InputPath: in, OutDir: out, WorkDir: work, Checkout: repo,
		Claude: l.claude, Mise: l.mise, Git: l.git,
		Env: append(stepEnv(os.Environ()), "MISE_CEILING_PATHS="+l.Dir), Log: &stderr, LocalLogin: true,
	})
	res := launcher.Result{Stderr: stderr.Bytes()}
	if err != nil {
		res.ExitCode = 1
		res.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	}
	if res.Files, err = readFiles(out); err != nil {
		return res, err
	}
	if l.Log != nil {
		l.Log.Info("local step finished", "run", s.Run, "step", s.Name, "exit_code", res.ExitCode, "out", out)
	}
	return res, nil
}

// stepEnv is the worker's environment minus the worker's own credentials: the GitHub tokens and
// the Hatchet token stay with the worker, as they do on every other launcher. A worker started from
// inside a Claude Code session also drops that session's variables, so the step's claude does not
// think it is nested in it. CLAUDE_CODE_OAUTH_TOKEN is the one kept: it is a way in, not a session.
func stepEnv(env []string) []string {
	keep := map[string]bool{"SATAT_GIT_NAME": true, "SATAT_GIT_EMAIL": true, "SATAT_MISE_TIMEOUT": true, "SATAT_MISE_DEFAULTS": true}
	var outEnv []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		session := k == "CLAUDECODE" || (strings.HasPrefix(k, "CLAUDE_CODE_") && k != "CLAUDE_CODE_OAUTH_TOKEN")
		if (strings.HasPrefix(k, "SATAT_") || strings.HasPrefix(k, "HATCHET_") || k == "GH_TOKEN" || k == "GITHUB_TOKEN" || session) && !keep[k] {
			continue
		}
		outEnv = append(outEnv, kv)
	}
	return outEnv
}

// extract writes a checkout tar under dst. Like the Docker launcher, it allows only directories,
// regular files and symlinks, and refuses a path that leaves the archive or goes under a symlink
// the archive made.
func extract(src io.Reader, dst string) error {
	tr := tar.NewReader(src)
	links := map[string]bool{}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		rel := path.Clean(h.Name)
		if rel == "." {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
			return fmt.Errorf("entry %q leaves the archive", h.Name)
		}
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			if links[d] {
				return fmt.Errorf("entry %q is under the symlink %q", h.Name, d)
			}
		}
		p := filepath.Join(dst, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, fs.FileMode(h.Mode&0o777)|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(h.Mode&0o777)|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			links[rel] = true
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, p); err != nil {
				return err
			}
		default:
			return fmt.Errorf("entry %q has type %q: only directories, files and symlinks are allowed", h.Name, h.Typeflag)
		}
	}
}

// readFiles reads the regular files under dir, keyed by slash path relative to it.
func readFiles(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	return files, err
}
