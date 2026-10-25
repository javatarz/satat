package pipeline

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/javatarz/satat/runner"
)

// CommandsFile is the repo's commands file, read from the base commit.
const CommandsFile = ".satat/commands.yaml"

// Checkout is a clone of the repo at the base branch, made by the worker before the step starts.
type Checkout struct {
	// Tar is the clone, .git included, as a tar archive.
	Tar io.Reader
	// BaseSHA is the commit the clone is at.
	BaseSHA string
	// Commands are the entries of .satat/commands.yaml at BaseSHA.
	Commands map[string]string

	closeFn func()
}

// Close releases the clone. Call it when the step is done with Tar.
func (c *Checkout) Close() {
	if c.closeFn != nil {
		c.closeFn()
	}
}

// Cloner clones a repo's base branch for a step. GitCloner is the real one.
type Cloner interface {
	Clone(ctx context.Context, repo, base string) (*Checkout, error)
}

// GitCloner clones with the read token, which only the worker process holds: git gets it through a
// credential helper reading the environment of that one git process, so it is in no file, no
// remote URL and no step. It also reads the commands file from the base commit, so what the
// harness uses is not what the agent later edits.
type GitCloner struct {
	Token string
	// Git is the git binary. Defaults to "git".
	Git string
	// URL builds the clone URL. Defaults to https://github.com/<repo>.git.
	URL func(repo string) string
	// TempDir is where clones are made. Defaults to os.TempDir().
	TempDir string
	// Depth is the clone depth. Defaults to 50.
	Depth int
	// Host is the only host the token is offered to. Defaults to github.com. A clone that is
	// redirected elsewhere, or a submodule-style request to another host, gets no credential.
	Host string
}

const (
	// maxCommandsBytes bounds .satat/commands.yaml; a real one is a few hundred bytes.
	maxCommandsBytes = 64 << 10
	// maxCheckoutBytes bounds the archive handed to the launcher. The worker's scratch space is
	// smaller than this, so it is a backstop.
	maxCheckoutBytes = 1 << 30
)

const cloneTokenVar = "SATAT_CLONE_TOKEN"

// Clone implements Cloner.
func (g GitCloner) Clone(ctx context.Context, repo, base string) (*Checkout, error) {
	if err := runner.CheckRepo(repo); err != nil {
		return nil, err
	}
	if err := runner.CheckBase(base); err != nil {
		return nil, err
	}
	if g.Token == "" {
		return nil, errors.New("clone: no read token")
	}
	url := "https://github.com/" + repo + ".git"
	if g.URL != nil {
		url = g.URL(repo)
	}
	depth := g.Depth
	if depth <= 0 {
		depth = 50
	}
	tmp, err := os.MkdirTemp(g.TempDir, "satat-clone-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(tmp)
		}
	}()
	dir := filepath.Join(tmp, "repo")
	host := g.Host
	if host == "" {
		host = "github.com"
	}
	// git sends the request's host on stdin; answer only for the configured one.
	helper := `!f() { test "$1" = get || exit 0; ok=; while read -r l; do test "$l" = "host=` + host + `" && ok=1; done; ` +
		`test -n "$ok" || exit 0; echo username=x-access-token; echo "password=$` + cloneTokenVar + `"; }; f`
	if _, err := g.git(ctx, tmp, "-c", "credential.helper="+helper, "clone", "--quiet", "--depth", fmt.Sprint(depth), "--branch", base, "--single-branch", "--", url, dir); err != nil {
		return nil, fmt.Errorf("clone %s: %w", repo, err)
	}
	sha, err := g.git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	size, err := g.git(ctx, dir, "cat-file", "-s", "HEAD:"+CommandsFile)
	if err != nil {
		return nil, fmt.Errorf("%s is missing on %s: %w", CommandsFile, base, err)
	}
	if n, _ := strconv.Atoi(size); n > maxCommandsBytes {
		return nil, fmt.Errorf("%s on %s is %s bytes, over the %d limit", CommandsFile, base, size, maxCommandsBytes)
	}
	raw, err := g.git(ctx, dir, "show", "HEAD:"+CommandsFile)
	if err != nil {
		return nil, fmt.Errorf("%s is missing on %s: %w", CommandsFile, base, err)
	}
	cmds, err := ParseCommands([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("%s on %s: %w", CommandsFile, base, err)
	}

	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		pw.CloseWithError(tarDir(pw, dir))
	}()
	keep = true
	return &Checkout{Tar: pr, BaseSHA: sha, Commands: cmds, closeFn: func() {
		pr.Close() // unblocks the writer if the reader gave up early
		<-done
		os.RemoveAll(tmp)
	}}, nil
}

// git runs git with an environment of its own: no user or system config, no prompts, and the read
// token under a name only the credential helper reads.
func (g GitCloner) git(ctx context.Context, dir string, args ...string) (string, error) {
	bin := g.Git
	if bin == "" {
		bin = "git"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		cloneTokenVar + "=" + g.Token,
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.ReplaceAll(stderr.String(), g.Token, "***")
		return "", fmt.Errorf("git: %w: %s", err, strings.TrimSpace(msg))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// tarDir writes dir to w as a tar archive of directories, regular files and symlinks.
func tarDir(w io.Writer, dir string) error {
	cw := &limitWriter{w: w, left: maxCheckoutBytes}
	tw := tar.NewWriter(cw)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		var link string
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		case !info.Mode().IsRegular() && !info.IsDir():
			return nil
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			h.Name += "/"
		}
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	return errors.Join(err, tw.Close())
}

// limitWriter fails once more than left bytes are written.
type limitWriter struct {
	w    io.Writer
	left int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, fmt.Errorf("checkout is over %d bytes", int64(maxCheckoutBytes))
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

// ParseCommands reads .satat/commands.yaml: version 1 and a commands map with setup and test.
// Any other key is a hint. Whether a repo is ready to start is the readiness check's job (#175);
// this only refuses a file the step could not use.
func ParseCommands(b []byte) (map[string]string, error) {
	if len(b) > maxCommandsBytes {
		return nil, fmt.Errorf("file is over %d bytes", maxCommandsBytes)
	}
	var f struct {
		Version  int                  `yaml:"version"`
		Commands map[string]yaml.Node `yaml:"commands"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("version is %d, want 1", f.Version)
	}
	// Every value is a string. yaml would coerce `test: true` to "true"; a command must be written
	// as a string so what the agent is told is what the author wrote.
	cmds := make(map[string]string, len(f.Commands))
	for k, n := range f.Commands {
		if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" {
			return nil, fmt.Errorf("commands.%s must be a string", k)
		}
		cmds[k] = n.Value
	}
	for _, k := range []string{"setup", "test"} {
		if strings.TrimSpace(cmds[k]) == "" {
			return nil, fmt.Errorf("commands.%s is required", k)
		}
	}
	return cmds, nil
}
