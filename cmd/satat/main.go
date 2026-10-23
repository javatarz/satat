// Command satat is the operator's CLI for Satat.
//
//	satat deploy render [--config satat.yaml] [--check]
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
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
		return fmt.Errorf("agent: not implemented yet")
	default:
		return fmt.Errorf("unknown command %q (want deploy or agent)", args[0])
	}
}
