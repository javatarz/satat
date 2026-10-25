package claude

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// laneImage is the Claude lane image to test: SATAT_CLAUDE_IMAGE, else the local build from
// scripts/build-claude-image.sh. The tests skip when it is absent and under -short.
func laneImage(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker and the lane image")
	}
	img := os.Getenv("SATAT_CLAUDE_IMAGE")
	if img == "" {
		img = "satat-claude:dev"
	}
	if out, err := exec.Command("docker", "image", "inspect", img).CombinedOutput(); err != nil {
		t.Skipf("lane image %s not available (scripts/build-claude-image.sh): %s", img, strings.TrimSpace(string(out)))
	}
	return img
}

// asAgent runs a shell script in the lane image the way the launcher does: the image's non-root
// user, read-only root filesystem, a writable workspace tmpfs, no capabilities.
func asAgent(t *testing.T, img, script string) (int, string) {
	t.Helper()
	cmd := exec.Command("docker", "run", "--rm", "--read-only",
		"--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--tmpfs", "/workspace:rw,noexec,nosuid,uid=10001,gid=10001",
		"--entrypoint", "sh", img, "-c", script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return 0, out.String()
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out.String()
	}
	t.Fatalf("docker run: %v", err)
	return -1, ""
}

func TestImageManagedFilesAreRootOwnedAndImmutable(t *testing.T) {
	img := laneImage(t)
	files := []string{"/etc/claude-code/managed-settings.json", "/usr/local/lib/satat/hooks/guard-git-hooks.js"}

	// Ownership and mode: root-owned, no group or other write bit, and the agent is not root.
	code, out := asAgent(t, img, `test "$(id -u)" != 0 || exit 9
for f in `+strings.Join(files, " ")+`; do
  stat -c '%u %a %n' "$f" || exit 1
done
stat -c '%u %a %n' /etc/claude-code /usr/local/lib/satat /usr/local/lib/satat/hooks`)
	if code != 0 {
		t.Fatalf("stat as agent: exit %d: %s", code, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != "0" || strings.ContainsAny(f[1][len(f[1])-2:], "2367") {
			t.Errorf("not root-owned and unwritable by group/other: %q", line)
		}
	}

	// Every way an agent's shell can modify them must fail, and leave the file as it was.
	for _, f := range files {
		for name, attack := range map[string]string{
			"redirect":   `echo '{}' > ` + f,
			"append":     `echo x >> ` + f,
			"tee":        `echo x | tee ` + f,
			"sed -i":     `sed -i 's/deny/allow/' ` + f,
			"rm":         `rm -f ` + f,
			"mv":         `echo x > /workspace/x && mv /workspace/x ` + f,
			"chmod":      `chmod 0666 ` + f,
			"python":     `python3 -c "open('` + f + `','w').write('x')"`,
			"node":       `node -e "require('fs').writeFileSync('` + f + `','x')"`,
			"remount rw": `mount -o remount,rw / 2>&1`,
		} {
			t.Run(name+" "+f, func(t *testing.T) {
				script := attack + ` && echo MODIFIED; sum=$(sha256sum ` + f + `); echo "$sum"`
				_, out := asAgent(t, img, script)
				if strings.Contains(out, "MODIFIED") {
					t.Fatalf("agent modified %s with %s: %s", f, name, out)
				}
				if !strings.Contains(out, f) {
					t.Fatalf("file missing or unreadable after %s: %s", name, out)
				}
			})
		}
	}
}

// The settings file the image ships must be the one in the repo, and the hook command it names
// must run as the agent user and block a hook-skipping Bash call.
func TestImageHookRunsAsAgent(t *testing.T) {
	img := laneImage(t)
	s := loadSettings(t)
	hook := s.Hooks.PreToolUse[0].Hooks[0].Command

	script := `cmp /etc/claude-code/managed-settings.json - < /dev/null >/dev/null 2>&1; ` +
		`printf '%s' "$CALL" | ` + hook + `; echo "exit=$?"`
	for cmd, want := range map[string]string{
		"git -c core.hooksPath=/dev/null commit -m x": "exit=2",
		"HUSKY=0 git commit -m x":                     "exit=2",
		"SKIP=pre-commit git commit -m x":             "exit=2",
		"git commit -m 'Add thing'":                   "exit=0",
		"git push origin HEAD":                        "exit=0",
	} {
		t.Run(cmd, func(t *testing.T) {
			full := "CALL='" + strings.ReplaceAll(bashCall(cmd), "'", `'\''`) + "'; " + script
			code, out := asAgent(t, img, full)
			if code != 0 || !strings.Contains(out, want) {
				t.Fatalf("exit %d, want %q in output: %s", code, want, out)
			}
		})
	}

	// The shipped settings are the repo's settings.
	want, err := os.ReadFile("managed-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	_, got := asAgent(t, img, "cat /etc/claude-code/managed-settings.json")
	if got != string(want) {
		t.Errorf("image settings differ from images/claude/managed-settings.json")
	}
}
