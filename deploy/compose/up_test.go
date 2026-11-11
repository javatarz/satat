package compose

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/deploy"
)

const testConfig = `
limits: {global: 1, per_repo: 1, per_account: 1}
deploy: {target: compose, secrets: {FOO: env, SATAT_GITHUB_TOKEN: env, SATAT_PUSH_TOKEN: env}}
lanes: {x: {image: "x@sha256:f1a5df711a66a10f4aaad2eaf8c69746b6f873efb8a6ea416183714876413a12", memory: 1GiB, cpus: 1, pids: 64, workspace: 64MiB, timeout: 1m, pass_env: [FOO], account: a, max_runs: 1}}
`

const (
	healthy   = `{"Service":"postgres","State":"running","Health":"healthy"}` + "\n" + `{"Service":"hatchet-lite","State":"running","Health":"healthy"}` + "\n" + `{"Service":"docker-proxy","State":"running","Health":""}` + "\n"
	workerOK  = `{"Service":"worker","State":"running","Health":"healthy"}` + "\n"
	workerBad = `{"Service":"worker","State":"running","Health":"starting"}` + "\n"
	token     = "eyJhbGciOiJ0ZXN0In0.payload.sig"
)

type call struct {
	env  []string
	args []string // after "docker compose -f ... --project-directory ..."
}

// fakeRunner records every command and answers it with respond.
type fakeRunner struct {
	t       *testing.T
	dir     string
	calls   []call
	respond func(args []string) ([]byte, error)
}

func (f *fakeRunner) Run(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	if name == "docker" && len(args) == 3 && args[0] == "image" && args[1] == "inspect" {
		f.calls = append(f.calls, call{env: env, args: args})
		return f.respond(args)
	}
	prefix := []string{"compose", "-f", filepath.Join(f.dir, "compose.yaml"), "--project-directory", f.dir}
	if name != "docker" || len(args) < len(prefix) || !slices.Equal(args[:len(prefix)], prefix) {
		f.t.Fatalf("unexpected command %s %q", name, args)
	}
	rest := args[len(prefix):]
	f.calls = append(f.calls, call{env: env, args: rest})
	return f.respond(rest)
}

// summary is each call's first words, for comparing the order.
func (f *fakeRunner) summary() []string {
	var out []string
	for _, c := range f.calls {
		s := strings.Join(c.args, " ")
		switch {
		case strings.HasPrefix(s, "exec -T postgres psql"):
			s = "exec postgres psql"
		case strings.HasPrefix(s, "exec -T hatchet-lite /hatchet-admin token create"):
			s = "exec hatchet-lite hatchet-admin"
		case strings.HasPrefix(s, "exec -T worker /usr/local/bin/satat-worker healthcheck"):
			s = "exec worker healthcheck"
		case strings.HasPrefix(s, "image inspect satat-worker:"):
			s = "image inspect worker"
		}
		out = append(out, s)
	}
	return out
}

func setup(t *testing.T) (*Target, *fakeRunner, *bytes.Buffer, config.Config) {
	t.Helper()
	cfg, err := config.Parse(strings.NewReader(testConfig))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files, err := (&Target{}).Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), files["compose.yaml"], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "satat.yaml"), []byte(testConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSource(t, dir)
	f := &fakeRunner{t: t, dir: dir}
	out := &bytes.Buffer{}
	tg := &Target{Dir: dir, Run: f, Out: out, poll: time.Millisecond, platformTimeout: time.Second, workerTimeout: 50 * time.Millisecond}
	return tg, f, out, cfg
}

// coldStack answers as a stack that starts from nothing and comes up healthy.
// A running worker means its image exists; otherwise Up has to build it.
func coldStack(platformUp, workerUp *bool) func([]string) ([]byte, error) {
	built := *workerUp
	return func(args []string) ([]byte, error) {
		s := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(s, "image inspect satat-worker:"):
			if built {
				return []byte("[{}]"), nil
			}
			return nil, errors.New("No such image")
		case s == "build worker":
			built = true
			return nil, nil
		case s == "ps --all --format json":
			out := ""
			if *platformUp {
				out += healthy
			}
			if *workerUp {
				out += workerOK
			}
			return []byte(out), nil
		case s == "up -d postgres hatchet-lite docker-proxy":
			*platformUp = true
			return nil, nil
		case s == "up -d --no-build worker":
			*workerUp = true
			return nil, nil
		case strings.HasPrefix(s, "exec -T postgres psql"):
			return []byte(" 707d0855-80ab-4e1f-a156-f1c4546cbf52 \n"), nil
		case strings.HasPrefix(s, "exec -T hatchet-lite /hatchet-admin token create"):
			return []byte(token + "\n"), nil
		}
		return nil, errors.New("unscripted: " + s)
	}
}

