// Command satat-step runs one issue through one lane by hand. It is the prototype's stand-in
// for the worker until the Hatchet workflow exists (slice 5): it fetches the issue with gh,
// launches the step through the Docker launcher and writes what the step left behind.
//
//	satat-step --repo owner/name --issue 12 [--base main] [--lane claude] [--push]
//
// The lane's pass_env names must be set in the shell, for example CLAUDE_CODE_OAUTH_TOKEN. Their
// values are passed to the container by name and never printed. SATAT_GITHUB_TOKEN (read-only)
// must be set too: this process clones the repo before the step starts and keeps the token to
// itself.
// --push also needs SATAT_PUSH_TOKEN, which only this process holds and never reaches a container.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/moby/moby/client"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher/docker"
	"github.com/javatarz/satat/pipeline"
	"github.com/javatarz/satat/pusher"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "satat-step:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		repo    = flag.String("repo", "", "owner/name of the repo")
		issue   = flag.Int("issue", 0, "issue number")
		base    = flag.String("base", "main", "branch to start from")
		lane    = flag.String("lane", "claude", "lane from satat.yaml")
		cfgPath = flag.String("config", "satat.yaml", "path to satat.yaml")
		out     = flag.String("out", "", "directory for the step's output (default docs/scratch/runs/<run>)")
		runID   = flag.String("run", fmt.Sprintf("r%d", time.Now().Unix()), "run id")
		push    = flag.Bool("push", false, "push the branch and open a draft PR (needs SATAT_PUSH_TOKEN)")
		quiet   = flag.Bool("quiet", false, "do not print the step's stdout and stderr")
	)
	flag.Parse()
	if *repo == "" || *issue <= 0 {
		return fmt.Errorf("--repo and --issue are required")
	}
	if *out == "" {
		*out = filepath.Join("docs", "scratch", "runs", *runID)
	}
	var p pipeline.Publisher
	if *push {
		tok := os.Getenv("SATAT_PUSH_TOKEN")
		if tok == "" {
			return fmt.Errorf("--push needs SATAT_PUSH_TOKEN (contents and pull requests: write on the one repo)")
		}
		p = &pusher.Pusher{Token: tok}
	}

	readToken := os.Getenv("SATAT_GITHUB_TOKEN")
	if readToken == "" {
		return fmt.Errorf("SATAT_GITHUB_TOKEN is not set: this process clones the repo before the step starts")
	}

	iss, err := pipeline.FetchIssue(*repo, *issue)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	api, err := client.New(client.FromEnv)
	if err != nil {
		return err
	}
	defer api.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	l, err := docker.New(api, cfg.Lanes, docker.WithLogger(log))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := l.Setup(ctx); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "run %s: repo %s issue #%d on lane %s (Ctrl-C stops the step)\n", *runID, *repo, *issue, *lane)
	start := time.Now()
	rep, err := pipeline.Run(ctx, pipeline.Options{Launcher: l, Pusher: p, Cloner: pipeline.GitCloner{Token: readToken}, Run: *runID, Lane: *lane, Repo: *repo, Base: *base, Issue: iss})
	if err != nil {
		return err
	}
	names, err := pipeline.Save(*out, rep)
	if err != nil {
		return err
	}
	if !*quiet {
		os.Stdout.Write(rep.Launch.Stdout)
		os.Stderr.Write(rep.Launch.Stderr)
	}
	res := rep.Launch
	fmt.Printf("\nrun %s finished in %s: exit %d, oom %v, timed out %v, cancelled %v\n", *runID, time.Since(start).Round(time.Second), res.ExitCode, res.OOMKilled, res.TimedOut, res.Cancelled)
	fmt.Printf("output in %s: %v\n", *out, names)
	switch {
	case rep.Push != nil:
		fmt.Printf("draft PR: %s (branch %s%s)\n", rep.Push.PRURL, rep.Push.Branch, map[bool]string{true: ", reused", false: ""}[rep.Push.Reused])
	case rep.PushErr != nil:
		fmt.Printf("not published: %v\n", rep.PushErr)
	}
	if !rep.OK() {
		return fmt.Errorf("run did not succeed")
	}
	return nil
}
