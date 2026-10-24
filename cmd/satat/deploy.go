package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"text/tabwriter"

	"github.com/moby/moby/client"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/deploy"
	"github.com/javatarz/satat/deploy/compose"
	"github.com/javatarz/satat/deploy/kubernetes"
	"github.com/javatarz/satat/launcher/docker"
)

const deployUsage = "usage: satat deploy render [--check] | up | down [--volumes] | status   [--config satat.yaml]\n" +
	"  kubernetes target: up, down and status also take [--context k3d-satat] [--registry host:port]"

func runDeploy(args []string) error {
	if len(args) == 0 {
		return errors.New(deployUsage)
	}
	switch args[0] {
	case "render":
		return runRender(args[1:])
	case "up":
		return runUp(args[1:])
	case "down":
		return runDown(args[1:])
	case "status":
		return runStatus(args[1:])
	default:
		return fmt.Errorf("deploy: unknown subcommand %q (%s)", args[0], deployUsage)
	}
}

// kubeFlags are the kubernetes target's settings from the command line, defaulted by kubeDefaults.
type kubeFlags struct{ Context, Registry string }

// kubeDefaults fills what the flags left empty: the context from SATAT_KUBE_CONTEXT, else
// k3d-satat, and the registry from SATAT_REGISTRY (only up needs one).
func kubeDefaults(k kubeFlags, lookup func(string) (string, bool)) kubeFlags {
	if k.Context == "" {
		k.Context, _ = lookup("SATAT_KUBE_CONTEXT")
	}
	if k.Context == "" {
		k.Context = kubernetes.DefaultContext
	}
	if k.Registry == "" {
		k.Registry, _ = lookup("SATAT_REGISTRY")
	}
	return k
}

// targetFor returns the deploy target the config names, working in dir. For kubernetes, dir is
// also the repo root the platform image is built from.
func targetFor(cfg config.Config, dir string, kube kubeFlags) (deploy.Target, error) {
	switch cfg.Deploy.Target {
	case config.TargetKubernetes:
		kube = kubeDefaults(kube, os.LookupEnv)
		return &kubernetes.Target{
			Dir:      dir,
			Root:     dir,
			Context:  kube.Context,
			Registry: kube.Registry,
			Run:      kubernetes.ExecRunner{},
			Out:      os.Stdout,
		}, nil
	case config.TargetCompose:
		return &compose.Target{
			Dir: dir,
			Run: compose.ExecRunner{},
			Out: os.Stdout,
			// Down removes agent containers by label from the host, as the worker's reaper does.
			Reap: func(ctx context.Context) (int, error) {
				api, err := client.New(client.FromEnv)
				if err != nil {
					return 0, err
				}
				defer api.Close()
				l, err := docker.New(api, cfg.Lanes)
				if err != nil {
					return 0, err
				}
				return l.Reap(ctx, 0)
			},
			// Up warns before the worker's up when agents are running, since a recreate orphans them.
			Running: func(ctx context.Context) (int, error) {
				api, err := client.New(client.FromEnv)
				if err != nil {
					return 0, err
				}
				defer api.Close()
				list, err := api.ContainerList(ctx, client.ContainerListOptions{
					Filters: make(client.Filters).Add("label", "satat.managed=true"),
				})
				if err != nil {
					return 0, err
				}
				return len(list.Items), nil
			},
		}, nil
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
	target, err := targetFor(cfg, dir, kubeFlags{})
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
		// The kubernetes target renders into satat.k8s/.
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
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

// loadTarget parses the flags common to up, down and status and returns the config and its target,
// working in the config's directory as an absolute path so compose runs from any directory.
func loadTarget(name string, args []string, extra func(*flag.FlagSet)) (config.Config, deploy.Target, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	path := fs.String("config", "satat.yaml", "path to satat.yaml")
	var kube kubeFlags
	fs.StringVar(&kube.Context, "context", "", "kubernetes target: the kube context to act on (default $SATAT_KUBE_CONTEXT, else "+kubernetes.DefaultContext+")")
	fs.StringVar(&kube.Registry, "registry", "", "kubernetes target: the registry deploy up pushes the platform image to (default $SATAT_REGISTRY)")
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil {
		return config.Config{}, nil, err
	}
	if fs.NArg() > 0 {
		return config.Config{}, nil, fmt.Errorf("%s: unexpected argument %q", name, fs.Arg(0))
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return config.Config{}, nil, err
	}
	dir, err := filepath.Abs(filepath.Dir(*path))
	if err != nil {
		return config.Config{}, nil, err
	}
	target, err := targetFor(cfg, dir, kube)
	return cfg, target, err
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func runUp(args []string) error {
	cfg, target, err := loadTarget("deploy up", args, nil)
	if err != nil {
		return err
	}
	if cfg.Deploy.Secrets == nil {
		return errors.New("satat.yaml has no deploy.secrets: list every secret the lanes and the worker use, as op:// references or env")
	}
	ctx, cancel := signalContext()
	defer cancel()
	secrets, err := deploy.Resolve(ctx, cfg.Deploy.Secrets, deploy.OpRead, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolving deploy.secrets: %w", err)
	}
	return target.Up(ctx, cfg, secrets)
}

func runDown(args []string) error {
	var volumes *bool
	_, target, err := loadTarget("deploy down", args, func(fs *flag.FlagSet) {
		volumes = fs.Bool("volumes", false, "also delete the platform's volumes (Postgres and Hatchet data) and the now-dead Hatchet token in .env")
	})
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return target.Down(ctx, *volumes)
}

func runStatus(args []string) error {
	_, target, err := loadTarget("deploy status", args, nil)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	st, err := target.Status(ctx)
	if err != nil {
		return err
	}
	if len(st.Services) == 0 {
		fmt.Println("no services: run satat deploy up")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tSTATE\tHEALTHY\tDETAIL")
	for _, s := range st.Services {
		fmt.Fprintf(w, "%s\t%s\t%v\t%s\n", s.Name, s.State, s.Healthy, s.Detail)
	}
	return w.Flush()
}
