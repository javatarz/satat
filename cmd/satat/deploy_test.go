package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/deploy/kubernetes"
)

func TestKubeDefaults(t *testing.T) {
	env := map[string]string{"SATAT_KUBE_CONTEXT": "k3d-env", "SATAT_REGISTRY": "env:5000"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	none := func(string) (string, bool) { return "", false }

	if got := kubeDefaults(kubeFlags{Context: "flag", Registry: "flag:1"}, lookup); got != (kubeFlags{"flag", "flag:1"}) {
		t.Errorf("flags lost to the env: %+v", got)
	}
	if got := kubeDefaults(kubeFlags{}, lookup); got != (kubeFlags{"k3d-env", "env:5000"}) {
		t.Errorf("env not used: %+v", got)
	}
	if got := kubeDefaults(kubeFlags{}, none); got != (kubeFlags{kubernetes.DefaultContext, ""}) {
		t.Errorf("defaults = %+v, want context %s and no registry", got, kubernetes.DefaultContext)
	}
}

// The kubernetes target builds from the repo root above the config, and installs that config.
func TestTargetForKubernetes(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"go.mod", "images/worker/Dockerfile", "deploy/site/satat.yaml"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(root, "deploy", "site", "satat.yaml")
	cfg := config.Config{Deploy: config.Deploy{Target: config.TargetKubernetes}}
	got, err := targetFor(cfg, configPath, kubeFlags{Context: "c", Registry: "r:1"})
	if err != nil {
		t.Fatal(err)
	}
	k, ok := got.(*kubernetes.Target)
	if !ok {
		t.Fatalf("target is %T", got)
	}
	if k.Dir != filepath.Dir(configPath) || k.Root != root || k.Root == k.Dir || k.Config != configPath ||
		k.Context != "c" || k.Registry != "r:1" {
		t.Errorf("target = %+v, want Dir %s, Root %s", k, filepath.Dir(configPath), root)
	}

	// No repo above the config: refused, naming what's missing.
	if _, err := targetFor(cfg, filepath.Join(t.TempDir(), "satat.yaml"), kubeFlags{}); err == nil ||
		!strings.Contains(err.Error(), "images/worker/Dockerfile") {
		t.Errorf("err = %v, want no repo root found", err)
	}
}
