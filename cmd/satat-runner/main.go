// Command satat-runner is the entrypoint of the Claude lane image.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/javatarz/satat/runner"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	cfg := runner.Config{
		Run:       os.Getenv("SATAT_RUN"),
		InputPath: os.Getenv("SATAT_INPUT"),
		OutDir:    os.Getenv("SATAT_OUTPUT"),
		WorkDir:   "/workspace",
		// The worker cloned the repo into the input volume before this container started.
		Checkout: filepath.Join(filepath.Dir(os.Getenv("SATAT_INPUT")), "repo"),
		// Absolute paths: PATH has the mise shims first (images/claude/Dockerfile). The claude
		// npm package installs a native binary (bin/claude.exe), so a repo-pinned node does not
		// change how claude itself runs.
		Claude: "/usr/local/bin/claude",
		Mise:   "/usr/local/bin/mise",
		Git:    "/usr/bin/git",
		Env:    os.Environ(),
		Log:    os.Stderr,
	}
	if err := runner.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "satat-runner:", err)
		os.Exit(1)
	}
}
