package compose

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
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
	movedAddr    = "10.157.1.12"
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
	password string // SATAT_OPERATOR_PASSWORD for the script; empty for none
	policies string
	override string // a compose file that sets the test subnet and the worker's address
}

// run runs a command in the repo root with the test project's environment and returns its exit
// code and combined output. Setsid: no controlling terminal, so the script cannot prompt.
func (s *store) run(stdin string, name string, args ...string) (int, string) {
	s.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = s.root
	cmd.Env = append(s.env, "SATAT_OPERATOR_PASSWORD="+s.password)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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

// ensure runs ensure with or without the operator password and wants the exit code and output.
func (s *store) ensure(withPassword bool, code int, contains string) string {
	s.t.Helper()
	password := s.password
	if !withPassword {
		s.password = ""
	}
	got, out := s.script("", "ensure")
	s.password = password
	if got != code || !strings.Contains(out, contains) {
		s.t.Fatalf("ensure: exit %d %q, want exit %d containing %q", got, out, code, contains)
	}
	return out
}

// grant writes a component's policy file granting read on names.
func (s *store) grant(component string, names ...string) {
	s.t.Helper()
	data := "# test policy\n"
	for _, n := range names {
		data += `path "satat/data/` + n + `" { capabilities = ["read"] }` + "\n"
	}
	s.writePolicy(component, data)
}

// moveWorker sets the worker's address on the secrets network in compose.yaml (the override).
func (s *store) moveWorker(addr string) {
	s.t.Helper()
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
		"        ipv4_address: " + addr,
	}, "\n")
	if err := os.WriteFile(s.override, []byte(yaml+"\n"), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *store) writePolicy(component, data string) {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.policies, component+".hcl"), []byte(data), 0o600); err != nil {
		s.t.Fatal(err)
	}
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

// wantWorkerReads wants the worker's credential, from addr, to read every placeholder.
func (s *store) wantWorkerReads(addr string) {
	s.t.Helper()
	for name, value := range placeholders {
		code, out := s.at(addr, "bao kv get -mount=satat -field=value "+name)
		if code != 0 || strings.TrimSpace(out) != value {
			s.t.Fatalf("worker reads %s: exit %d %q", name, code, out)
		}
	}
}

