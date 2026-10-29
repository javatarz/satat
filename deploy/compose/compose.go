// Package compose is the deploy target for Docker Compose on one machine (ADR 0026).
package compose

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/deploy"
)

//go:embed compose.yaml.tmpl
var composeTmpl string

var tmpl = template.Must(template.New("compose.yaml").Parse(composeTmpl))

// Runner runs an external command with exactly env as its environment and returns its stdout.
// The output is returned on failure too, because `satat-worker healthcheck` exits 1 when unready
// and its JSON is what explains why.
type Runner interface {
	Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands with os/exec.
type ExecRunner struct{}

// Run runs name with args. A failure carries the tail of stderr, never stdout: stdout can hold a
// token (hatchet-admin prints one).
func (ExecRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 2048 {
			msg = "..." + msg[len(msg)-2048:]
		}
		if msg != "" {
			msg = ": " + msg
		}
		return out, fmt.Errorf("%s %s: %w%s", name, strings.Join(args, " "), err, msg)
	}
	return out, nil
}

// Target deploys with Docker Compose in Dir, where compose.yaml and the env file live.
type Target struct {
	Dir string
	// Root is the repo the worker image is built from, when it is not Dir. Empty keeps
	// compose.yaml's default, its own directory.
	Root string
	Run  Runner
	Out  io.Writer
	// Reap removes agent containers and volumes left behind, by their satat.* labels. Down calls
	// it after `docker compose down` when it is set.
	Reap func(ctx context.Context) (int, error)
	// Running counts agent containers running now, by their satat.* labels. Up calls it before the
	// worker's up, which may recreate the worker and orphan them, and warns when there are any.
	// Nil skips the check.
	Running func(ctx context.Context) (int, error)

	// Zero means the defaults; tests shorten them.
	poll, platformTimeout, workerTimeout time.Duration
}

var _ deploy.Target = (*Target)(nil)

// Render returns compose.yaml. Only the worker's secret and optional environment vary with cfg; the
// image digests are deploy's.
func (t *Target) Render(cfg config.Config) (map[string][]byte, error) {
	var buf bytes.Buffer
	data := struct {
		WorkerEnv, OptionalEnv                              []string
		PostgresImage, HatchetLiteImage, GoImage, BaseImage string
	}{
		WorkerEnv:        cfg.SecretNames(),
		OptionalEnv:      cfg.OptionalEnvNames(),
		PostgresImage:    deploy.PostgresImage,
		HatchetLiteImage: deploy.HatchetLiteImage,
		GoImage:          deploy.GoImage,
		BaseImage:        deploy.BaseImage,
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render compose.yaml: %w", err)
	}
	return map[string][]byte{"compose.yaml": buf.Bytes()}, nil
}

const (
	keyPassword = "POSTGRES_PASSWORD"
	keyToken    = "HATCHET_CLIENT_TOKEN"
	// keyConfigHash carries satat.yaml's sha256 into the worker's environment. The worker mounts
	// satat.yaml as a single file, which an editor's save replaces, and compose recreates a container
	// only when its config changes; the hash makes an edited satat.yaml such a change.
	keyConfigHash = "SATAT_CONFIG_SHA256"
	// keyBuildContext is the worker's build context, Root, for a satat.yaml outside the repo root.
	keyBuildContext = "SATAT_BUILD_CONTEXT"
	// keyWorkerTag tags the worker image with a hash of what it is built from (workerSource). A
	// rebuild from cache is not reliably the same image ID (on the containerd image store it carries
	// the build's metadata, which varies with the BuildKit version), and compose recreates a
	// container whose image ID changed, so Up builds only when no image has the tag.
	keyWorkerTag = "SATAT_WORKER_TAG"
	// workerImage is the worker image's name in compose.yaml.
	workerImage = "satat-worker"

	defaultPoll            = 2 * time.Second
	defaultPlatformTimeout = 3 * time.Minute
	defaultWorkerTimeout   = 3 * time.Minute
)

// platform is what the worker needs before it can start, in the order compose is given them.
var platform = []string{"postgres", "hatchet-lite", "docker-proxy"}

