package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const hookScript = "hooks/guard-git-hooks.js"

func bashCall(cmd string) string {
	b, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": cmd}})
	return string(b)
}

// runHook feeds a tool call to the hook and returns its exit code and stderr.
func runHook(t *testing.T, node, stdin string) (int, string) {
	t.Helper()
	cmd := exec.Command(node, hookScript)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return 0, stderr.String()
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), stderr.String()
	}
	t.Fatalf("run hook: %v", err)
	return -1, ""
}

func TestGuardBlocksHookSkipping(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	blocked := []string{
		// the forms named in #174
		"git -c core.hooksPath=/dev/null commit -m x",
		"git config core.hooksPath /dev/null",
		"git config --global core.hooksPath /dev/null",
		"HUSKY=0 git commit -m x",
		"SKIP=pre-commit git commit -m x",
		"SKIP=ruff,black git commit -m x",
		"export SKIP=all; git commit -m x",
		"env SKIP=x git commit -m x",
		"PRE_COMMIT_ALLOW_NO_CONFIG=1 git commit -m x",
		"git commit-tree HEAD^{tree} -p HEAD -m x",
		"git update-ref refs/heads/satat/run1 abc123",
		"git fast-import < dump",
		"echo 'exit 0' > .git/hooks/pre-commit",
		"chmod -x .git/hooks/pre-commit",
		"rm -rf .git/hooks",
		// flags
		"git commit --no-verify -m x",
		"git commit -m x --no-verify",
		"git push --no-verify",
		"git push origin HEAD --no-verify",
		"git commit -n -m x",
		"git commit -nm x",
		"git commit -anm x",
		"git commit -a -n",
		"git -C sub commit -n -m x",
		"git add -A && git commit -n -m x",
		"git add -A; git commit -n -m x",
		// variants
		"HUSKY_SKIP_HOOKS=1 git commit -m x",
		"LEFTHOOK=0 git commit -m x",
		"GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath GIT_CONFIG_VALUE_0=/dev/null git commit -m x",
		"GIT_CONFIG_PARAMETERS=\"'core.hooksPath=/x'\" git commit -m x",
		"git -c core.hookspath=/dev/null commit -m x",
		"sh -c 'git commit --no-verify -m x'",
		"git config core.hooksPath \"$(mktemp -d)\"",
		"echo '[core]\nhooksPath=/dev/null' >> .git/config",
		"sed -i 's/x/y/' .git/config",
		// the policy and settings files
		"echo '{}' > .claude/settings.local.json",
		"echo '{}' >> /workspace/.claude/settings.json",
		"cat x | tee .claude/settings.local.json",
		"sed -i 's/deny/allow/' /etc/claude-code/managed-settings.json",
		"cp evil.js /usr/local/lib/satat/hooks/guard-git-hooks.js",
		"rm /etc/claude-code/managed-settings.json",
	}
	for _, cmd := range blocked {
		t.Run("block/"+cmd, func(t *testing.T) {
			code, stderr := runHook(t, node, bashCall(cmd))
			if code != 2 {
				t.Fatalf("exit %d, want 2 (blocked); stderr %q", code, stderr)
			}
			if !strings.Contains(stderr, "Blocked:") {
				t.Errorf("stderr %q has no reason", stderr)
			}
		})
	}

	allowed := []string{
		"git commit -m 'Add feature #12'",
		"git commit -am 'Fix bug'",
		"git commit --amend --no-edit",
		"git commit --amend -m x",
		"git commit -m \"docs: explain the -n flag and why we never skip hooks\"",
		"git commit -s -S -m x",
		"git commit -m \"$(cat <<'EOF'\nAdd thing\n\nBody with -n and SKIP in prose\nEOF\n)\"",
		"git add -A && git commit -m x && git status",
		"git push origin HEAD",
		"git push -n origin HEAD",
		"git push --force-with-lease origin satat/run1",
		"git status --short",
		"git log --oneline -n 5",
		"git diff -n",
		"git config user.name Satat",
		"git config --get core.editor",
		"git checkout -b satat/run1",
		"git rebase -i HEAD~3",
		"git stash list",
		"ls -la .git",
		"cat .git/config",
		"echo hello > out.txt",
		"cat .claude/settings.json",
		"pre-commit run --all-files",
		"npm test -- -n",
		"go test ./...",
		"HUSKY=1 git commit -m x",
		"skip=1 make test",
	}
	for _, cmd := range allowed {
		t.Run("allow/"+cmd, func(t *testing.T) {
			if code, stderr := runHook(t, node, bashCall(cmd)); code != 0 {
				t.Fatalf("exit %d, want 0 (allowed); stderr %q", code, stderr)
			}
		})
	}
}

func TestGuardFailsClosed(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	for name, stdin := range map[string]string{
		"not json":           "{",
		"empty":              "",
		"bash without cmd":   `{"tool_name":"Bash","tool_input":{}}`,
		"command not string": `{"tool_name":"Bash","tool_input":{"command":["git"]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if code, _ := runHook(t, node, stdin); code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
		})
	}
	if code, _ := runHook(t, node, `{"tool_name":"Read","tool_input":{"file_path":"x"}}`); code != 0 {
		t.Errorf("non-Bash call blocked, exit %d", code)
	}
}

type managedSettings struct {
	ForceLoginMethod string   `json:"forceLoginMethod"`
	AllowedProviders []string `json:"allowedProviders"`
	Permissions      struct {
		Deny []string `json:"deny"`
	} `json:"permissions"`
	Hooks struct {
		PreToolUse []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"PreToolUse"`
	} `json:"hooks"`
}

func loadSettings(t *testing.T) managedSettings {
	t.Helper()
	b, err := os.ReadFile("managed-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var s managedSettings
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("managed-settings.json: %v", err)
	}
	return s
}

func TestManagedSettingsContent(t *testing.T) {
	s := loadSettings(t)
	// The #102 rules must survive: subscription login only.
	if s.ForceLoginMethod != "claudeai" || len(s.AllowedProviders) != 1 || s.AllowedProviders[0] != "anthropic" {
		t.Errorf("login rules lost: %+v", s)
	}
	deny := strings.Join(s.Permissions.Deny, "\n")
	for _, want := range []string{
		"Bash(git commit --no-verify*)", "Bash(git commit -n *)", "Bash(git push --no-verify*)",
		"Edit(**/.claude/settings*.json)", "Edit(/etc/claude-code/**)",
	} {
		if !strings.Contains(deny, want) {
			t.Errorf("deny rules lack %q", want)
		}
	}
	if len(s.Hooks.PreToolUse) != 1 || s.Hooks.PreToolUse[0].Matcher != "Bash" {
		t.Fatalf("want one PreToolUse hook on Bash, got %+v", s.Hooks.PreToolUse)
	}
}
