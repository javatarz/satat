// Command satat-egress is the lane egress proxy (package egress). It runs in its own container,
// attached to one lane's internal network and to a network with a route out. The allowlist comes
// from the lane's configuration in satat.yaml, passed in SATAT_EGRESS_ALLOW by the launcher.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/javatarz/satat/egress"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "satat-egress:", err)
		os.Exit(1)
	}
}

func run() error {
	var entries []string
	for _, e := range strings.Split(os.Getenv("SATAT_EGRESS_ALLOW"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			entries = append(entries, e)
		}
	}
	allow, err := egress.ParseAllowlist(entries)
	if err != nil {
		return err
	}
	addr := os.Getenv("SATAT_EGRESS_LISTEN")
	if addr == "" {
		addr = ":3128"
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("start", "listen", addr, "allowed", len(entries))
	srv := &http.Server{
		Addr:              addr,
		Handler:           egress.New(allow, egress.Dialer{Timeout: 10 * time.Second}, log),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}
