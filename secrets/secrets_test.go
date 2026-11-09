package secrets

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openbao/openbao/api/v2"

	"github.com/javatarz/satat/deploy"
)

// The store as secrets-init leaves it, on an OpenBao dev server: the satat/ mount, AppRole, and a
// component policy that reads two named entries. Placeholder values only.
func TestRead(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker not available: %s", strings.TrimSpace(string(out)))
	}
	ctx := context.Background()
	addr := startStore(t)
	root := client(t, addr, "root")
	write := func(path string, data map[string]any) *api.Secret {
		t.Helper()
		s, err := root.Logical().Write(path, data)
		if err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return s
	}
	write("sys/mounts/"+Mount, map[string]any{"type": "kv", "options": map[string]any{"version": "2"}})
	write("sys/auth/approle", map[string]any{"type": "approle"})
	write("sys/policies/acl/worker", map[string]any{"policy": `
path "satat/data/read-token" { capabilities = ["read"] }
path "satat/data/not-loaded" { capabilities = ["read"] }
`})
	write("auth/approle/role/worker", map[string]any{"token_policies": "worker"})
	for name, value := range map[string]string{
		"read-token":  "placeholder-read-not-a-token",
		"other-token": "placeholder-other-not-a-token",
	} {
		if _, err := root.KVv2(Mount).Put(ctx, name, map[string]any{"value": value}); err != nil {
			t.Fatal(err)
		}
	}
	role, err := root.Logical().Read("auth/approle/role/worker/role-id")
	if err != nil {
		t.Fatal(err)
	}
	secret := write("auth/approle/role/worker/secret-id", nil)
	cred := t.TempDir()
	for file, v := range map[string]any{"role_id": role.Data["role_id"], "secret_id": secret.Data["secret_id"]} {
		if err := os.WriteFile(filepath.Join(cred, file), []byte(v.(string)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Read(ctx, addr, AppRole{Dir: cred}, []string{"read-token"})
	if err != nil {
		t.Fatal(err)
	}
	if got["read-token"] != "placeholder-read-not-a-token" || len(got) != 1 {
		t.Errorf("Read = %v", got)
	}

	for name, tc := range map[string]struct {
		entries []string
		want    string
	}{
		"denied":     {[]string{"read-token", "other-token"}, "denies entry other-token"},
		"not loaded": {[]string{"not-loaded"}, "entry not-loaded is not in the secret store"},
	} {
		_, err := Read(ctx, addr, AppRole{Dir: cred}, tc.entries)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "placeholder-") {
			t.Errorf("%s: the error holds a value: %v", name, err)
		}
	}

	if err := os.WriteFile(filepath.Join(cred, "secret_id"), []byte("placeholder-wrong-secret-id"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(ctx, addr, AppRole{Dir: cred}, []string{"read-token"}); err == nil || !strings.Contains(err.Error(), "logging in to the secret store") {
		t.Errorf("wrong secret_id: err = %v", err)
	}
	if _, err := Read(ctx, addr, AppRole{Dir: t.TempDir()}, []string{"read-token"}); err == nil || !strings.Contains(err.Error(), "role_id") {
		t.Errorf("no credential: err = %v", err)
	}
}

// startStore starts an OpenBao dev server on a loopback port and returns its address.
func startStore(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("docker", "run", "-d", "--rm", "--label", "satat.test=secrets",
		"-p", "127.0.0.1::8200", deploy.OpenBaoImage,
		"server", "-dev", "-dev-root-token-id=root", "-dev-listen-address=0.0.0.0:8200").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	out, err = exec.Command("docker", "port", id, "8200/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	addr := "http://" + strings.TrimSpace(strings.Split(string(out), "\n")[0])
	c := client(t, addr, "root")
	deadline := time.Now().Add(30 * time.Second)
	for {
		if h, err := c.Sys().Health(); err == nil && h.Initialized && !h.Sealed {
			return addr
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", id).CombinedOutput()
			t.Fatalf("OpenBao dev server not ready after 30s: %s", logs)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func client(t *testing.T, addr, token string) *api.Client {
	t.Helper()
	cfg := api.DefaultConfig()
	cfg.Address = addr
	c, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.SetToken(token)
	return c
}