// wantStateHasNoSecretID wants the OpenTofu state to hold the worker's role_id (the right file) but
// not its secret_id.
func (s *store) wantStateHasNoSecretID() {
	s.t.Helper()
	for file, want := range map[string]int{"role_id": 0, "secret_id": 1} {
		code, out := s.run("", "docker", "run", "--rm", "--user", "0:0", "-v", storeProject+"_cred-worker:/c:ro",
			"-v", storeProject+"_bao-seal:/s:ro", "--entrypoint", "sh", deploy.OpenBaoImage, "-c",
			`test -s /c/`+file+` || exit 2; grep -qF "$(cat /c/`+file+`)" /s/tofu/terraform.tfstate`)
		if code != want {
			s.t.Errorf("grep the state for the worker's %s: exit %d %q, want %d", file, code, out, want)
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
	s := &store{t: t, root: root, policies: t.TempDir(), override: override, env: append(os.Environ(),
		"COMPOSE_PROJECT_NAME="+storeProject,
		"COMPOSE_FILE="+filepath.Join(root, "compose.yaml")+":"+override)}
	s.moveWorker(workerAddr)
	s.env = append(s.env, "SATAT_POLICIES_DIR="+s.policies)
	all := []string{"github-read-token", "github-push-token", "claude-oauth-token"}
	s.grant("worker", all...)
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

	// Before the first run doctor says what to run.
	s.wantCheck(3, "not set up (just run sets it up)")

	// The first ensure sets up the store with no password (self-init), prints the operator password
	// once and deletes its file, applies the policies, and with no terminal to ask on, fails naming
	// the entries with no value.
	out := s.ensure(false, 1, "missing entries: claude-oauth-token github-push-token github-read-token (run: just secrets)")
	m := regexp.MustCompile(`operator password: +(\S+)`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, "shown only this once") {
		t.Fatalf("ensure printed no operator password: %s", out)
	}
	s.password = m[1]
	if code, out := s.run("", "docker", "run", "--rm", "-v", storeProject+"_bao-seal:/v:ro", "--entrypoint", "ls",
		deploy.OpenBaoImage, "/v"); code != 0 || strings.Contains(out, "operator-password") {
		t.Fatalf("bao-seal after the first ensure: exit %d %q", code, out)
	}
	s.wantCheck(1, "missing entries: claude-oauth-token github-push-token github-read-token (run: just secrets)")
	if out := s.ensure(false, 1, "missing entries"); strings.Contains(out, "operator password") {
		t.Fatalf("second ensure printed the keys again: %s", out)
	}

	// just secrets refuses empty, unknown, malformed and repeated input, names the problem, never
	// prints a value, and writes nothing.
	for name, tc := range map[string]struct{ in, want string }{
		"empty":     {envFile(placeholders, all[:2]...) + "claude-oauth-token=\n", "claude-oauth-token: empty"},
		"unknown":   {envFile(placeholders, all...) + "not-granted=placeholder-unknown-value\n", "not-granted: unknown"},
		"malformed": {envFile(placeholders, all...) + "placeholder-bare-value\n", "line 4: not NAME=value"},
		"twice":     {envFile(placeholders, all...) + envFile(placeholders, all[0]), all[0] + ": given twice"},
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

	// A partial load writes only what it is given.
	if code, out := s.script(envFile(placeholders, all[0]), "load"); code != 0 {
		t.Fatalf("partial load: exit %d: %s", code, out)
	}
	s.wantCheck(1, "missing entries: claude-oauth-token github-push-token (run")
	if code, out := s.script("# from secrets.example.env\n\n"+envFile(placeholders, all[1:]...), "load"); code != 0 {
		t.Fatalf("load: exit %d: %s", code, out)
	}
	s.wantCheck(0, "every granted entry loaded")

	// Nothing to change: ensure needs no password and changes nothing.
	if out := s.ensure(false, 0, ""); out != "" {
		t.Fatalf("ensure with nothing to change printed %q", out)
	}

	// The worker's credential reads its entries and nothing else.
	s.wantWorkerReads(workerAddr)
	s.wantStateHasNoSecretID()
	for name, cmd := range map[string]string{
		"read an ungranted entry":        "bao kv get -mount=satat not-granted",
		"list the store":                 "bao kv list -mount=satat",
		"write a value":                  "echo x | bao kv put -mount=satat github-read-token value=-",
		"read its policy":                "bao policy read worker",
		"edit its policy":                `echo 'path "satat/*" { capabilities = ["read"] }' | bao policy write worker -`,
		"create a token":                 "bao token create -policy=worker",
		"issue itself a secret_id":       "bao write -f auth/approle/role/worker/secret-id",
		"read doctor's user entry":       "bao read auth/userpass/users/doctor",
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
	code, out := s.at(workerAddr, "echo $BAO_TOKEN")
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

	// A policy file that grants more than read on named entries is refused, and nothing changes.
	for name, line := range map[string]string{
		"list":     `path "satat/data/github-read-token" { capabilities = ["read", "list"] }`,
		"wildcard": `path "satat/data/*" { capabilities = ["read"] }`,
		"metadata": `path "satat/metadata/github-read-token" { capabilities = ["read"] }`,
	} {
		s.writePolicy("worker", line+"\n")
		s.ensure(true, 1, "grants more than read on named entries")
		code, out := s.script("", "check")
		if code != 1 || !strings.Contains(out, "grants more than read on named entries") {
			t.Errorf("check with a %s policy: exit %d %q", name, code, out)
		}
	}
	s.grant("worker", all...)
	s.wantCheck(0, "every granted entry loaded")

	// A new name in a policy file: doctor sees the difference, ensure needs the operator to apply
	// it, then asks for the value, and a partial load supplies it.
	s.grant("worker", append(all, "extra-token")...)
	s.wantCheck(1, "grants differ")
	s.ensure(false, 1, "set SATAT_OPERATOR_PASSWORD")
	s.ensure(true, 1, "missing entries: extra-token (run: just secrets)")
	if code, out := s.script("extra-token=placeholder-extra-not-a-token\n", "load"); code != 0 {
		t.Fatalf("load one entry: exit %d %q", code, out)
	}
	s.wantCheck(0, "every granted entry loaded")
	if code, out := s.at(workerAddr, "bao kv get -mount=satat -field=value extra-token"); code != 0 || strings.TrimSpace(out) != "placeholder-extra-not-a-token" {
		t.Errorf("worker reads a granted entry: exit %d %q", code, out)
	}

	// A name removed from the file loses its grant.
	s.grant("worker", all...)
	s.wantCheck(1, "grants differ")
	s.ensure(true, 0, "applied")
	if code, out := s.at(workerAddr, "bao kv get -mount=satat extra-token"); code == 0 || !strings.Contains(out, "permission denied") {
		t.Errorf("worker reads a removed grant: exit %d %q", code, out)
	}
	s.wantWorkerReads(workerAddr)

	// A deleted credential file: the next ensure issues a new secret_id, which needs the operator.
	if code, out := s.run("", "docker", "run", "--rm", "--user", "0:0", "-v", storeProject+"_cred-worker:/c",
		"--entrypoint", "rm", deploy.OpenBaoImage, "/c/secret_id"); code != 0 {
		t.Fatalf("delete the worker's secret_id: exit %d %q", code, out)
	}
	if code, out := s.at(workerAddr, "true"); code != 9 {
		t.Errorf("worker login with no secret_id: exit %d %q", code, out)
	}
	s.ensure(false, 1, "set SATAT_OPERATOR_PASSWORD")
	s.ensure(true, 0, "wrote the worker credential")
	s.wantWorkerReads(workerAddr)
	s.wantStateHasNoSecretID()

	// A new address for the worker: doctor sees the difference, and ensure updates the role in place
	// (same role_id). The credential then works from the new address only.
	_, roleID := s.asOperator("read -field=role_id auth/approle/role/worker/role-id")
	s.moveWorker(movedAddr)
	s.wantCheck(1, "grants differ")
	s.ensure(true, 0, "applied")
	if _, out := s.asOperator("read -field=role_id auth/approle/role/worker/role-id"); out != roleID {
		t.Errorf("role_id after an address change: %q, want %q", out, roleID)
	}
	s.wantWorkerReads(movedAddr)
	if code, out := s.at(workerAddr, "true"); code != 9 || !strings.Contains(out, "unauthorized by CIDR") {
		t.Errorf("worker credential from its old address: exit %d %q", code, out)
	}
	s.moveWorker(workerAddr)
	s.ensure(true, 0, "applied")
	s.wantWorkerReads(workerAddr)

	// A component whose policy file is removed loses its policy, its role and its credential.
	if err := os.Remove(filepath.Join(s.policies, "worker.hcl")); err != nil {
		t.Fatal(err)
	}
	s.wantCheck(1, "grants differ")
	s.ensure(true, 0, "applied")
	for _, path := range []string{"policy read worker", "read auth/approle/role/worker"} {
		if code, out := s.asOperator(path); code == 0 {
			t.Errorf("operator %s after the removal: exit 0 %q", path, out)
		}
	}
	if code, out := s.at(workerAddr, "true"); code != 9 {
		t.Errorf("worker login after the removal: exit %d %q", code, out)
	}
	s.wantCheck(0, "every granted entry loaded")
	// The file back: a new role, so a new role_id, and a new secret_id (the old one went with the role).
	s.grant("worker", all...)
	if out := s.ensure(true, 0, "applied"); !strings.Contains(out, "wrote the worker credential") {
		t.Errorf("ensure after a new role wrote no credential: %q", out)
	}
	s.wantWorkerReads(workerAddr)
	s.wantCheck(0, "every granted entry loaded")

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
	s.wantWorkerReads(workerAddr)
	s.ensure(false, 0, "")
}