// Up does what the operator used to do by hand: check the render, create the Postgres password and
// the Hatchet token once, start the platform, then the worker with the secrets in its environment,
// waiting for each to be healthy. A platform service already running and healthy is left alone.
// The worker's up always runs, so a change rolls out; on an unchanged healthy stack compose
// recreates nothing, and no token or password is created.
func (t *Target) Up(ctx context.Context, cfg config.Config, secrets deploy.Secrets) error {
	files, err := t.Render(cfg)
	if err != nil {
		return err
	}
	have, err := os.ReadFile(t.file())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !bytes.Equal(have, files["compose.yaml"]) {
		return fmt.Errorf("%s is out of date: run satat deploy render", t.file())
	}
	mounted, err := os.ReadFile(t.configFile())
	if err != nil {
		return fmt.Errorf("reading the worker's config: %w", err)
	}
	configHash := sha256.Sum256(mounted)

	envFile := t.envFile()
	for _, key := range []string{keyPassword, keyToken} {
		v, ok, err := deploy.ReadKey(envFile, key)
		if err != nil {
			return err
		}
		if ok && v == "" {
			// Never filled in here: a new password would lock out the existing Postgres volume.
			return fmt.Errorf("%s is empty in %s: set it or delete the line", key, envFile)
		}
	}
	created, err := deploy.EnsureKey(envFile, keyPassword, deploy.RandomPassword)
	if err != nil {
		return err
	}
	if created {
		t.say("generated %s in %s", keyPassword, envFile)
	}

	entries, err := t.ps(ctx)
	if err != nil {
		return err
	}
	if start := notHealthy(entries, platform); len(start) > 0 {
		t.say("starting %s", strings.Join(start, " "))
		if _, err := t.compose(ctx, t.env(), append([]string{"up", "-d"}, start...)...); err != nil {
			return err
		}
		if err := t.wait(ctx, start, or(t.platformTimeout, defaultPlatformTimeout)); err != nil {
			return err
		}
	}

	if _, ok, err := deploy.ReadKey(envFile, keyToken); err != nil {
		return err
	} else if !ok {
		if err := t.createToken(ctx, envFile); err != nil {
			return err
		}
		t.say("created %s in %s", keyToken, envFile)
	}

	// The worker is always brought up, so new worker code or a changed environment rolls out;
	// compose leaves an unchanged container alone. This is the only command that gets the real
	// secrets, and only in its environment. The config hash is part of that environment, so an
	// edited satat.yaml rolls out too.
	env := os.Environ()
	names := make([]string, 0, len(secrets))
	for name := range secrets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		env = append(env, name+"="+secrets[name])
	}
	env = append(env, keyConfigHash+"="+hex.EncodeToString(configHash[:]))
	root := t.Dir
	if t.Root != "" {
		root = t.Root
	}
	tag, err := workerSource(root, t.file())
	if err != nil {
		return err
	}
	image := []string{keyBuildContext + "=" + root, keyWorkerTag + "=" + tag}
	env = append(env, image...)
	// The build gets no secret: compose reads the whole file for it, so placeholders do.
	if _, err := t.Run.Run(ctx, os.Environ(), "docker", "image", "inspect", workerImage+":"+tag); err != nil {
		t.say("building the worker image %s:%s", workerImage, tag)
		if _, err := t.compose(ctx, append(t.env(), image...), "build", "worker"); err != nil {
			return err
		}
	}
	t.warnRunningAgents(ctx)
	t.say("starting worker")
	if _, err := t.compose(ctx, env, "up", "-d", "--no-build", "worker"); err != nil {
		return err
	}
	timeout := or(t.workerTimeout, defaultWorkerTimeout)
	if err := t.wait(ctx, []string{"worker"}, timeout); err != nil {
		if errors.Is(err, errTimeout) {
			// Docker's health lags the check by up to its interval, so give both.
			return fmt.Errorf("%w; worker readiness now: %s", err, t.healthcheck(ctx))
		}
		return err
	}
	t.say("all services healthy")
	return nil
}