func secrets() deploy.Secrets {
	return deploy.Secrets{"FOO": "foo-secret-value", "SATAT_GITHUB_TOKEN": "read-secret-value", "SATAT_PUSH_TOKEN": "push-secret-value"}
}

func TestUpRunsInOrder(t *testing.T) {
	tg, f, _, cfg := setup(t)
	var platformUp, workerUp bool
	f.respond = coldStack(&platformUp, &workerUp)

	if err := tg.Up(context.Background(), cfg, secrets()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ps --all --format json",
		"up -d postgres hatchet-lite docker-proxy",
		"ps --all --format json",
		"exec postgres psql",
		"exec hatchet-lite hatchet-admin",
		"image inspect worker",
		"build worker",
		"up -d --no-build worker",
		"ps --all --format json",
	}
	if got := f.summary(); !slices.Equal(got, want) {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	env := filepath.Join(tg.Dir, ".env")
	if pw, ok, _ := deploy.ReadKey(env, "POSTGRES_PASSWORD"); !ok || pw == "" {
		t.Error(".env has no POSTGRES_PASSWORD")
	}
	if tok, _, _ := deploy.ReadKey(env, "HATCHET_CLIENT_TOKEN"); tok != token {
		t.Errorf("HATCHET_CLIENT_TOKEN = %q, want %q", tok, token)
	}
	if info, err := os.Stat(env); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
	// The tenant ID from psql is trimmed before it reaches hatchet-admin.
	admin := f.calls[4].args
	if i := slices.Index(admin, "--tenant-id"); i < 0 || admin[i+1] != "707d0855-80ab-4e1f-a156-f1c4546cbf52" {
		t.Errorf("hatchet-admin args = %q", admin)
	}
}

func TestUpIsIdempotent(t *testing.T) {
	tg, f, _, cfg := setup(t)
	env := filepath.Join(tg.Dir, ".env")
	const before = "POSTGRES_PASSWORD=keep-me\nHATCHET_CLIENT_TOKEN=eyJold\n"
	if err := os.WriteFile(env, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	platformUp, workerUp := true, true
	f.respond = coldStack(&platformUp, &workerUp)

	if err := tg.Up(context.Background(), cfg, secrets()); err != nil {
		t.Fatal(err)
	}
	// The worker's up always runs (compose recreates nothing unchanged); no platform up, no psql,
	// no hatchet-admin, and no build: an image with the source's tag exists.
	want := []string{"ps --all --format json", "image inspect worker", "up -d --no-build worker", "ps --all --format json"}
	if got := f.summary(); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if got, _ := os.ReadFile(env); string(got) != before {
		t.Fatalf(".env changed:\n%s", got)
	}
}

func TestUpStartsOnlyWhatIsNotHealthy(t *testing.T) {
	tg, f, _, cfg := setup(t)
	if err := os.WriteFile(filepath.Join(tg.Dir, ".env"), []byte("POSTGRES_PASSWORD=p\nHATCHET_CLIENT_TOKEN=eyJold\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	platformUp, workerUp := true, false
	f.respond = coldStack(&platformUp, &workerUp)

	if err := tg.Up(context.Background(), cfg, secrets()); err != nil {
		t.Fatal(err)
	}
	want := []string{"ps --all --format json", "image inspect worker", "build worker", "up -d --no-build worker", "ps --all --format json"}
	if got := f.summary(); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestUpRefusesStaleCompose(t *testing.T) {
	tg, f, _, cfg := setup(t)
	f.respond = func([]string) ([]byte, error) { return nil, nil }
	if err := os.WriteFile(filepath.Join(tg.Dir, "compose.yaml"), []byte("name: satat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := tg.Up(context.Background(), cfg, secrets())
	if err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("err = %v, want out of date", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("runner called: %q", f.summary())
	}
	if _, err := os.Stat(filepath.Join(tg.Dir, ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".env was created (%v)", err)
	}
}

func TestUpRefusesEmptyEnvValue(t *testing.T) {
	for _, key := range []string{"POSTGRES_PASSWORD", "HATCHET_CLIENT_TOKEN"} {
		t.Run(key, func(t *testing.T) {
			tg, f, _, cfg := setup(t)
			platformUp, workerUp := true, true
			f.respond = coldStack(&platformUp, &workerUp)
			env := filepath.Join(tg.Dir, ".env")
			content := "POSTGRES_PASSWORD=p\nHATCHET_CLIENT_TOKEN=eyJold\n"
			content = strings.Replace(content, key+"=", key+"=\n#", 1)
			if err := os.WriteFile(env, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			err := tg.Up(context.Background(), cfg, secrets())
			if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "empty") {
				t.Fatalf("err = %v, want an error naming %s as empty", err, key)
			}
			if len(f.calls) != 0 {
				t.Fatalf("runner called: %q", f.summary())
			}
			if got, _ := os.ReadFile(env); string(got) != content {
				t.Fatalf(".env changed:\n%s", got)
			}
		})
	}
}

func TestUpSecretsOnlyInChildEnv(t *testing.T) {
	tg, f, out, cfg := setup(t)
	var platformUp, workerUp bool
	f.respond = coldStack(&platformUp, &workerUp)

	s := secrets()
	if err := tg.Up(context.Background(), cfg, s); err != nil {
		t.Fatal(err)
	}
	var workerUpEnv []string
	for _, c := range f.calls {
		joined := strings.Join(c.args, " ")
		if joined == "up -d --no-build worker" {
			workerUpEnv = c.env
		}
		for name, v := range s {
			if strings.Contains(joined, v) {
				t.Errorf("%s value in args of %q", name, joined)
			}
			if joined != "up -d --no-build worker" && slices.Contains(c.env, name+"="+v) {
				t.Errorf("%s value in env of %q", name, joined)
			}
		}
	}
	// Placeholders are for the other commands only; the worker container must never get one.
	for _, kv := range workerUpEnv {
		if strings.HasSuffix(kv, "=unused") {
			t.Errorf("worker compose up env has placeholder %s", kv)
		}
	}
	for name, v := range s {
		if !slices.Contains(workerUpEnv, name+"="+v) {
			t.Errorf("worker compose up env lacks %s", name)
		}
		if strings.Contains(out.String(), v) {
			t.Errorf("%s value in Out", name)
		}
	}
	data, _ := os.ReadFile(filepath.Join(tg.Dir, ".env"))
	for name, v := range s {
		if strings.Contains(string(data), v) {
			t.Errorf("%s value in .env", name)
		}
	}
	if strings.Contains(out.String(), token) {
		t.Error("Hatchet token in Out")
	}
}

// The worker mounts satat.yaml as a single file, and compose recreates a container only when its
// config changes, so an edited satat.yaml would never reach a running worker. Up passes the file's
// hash into the worker's environment: a changed file changes the environment and rolls the worker.
func TestUpRollsWorkerWhenSatatYAMLChanges(t *testing.T) {
	tg, f, _, cfg := setup(t)
	if !strings.Contains(string(mustRead(t, filepath.Join(tg.Dir, "compose.yaml"))), "SATAT_CONFIG_SHA256: ${SATAT_CONFIG_SHA256:-}") {
		t.Fatal("compose.yaml does not pass SATAT_CONFIG_SHA256 to the worker")
	}
	platformUp, workerUp := true, true
	f.respond = coldStack(&platformUp, &workerUp)
	if err := os.WriteFile(filepath.Join(tg.Dir, ".env"), []byte("POSTGRES_PASSWORD=p\nHATCHET_CLIENT_TOKEN=eyJold\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	hash := func() string {
		t.Helper()
		f.calls = nil
		if err := tg.Up(context.Background(), cfg, secrets()); err != nil {
			t.Fatal(err)
		}
		for _, c := range f.calls {
			if strings.Join(c.args, " ") != "up -d --no-build worker" {
				continue
			}
			for _, kv := range c.env {
				if v, ok := strings.CutPrefix(kv, "SATAT_CONFIG_SHA256="); ok {
					return v
				}
			}
		}
		t.Fatal("worker compose up env lacks SATAT_CONFIG_SHA256")
		return ""
	}

	first := hash()
	if len(first) != 64 {
		t.Fatalf("SATAT_CONFIG_SHA256 = %q, want a sha256 in hex", first)
	}
	if again := hash(); again != first {
		t.Fatalf("hash changed with satat.yaml unchanged: %s then %s", first, again)
	}
	if err := os.WriteFile(filepath.Join(tg.Dir, "satat.yaml"), []byte(testConfig+"# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if edited := hash(); edited == first {
		t.Fatal("hash unchanged after satat.yaml was edited")
	}
}

// A config outside the repo root (the e2e stack's, say) still builds the worker from the repo:
// Up passes Root as the build context, and Dir when there is no Root.
func TestUpBuildsWorkerFromRoot(t *testing.T) {
	tg, f, _, cfg := setup(t)
	if !strings.Contains(string(mustRead(t, filepath.Join(tg.Dir, "compose.yaml"))), "context: ${SATAT_BUILD_CONTEXT:-.}") {
		t.Fatal("compose.yaml does not take the worker's build context from SATAT_BUILD_CONTEXT")
	}
	if err := os.WriteFile(filepath.Join(tg.Dir, ".env"), []byte("POSTGRES_PASSWORD=p\nHATCHET_CLIENT_TOKEN=eyJold\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	buildContext := func() string {
		t.Helper()
		platformUp, workerUp := true, false
		f.respond = coldStack(&platformUp, &workerUp)
		f.calls = nil
		if err := tg.Up(context.Background(), cfg, secrets()); err != nil {
			t.Fatal(err)
		}
		for _, c := range f.calls {
			if strings.Join(c.args, " ") != "build worker" {
				continue
			}
			for _, kv := range c.env {
				if v, ok := strings.CutPrefix(kv, "SATAT_BUILD_CONTEXT="); ok {
					return v
				}
			}
			t.Fatal("worker build env lacks SATAT_BUILD_CONTEXT")
		}
		t.Fatalf("no worker build in %q", f.summary())
		return ""
	}

	if v := buildContext(); v != tg.Dir {
		t.Errorf("SATAT_BUILD_CONTEXT = %q with no Root, want Dir %s", v, tg.Dir)
	}
	tg.Root = t.TempDir()
	writeSource(t, tg.Root)
	if v := buildContext(); v != tg.Root {
		t.Errorf("SATAT_BUILD_CONTEXT = %q, want Root %s", v, tg.Root)
	}
}

// Up tags the worker image with a hash of its source and builds only when no image has the tag:
// a rebuild from cache is not reliably the same image ID, and compose recreates a worker whose
// image ID changed, orphaning running agents (#224). The tag goes to the build and to the up.
func TestUpBuildsWorkerOnlyWhenItsSourceChanged(t *testing.T) {
	tg, f, _, cfg := setup(t)
	if !strings.Contains(string(mustRead(t, filepath.Join(tg.Dir, "compose.yaml"))), "image: satat-worker:${SATAT_WORKER_TAG:-dev}") {
		t.Fatal("compose.yaml does not tag the worker image with SATAT_WORKER_TAG")
	}
	if err := os.WriteFile(filepath.Join(tg.Dir, ".env"), []byte("POSTGRES_PASSWORD=p\nHATCHET_CLIENT_TOKEN=eyJold\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	images := map[string]bool{}
	platformUp, workerUp := true, true
	f.respond = func(args []string) ([]byte, error) {
		s := strings.Join(args, " ")
		if ref, ok := strings.CutPrefix(s, "image inspect "); ok {
			if images[ref] {
				return []byte("[{}]"), nil
			}
			return nil, errors.New("No such image")
		}
		return coldStack(&platformUp, &workerUp)(args)
	}
	up := func() (built bool, tag string) {
		t.Helper()
		f.calls = nil
		if err := tg.Up(context.Background(), cfg, secrets()); err != nil {
			t.Fatal(err)
		}
		for _, c := range f.calls {
			switch strings.Join(c.args, " ") {
			case "build worker":
				built = true
			case "up -d --no-build worker":
				for _, kv := range c.env {
					if v, ok := strings.CutPrefix(kv, "SATAT_WORKER_TAG="); ok {
						tag = v
					}
				}
			}
		}
		if tag == "" {
			t.Fatal("worker compose up env lacks SATAT_WORKER_TAG")
		}
		if built {
			images["satat-worker:"+tag] = true
		}
		return built, tag
	}

	built, first := up()
	if !built {
		t.Fatal("no build with no image for the source")
	}
	if built, again := up(); built || again != first {
		t.Fatalf("second up: built %v, tag %s then %s; want no build and the same tag", built, first, again)
	}
	if err := os.WriteFile(filepath.Join(tg.Dir, "main.go"), []byte("package main // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if built, edited := up(); !built || edited == first {
		t.Fatalf("after a .go edit: built %v, tag %s then %s; want a build and a new tag", built, first, edited)
	}
}

func TestUpRefusesMissingSatatYAML(t *testing.T) {
	tg, f, _, cfg := setup(t)
	platformUp, workerUp := true, true
	f.respond = coldStack(&platformUp, &workerUp)
	if err := os.Remove(filepath.Join(tg.Dir, "satat.yaml")); err != nil {
		t.Fatal(err)
	}
	err := tg.Up(context.Background(), cfg, secrets())
	if err == nil || !strings.Contains(err.Error(), "satat.yaml") {
		t.Fatalf("err = %v, want an error naming satat.yaml", err)
	}
	if slices.Contains(f.summary(), "up -d --no-build worker") {
		t.Fatal("worker brought up without satat.yaml to mount")
	}
}

// writeSource writes the files workerSource reads, so dir can be a worker build context.
func writeSource(t *testing.T, dir string) {
	t.Helper()
	for name, data := range map[string]string{
		"go.mod":                   "module example\n",
		"go.sum":                   "",
		"main.go":                  "package main\n",
		"images/worker/Dockerfile": "FROM scratch\n",
	} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Recreating the worker orphans agents still running, so up warns before the worker's up.
func TestUpWarnsAboutRunningAgents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		running func(context.Context) (int, error)
		want    string
	}{
		{"none", func(context.Context) (int, error) { return 0, nil }, ""},
		{"two", func(context.Context) (int, error) { return 2, nil }, "warning: 2 agent containers are running"},
		{"error", func(context.Context) (int, error) { return 0, errors.New("daemon gone") }, "warning: could not check for running agents: daemon gone"},
		{"nil", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg, f, out, cfg := setup(t)
			if err := os.WriteFile(filepath.Join(tg.Dir, ".env"), []byte("POSTGRES_PASSWORD=p\nHATCHET_CLIENT_TOKEN=eyJold\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			platformUp, workerUp := true, true
			f.respond = coldStack(&platformUp, &workerUp)
			callsAtCheck := -1
			if tc.running != nil {
				tg.Running = func(ctx context.Context) (int, error) {
					callsAtCheck = len(f.calls)
					return tc.running(ctx)
				}
			}

			if err := tg.Up(context.Background(), cfg, secrets()); err != nil {
				t.Fatal(err)
			}
			if tc.running != nil && (callsAtCheck < 0 || f.calls[callsAtCheck].args[0] != "up") {
				t.Errorf("Running not checked just before the worker's up (calls %q)", f.summary())
			}
			hasWarning := strings.Contains(out.String(), "warning")
			if tc.want == "" && hasWarning {
				t.Errorf("unexpected warning:\n%s", out)
			}
			if tc.want != "" && !strings.Contains(out.String(), tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
		})
	}
}

func TestUpTimeoutReportsFailingCheck(t *testing.T) {
	tg, f, _, cfg := setup(t)
	if err := os.WriteFile(filepath.Join(tg.Dir, ".env"), []byte("POSTGRES_PASSWORD=p\nHATCHET_CLIENT_TOKEN=eyJold\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.respond = func(args []string) ([]byte, error) {
		switch s := strings.Join(args, " "); {
		case s == "ps --all --format json":
			return []byte(healthy + workerBad), nil
		case s == "up -d --no-build worker", strings.HasPrefix(s, "image inspect satat-worker:"):
			return nil, nil
		case s == "exec -T worker /usr/local/bin/satat-worker healthcheck":
			return []byte(`{"status":"unready","failing":{"hatchet":"worker not registered"}}` + "\n"), errors.New("exit status 1")
		default:
			return nil, errors.New("unscripted: " + s)
		}
	}
	err := tg.Up(context.Background(), cfg, secrets())
	if err == nil {
		t.Fatal("Up succeeded with a worker that is never healthy")
	}
	for _, want := range []string{"worker", "hatchet", "worker not registered"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
}

func TestDownPassesVolumesFlag(t *testing.T) {
	for _, removeData := range []bool{false, true} {
		tg, f, _, _ := setup(t)
		f.respond = func([]string) ([]byte, error) { return nil, nil }
		reaped := false
		tg.Reap = func(context.Context) (int, error) { reaped = true; return 2, nil }

		if err := tg.Down(context.Background(), removeData); err != nil {
			t.Fatal(err)
		}
		want := []string{"down"}
		if removeData {
			want = []string{"down -v"}
		}
		if got := f.summary(); !slices.Equal(got, want) {
			t.Errorf("removeData=%v: calls = %q, want %q", removeData, got, want)
		}
		if !reaped {
			t.Errorf("removeData=%v: Reap not called", removeData)
		}
	}
}

// down -v deletes Hatchet's volumes, so the token in the env file would be dead on the next up.
// The password stays: a fresh Postgres volume adopts it.
func TestDownVolumesForgetsHatchetToken(t *testing.T) {
	for _, removeData := range []bool{false, true} {
		tg, f, _, _ := setup(t)
		f.respond = func([]string) ([]byte, error) { return nil, nil }
		env := filepath.Join(tg.Dir, ".env")
		if err := os.WriteFile(env, []byte("POSTGRES_PASSWORD=keep-me\nHATCHET_CLIENT_TOKEN=eyJold\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := tg.Down(context.Background(), removeData); err != nil {
			t.Fatal(err)
		}
		if pw, ok, _ := deploy.ReadKey(env, "POSTGRES_PASSWORD"); !ok || pw != "keep-me" {
			t.Errorf("removeData=%v: POSTGRES_PASSWORD = %q %v, want kept", removeData, pw, ok)
		}
		_, ok, _ := deploy.ReadKey(env, "HATCHET_CLIENT_TOKEN")
		if ok == removeData {
			t.Errorf("removeData=%v: HATCHET_CLIENT_TOKEN present = %v", removeData, ok)
		}
	}
}

func TestDownReportsReapError(t *testing.T) {
	tg, f, _, _ := setup(t)
	f.respond = func([]string) ([]byte, error) { return nil, nil }
	tg.Reap = func(context.Context) (int, error) { return 0, errors.New("list containers: boom") }
	if err := tg.Down(context.Background(), false); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the reap error", err)
	}
}

// Commands other than the worker's up still interpolate the whole compose.yaml, so a required
// variable that is not set must be given a placeholder or compose refuses to run.
func TestPlaceholdersForRequiredVariables(t *testing.T) {
	tg, f, _, _ := setup(t)
	f.respond = func([]string) ([]byte, error) { return nil, nil }
	if err := tg.Down(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	env := f.calls[0].env
	for _, name := range []string{"POSTGRES_PASSWORD", "HATCHET_CLIENT_TOKEN", "FOO", "SATAT_GITHUB_TOKEN", "SATAT_PUSH_TOKEN"} {
		if _, set := os.LookupEnv(name); set {
			continue
		}
		if !slices.Contains(env, name+"=unused") {
			t.Errorf("down env lacks a placeholder for %s", name)
		}
	}
}

func TestStatus(t *testing.T) {
	tg, f, _, _ := setup(t)
	const detail = `{"status":"ready"}`
	f.respond = func(args []string) ([]byte, error) {
		switch s := strings.Join(args, " "); s {
		case "ps --all --format json":
			return []byte(healthy + workerOK), nil
		case "exec -T worker /usr/local/bin/satat-worker healthcheck":
			return []byte(detail + "\n"), nil
		default:
			return nil, errors.New("unscripted: " + s)
		}
	}
	st, err := tg.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Services) != 4 {
		t.Fatalf("services = %+v", st.Services)
	}
	for _, s := range st.Services {
		if !s.Healthy || s.State != "running" {
			t.Errorf("%s: %+v, want running and healthy", s.Name, s)
		}
		if s.Name == "worker" && s.Detail != detail {
			t.Errorf("worker detail = %q, want %q", s.Detail, detail)
		}
	}
}

func TestParsePSAcceptsAnArray(t *testing.T) {
	got, err := parsePS([]byte(`[{"Service":"postgres","State":"running","Health":"healthy"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Service != "postgres" || !got[0].healthy() {
		t.Fatalf("got %+v", got)
	}
}
