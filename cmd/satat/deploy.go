package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/deploy"
	"github.com/javatarz/satat/deploy/compose"
)

func runDeploy(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: satat deploy render [--config satat.yaml] [--check]")
	}
	switch args[0] {
	case "render":
		return runRender(args[1:])
	default:
		return fmt.Errorf("deploy: unknown subcommand %q", args[0])
	}
}

// targetFor returns the deploy target the config names, working in dir.
func targetFor(cfg config.Config, dir string) (deploy.Target, error) {
	switch cfg.Deploy.Target {
	case "compose":
		return &compose.Target{Dir: dir, Out: os.Stdout}, nil
	case "":
		return nil, errors.New("config has no deploy.target")
	default:
		return nil, fmt.Errorf("unknown deploy.target %q", cfg.Deploy.Target)
	}
}

func runRender(args []string) error {
	fs := flag.NewFlagSet("deploy render", flag.ContinueOnError)
	path := fs.String("config", "satat.yaml", "path to satat.yaml")
	check := fs.Bool("check", false, "fail if the checked-in files differ from the render")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(*path)
	target, err := targetFor(cfg, dir)
	if err != nil {
		return err
	}
	files, err := target.Render(cfg)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	stale := false
	for _, name := range names {
		file := filepath.Join(dir, name)
		if *check {
			have, err := os.ReadFile(file)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if !bytes.Equal(have, files[name]) {
				fmt.Fprintf(os.Stderr, "%s is out of date: run satat deploy render\n", name)
				stale = true
			}
			continue
		}
		if err := os.WriteFile(file, files[name], 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", file)
	}
	if stale {
		os.Exit(1)
	}
	return nil
}