// workerSource hashes what the worker image is built from: the files images/worker/Dockerfile's
// build context lets through (Dockerfile.dockerignore: go.mod, go.sum and every .go file), the
// Dockerfile and composeFile, which holds the base images' digests. It is the image's tag, so it
// must change whenever the image would. TestWorkerSourceMatchesDockerignore holds the two together.
func workerSource(root, composeFile string) (string, error) {
	files := []string{"go.mod", "go.sum", "images/worker/Dockerfile"}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".go") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hashing the worker's source: %w", err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return "", fmt.Errorf("hashing the worker's source: %w", err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", f, len(data))
		h.Write(data)
	}
	compose, err := os.ReadFile(composeFile)
	if err != nil {
		return "", fmt.Errorf("hashing the worker's source: %w", err)
	}
	fmt.Fprintf(h, "compose.yaml\x00%d\x00", len(compose))
	h.Write(compose)
	return hex.EncodeToString(h.Sum(nil))[:24], nil
}

// warnRunningAgents says when agents are running, because a recreated worker orphans them: they
// keep running, with their tokens, until they exit, the reaper removes them or deploy down, and
// agent rm can no longer stop them. A failed check is a warning too, never a failed up.
func (t *Target) warnRunningAgents(ctx context.Context) {
	if t.Running == nil {
		return
	}
	n, err := t.Running(ctx)
	switch {
	case err != nil:
		t.say("warning: could not check for running agents: %v", err)
	case n > 0:
		t.say("warning: %d agent containers are running; if the worker is recreated they are orphaned "+
			"and keep running until they exit, the reaper removes them or satat deploy down", n)
	}
}

// createToken is scripts/hatchet-token.sh: look up the default tenant, mint a token for it and
// add it to the env file. The token is never printed.
func (t *Target) createToken(ctx context.Context, envFile string) error {
	out, err := t.compose(ctx, t.env(), "exec", "-T", "postgres", "psql", "-U", "hatchet", "-d", "hatchet",
		"-tAc", `select id from "Tenant" where slug = 'default'`)
	if err != nil {
		return fmt.Errorf("looking up the Hatchet tenant: %w", err)
	}
	tenant := strings.TrimSpace(string(out))
	if tenant == "" {
		return errors.New("no 'default' tenant found: is Hatchet healthy?")
	}
	out, err = t.compose(ctx, t.env(), "exec", "-T", "hatchet-lite", "/hatchet-admin", "token", "create",
		"--config", "/config", "--tenant-id", tenant, "--name", "satat-"+time.Now().UTC().Format("20060102150405"))
	if err != nil {
		return fmt.Errorf("creating the Hatchet token: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if !strings.HasPrefix(token, "eyJ") {
		return errors.New("unexpected output from hatchet-admin (not a token)")
	}
	_, err = deploy.EnsureKey(envFile, keyToken, func() (string, error) { return token, nil })
	return err
}

// Down runs `docker compose down` (with -v when removeData), then reaps agent containers. With
// removeData it also forgets the Hatchet token: the volumes holding Hatchet's tenant and keys are
// gone, so the token is dead and the next up must mint a new one. The Postgres password is kept;
// a fresh volume adopts it.
func (t *Target) Down(ctx context.Context, removeData bool) error {
	args := []string{"down"}
	if removeData {
		args = append(args, "-v")
	}
	var errs []error
	if _, err := t.compose(ctx, t.env(), args...); err != nil {
		errs = append(errs, err)
	} else if removeData {
		if removed, err := deploy.RemoveKey(t.envFile(), keyToken); err != nil {
			errs = append(errs, err)
		} else if removed {
			t.say("removed %s from %s", keyToken, t.envFile())
		}
	}
	if t.Reap != nil {
		n, err := t.Reap(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("removing agent containers: %w", err))
		}
		if n > 0 {
			t.say("removed %d agent containers", n)
		}
	}
	return errors.Join(errs...)
}

// Status reports each service compose knows of; the worker's Detail is its readiness JSON.
func (t *Target) Status(ctx context.Context) (deploy.Status, error) {
	entries, err := t.ps(ctx)
	if err != nil {
		return deploy.Status{}, err
	}
	var st deploy.Status
	for _, e := range entries {
		s := deploy.ServiceStatus{Name: e.Service, State: e.State, Healthy: e.healthy()}
		if e.Service == "worker" && e.State == "running" {
			s.Detail = t.healthcheck(ctx)
		}
		st.Services = append(st.Services, s)
	}
	sort.Slice(st.Services, func(i, j int) bool { return st.Services[i].Name < st.Services[j].Name })
	return st, nil
}

