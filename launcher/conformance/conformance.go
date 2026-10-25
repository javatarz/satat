// Package conformance is the behavioural contract every launcher backend must meet. A backend's
// test calls Run with a constructor; the same suite then runs against Docker now and against the
// Kubernetes backend later.
package conformance

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/javatarz/satat/launcher"
)

// Config says how to build the launcher under test.
type Config struct {
	// Image is a digest-pinned image with a POSIX shell (busybox is enough).
	Image string
	// New returns a launcher serving exactly these lanes.
	New func(t *testing.T, lanes []launcher.Lane) launcher.Launcher
}

const (
	mib = 1 << 20
	// uniqueID prints a value that differs between two executions of the same step.
	uniqueID = "cat /proc/sys/kernel/random/uuid"
)

func (c Config) lane(name string, script string, mod func(*launcher.Lane)) launcher.Lane {
	l := launcher.Lane{
		Name: name, Image: c.Image, Command: []string{"sh", "-c", script},
		MemoryBytes: 256 * mib, CPUs: 1, PidsLimit: 128, WorkspaceBytes: 32 * mib,
		Timeout: 60 * time.Second,
	}
	if mod != nil {
		mod(&l)
	}
	return l
}

func launch(t *testing.T, l launcher.Launcher, s launcher.Step) launcher.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := l.Launch(ctx, s)
	if err != nil {
		t.Fatalf("Launch(%s/%s): %v", s.Run, s.Name, err)
	}
	return res
}

