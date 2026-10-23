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
deploy: {target: compose, secrets: {FOO: env, SATAT_PUSH_TOKEN: env}}
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
	f := &fakeRunner{t: t, dir: dir}
	out := &bytes.Buffer{}
	tg := &Target{Dir: dir, Run: f, Out: out, poll: time.Millisecond, platformTimeout: time.Second, workerTimeout: 50 * time.Millisecond}
	return tg, f, out, cfg
}

// coldStack answers as a stack that starts from nothing and comes up healthy.
func coldStack(platformUp, workerUp *bool) func([]string) ([]byte, error) {
	return func(args []string) ([]byte, error) {
		s := strings.Join(args, " ")
		switch {
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
		case s == "up -d --build worker":
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
	return deploy.Secrets{"FOO": "foo-secret-value", "SATAT_PUSH_TOKEN": "push-secret-value"}
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
		"up -d --build worker",
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
	// no hatchet-admin.
	want := []string{"ps --all --format json", "up -d --build worker", "ps --all --format json"}
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
	want := []string{"ps --all --format json", "up -d --build worker", "ps --all --format json"}
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
		if joined == "up -d --build worker" {
			workerUpEnv = c.env
		}
		for name, v := range s {
			if strings.Contains(joined, v) {
				t.Errorf("%s value in args of %q", name, joined)
			}
			if joined != "up -d --build worker" && slices.Contains(c.env, name+"="+v) {
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

func TestUpTimeoutReportsFailingCheck(t *testing.T) {
	tg, f, _, cfg := setup(t)
	if err := os.WriteFile(filepath.Join(tg.Dir, ".env"), []byte("POSTGRES_PASSWORD=p\nHATCHET_CLIENT_TOKEN=eyJold\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.respond = func(args []string) ([]byte, error) {
		switch s := strings.Join(args, " "); s {
		case "ps --all --format json":
			return []byte(healthy + workerBad), nil
		case "up -d --build worker":
			return nil, nil
		case "exec -T worker /usr/local/bin/satat-worker healthcheck":
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
	for _, name := range []string{"POSTGRES_PASSWORD", "HATCHET_CLIENT_TOKEN", "FOO", "SATAT_PUSH_TOKEN"} {
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
