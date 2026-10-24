package main

import (
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

func TestTargetForKubernetes(t *testing.T) {
	cfg := config.Config{Deploy: config.Deploy{Target: config.TargetKubernetes}}
	got, err := targetFor(cfg, "/repo", kubeFlags{Context: "c", Registry: "r:1"})
	if err != nil {
		t.Fatal(err)
	}
	k, ok := got.(*kubernetes.Target)
	if !ok {
		t.Fatalf("target is %T", got)
	}
	if k.Dir != "/repo" || k.Root != "/repo" || k.Context != "c" || k.Registry != "r:1" {
		t.Errorf("target = %+v", k)
	}
}
