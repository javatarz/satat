// Command satat-enqueue puts one issue on the queue by pushing the event the workflow listens
// for. It is the hand trigger for the Hatchet path and for a hand run beside the webhook intake (slice 6).
//
//	satat-enqueue --repo owner/name --issue 12 [--base main] [--lane claude]
//
// Needs HATCHET_CLIENT_TOKEN (and HATCHET_CLIENT_HOST_PORT, HATCHET_CLIENT_TLS_STRATEGY=none for
// the Compose stack on this machine). It only queues; the worker does the work.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/pipeline"
	"github.com/javatarz/satat/workflows"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "satat-enqueue:", err)
		os.Exit(1)
	}
}

func run() error {
	repo := flag.String("repo", "", "owner/name of the repo")
	issue := flag.Int("issue", 0, "issue number")
	base := flag.String("base", "main", "branch to start from")
	lane := flag.String("lane", "claude", "lane from satat.yaml")
	cfgPath := flag.String("config", "satat.yaml", "path to satat.yaml")
	flag.Parse()
	if *repo == "" || *issue <= 0 {
		return fmt.Errorf("--repo and --issue are required")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	iss, err := pipeline.FetchIssue(*repo, *issue)
	if err != nil {
		return err
	}
	in, err := workflows.MapIssue(cfg, *repo, *base, *lane, iss)
	if err != nil {
		return err
	}
	hc, err := hatchet.NewClient()
	if err != nil {
		return fmt.Errorf("hatchet client: %w", err)
	}
	if err := hc.Events().Push(context.Background(), workflows.EventIssue, in); err != nil {
		return fmt.Errorf("push event: %w", err)
	}
	fmt.Printf("queued %s#%d on lane %s (account %s). Watch it at http://localhost:8888\n", *repo, *issue, *lane, in.Account)
	return nil
}
