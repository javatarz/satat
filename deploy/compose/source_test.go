package compose

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// workerSource is the worker image's tag, so it must cover everything the build copies. The build
// copies what Dockerfile.dockerignore lets through; if that changes, workerSource must change with
// it, or an edited file would not rebuild the image.
func TestWorkerSourceMatchesDockerignore(t *testing.T) {
	var rules []string
	for _, line := range strings.Split(string(mustRead(t, "../../images/worker/Dockerfile.dockerignore")), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			rules = append(rules, line)
		}
	}
	want := []string{"*", "!go.mod", "!go.sum", "!**/*.go"}
	if !slices.Equal(rules, want) {
		t.Fatalf("Dockerfile.dockerignore rules = %q, want %q: update workerSource to hash what the build now copies", rules, want)
	}
}

func TestWorkerSourceChangesWithWhatTheBuildReads(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir)
	compose := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(compose, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := func() string {
		t.Helper()
		h, err := workerSource(dir, compose)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := hash()
	if again := hash(); again != base {
		t.Fatalf("hash changed with nothing changed: %s then %s", base, again)
	}
	// Not in the build context: no new tag, so no rebuild.
	for _, f := range []string{"README.md", "docs/scratch/e2e/.env", ".git/HEAD"} {
		write(t, filepath.Join(dir, f), "x")
		if h := hash(); h != base {
			t.Errorf("hash changed after writing %s, which the build does not read", f)
		}
	}
	// What the build reads: each change is a new tag.
	seen := map[string]string{base: "base"}
	for _, f := range []string{"main.go", "pkg/new.go", "go.sum", "go.mod", "images/worker/Dockerfile", "compose.yaml"} {
		write(t, filepath.Join(dir, f), "changed "+f)
		h := hash()
		if prev, ok := seen[h]; ok {
			t.Errorf("hash after changing %s is the same as after %s", f, prev)
		}
		seen[h] = f
	}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
