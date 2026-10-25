// Command satat-check-repo prints whether a repo meets Satat's prerequisites (#175), the same
// check the worker runs before it starts an issue. It only reads: it never comments, relabels or
// touches an issue.
//
//	satat-check-repo [--base main] [--issue N] owner/name
//
// Needs SATAT_PUSH_TOKEN, because the check is about what that token can do and reach. The repos
// the token may reach are SATAT_INTAKE_REPOS plus poller.repos of satat.yaml (SATAT_CONFIG
// overrides the path), the same list the worker uses. With --issue it also checks who labelled
// that issue ready-for-agent. Exit status: 0 ready, 1 not ready, 2 the check could not run.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/readiness"
)

func main() {
	ready, err := run()
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "satat-check-repo:", err)
		os.Exit(2)
	case !ready:
		os.Exit(1)
	}
}

func run() (bool, error) {
	base := flag.String("base", "", "branch runs start from (default: the repo's default branch)")
	issue := flag.Int("issue", 0, "also check who labelled this issue ready-for-agent")
	flag.Parse()
	if flag.NArg() != 1 || !readiness.ValidRepo(flag.Arg(0)) {
		return false, fmt.Errorf("usage: satat-check-repo [--base BRANCH] [--issue N] owner/name")
	}
	token := os.Getenv("SATAT_PUSH_TOKEN")
	if token == "" {
		return false, fmt.Errorf("SATAT_PUSH_TOKEN is not set: the check is about what the push token can do and reach")
	}
	path := os.Getenv("SATAT_CONFIG")
	if path == "" {
		path = "satat.yaml"
	}
	cfg, err := config.Load(path)
	if err != nil {
		return false, err
	}
	var allowed []string
	for _, r := range strings.Split(os.Getenv("SATAT_INTAKE_REPOS"), ",") {
		if r = strings.TrimSpace(r); r != "" {
			allowed = append(allowed, r)
		}
	}
	for _, r := range cfg.Poller.Repos {
		allowed = append(allowed, r.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := readiness.Checker{GH: readiness.HTTP{Token: token}, Allowed: allowed}.
		Check(ctx, readiness.Request{Repo: flag.Arg(0), Base: *base, Issue: *issue})
	if err != nil {
		return false, err
	}
	fmt.Print(res)
	return res.Ready(), nil
}
