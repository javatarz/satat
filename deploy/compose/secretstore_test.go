package compose

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/javatarz/satat/deploy"
)

// The secret store's checks from the Compose spike (docs/research/v2/secret-store.md) that need the
// store alone: scripts/secret-store.sh against compose.yaml's openbao service, in its own project
// and subnet so a running Satat stack is left alone. A container of the OpenBao image stands in
// for the worker: at the worker's address, with the worker's credential volume.
const (
	storeProject = "satat-secretstore-test"
	storeSubnet  = "10.157.1.0/24"
	workerAddr   = "10.157.1.10"
	otherAddr    = "10.157.1.11"
)

var placeholders = map[string]string{
	"github-read-token":  "placeholder-read-not-a-token",
	"github-push-token":  "placeholder-push-not-a-token",
	"claude-oauth-token": "placeholder-claude-not-a-token",
}

type store struct {
	t        *testing.T
	root     string
	env      []string
	password string
}

// run runs a command in the repo root with the test project's environment and returns its exit
// code and combined output.
func (s *store) run(stdin string, name string, args ...string) (int, string) {
	s.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = s.root
	cmd.Env = s.env
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out.String()
	}
	if err != nil {
		s.t.Fatalf("%s %v: %v", name, args, err)
	}
	return 0, out.String()
}

func (s *store) script(stdin string, args ...string) (int, string) {
	s.t.Helper()
	return s.run(stdin, "scripts/secret-store.sh", args...)
}

func (s *store) compose(args ...string) string {
	s.t.Helper()
	code, out := s.run("", "docker", append([]string{"compose"}, args...)...)
	if code != 0 {
		s.t.Fatalf("docker compose %v: exit %d: %s", args, code, out)
	}
	return out
}

// at runs a shell script in a container at addr on the secrets network with the worker's
// credential mounted where the worker has it, as the worker's uid, logged in with it first.
func (s *store) at(addr, script string) (int, string) {
	s.t.Helper()
	return s.run("", "docker", "run", "--rm", "--network", storeProject+"_secrets", "--ip", addr,
		"-v", storeProject+"_cred-worker:/run/satat/cred:ro", "--user", "10002:10002",
		"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"-e", "BAO_ADDR=http://openbao:8200", "--entrypoint", "sh", deploy.OpenBaoImage, "-c",
		`BAO_TOKEN="$(bao write -field=token auth/approle/login role_id=@/run/satat/cred/role_id secret_id=@/run/satat/cred/secret_id)" || exit 9
export BAO_TOKEN
`+script)
}

// asOperator runs a bao command as the operator from loopback inside the OpenBao container.
func (s *store) asOperator(args string) (int, string) {
	s.t.Helper()
	return s.run(s.password, "docker", "compose", "exec", "-T", "openbao", "sh", "-c",
		`BAO_TOKEN="$(bao write -field=token auth/userpass/login/operator password=-)" || exit 9
export BAO_TOKEN
bao `+args)
}

func (s *store) wantCheck(code int, contains string) {
	s.t.Helper()
	got, out := s.script("", "check")
	if got != code || !strings.Contains(out, contains) {
		s.t.Fatalf("check: exit %d %q, want exit %d containing %q", got, out, code, contains)
	}
}

func (s *store) wantWorkerReads() {
	s.t.Helper()
	for name, value := range placeholders {
		code, out := s.at(workerAddr, "bao kv get -mount=satat -field=value "+name)
		if code != 0 || strings.TrimSpace(out) != value {
			s.t.Fatalf("worker reads %s: exit %d %q", name, code, out)
		}
	}
}

func envFile(m map[string]string, names ...string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + "=" + m[n] + "\n")
	}
	return b.String()
}

