// Command satat is the operator's CLI for Satat.
//
//	satat deploy render [--config satat.yaml] [--check]
//	satat deploy up [--config satat.yaml]
//	satat deploy down [--volumes] [--config satat.yaml]
//	satat deploy status [--config satat.yaml]
//	satat agent run <lane> [--input file.json] [--wait] [--config satat.yaml]
//	satat agent ls
//	satat agent rm <run>
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
		return fmt.Errorf("usage: satat deploy|agent ...")
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
