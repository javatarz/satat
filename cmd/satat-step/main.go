// Command satat-step runs one issue through one lane by hand. It is the prototype's stand-in
// for the worker until the Hatchet workflow exists (slice 5): it fetches the issue with gh,
// launches the step through the Docker launcher and writes what the step left behind.
//
//	satat-step --repo owner/name --issue 12 [--base main] [--lane claude]
//
// The lane's pass_env names must be set in the shell, for example CLAUDE_CODE_OAUTH_TOKEN and
// SATAT_GITHUB_TOKEN. Their values are passed to the container by name and never printed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/moby/moby/client"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/launcher/docker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "satat-step:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		repo     = flag.String("repo", "", "owner/name of the repo")
		issue    = flag.Int("issue", 0, "issue number")
		base     = flag.String("base", "main", "branch to start from")
		lane     = flag.String("lane", "claude", "lane from satat.yaml")
		cfgPath  = flag.String("config", "satat.yaml", "path to satat.yaml")
		out      = flag.String("out", "", "directory for the step's output (default docs/scratch/runs/<run>)")
		runID    = flag.String("run", fmt.Sprintf("r%d", time.Now().Unix()), "run id")
		keepNote = flag.Bool("quiet", false, "do not print the step's stdout and stderr")
	)
	flag.Parse()
	if *repo == "" || *issue <= 0 {
		return fmt.Errorf("--repo and --issue are required")
	}
	if *out == "" {
		*out = filepath.Join("docs", "scratch", "runs", *runID)
	}

	ghOut, err := exec.Command("gh", "issue", "view", fmt.Sprint(*issue), "-R", *repo, "--json", "number,title,body").Output()
	if err != nil {
		return fmt.Errorf("gh issue view: %w", err)
	}
	var iss map[string]any
	if err := json.Unmarshal(ghOut, &iss); err != nil {
		return err
	}
	input, err := json.Marshal(map[string]any{"repo": *repo, "base": *base, "issue": iss})
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
	res, err := l.Launch(ctx, launcher.Step{Run: *runID, Name: "claude", Lane: *lane, Input: input})
	if err != nil {
		return err
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	var names []string
	for name, b := range res.Files {
		p := filepath.Join(*out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for name, b := range map[string][]byte{"stdout.log": res.Stdout, "stderr.log": res.Stderr} {
		if err := os.WriteFile(filepath.Join(*out, name), b, 0o644); err != nil {
			return err
		}
	}
	if !*keepNote {
		os.Stdout.Write(res.Stdout)
		os.Stderr.Write(res.Stderr)
	}
	fmt.Printf("\nrun %s finished in %s: exit %d, oom %v, timed out %v, cancelled %v\n", *runID, time.Since(start).Round(time.Second), res.ExitCode, res.OOMKilled, res.TimedOut, res.Cancelled)
	fmt.Printf("output in %s: %v\n", *out, names)
	if res.ExitCode != 0 || res.TimedOut || res.OOMKilled {
		return fmt.Errorf("step did not succeed")
	}
	return nil
}
