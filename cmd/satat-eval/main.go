// Command satat-eval runs one eval case through the whole path and grades the result.
//
//	satat-eval --case eval/cases/sandbox-greet [--push]       run the issue, then grade it
//	satat-eval --case eval/cases/sandbox-greet --grade DIR    grade a saved run (no new run, no tokens)
//
// The lane's pass_env names must be set, as for satat-step, and so must SATAT_GITHUB_TOKEN, with
// which this process clones the repo before the step starts. --push also needs SATAT_PUSH_TOKEN.
// Grading clones the repo and reads the PR back with SATAT_PUSH_TOKEN or, failing that,
// SATAT_GITHUB_TOKEN.
package main

import (
	"context"
	"encoding/json"
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
	"github.com/javatarz/satat/eval"
	"github.com/javatarz/satat/launcher/docker"
	"github.com/javatarz/satat/pipeline"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/runner"
)

func main() {
	ok, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "satat-eval:", err)
		os.Exit(2)
	}
	if !ok {
		os.Exit(1)
	}
}

func run() (bool, error) {
	var (
		caseDir = flag.String("case", "", "directory holding case.yaml")
		cfgPath = flag.String("config", "satat.yaml", "path to satat.yaml")
		lane    = flag.String("lane", "claude", "lane to run the issue in")
		push    = flag.Bool("push", false, "publish a draft PR and grade it too (needs SATAT_PUSH_TOKEN)")
		grade   = flag.String("grade", "", "grade the saved run in this directory instead of running")
		runID   = flag.String("run", fmt.Sprintf("e%d", time.Now().Unix()), "run id")
	)
	flag.Parse()
	if *caseDir == "" {
		return false, fmt.Errorf("--case is required")
	}
	c, err := eval.LoadCase(*caseDir)
	if err != nil {
		return false, err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return false, err
	}
	api, err := client.New(client.FromEnv)
	if err != nil {
		return false, err
	}
	defer api.Close()
	l, err := docker.New(api, cfg.Lanes, docker.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))))
	if err != nil {
		return false, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := l.Setup(ctx); err != nil {
		return false, err
	}

	var subject eval.Subject
	if *grade != "" {
		if subject, err = loadSaved(*grade); err != nil {
			return false, err
		}
	} else {
		var pub pipeline.Publisher
		if *push {
			tok := os.Getenv("SATAT_PUSH_TOKEN")
			if tok == "" {
				return false, fmt.Errorf("--push needs SATAT_PUSH_TOKEN")
			}
			pub = &pusher.Pusher{Token: tok}
		}
		readToken := os.Getenv("SATAT_GITHUB_TOKEN")
		if readToken == "" {
			return false, fmt.Errorf("SATAT_GITHUB_TOKEN is not set: this process clones the repo before the step starts")
		}
		iss, err := pipeline.FetchIssue(c.Repo, c.Issue)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(os.Stderr, "run %s: %s issue #%d on lane %s\n", *runID, c.Repo, c.Issue, *lane)
		rep, err := pipeline.Run(ctx, pipeline.Options{Launcher: l, Pusher: pub, Cloner: pipeline.GitCloner{Token: readToken}, Run: *runID, Lane: *lane, Repo: c.Repo, Base: c.Base, Issue: iss})
		if err != nil {
			return false, err
		}
		dir := filepath.Join("docs", "scratch", "runs", *runID)
		if _, err := pipeline.Save(dir, rep); err != nil {
			return false, err
		}
		fmt.Fprintf(os.Stderr, "run saved in %s\n", dir)
		if rep.PushErr != nil {
			fmt.Fprintf(os.Stderr, "not published: %v\n", rep.PushErr)
		}
		subject = eval.Subject{Run: *runID, ExitCode: rep.Launch.ExitCode, Runner: rep.Runner, Bundle: rep.Launch.Files["changes.bundle"], Transcript: rep.Launch.Files["transcript.jsonl"], PR: rep.Push}
	}

	token := os.Getenv("SATAT_PUSH_TOKEN")
	if token == "" {
		token = os.Getenv("SATAT_GITHUB_TOKEN")
	}
	g := &eval.Grader{Token: token, Checker: eval.LauncherChecker{Launcher: l, RunID: "eval-" + subject.Run}}
	res, err := g.Grade(ctx, c, subject)
	if err != nil {
		return false, err
	}
	print(res)
	b, _ := json.MarshalIndent(res, "", "  ")
	out := filepath.Join("docs", "scratch", "evals", subject.Run+".json")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err == nil {
		_ = os.WriteFile(out, append(b, '\n'), 0o644)
		fmt.Printf("\ngrade saved in %s\n", out)
	}
	return res.Passed(), nil
}

func loadSaved(dir string) (eval.Subject, error) {
	var s eval.Subject
	b, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		return s, err
	}
	var r runner.Result
	if err := json.Unmarshal(b, &r); err != nil {
		return s, err
	}
	s.Run, s.Runner = r.Run, &r
	s.Bundle, _ = os.ReadFile(filepath.Join(dir, "changes.bundle"))
	s.Transcript, _ = os.ReadFile(filepath.Join(dir, "transcript.jsonl"))
	return s, nil
}

func print(r eval.Result) {
	fmt.Printf("\ncase %s, run %s\n", r.Case, r.Run)
	for _, c := range r.Checks {
		mark := "PASS"
		switch {
		case c.Skip:
			mark = "skip"
		case !c.Pass:
			mark = "FAIL"
		}
		fmt.Printf("  %-4s  %-8s %s\n", mark, c.Name, c.Detail)
	}
	if r.Passed() {
		fmt.Println("result: PASS")
	} else {
		fmt.Println("result: FAIL")
	}
}
