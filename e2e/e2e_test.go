//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// project is the Compose project the stack runs as, so its containers and volumes never mix with
// a dev stack's (project satat).
const project = "satat-e2e"

// configTmpl is the stack's satat.yaml. %s is the smoke lane's marker, which the config roll
// changes. Every lane is agent: false, so no run needs a model or GitHub.
const configTmpl = `# Written by the e2e tests (e2e/e2e_test.go). Do not edit.
limits: {global: 2, per_repo: 1, per_account: 1}
deploy: {target: compose, secrets: {SATAT_GITHUB_TOKEN: env, SATAT_PUSH_TOKEN: env}}
lanes:
` + `  smoke: {agent: false, image: "` + busybox + `", command: ["sh", "-c", "id && echo %s"], ` + laneShape + `}
` + `  input: {agent: false, image: "` + busybox + `", command: ["cat", "/etc/satat/input.json"], ` + laneShape + `}
`

const (
	busybox   = "busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"
	laneShape = "memory: 128MiB, cpus: 0.25, pids: 16, workspace: 8MiB, timeout: 2m"
)

var (
	satat      string // the satat CLI, built from this checkout
	configPath string // the stack's satat.yaml
)

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		code = 1
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	root, err := filepath.Abs("..")
	if err != nil {
		return 1, err
	}
	if err := refuseOtherStacks(); err != nil {
		return 1, err
	}

	bin, err := os.MkdirTemp("", "satat-e2e-bin")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(bin)
	satat = filepath.Join(bin, "satat")
	if out, err := exec.Command("go", "build", "-o", satat, filepath.Join(root, "cmd", "satat")).CombinedOutput(); err != nil {
		return 1, fmt.Errorf("building satat: %w\n%s", err, out)
	}

	// Every command the tests run inherits this environment: the project name and the worker's
	// placeholder tokens, which no lane gets and nothing calls GitHub with.
	for k, v := range map[string]string{
		"COMPOSE_PROJECT_NAME": project,
		"SATAT_GITHUB_TOKEN":   "e2e-unused",
		"SATAT_PUSH_TOKEN":     "e2e-unused",
	} {
		os.Setenv(k, v)
	}

	dir := filepath.Join(root, "docs", "scratch", "e2e")
	configPath = filepath.Join(dir, "satat.yaml")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 1, err
	}
	if err := writeConfig("ok"); err != nil {
		return 1, err
	}
	if out, err := satatCmd(context.Background(), "deploy", "render", "--config", configPath); err != nil {
		return 1, fmt.Errorf("deploy render: %w\n%s", err, out)
	}
	// A run that was stopped part way leaves the project behind; start from nothing.
	if out, err := satatCmd(context.Background(), "deploy", "down", "--volumes", "--config", configPath); err != nil {
		return 1, fmt.Errorf("removing a stack left by an earlier run: %w\n%s", err, out)
	}
	if err := refusePortsInUse(); err != nil {
		return 1, err
	}

	code := m.Run()
	if os.Getenv("SATAT_E2E_KEEP") != "" {
		fmt.Fprintf(os.Stderr, "e2e: SATAT_E2E_KEEP is set: the stack is left up; remove it with\n"+
			"  COMPOSE_PROJECT_NAME=%s go run ./cmd/satat deploy down --volumes --config %s\n", project, configPath)
		return code, nil
	}
	if out, err := satatCmd(context.Background(), "deploy", "down", "--volumes", "--config", configPath); err != nil {
		return 1, fmt.Errorf("deploy down: %w\n%s", err, out)
	}
	return code, nil
}

// refuseOtherStacks stops the suite before it touches anything when a dev stack or an agent
// container exists: the e2e stack publishes the same ports, and deploy down removes every Satat
// agent container on the host, not only its own.
func refuseOtherStacks() error {
	if ids, err := dockerIDs("ps", "-a", "--filter", "label=com.docker.compose.project=satat"); err != nil {
		return err
	} else if len(ids) > 0 {
		return errors.New("a Satat stack (Compose project satat) exists: stop it with `just down` first")
	}
	if ids, err := dockerIDs("ps", "-a", "--filter", "label=satat.managed=true"); err != nil {
		return err
	} else if len(ids) > 0 {
		return fmt.Errorf("%d Satat agent containers exist (label satat.managed=true), and deploy down would "+
			"remove them: remove them or wait for them to finish first", len(ids))
	}
	return nil
}

// refusePortsInUse names a published port something else already holds, before compose fails on it.
func refusePortsInUse() error {
	for _, port := range []string{"7077", "8888"} {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second); err == nil {
			c.Close()
			return fmt.Errorf("127.0.0.1:%s is in use: the e2e stack publishes it", port)
		}
	}
	return nil
}

// TestStack runs the stack's life in order: each step needs the one before, so a failure stops
// the rest.
func TestStack(t *testing.T) {
	steps := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"up from nothing reaches healthy", testUpFromNothing},
		{"up again changes nothing", testUpAgain},
		{"smoke run goes through the worker", testSmokeRun},
		{"input reaches the step", testInput},
		{"edited satat.yaml reaches the worker", testConfigRoll},
		{"down --volumes leaves nothing", testDown},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.fn) {
			return
		}
	}
}

func testUpFromNothing(t *testing.T) {
	satatOK(t, 10*time.Minute, "deploy", "up", "--config", configPath)
	out := satatOK(t, time.Minute, "deploy", "status", "--config", configPath)
	want := map[string]bool{"postgres": false, "hatchet-lite": false, "docker-proxy": false, "worker": false}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		if _, ok := want[f[0]]; ok {
			want[f[0]] = f[1] == "running" && f[2] == "true"
		}
	}
	for svc, ok := range want {
		if !ok {
			t.Errorf("%s is not running and healthy:\n%s", svc, out)
		}
	}
}