func TestSecretStore(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker not available: %s", strings.TrimSpace(string(out)))
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(t.TempDir(), "override.yaml")
	yaml := strings.Join([]string{
		"networks:",
		"  secrets:",
		"    ipam:",
		"      config:",
		"        - subnet: " + storeSubnet,
		"services:",
		"  worker:",
		"    networks:",
		"      secrets:",
		"        ipv4_address: " + workerAddr,
	}, "\n")
	if err := os.WriteFile(override, []byte(yaml+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &store{t: t, root: root, env: append(os.Environ(),
		"COMPOSE_PROJECT_NAME="+storeProject,
		"COMPOSE_FILE="+filepath.Join(root, "compose.yaml")+":"+override,
		"SATAT_OPERATOR_PASSWORD=")}
	// compose interpolates the whole file for every command; nothing here starts the worker.
	data, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range required.FindAllStringSubmatch(string(data), -1) {
		s.env = append(s.env, m[1]+"=unused")
	}
	s.run("", "docker", "compose", "--profile", "setup", "down", "-v", "--remove-orphans")
	t.Cleanup(func() { s.run("", "docker", "compose", "--profile", "setup", "down", "-v", "--remove-orphans") })

	// Before init doctor says what to run.
	s.wantCheck(1, "run: just secrets-init")

	// Init once; a second run refuses.
	code, out := s.script("", "init")
	if code != 0 || !strings.Contains(out, "root token is revoked") {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	m := regexp.MustCompile(`operator password: +(\S+)`).FindStringSubmatch(out)
	if m == nil || !regexp.MustCompile(`recovery key: +\S+`).MatchString(out) {
		t.Fatalf("init printed no recovery key or operator password: %s", out)
	}
	s.password = m[1]
	s.env = append(s.env, "SATAT_OPERATOR_PASSWORD="+s.password)
	if code, out := s.script("", "init"); code == 0 || !strings.Contains(out, "already initialised") {
		t.Fatalf("second init: exit %d: %s", code, out)
	}
	s.wantCheck(1, "run: just secrets-load")

	// Load refuses missing, empty, unknown and malformed input, names the problem, never prints a
	// value, and writes nothing.
	all := []string{"github-read-token", "github-push-token", "claude-oauth-token"}
	for name, tc := range map[string]struct{ in, want string }{
		"missing":   {envFile(placeholders, all[:2]...), "claude-oauth-token: missing"},
		"empty":     {envFile(placeholders, all[:2]...) + "claude-oauth-token=\n", "claude-oauth-token: empty"},
		"unknown":   {envFile(placeholders, all...) + "not-granted=placeholder-unknown-value\n", "not-granted: unknown"},
		"malformed": {envFile(placeholders, all...) + "placeholder-bare-value\n", "line 4: not NAME=value"},
	} {
		code, out := s.script(tc.in, "load")
		if code == 0 || !strings.Contains(out, tc.want) || !strings.Contains(out, "nothing loaded") {
			t.Errorf("load %s: exit %d %q, want a refusal containing %q", name, code, out, tc.want)
		}
		if strings.Contains(out, "placeholder-") {
			t.Errorf("load %s printed a value: %q", name, out)
		}
	}
	s.wantCheck(1, "missing entries: claude-oauth-token github-push-token github-read-token")

	if code, out := s.script("# from secrets.example.env\n\n"+envFile(placeholders, all...), "load"); code != 0 {
		t.Fatalf("load: exit %d: %s", code, out)
	}
	s.wantCheck(0, "every granted entry loaded")

	// The worker's credential reads its entries and nothing else.
	s.wantWorkerReads()
	for name, cmd := range map[string]string{
		"read an ungranted entry":        "bao kv get -mount=satat not-granted",
		"list the store":                 "bao kv list -mount=satat",
		"write a value":                  "echo x | bao kv put -mount=satat github-read-token value=-",
		"read its policy":                "bao policy read worker",
		"edit its policy":                `echo 'path "satat/*" { capabilities = ["read"] }' | bao policy write worker -`,
		"create a token":                 "bao token create -policy=worker",
		"issue itself a secret_id":       "bao write -f auth/approle/role/worker/secret-id",
		"read doctor's role_id":          "bao read auth/approle/role/doctor/role-id",
		"create a user":                  "bao write auth/userpass/users/x password=x",
		"enable an auth method":          "bao auth enable -path=x userpass",
		"read the operator's user entry": "bao read auth/userpass/users/operator",
	} {
		if code, out := s.at(workerAddr, cmd); code == 0 || !strings.Contains(out, "permission denied") {
			t.Errorf("worker can %s: exit %d %q", name, code, out)
		}
	}

	// The worker's credential and tokens work only from the worker's address.
	if code, out := s.at(otherAddr, "true"); code != 9 || !strings.Contains(out, "unauthorized by CIDR") {
		t.Errorf("worker credential from another address: exit %d %q", code, out)
	}
	code, out = s.at(workerAddr, "echo $BAO_TOKEN")
	if code != 0 {
		t.Fatalf("worker login: exit %d %q", code, out)
	}
	stolen := strings.TrimSpace(out)
	code, out = s.run("", "docker", "run", "--rm", "--network", storeProject+"_secrets", "--ip", otherAddr,
		"-e", "BAO_ADDR=http://openbao:8200", "-e", "BAO_TOKEN="+stolen, "--entrypoint", "bao",
		deploy.OpenBaoImage, "kv", "get", "-mount=satat", "github-read-token")
	if code == 0 || !strings.Contains(out, "permission denied") {
		t.Errorf("worker token from another address: exit %d %q", code, out)
	}

	// The operator writes from loopback but cannot read a value back, and its login is useless
	// from anywhere else.
	if code, out := s.asOperator("kv get -mount=satat github-read-token"); code == 0 || !strings.Contains(out, "permission denied") {
		t.Errorf("operator reads a value: exit %d %q", code, out)
	}
	code, out = s.run(s.password, "docker", "run", "-i", "--rm", "--network", storeProject+"_secrets", "--ip", otherAddr,
		"-e", "BAO_ADDR=http://openbao:8200", "--entrypoint", "sh", deploy.OpenBaoImage, "-c",
		`export BAO_TOKEN="$(bao write -field=token auth/userpass/login/operator password=-)"; bao policy list`)
	if code == 0 || !strings.Contains(out, "permission denied") {
		t.Errorf("operator token from the secrets network: exit %d %q", code, out)
	}

	// A grant is a policy change by the operator: the new name becomes required, then readable.
	if code, out := s.script("", "grant", "worker", "extra-token"); code != 0 || !strings.Contains(out, "now reads extra-token") {
		t.Fatalf("grant: exit %d %q", code, out)
	}
	s.wantCheck(1, "missing entries: extra-token")
	if code, out := s.script("extra-token=placeholder-extra-not-a-token\n", "load"); code != 0 {
		t.Fatalf("load one entry: exit %d %q", code, out)
	}
	s.wantCheck(0, "every granted entry loaded")
	if code, out := s.at(workerAddr, "bao kv get -mount=satat -field=value extra-token"); code != 0 || strings.TrimSpace(out) != "placeholder-extra-not-a-token" {
		t.Errorf("worker reads a granted entry: exit %d %q", code, out)
	}

	// Encrypted at rest: no value in the Raft files.
	code, out = s.run("", "docker", "run", "--rm", "-v", storeProject+"_bao-data:/data:ro", "--entrypoint", "grep",
		deploy.OpenBaoImage, "-rlF", "placeholder-", "/data")
	if code != 1 {
		t.Errorf("a value is in the Raft data in plain text: exit %d %q", code, out)
	}

	// down and up: the store unseals itself, with nothing from outside.
	s.compose("down")
	s.compose("up", "-d", "--no-deps", "openbao")
	deadline := time.Now().Add(time.Minute)
	for {
		if code, _ := s.script("", "check"); code == 0 {
			break
		}
		if time.Now().After(deadline) {
			code, out := s.script("", "check")
			t.Fatalf("store not ready a minute after up: exit %d %q", code, out)
		}
		time.Sleep(time.Second)
	}
	s.wantWorkerReads()
}