var errTimeout = errors.New("timed out")

// wait polls `ps` until every service is running and healthy.
func (t *Target) wait(ctx context.Context, services []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		entries, err := t.ps(ctx)
		if err != nil {
			return err
		}
		pending := notHealthy(entries, services)
		if len(pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			states := make([]string, 0, len(pending))
			for _, name := range pending {
				states = append(states, name+" ("+describe(entries, name)+")")
			}
			return fmt.Errorf("waiting for %s: %w after %s", strings.Join(states, ", "), errTimeout, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(or(t.poll, defaultPoll)):
		}
	}
}

// healthcheck runs the worker's own readiness check and returns its JSON, or why it could not run.
func (t *Target) healthcheck(ctx context.Context) string {
	out, err := t.compose(ctx, t.env(), "exec", "-T", "worker", "/usr/local/bin/satat-worker", "healthcheck")
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	if err != nil {
		return "healthcheck did not run: " + err.Error()
	}
	return "healthcheck printed nothing"
}

type psEntry struct{ Service, State, Health string }

// healthy is running and, for a service with a healthcheck, passing it.
func (e psEntry) healthy() bool {
	return e.State == "running" && (e.Health == "" || e.Health == "healthy")
}

// parsePS reads `docker compose ps --format json`: one object per line (Compose 2.21 and later)
// or one array (earlier).
func parsePS(out []byte) ([]psEntry, error) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	var entries []psEntry
	if out[0] == '[' {
		if err := json.Unmarshal(out, &entries); err != nil {
			return nil, fmt.Errorf("parsing docker compose ps: %w", err)
		}
		return entries, nil
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var e psEntry
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("parsing docker compose ps: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ps lists every container of the project, stopped ones too, so an exited service shows as such.
func (t *Target) ps(ctx context.Context) ([]psEntry, error) {
	out, err := t.compose(ctx, t.env(), "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parsePS(out)
}

func notHealthy(entries []psEntry, services []string) []string {
	var out []string
	for _, name := range services {
		ok := false
		for _, e := range entries {
			ok = ok || (e.Service == name && e.healthy())
		}
		if !ok {
			out = append(out, name)
		}
	}
	return out
}

func describe(entries []psEntry, service string) string {
	for _, e := range entries {
		if e.Service == service {
			if e.Health != "" {
				return e.State + ", " + e.Health
			}
			return e.State
		}
	}
	return "not created"
}

// compose runs docker compose on Dir's compose.yaml, whatever the caller's working directory.
func (t *Target) compose(ctx context.Context, env []string, args ...string) ([]byte, error) {
	full := append([]string{"compose", "-f", t.file(), "--project-directory", t.Dir}, args...)
	return t.Run.Run(ctx, env, "docker", full...)
}

// required matches ${NAME:?msg} and ${NAME?msg}: variables compose refuses to run without.
var required = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):?\?`)

// env is the environment for every command but the worker's up. Compose interpolates the whole
// file for any command, so a required variable that is neither in the environment nor in the env
// file gets a placeholder (as scripts/hatchet-token.sh did). No container is created from one:
// only the worker uses those variables, and its up never gets a placeholder.
func (t *Target) env() []string {
	env := os.Environ()
	data, err := os.ReadFile(t.file())
	if err != nil {
		return env // compose reports the missing file itself
	}
	seen := map[string]bool{}
	for _, m := range required.FindAllStringSubmatch(string(data), -1) {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if _, ok := os.LookupEnv(name); ok {
			continue
		}
		if v, ok, _ := deploy.ReadKey(t.envFile(), name); ok && v != "" {
			continue
		}
		env = append(env, name+"=unused")
	}
	return env
}

func (t *Target) file() string    { return filepath.Join(t.Dir, "compose.yaml") }
func (t *Target) envFile() string { return filepath.Join(t.Dir, ".env") }

// configFile is the satat.yaml compose.yaml mounts into the worker (./satat.yaml).
func (t *Target) configFile() string { return filepath.Join(t.Dir, "satat.yaml") }

func (t *Target) say(format string, args ...any) {
	if t.Out != nil {
		fmt.Fprintf(t.Out, format+"\n", args...)
	}
}

func or(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
