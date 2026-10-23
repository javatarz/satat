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
deploy: {target: compose, secrets: {FOO: env, SATAT_PUSH_TOKEN: env}}
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
