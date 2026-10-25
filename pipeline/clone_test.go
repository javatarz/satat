package pipeline

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const goodCommands = "version: 1\ncommands:\n  setup: just setup\n  test: go test ./...\n  lint: just lint\n"

// sourceRepo makes a repo on main whose commands file is body, and returns its path.
func sourceRepo(t *testing.T, body string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(dir, ".satat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, ".satat", "commands.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("README.md", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"add", "-A"},
		{"-c", "user.name=a", "-c", "user.email=a@b", "commit", "-q", "-m", "Initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func cloner(src, tmp string) GitCloner {
	return GitCloner{Token: "read-token-value", URL: func(string) string { return "file://" + src }, TempDir: tmp}
}

func TestCloneReadsTheBaseCommitsCommands(t *testing.T) {
	src := sourceRepo(t, goodCommands)
	tmp := t.TempDir()
	ck, err := cloner(src, tmp).Clone(context.Background(), "a/b", "main")
	if err != nil {
		t.Fatal(err)
	}
	if ck.Commands["setup"] != "just setup" || ck.Commands["test"] != "go test ./..." || ck.Commands["lint"] != "just lint" {
		t.Errorf("commands: %v", ck.Commands)
	}
	want, _ := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()
	if ck.BaseSHA != strings.TrimSpace(string(want)) {
		t.Errorf("base sha %q, want %q", ck.BaseSHA, want)
	}
	names := map[string]string{}
	tr := tar.NewReader(ck.Tar)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[h.Name] = h.Linkname
		// The credential must not be in the clone: not in the remote, not in the git config.
		if h.Name == ".git/config" {
			b, _ := io.ReadAll(tr)
			if strings.Contains(string(b), "read-token-value") || strings.Contains(string(b), "credential") {
				t.Errorf(".git/config holds the credential:\n%s", b)
			}
		}
	}
	for _, n := range []string{"README.md", ".satat/commands.yaml", ".git/HEAD"} {
		if _, ok := names[n]; !ok {
			t.Errorf("archive lacks %s", n)
		}
	}
	if names["link"] != "README.md" {
		t.Errorf("symlink not kept: %q", names["link"])
	}
	ck.Close()
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("clone left behind: %v", left)
	}
}

func TestCloneRefusesWhatItCannotUse(t *testing.T) {
	tmp := t.TempDir()
	for name, tc := range map[string]struct{ body, base, want string }{
		"no commands file": {"", "main", "is missing"},
		"wrong version":    {"version: 2\ncommands:\n  setup: a\n  test: b\n", "main", "version is 2"},
		"no test":          {"version: 1\ncommands:\n  setup: a\n", "main", "commands.test is required"},
		"no such branch":   {goodCommands, "nope", "clone a/b"},
		"base is a flag":   {goodCommands, "--orphan", "plain branch name"},
		"bool command":     {"version: 1\ncommands:\n  setup: a\n  test: true\n", "main", "commands.test must be a string"},
		"list command":     {"version: 1\ncommands:\n  setup: a\n  test: [b]\n", "main", "commands.test must be a string"},
		"hint is a number": {"version: 1\ncommands:\n  setup: a\n  test: b\n  n: 3\n", "main", "commands.n must be a string"},
		"too big":          {"version: 1\ncommands:\n  setup: a\n  test: b\n  x: " + strings.Repeat("y", 70<<10) + "\n", "main", "over the"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := cloner(sourceRepo(t, tc.body), tmp).Clone(context.Background(), "a/b", tc.base)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "read-token-value") {
				t.Error("the error carries the token")
			}
			if left, _ := os.ReadDir(tmp); len(left) != 0 {
				t.Errorf("clone left behind: %v", left)
			}
		})
	}
	if _, err := (GitCloner{}).Clone(context.Background(), "a/b", "main"); err == nil {
		t.Error("cloned without a token")
	}
}

// The credential helper answers for the configured host only.
func TestCredentialHelperAnswersOnlyForItsHost(t *testing.T) {
	src := sourceRepo(t, goodCommands)
	var helper string
	spy := filepath.Join(t.TempDir(), "git")
	// A git that records the -c credential.helper value and then runs the real one.
	real, _ := exec.LookPath("git")
	script := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in credential.helper=*) printf '%s' \"${a#credential.helper=}\" > " + spy + ".helper;; esac; done\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(spy, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := cloner(src, t.TempDir())
	c.Git = spy
	ck, err := c.Clone(context.Background(), "a/b", "main")
	if err != nil {
		t.Fatal(err)
	}
	ck.Close()
	b, err := os.ReadFile(spy + ".helper")
	if err != nil {
		t.Fatal(err)
	}
	helper = strings.TrimPrefix(string(b), "!")
	for host, want := range map[string]bool{"github.com": true, "evil.example": false} {
		cmd := exec.Command("sh", "-c", helper+" get")
		cmd.Env = []string{"SATAT_CLONE_TOKEN=tok"}
		cmd.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\n\n")
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(out), "password=tok"); got != want {
			t.Errorf("host %s: token offered = %v, want %v", host, got, want)
		}
	}
}
