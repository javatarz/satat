package compose

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/javatarz/satat/config"
)

type composeFile struct {
	Services map[string]struct {
		Environment map[string]string `yaml:"environment"`
		Volumes     []string          `yaml:"volumes"`
	} `yaml:"services"`
}

func render(t *testing.T, cfg config.Config) []byte {
	t.Helper()
	files, err := (&Target{}).Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := files["compose.yaml"]
	if !ok {
		t.Fatalf("Render returned %d files, none named compose.yaml", len(files))
	}
	return out
}

func TestRenderMatchesCheckedInCompose(t *testing.T) {
	cfg, err := config.Load("../../satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := render(t, cfg); string(got) != string(want) {
		t.Fatal("compose.yaml is out of date: run satat deploy render")
	}
}

func TestRenderAddsEveryPassEnvToTheWorker(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(`
limits: {global: 1, per_repo: 1, per_account: 1}
deploy: {target: compose, secrets: {FOO: env, SATAT_GITHUB_TOKEN: env, SATAT_PUSH_TOKEN: env}}
lanes: {x: {image: "x@sha256:f1a5df711a66a10f4aaad2eaf8c69746b6f873efb8a6ea416183714876413a12", memory: 1GiB, cpus: 1, pids: 64, workspace: 64MiB, timeout: 1m, pass_env: [FOO], account: a, max_runs: 1}}
`))
	if err != nil {
		t.Fatal(err)
	}
	var f composeFile
	if err := yaml.Unmarshal(render(t, cfg), &f); err != nil {
		t.Fatal(err)
	}
	got := f.Services["worker"].Environment["FOO"]
	if want := "${FOO:?run satat deploy up}"; got != want {
		t.Fatalf("worker.environment.FOO = %q, want %q", got, want)
	}
}

func TestRenderPassesOptionalEnvOnlyWhenSet(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(`
limits: {global: 1, per_repo: 1, per_account: 1}
deploy: {target: compose, secrets: {SATAT_GITHUB_TOKEN: env, SATAT_PUSH_TOKEN: env}}
lanes: {x: {image: "x@sha256:f1a5df711a66a10f4aaad2eaf8c69746b6f873efb8a6ea416183714876413a12", memory: 1GiB, cpus: 1, pids: 64, workspace: 64MiB, timeout: 1m, optional_env: [SATAT_GIT_NAME], account: a, max_runs: 1}}
`))
	if err != nil {
		t.Fatal(err)
	}
	var f composeFile
	if err := yaml.Unmarshal(render(t, cfg), &f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.Services["worker"].Environment["SATAT_GIT_NAME"], "${SATAT_GIT_NAME:-}"; got != want {
		t.Fatalf("worker.environment.SATAT_GIT_NAME = %q, want %q", got, want)
	}
}

// The cache reaper reads /system/df through the proxy (ADR 0033); a render without SYSTEM breaks it.
func TestRenderKeepsSystemOnTheProxy(t *testing.T) {
	cfg, err := config.Load("../../satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var f composeFile
	if err := yaml.Unmarshal(render(t, cfg), &f); err != nil {
		t.Fatal(err)
	}
	if got := f.Services["docker-proxy"].Environment["SYSTEM"]; got != "1" {
		t.Fatalf("docker-proxy SYSTEM = %q, want 1", got)
	}
}

func TestRenderMountsTheSocketOnlyInTheProxy(t *testing.T) {
	cfg, err := config.Load("../../satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var f composeFile
	if err := yaml.Unmarshal(render(t, cfg), &f); err != nil {
		t.Fatal(err)
	}
	for name, svc := range f.Services {
		has := false
		for _, v := range svc.Volumes {
			has = has || strings.Contains(v, "docker.sock")
		}
		if has != (name == "docker-proxy") {
			t.Errorf("service %s mounts docker.sock = %v", name, has)
		}
	}
}

// On the containerd image store the build time was part of the worker's image ID, through the
// provenance attestation and (BuildKit 0.29) the image's created time. So every rebuild got a new ID,
// even from cache, and compose recreated the worker on every deploy up, orphaning running agents.
// The e2e suite found it (#224).
func TestRenderBuildsWorkerReproducibly(t *testing.T) {
	cfg, err := config.Load("../../satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Services map[string]struct {
			Build struct {
				Provenance *bool             `yaml:"provenance"`
				Args       map[string]string `yaml:"args"`
			} `yaml:"build"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(render(t, cfg), &f); err != nil {
		t.Fatal(err)
	}
	build := f.Services["worker"].Build
	if p := build.Provenance; p == nil || *p {
		t.Error("the worker's build does not set provenance: false")
	}
	if _, ok := build.Args["SOURCE_DATE_EPOCH"]; !ok {
		t.Error("the worker's build does not set SOURCE_DATE_EPOCH")
	}
}
