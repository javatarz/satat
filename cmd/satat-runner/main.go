// Command satat-runner is the entrypoint of the Claude lane image.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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
		Claude:    "claude",
		Git:       "git",
		Env:       os.Environ(),
		Log:       os.Stderr,
	}
	if err := runner.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "satat-runner:", err)
		os.Exit(1)
	}
}
