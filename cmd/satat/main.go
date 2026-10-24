// Command satat is the operator's CLI for Satat.
//
//	satat deploy render [--config satat.yaml] [--check]
//	satat deploy up [--config satat.yaml]
//	satat deploy down [--volumes] [--config satat.yaml]
//	satat deploy status [--config satat.yaml]
//	satat agent run <lane> [--input file.json] [--wait] [--config satat.yaml]
//	satat agent ls [--config satat.yaml]
//	satat agent rm <run> [--config satat.yaml]
//
// The agent commands talk to Hatchet with the SDK's HATCHET_CLIENT_* variables. Any that are unset
// are defaulted: HATCHET_CLIENT_TOKEN from the .env file beside --config (written by deploy up),
// and, when deploy.target is compose, HATCHET_CLIENT_HOST_PORT=localhost:7077,
// HATCHET_CLIENT_TLS_STRATEGY=none and HATCHET_CLIENT_SERVER_URL=http://localhost:8888, the ports
// compose.yaml publishes on this machine. A variable already set always wins.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var code exitCode
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "satat:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: satat deploy|agent ... (agent defaults the HATCHET_CLIENT_* env from .env beside --config)")
	}
	switch args[0] {
	case "deploy":
		return runDeploy(args[1:])
	case "agent":
		return runAgentCmd(args[1:])
	default:
		return fmt.Errorf("unknown command %q (want deploy or agent)", args[0])
	}
}