func testUpAgain(t *testing.T) {
	before := workerFacts(t)
	out := satatOK(t, 5*time.Minute, "deploy", "up", "--config", configPath)
	if after := workerFacts(t); after.id != before.id {
		// Compose recreates a container when its config hash or its image changes; say which.
		t.Errorf("worker recreated with nothing changed:\n before %+v\n after  %+v\n%s", before, after, out)
		if before.image != after.image {
			t.Logf("images:\n before %s\n after  %s", imageFacts(before.image), imageFacts(after.image))
		}
	}
	for _, s := range []string{"generated POSTGRES_PASSWORD", "created HATCHET_CLIENT_TOKEN", "starting postgres"} {
		if strings.Contains(out, s) {
			t.Errorf("second up did %q, want nothing new:\n%s", s, out)
		}
	}
}

func testSmokeRun(t *testing.T) {
	out := agentRun(t, "smoke")
	if !strings.Contains(out, "status: ok") || !strings.Contains(out, "uid=10001") || !lastLineIs(out, "ok") {
		t.Errorf("smoke run did not report ok as uid 10001:\n%s", out)
	}
}

func testInput(t *testing.T) {
	marker := nonce(t)
	in := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(in, []byte(`{"marker":"`+marker+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := agentRun(t, "input", "--input", in)
	if !strings.Contains(out, marker) {
		t.Errorf("the step's stdout lacks the input's marker %s:\n%s", marker, out)
	}
}

// The regression #218 fixed: editors save by replacing the file, a single-file bind mount keeps the
// old one, and compose recreated the worker only for a change to its own config. The edit has to
// reach the running worker, which the smoke lane's new output proves.
func testConfigRoll(t *testing.T) {
	before := workerID(t)
	marker := "v2-" + nonce(t)
	if err := writeConfig(marker); err != nil {
		t.Fatal(err)
	}
	satatOK(t, 5*time.Minute, "deploy", "up", "--config", configPath)
	if after := workerID(t); after == before {
		t.Error("worker not recreated after satat.yaml changed")
	}
	if out := agentRun(t, "smoke"); !lastLineIs(out, marker) {
		t.Errorf("smoke run did not print the edited marker %s:\n%s", marker, out)
	}
}

func testDown(t *testing.T) {
	satatOK(t, 5*time.Minute, "deploy", "down", "--volumes", "--config", configPath)
	for what, args := range map[string][]string{
		"containers of the project": {"ps", "-a", "--filter", "label=com.docker.compose.project=" + project},
		"agent containers":          {"ps", "-a", "--filter", "label=satat.managed=true"},
		"volumes of the project":    {"volume", "ls", "--filter", "label=com.docker.compose.project=" + project},
	} {
		ids, err := dockerIDs(args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) > 0 {
			t.Errorf("%d %s left after down --volumes", len(ids), what)
		}
	}
	env, err := os.ReadFile(filepath.Join(filepath.Dir(configPath), ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), "HATCHET_CLIENT_TOKEN=") {
		t.Error(".env still holds HATCHET_CLIENT_TOKEN, which died with the volumes")
	}
}

// writeConfig replaces satat.yaml the way an editor saves: a new file renamed over the old one.
func writeConfig(marker string) error {
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf(configTmpl, marker)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}

func agentRun(t *testing.T, lane string, args ...string) string {
	t.Helper()
	return satatOK(t, 3*time.Minute, append([]string{"agent", "run", lane, "--wait", "--config", configPath}, args...)...)
}

// lastLineIs reports whether the step's stdout ends with the line want.
func lastLineIs(out, want string) bool {
	_, tail, ok := strings.Cut(out, "stdout (tail):\n")
	if !ok {
		return false
	}
	lines := strings.Split(strings.TrimSpace(tail), "\n")
	return lines[len(lines)-1] == want
}

func workerID(t *testing.T) string {
	t.Helper()
	ids, err := dockerIDs("ps", "--filter", "label=com.docker.compose.project="+project,
		"--filter", "label=com.docker.compose.service=worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("want one running worker, found %d", len(ids))
	}
	return ids[0]
}

// facts is what compose decides a recreate on, with the container's ID.
type facts struct{ id, image, configHash string }

func workerFacts(t *testing.T) facts {
	t.Helper()
	id := workerID(t)
	out, err := exec.Command("docker", "inspect", "--format",
		`{{.Image}} {{index .Config.Labels "com.docker.compose.config-hash"}}`, id).Output()
	if err != nil {
		t.Fatalf("docker inspect %s: %v", id, err)
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		t.Fatalf("docker inspect %s printed %q", id, out)
	}
	return facts{id: id, image: f[0], configHash: f[1]}
}

// imageFacts is what can make two builds of the same source differ: the creation time, the layers
// and the config's labels.
func imageFacts(id string) string {
	out, err := exec.Command("docker", "image", "inspect", "--format",
		`created {{.Created}} layers {{json .RootFS.Layers}} labels {{json .Config.Labels}}`, id).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("docker image inspect %s: %v: %s", id, err, out)
	}
	return strings.TrimSpace(string(out))
}

func satatOK(t *testing.T, timeout time.Duration, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := satatCmd(ctx, args...)
	if err != nil {
		t.Fatalf("satat %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func satatCmd(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, satat, args...).CombinedOutput()
	return string(out), err
}

// dockerIDs runs a docker listing with -q and returns the IDs it printed.
func dockerIDs(args ...string) ([]string, error) {
	out, err := exec.Command("docker", append(args, "-q")...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return strings.Fields(string(out)), nil
}

func nonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
