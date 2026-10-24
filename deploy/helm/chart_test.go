package helm

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/javatarz/satat/deploy"
)

const testImage = "r@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// writableRoot are the containers whose upstream images need a writable root filesystem
// (documented in their templates).
var writableRoot = map[string]bool{"postgres": true, "hatchet-lite": true}

type container struct {
	Name            string `yaml:"name"`
	SecurityContext struct {
		ReadOnlyRootFilesystem *bool `yaml:"readOnlyRootFilesystem"`
		Capabilities           struct {
			Drop []string `yaml:"drop"`
		} `yaml:"capabilities"`
		AllowPrivilegeEscalation *bool `yaml:"allowPrivilegeEscalation"`
	} `yaml:"securityContext"`
	LivenessProbe  *probe `yaml:"livenessProbe"`
	ReadinessProbe *probe `yaml:"readinessProbe"`
	Resources      struct {
		Requests map[string]string `yaml:"requests"`
		Limits   map[string]string `yaml:"limits"`
	} `yaml:"resources"`
	Env []struct {
		Name string `yaml:"name"`
	} `yaml:"env"`
}

type probe struct {
	HTTPGet *struct {
		Path string `yaml:"path"`
	} `yaml:"httpGet"`
}

type object struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Template struct {
			Spec struct {
				SecurityContext struct {
					RunAsNonRoot   *bool `yaml:"runAsNonRoot"`
					SeccompProfile struct {
						Type string `yaml:"type"`
					} `yaml:"seccompProfile"`
				} `yaml:"securityContext"`
				Containers []container `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func helmTemplate(t *testing.T, args ...string) []object {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "satat")
	if err := WriteTo(dir); err != nil {
		t.Fatal(err)
	}
	full := append([]string{"template", "satat", dir, "--namespace", "satat"}, args...)
	cmd := exec.Command("helm", full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template: %v: %s", err, stderr.String())
	}
	var objs []object
	dec := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var o object
		err := dec.Decode(&o)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if o.Kind != "" {
			objs = append(objs, o)
		}
	}
	return objs
}

func TestChartTemplates(t *testing.T) {
	objs := helmTemplate(t, "--set", "image.ref="+testImage, "--set-string", "config=lanes: {}")
	kinds := map[string]bool{}
	var worker *object
	for i, o := range objs {
		kinds[o.Kind] = true
		switch o.Kind {
		case "Role", "ClusterRole", "RoleBinding", "ClusterRoleBinding", "Secret":
			t.Errorf("chart renders a %s (%s): RBAC and Secrets are applied by deploy up", o.Kind, o.Metadata.Name)
		}
		if o.Kind == "Deployment" && o.Metadata.Name == "satat-worker" {
			worker = &objs[i]
		}
		pod := o.Spec.Template.Spec
		if len(pod.Containers) == 0 {
			continue
		}
		if pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
			t.Errorf("%s %s: runAsNonRoot is not set", o.Kind, o.Metadata.Name)
		}
		if pod.SecurityContext.SeccompProfile.Type != "RuntimeDefault" {
			t.Errorf("%s %s: seccomp is %q", o.Kind, o.Metadata.Name, pod.SecurityContext.SeccompProfile.Type)
		}
		for _, c := range pod.Containers {
			sc := c.SecurityContext
			ro := sc.ReadOnlyRootFilesystem != nil && *sc.ReadOnlyRootFilesystem
			if ro == writableRoot[c.Name] {
				t.Errorf("container %s: readOnlyRootFilesystem = %v", c.Name, ro)
			}
			if len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
				t.Errorf("container %s: capabilities.drop = %v, want [ALL]", c.Name, sc.Capabilities.Drop)
			}
			if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
				t.Errorf("container %s: allowPrivilegeEscalation is not false", c.Name)
			}
			if c.Resources.Requests["memory"] == "" || c.Resources.Limits["memory"] == "" {
				t.Errorf("container %s: memory request and limit are required (ADR 0024)", c.Name)
			}
		}
	}
	for _, k := range []string{"StatefulSet", "Deployment", "Service", "ConfigMap", "ServiceAccount", "PersistentVolumeClaim"} {
		if !kinds[k] {
			t.Errorf("no %s rendered", k)
		}
	}
	if worker == nil {
		t.Fatal("no satat-worker Deployment")
	}
	c := worker.Spec.Template.Spec.Containers[0]
	if c.LivenessProbe == nil || c.LivenessProbe.HTTPGet == nil || c.LivenessProbe.HTTPGet.Path != "/livez" {
		t.Error("worker: no httpGet /livez liveness probe")
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil || c.ReadinessProbe.HTTPGet.Path != "/readyz" {
		t.Error("worker: no httpGet /readyz readiness probe")
	}
	var env []string
	for _, e := range c.Env {
		env = append(env, e.Name)
	}
	if got := strings.Join(env, " "); !strings.Contains(got, "HATCHET_CLIENT_TOKEN") || !strings.Contains(got, "SATAT_PUSH_TOKEN") {
		t.Errorf("worker env = %s, want the token and SATAT_PUSH_TOKEN", got)
	}
}

func TestChartRefusesAnotherNamespace(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not on PATH")
	}
	dir := filepath.Join(t.TempDir(), "satat")
	if err := WriteTo(dir); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("helm", "template", "satat", dir, "--namespace", "default",
		"--set", "image.ref="+testImage, "--set-string", "config=x").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "namespace satat") {
		t.Fatalf("helm template in namespace default: err = %v, output %s", err, out)
	}
}

// The chart's default images are the ones Compose uses.
func TestValuesMatchDeployImages(t *testing.T) {
	data, err := fs.ReadFile(Chart, "satat/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Postgres struct{ Image string } `yaml:"postgres"`
		Hatchet  struct{ Image string } `yaml:"hatchet"`
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if v.Postgres.Image != deploy.PostgresImage {
		t.Errorf("postgres.image = %q, want deploy.PostgresImage %q", v.Postgres.Image, deploy.PostgresImage)
	}
	if v.Hatchet.Image != deploy.HatchetLiteImage {
		t.Errorf("hatchet.image = %q, want deploy.HatchetLiteImage %q", v.Hatchet.Image, deploy.HatchetLiteImage)
	}
}
