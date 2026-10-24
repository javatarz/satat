package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/javatarz/satat/entry"
)

// setup points the wrapper's paths at temp dirs, writes the Downward API files and returns options
// whose probe target refuses connections, as a pod's would once its network policy is in force.
func setup(t *testing.T, cmd ...string) options {
	t.Helper()
	root := t.TempDir()
	podinfoDir = filepath.Join(root, "podinfo")
	secretsDir = filepath.Join(root, "secrets")
	satatDir = filepath.Join(root, "satat")
	cgroupEvents = filepath.Join(root, "memory.events")
	for _, d := range []string{podinfoDir, secretsDir, satatDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(podinfoDir, "run"), "r1")
	writeFile(t, filepath.Join(podinfoDir, "step"), "s1")
	writeFile(t, filepath.Join(podinfoDir, "input"), `{"a":1}`)
	return options{
		probe:        refusedAddr(t),
		probeTimeout: 5 * time.Second,
		timeout:      time.Minute,
		command:      cmd,
	}
}

func refusedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func writeFile(t *testing.T, name, s string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runFrame runs the wrapper and returns its exit code and the single frame it wrote.
func runFrame(t *testing.T, o options) (int, entry.Result) {
	t.Helper()
	var out bytes.Buffer
	code := run(context.Background(), o, &out)
	r, ok, err := entry.Parse(out.Bytes())
	if err != nil || !ok {
		t.Fatalf("no frame: ok=%v err=%v out=%q", ok, err, out.String())
	}
	if rest := strings.TrimSpace(strings.Replace(out.String(), frameText(t, r), "", 1)); rest != "" {
		t.Fatalf("stdout has more than the frame: %q", rest)
	}
	return code, r
}

func frameText(t *testing.T, r entry.Result) string {
	t.Helper()
	var b bytes.Buffer
	if err := entry.Write(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestProbeWaitsForRefusal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	time.AfterFunc(600*time.Millisecond, func() { ln.Close() })

	o := setup(t, "sh", "-c", "exit 0")
	o.probe = ln.Addr().String()
	start := time.Now()
	code, r := runFrame(t, o)
	if code != 0 || r.ExitCode != 0 {
		t.Fatalf("code=%d frame=%d stderr=%q, want 0", code, r.ExitCode, r.Stderr)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatalf("ran after %s, before the probe target stopped answering", time.Since(start))
	}
}

func TestProbeFailsClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	marker := filepath.Join(t.TempDir(), "ran")
	o := setup(t, "touch", marker)
	o.probe = ln.Addr().String()
	o.probeTimeout = time.Second
	code, r := runFrame(t, o)
	if code != 125 || r.ExitCode != 125 {
		t.Fatalf("code=%d frame=%d, want 125", code, r.ExitCode)
	}
	if !strings.Contains(string(r.Stderr), "network policy") {
		t.Fatalf("stderr %q does not mention network policy", r.Stderr)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the command ran: %v", err)
	}
}

func TestSecretsLoadedAndScrubbed(t *testing.T) {
	o := setup(t, "sh", "-c", `echo $TOK; echo $TOK >&2`)
	o.passEnv = []string{"TOK"}
	writeFile(t, filepath.Join(secretsDir, "TOK"), "s3cret")
	code, r := runFrame(t, o)
	if code != 0 || string(r.Stdout) != "***\n" || string(r.Stderr) != "***\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, r.Stdout, r.Stderr)
	}
}

func TestMissingSecretFails(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	o := setup(t, "touch", marker)
	o.passEnv = []string{"NOPE"}
	code, r := runFrame(t, o)
	if code == 0 || r.ExitCode == 0 {
		t.Fatalf("code=%d frame=%d, want non-zero", code, r.ExitCode)
	}
	if !strings.Contains(string(r.Stderr), "NOPE") {
		t.Fatalf("stderr %q does not name NOPE", r.Stderr)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the command ran: %v", err)
	}
}

func TestEnvAndInput(t *testing.T) {
	o := setup(t, "sh", "-c", `echo "$SATAT_RUN $SATAT_STEP"; cat "$SATAT_INPUT"; echo hi > "$SATAT_OUTPUT/f.txt"`)
	code, r := runFrame(t, o)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, r.Stderr)
	}
	if string(r.Stdout) != "r1 s1\n{\"a\":1}" {
		t.Fatalf("stdout %q", r.Stdout)
	}
	files, err := entry.UnpackFiles(r.Output)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, map[string][]byte{"f.txt": []byte("hi\n")}) {
		t.Fatalf("files %q", files)
	}
	info, err := os.Stat(filepath.Join(satatDir, "input.json"))
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("input.json: %v %v, want mode 0444", info, err)
	}
}

func TestExitCodeAndStreams(t *testing.T) {
	code, r := runFrame(t, setup(t, "sh", "-c", "echo out; echo err >&2; exit 3"))
	if code != 3 || r.ExitCode != 3 || string(r.Stdout) != "out\n" || string(r.Stderr) != "err\n" {
		t.Fatalf("code=%d frame=%d stdout=%q stderr=%q", code, r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestTimeout(t *testing.T) {
	o := setup(t, "sleep", "30")
	o.timeout = time.Second
	start := time.Now()
	code, r := runFrame(t, o)
	if !r.TimedOut {
		t.Fatalf("not timed out: code=%d stderr=%q", code, r.Stderr)
	}
	if code != 128+15 {
		t.Fatalf("code=%d, want 143 (SIGTERM)", code)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("returned after %s", d)
	}
}

func TestStreamCap(t *testing.T) {
	code, r := runFrame(t, setup(t, "sh", "-c", "head -c 2097152 /dev/zero"))
	if code != 0 || len(r.Stdout) != entry.MaxStream || !r.LogsTruncated {
		t.Fatalf("code=%d len=%d truncated=%v", code, len(r.Stdout), r.LogsTruncated)
	}
}

func TestOOMFromCgroup(t *testing.T) {
	o := setup(t, "sh", "-c", `printf 'oom 1\noom_kill 1\n' > "$EVENTS"`)
	writeFile(t, cgroupEvents, "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n")
	t.Setenv("EVENTS", cgroupEvents)
	_, r := runFrame(t, o)
	if !r.OOMKilled {
		t.Fatalf("OOMKilled false, stderr=%q", r.Stderr)
	}
}