// Run executes the suite.
func Run(t *testing.T, c Config) {
	t.Run("exit code and logs", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("logs", "echo out; echo err >&2; exit 3", nil)})
		res := launch(t, l, launcher.Step{Run: "conf1", Name: "logs", Lane: "logs"})
		if res.ExitCode != 3 {
			t.Errorf("exit code = %d, want 3", res.ExitCode)
		}
		if string(res.Stdout) != "out\n" || string(res.Stderr) != "err\n" {
			t.Errorf("stdout=%q stderr=%q", res.Stdout, res.Stderr)
		}
	})

	t.Run("input file", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("input", "cat /etc/satat/input.json", nil)})
		in := `{"repo":"javatarz/satat","issue":112}`
		res := launch(t, l, launcher.Step{Run: "conf2", Name: "input", Lane: "input", Input: []byte(in)})
		if res.ExitCode != 0 || string(res.Stdout) != in {
			t.Errorf("exit=%d stdout=%q, want the input back", res.ExitCode, res.Stdout)
		}
	})

	t.Run("output files", func(t *testing.T) {
		script := `mkdir -p "$SATAT_OUTPUT/sub" && echo -n one > "$SATAT_OUTPUT/a.txt" && echo -n two > "$SATAT_OUTPUT/sub/b.txt" && ln -s /etc/passwd "$SATAT_OUTPUT/link"`
		l := c.New(t, []launcher.Lane{c.lane("output", script, nil)})
		res := launch(t, l, launcher.Step{Run: "conf2b", Name: "output", Lane: "output"})
		if res.ExitCode != 0 {
			t.Fatalf("exit=%d stderr=%q", res.ExitCode, res.Stderr)
		}
		if len(res.Files) != 2 || string(res.Files["a.txt"]) != "one" || string(res.Files["sub/b.txt"]) != "two" {
			t.Errorf("files = %v, want a.txt and sub/b.txt only (a symlink is not a file)", res.Files)
		}
		if res.FilesTruncated {
			t.Error("FilesTruncated set for a small output")
		}
	})

	t.Run("output survives a failing step", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("outfail", `echo -n partial > "$SATAT_OUTPUT/r.txt"; exit 4`, nil)})
		res := launch(t, l, launcher.Step{Run: "conf2c", Name: "outfail", Lane: "outfail"})
		if res.ExitCode != 4 || string(res.Files["r.txt"]) != "partial" {
			t.Errorf("exit=%d files=%v, want the partial output of a failed step", res.ExitCode, res.Files)
		}
	})

	t.Run("timeout stops the step", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("slow", "sleep 60", func(l *launcher.Lane) { l.Timeout = 2 * time.Second })})
		start := time.Now()
		res := launch(t, l, launcher.Step{Run: "conf3", Name: "slow", Lane: "slow"})
		if !res.TimedOut {
			t.Errorf("TimedOut = false, result %+v", res)
		}
		if d := time.Since(start); d > 30*time.Second {
			t.Errorf("took %s to stop a 2s step", d)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("cancel", "sleep 60", nil)})
		done := make(chan launcher.Result, 1)
		go func() { done <- launch(t, l, launcher.Step{Run: "conf4", Name: "cancel", Lane: "cancel"}) }()
		deadline := time.After(60 * time.Second)
		for {
			if err := l.Cancel(context.Background(), "conf4", "cancel"); err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			select {
			case res := <-done:
				if !res.Cancelled {
					t.Errorf("Cancelled = false, result %+v", res)
				}
				return
			case <-deadline:
				t.Fatal("step was not cancelled")
			case <-time.After(300 * time.Millisecond):
			}
		}
	})

	t.Run("cancelling a step that is not running is not an error", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("none", "true", nil)})
		if err := l.Cancel(context.Background(), "conf5", "nothing"); err != nil {
			t.Errorf("Cancel of a missing step: %v", err)
		}
	})

	t.Run("caller context cancel returns its error", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("ctx", "sleep 60", nil)})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := l.Launch(ctx, launcher.Step{Run: "conf6", Name: "ctx", Lane: "ctx"})
		if err == nil {
			t.Fatal("Launch returned no error after its context ended")
		}
	})

	t.Run("duplicate launches share one execution", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("dup", uniqueID+"; sleep 2", nil)})
		var wg sync.WaitGroup
		out := make([]string, 2)
		for i := range out {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i] = string(launch(t, l, launcher.Step{Run: "conf7", Name: "dup", Lane: "dup"}).Stdout)
			}()
			time.Sleep(500 * time.Millisecond)
		}
		wg.Wait()
		if out[0] == "" || out[0] != out[1] {
			t.Errorf("two launches of one step ran twice: %q vs %q", out[0], out[1])
		}
	})

	t.Run("a finished step can run again", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("again", uniqueID, nil)})
		a := launch(t, l, launcher.Step{Run: "conf8", Name: "again", Lane: "again"})
		b := launch(t, l, launcher.Step{Run: "conf8", Name: "again", Lane: "again"})
		if a.Adopted || b.Adopted || string(a.Stdout) == string(b.Stdout) {
			t.Errorf("a rerun after cleanup must be a fresh execution: %+v / %+v", a, b)
		}
	})

	t.Run("out of memory is reported", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("oom", `x=$(head -c 200000000 /dev/zero | tr '\0' a); echo survived`, func(l *launcher.Lane) {
			l.MemoryBytes = 160 * mib
			l.WorkspaceBytes = 16 * mib
		})})
		res := launch(t, l, launcher.Step{Run: "conf9", Name: "oom", Lane: "oom"})
		if !res.OOMKilled {
			t.Errorf("OOMKilled = false, exit %d, stdout %q", res.ExitCode, res.Stdout)
		}
	})

	t.Run("step is locked down", func(t *testing.T) {
		script := `echo uid=$(id -u); (touch /rootfs-write 2>&1 && echo rootfs=writable) || echo rootfs=readonly; ` +
			`touch /workspace/ok && echo workspace=writable; ` +
			`test -e /var/run/docker.sock && echo socket=present || echo socket=absent; ` +
			`grep CapEff /proc/self/status; grep NoNewPrivs /proc/self/status`
		l := c.New(t, []launcher.Lane{c.lane("lock", script, nil)})
		res := launch(t, l, launcher.Step{Run: "conf10", Name: "lock", Lane: "lock"})
		out := string(res.Stdout)
		for _, want := range []string{"uid=10001", "rootfs=readonly", "workspace=writable", "socket=absent", "CapEff:\t0000000000000000", "NoNewPrivs:\t1"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("caches are disk mounts at their paths", func(t *testing.T) {
		script := `for p in /cache/a /cache/b; do grep " $p " /proc/mounts | awk '{print $2, ($3 == "tmpfs" ? "memory" : "disk")}'; done`
		l := c.New(t, []launcher.Lane{c.lane("caches", script, func(l *launcher.Lane) {
			l.Caches = []launcher.Cache{{Name: "a", Path: "/cache/a"}, {Name: "b", Path: "/cache/b"}}
		})})
		res := launch(t, l, launcher.Step{Run: "conf12", Name: "caches", Lane: "caches"})
		if got := string(res.Stdout); got != "/cache/a disk\n/cache/b disk\n" {
			t.Errorf("cache mounts = %q, want both paths disk-backed (stderr %q)", got, res.Stderr)
		}
	})

	t.Run("internal lane has no route out", func(t *testing.T) {
		l := c.New(t, []launcher.Lane{c.lane("internal", `wget -T 4 -q -O /dev/null http://1.1.1.1/ && echo reached || echo blocked`, func(l *launcher.Lane) { l.Internal = true })})
		res := launch(t, l, launcher.Step{Run: "conf11", Name: "internal", Lane: "internal"})
		if got := strings.TrimSpace(string(res.Stdout)); got != "blocked" {
			t.Errorf("internal lane output = %q, want blocked", got)
		}
	})
}
