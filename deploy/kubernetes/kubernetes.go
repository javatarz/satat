// Package kubernetes is the deploy target for a Kubernetes cluster (k3d on the laptop first). It
// renders the lane objects and the chart values from satat.yaml, and drives docker, kubectl and
// helm on a named kube context only, never the shell's current one.
package kubernetes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/deploy"
	"github.com/javatarz/satat/deploy/helm"
	"github.com/javatarz/satat/launcher"
	k8slauncher "github.com/javatarz/satat/launcher/kubernetes"
)

// Runner runs an external command with stdin and returns its stdout. The output is returned on
// failure too, because `satat-worker healthcheck` exits 1 when unready and its JSON says why.
type Runner interface {
	Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands with os/exec, in the caller's environment.
type ExecRunner struct{}

// Run runs name with args. A failure carries the tail of stderr, never stdout: stdout can hold a
// token (hatchet-admin prints one).
func (ExecRunner) Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
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

// DefaultContext is the kube context scripts/k3d-up.sh creates.
const DefaultContext = "k3d-satat"

// Target deploys onto the cluster of kube context Context. Config is the satat.yaml installed as
// the worker's config (default Dir/satat.yaml). Dir holds satat.yaml, its render
// (satat.k8s/) and the env file; Root is the repo root, the platform image's build context.
// Registry is where the platform image is pushed, and where the cluster pulls it from.
type Target struct {
	Dir, Root, Context, Registry string
	Config                       string
	Run                          Runner
	Out                          io.Writer

	// Zero means the defaults; tests shorten them.
	poll, timeout time.Duration
}

var _ deploy.Target = (*Target)(nil)

const (
	release = "satat"
	// fieldManager owns the Secrets deploy up applies server-side.
	fieldManager = "satat"

	passwordSecret = "satat-postgres"
	passwordKey    = "POSTGRES_PASSWORD"
	tokenSecret    = "satat-hatchet-token"
	tokenKey       = "HATCHET_CLIENT_TOKEN"
	workerSecret   = "satat-worker-secrets"
	laneSecret     = "satat-lane-secrets"

	platformRepo = "satat-platform"
	platformTag  = "dev"

	managedLabel = "satat.dev/managed=true"
	podSelector  = "app.kubernetes.io/instance=" + release

	healthcheckBin = "/usr/local/bin/satat-worker"

	defaultPoll    = 2 * time.Second
	defaultTimeout = 3 * time.Minute
	helmTimeout    = "10m"
)

// crds are the agent-sandbox resources the lane objects and the worker use.
var crds = []string{
	k8slauncher.ClaimGVR.Resource + "." + k8slauncher.ClaimGVR.Group,
	k8slauncher.TemplateGVR.Resource + "." + k8slauncher.TemplateGVR.Group,
	k8slauncher.WarmPoolGVR.Resource + "." + k8slauncher.WarmPoolGVR.Group,
}

// pushDigest is the line `docker push` ends with: "<tag>: digest: sha256:<hex> size: <n>".
var pushDigest = regexp.MustCompile(`(?m)^` + platformTag + `: digest: (sha256:[0-9a-f]{64}) size: [0-9]+\s*$`)

// Up brings the platform up, idempotently (spec decision 10): check the render, the context and
// agent-sandbox; build and push the platform image; apply the lane objects; create the Postgres
// password once and apply every other Secret from secrets; install the chart, minting the
// Hatchet token on the first run with the worker at zero replicas; then wait for the worker.
// Secret values reach kubectl on stdin only.
func (t *Target) Up(ctx context.Context, cfg config.Config, secrets deploy.Secrets) error {
	files, err := t.Render(cfg)
	if err != nil {
		return err
	}
	for _, name := range []string{valuesFile, lanesFile} {
		have, err := os.ReadFile(filepath.Join(t.Dir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !bytes.Equal(have, files[name]) {
			return fmt.Errorf("%s is out of date: run satat deploy render", filepath.Join(t.Dir, name))
		}
	}
	if t.Registry == "" {
		return errors.New("no registry for the platform image: set SATAT_REGISTRY or --registry (scripts/k3d-up.sh prints it)")
	}
	if err := t.checkContext(ctx); err != nil {
		return err
	}
	if err := t.checkCRDs(ctx); err != nil {
		return err
	}
	t.warnRemovedLanes(ctx, cfg)

	ref, err := t.buildAndPush(ctx)
	if err != nil {
		return err
	}

	if _, err := t.kubectl(ctx, namespaceManifest(Namespace), "apply", "-f", "-"); err != nil {
		return err
	}
	lanes, err := withImage(files[lanesFile], ref)
	if err != nil {
		return err
	}
	t.say("applying lane objects")
	if _, err := t.kubectl(ctx, lanes, "apply", "-f", "-"); err != nil {
		return err
	}

	if err := t.ensurePassword(ctx); err != nil {
		return err
	}
	if err := t.applySecrets(ctx, cfg, secrets); err != nil {
		return err
	}

	chart, err := os.MkdirTemp("", "satat-chart-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(chart)
	chartDir := filepath.Join(chart, "satat")
	if err := helm.WriteTo(chartDir); err != nil {
		return fmt.Errorf("writing the chart: %w", err)
	}

	haveToken, err := t.secretExists(ctx, tokenSecret)
	if err != nil {
		return err
	}
	if haveToken {
		if err := t.restoreToken(ctx); err != nil {
			return err
		}
	} else {
		// The worker can't start without the token, and the token needs a running Hatchet.
		t.say("installing the platform without the worker")
		if err := t.install(ctx, chartDir, ref, 0); err != nil {
			return err
		}
		if err := t.createToken(ctx); err != nil {
			return err
		}
	}
	t.say("installing the platform")
	if err := t.install(ctx, chartDir, ref, 1); err != nil {
		return err
	}
	if err := t.waitReady(ctx); err != nil {
		return err
	}
	t.say("all services healthy")
	return nil
}

// checkContext refuses a context kubectl doesn't know, before anything acts on a cluster.
func (t *Target) checkContext(ctx context.Context) error {
	out, err := t.kubectl(ctx, nil, "config", "get-contexts", "-o", "name")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(string(out)), t.kubeContext()) {
		return fmt.Errorf("kube context %q does not exist: create it with scripts/k3d-up.sh, or pass --context", t.kubeContext())
	}
	return nil
}

func (t *Target) checkCRDs(ctx context.Context) error {
	out, err := t.kubectl(ctx, nil, append(append([]string{"get", "crd"}, crds...), "--ignore-not-found", "-o", "name")...)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, f := range strings.Fields(string(out)) {
		have[f[strings.LastIndex(f, "/")+1:]] = true
	}
	var missing []string
	for _, crd := range crds {
		if !have[crd] {
			missing = append(missing, crd)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("agent-sandbox is not installed on %s (missing %s): run scripts/k3d-up.sh",
			t.kubeContext(), strings.Join(missing, ", "))
	}
	return nil
}

// warnRemovedLanes says which lane namespaces exist on the cluster but not in satat.yaml. They are
// left alone: deleting one would delete its running claims. A failed check is a warning too.
func (t *Target) warnRemovedLanes(ctx context.Context, cfg config.Config) {
	have, err := t.managedNamespaces(ctx)
	if err != nil {
		t.say("warning: could not list lane namespaces: %v", err)
		return
	}
	for _, ns := range have {
		if !slices.ContainsFunc(cfg.Lanes, func(l launcher.Lane) bool { return k8slauncher.Namespace("", l.Name) == ns }) {
			t.say("warning: namespace %s is not a lane in satat.yaml; it was left in place "+
				"(kubectl --context %s delete namespace %s removes it)", ns, t.kubeContext(), ns)
		}
	}
}

// buildAndPush builds the platform image from Root, pushes it and returns it by digest, which
// `docker push` prints last.
func (t *Target) buildAndPush(ctx context.Context) (string, error) {
	repo := strings.TrimSuffix(t.Registry, "/") + "/" + platformRepo
	tag := repo + ":" + platformTag
	t.say("building %s", tag)
	if _, err := t.Run.Run(ctx, nil, "docker", "build",
		"-f", filepath.Join(t.Root, "images", "worker", "Dockerfile"),
		"--build-arg", "GO_IMAGE="+deploy.GoImage,
		"--build-arg", "BASE_IMAGE="+deploy.BaseImage,
		"-t", tag, t.Root); err != nil {
		return "", err
	}
	t.say("pushing %s", tag)
	out, err := t.Run.Run(ctx, nil, "docker", "push", tag)
	if err != nil {
		return "", err
	}
	m := pushDigest.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("docker push %s printed no %q line with the digest", tag, platformTag+": digest: sha256:…")
	}
	return repo + "@" + string(m[1]), nil
}

// ensurePassword creates the Postgres password Secret if it's absent, and never replaces it: a
// new password would lock out the existing volume. create, not apply, so a race fails safe.
func (t *Target) ensurePassword(ctx context.Context) error {
	ok, err := t.secretExists(ctx, passwordSecret)
	if err != nil || ok {
		return err
	}
	password, err := deploy.RandomPassword()
	if err != nil {
		return err
	}
	manifest, err := secretManifest(Namespace, passwordSecret, map[string]string{passwordKey: password})
	if err != nil {
		return err
	}
	if _, err := t.kubectl(ctx, manifest, "create", "-f", "-"); err != nil {
		return fmt.Errorf("creating Secret %s: %w", passwordSecret, err)
	}
	t.say("created Secret %s/%s", Namespace, passwordSecret)
	return nil
}

// applySecrets applies each lane's Secret (its pass_env values) and the worker's own. A lane that
// passes nothing gets none, and its template mounts no Secret.
func (t *Target) applySecrets(ctx context.Context, cfg config.Config, secrets deploy.Secrets) error {
	pick := func(names []string) (map[string]string, error) {
		data := map[string]string{}
		for _, n := range names {
			v, ok := secrets[n]
			if !ok {
				return nil, fmt.Errorf("secret %s was not resolved", n)
			}
			data[n] = v
		}
		return data, nil
	}
	lanes := slices.Clone(cfg.Lanes)
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].Name < lanes[j].Name })
	for _, l := range lanes {
		if len(l.PassEnv) == 0 {
			continue
		}
		data, err := pick(l.PassEnv)
		if err != nil {
			return fmt.Errorf("lane %q: %w", l.Name, err)
		}
		if err := t.applySecret(ctx, k8slauncher.Namespace("", l.Name), laneSecret, data); err != nil {
			return err
		}
	}
	data, err := pick(config.WorkerSecrets)
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	return t.applySecret(ctx, Namespace, workerSecret, data)
}

func (t *Target) applySecret(ctx context.Context, ns, name string, data map[string]string) error {
	manifest, err := secretManifest(ns, name, data)
	if err != nil {
		return err
	}
	// Server-side: a client-side apply would copy the values into the last-applied annotation.
	if _, err := t.kubectl(ctx, manifest, "apply", "--server-side", "--field-manager="+fieldManager, "-f", "-"); err != nil {
		return fmt.Errorf("applying Secret %s/%s: %w", ns, name, err)
	}
	return nil
}

func (t *Target) secretExists(ctx context.Context, name string) (bool, error) {
	out, err := t.kubectl(ctx, nil, "get", "secret", name, "-n", Namespace, "--ignore-not-found", "-o", "name")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// install is `helm upgrade --install` of the embedded chart, waiting for it to be ready.
func (t *Target) install(ctx context.Context, chartDir, ref string, replicas int) error {
	_, err := t.helm(ctx, "upgrade", "--install", release, chartDir,
		"--namespace", Namespace,
		"--values", filepath.Join(t.Dir, valuesFile),
		"--set-file", "config="+t.configFile(),
		"--set", "image.ref="+ref,
		"--set", fmt.Sprintf("worker.replicas=%d", replicas),
		"--wait", "--timeout", helmTimeout)
	return err
}

// createToken is scripts/hatchet-token.sh in the cluster: look up the default tenant, mint a token
// for it, store it as a Secret and in the env file for the satat agent CLI. It is never printed.
func (t *Target) createToken(ctx context.Context) error {
	out, err := t.kubectl(ctx, nil, "exec", "-n", Namespace, "statefulset/postgres", "-c", "postgres", "--",
		"psql", "-U", "hatchet", "-d", "hatchet", "-tAc", `select id from "Tenant" where slug = 'default'`)
	if err != nil {
		return fmt.Errorf("looking up the Hatchet tenant: %w", err)
	}
	tenant := strings.TrimSpace(string(out))
	if tenant == "" {
		return errors.New("no 'default' tenant found: is Hatchet healthy?")
	}
	out, err = t.kubectl(ctx, nil, "exec", "-n", Namespace, "deploy/hatchet-lite", "-c", "hatchet-lite", "--",
		"/hatchet-admin", "token", "create", "--config", "/config", "--tenant-id", tenant,
		"--name", "satat-"+time.Now().UTC().Format("20060102150405"))
	if err != nil {
		return fmt.Errorf("creating the Hatchet token: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if !strings.HasPrefix(token, "eyJ") {
		return errors.New("unexpected output from hatchet-admin (not a token)")
	}
	if err := t.applySecret(ctx, Namespace, tokenSecret, map[string]string{tokenKey: token}); err != nil {
		return err
	}
	// A token already in the env file belongs to a Hatchet that's gone (the Secret was absent).
	envFile := t.envFile()
	if _, err := deploy.RemoveKey(envFile, tokenKey); err != nil {
		return err
	}
	if _, err := deploy.EnsureKey(envFile, tokenKey, func() (string, error) { return token, nil }); err != nil {
		return err
	}
	t.say("created %s (Secret %s/%s and %s)", tokenKey, Namespace, tokenSecret, envFile)
	return nil
}

// restoreToken copies the token Secret into the env file when the file has none (a fresh checkout,
// say) or a different one (a recreated cluster), for the satat agent CLI. The value is read from
// kubectl's stdout only and neither value is ever printed.
func (t *Target) restoreToken(ctx context.Context) error {
	envFile := t.envFile()
	have, _, err := deploy.ReadKey(envFile, tokenKey)
	if err != nil {
		return err
	}
	out, err := t.kubectl(ctx, nil, "get", "secret", tokenSecret, "-n", Namespace,
		"-o", "jsonpath={.data."+tokenKey+"}")
	if err != nil {
		return fmt.Errorf("reading Secret %s: %w", tokenSecret, err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil || len(raw) == 0 {
		// The decode error names an offset only, but say nothing of the content anyway.
		return fmt.Errorf("the token Secret %s/%s has no readable %s", Namespace, tokenSecret, tokenKey)
	}
	token := string(raw)
	if have == token {
		return nil
	}
	if _, err := deploy.RemoveKey(envFile, tokenKey); err != nil { // an empty or stale line
		return err
	}
	if _, err := deploy.EnsureKey(envFile, tokenKey, func() (string, error) { return token, nil }); err != nil {
		return err
	}
	if have != "" {
		t.say("replaced %s in %s: it differed from Secret %s/%s", tokenKey, envFile, Namespace, tokenSecret)
	} else {
		t.say("restored %s in %s from Secret %s/%s", tokenKey, envFile, Namespace, tokenSecret)
	}
	return nil
}

// waitReady polls the worker's own readiness check until it passes.
func (t *Target) waitReady(ctx context.Context) error {
	timeout := or(t.timeout, defaultTimeout)
	deadline := time.Now().Add(timeout)
	for {
		detail, err := t.healthcheck(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("worker not ready after %s; readiness now: %s", timeout, detail)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(or(t.poll, defaultPoll)):
		}
	}
}

// healthcheck runs the worker's readiness check in its pod and returns its JSON, or why it could
// not run. err is non-nil when the worker is not ready.
func (t *Target) healthcheck(ctx context.Context) (string, error) {
	out, err := t.kubectl(ctx, nil, "exec", "-n", Namespace, "deploy/satat-worker", "-c", "worker", "--",
		healthcheckBin, "healthcheck")
	if s := strings.TrimSpace(string(out)); s != "" {
		return s, err
	}
	if err != nil {
		return "healthcheck did not run: " + err.Error(), err
	}
	return "healthcheck printed nothing", nil
}

// Down deletes every SandboxClaim in the lane namespaces, uninstalls the release and deletes the
// lane namespaces with their Secrets. Namespace satat keeps the password, the worker Secret and
// the volumes. removeData also deletes the volumes and the now-dead Hatchet token (Secret and env
// file line), keeping the password: a fresh volume adopts it.
func (t *Target) Down(ctx context.Context, removeData bool) error {
	lanes, err := t.managedNamespaces(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, ns := range lanes {
		if _, err := t.kubectl(ctx, nil, "delete", crds[0], "--all", "-n", ns, "--ignore-not-found", "--wait=true"); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := t.helm(ctx, "uninstall", release, "--namespace", Namespace, "--ignore-not-found", "--wait"); err != nil {
		// The volumes may still be in use: leave the data alone.
		return errors.Join(append(errs, err)...)
	}
	if len(lanes) > 0 {
		args := append(append([]string{"delete", "namespace"}, lanes...), "--ignore-not-found", "--wait=true")
		if _, err := t.kubectl(ctx, nil, args...); err != nil {
			errs = append(errs, err)
		}
	}
	if removeData {
		if _, err := t.kubectl(ctx, nil, "delete", "persistentvolumeclaim", "--all", "-n", Namespace,
			"--ignore-not-found", "--wait=true"); err != nil {
			errs = append(errs, err)
		} else if _, err := t.kubectl(ctx, nil, "delete", "secret", tokenSecret, "-n", Namespace, "--ignore-not-found"); err != nil {
			errs = append(errs, err)
		} else if removed, err := deploy.RemoveKey(t.envFile(), tokenKey); err != nil {
			errs = append(errs, err)
		} else if removed {
			t.say("removed %s from %s", tokenKey, t.envFile())
		}
	}
	return errors.Join(errs...)
}

// managedNamespaces lists the lane namespaces on the cluster, by label, sorted.
func (t *Target) managedNamespaces(ctx context.Context) ([]string, error) {
	out, err := t.kubectl(ctx, nil, "get", "namespace", "-l", managedLabel, "-o", "name")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range strings.Fields(string(out)) {
		names = append(names, strings.TrimPrefix(f, "namespace/"))
	}
	sort.Strings(names)
	return names, nil
}

type podList struct {
	Items []struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Status struct {
			Phase      string `json:"phase"`
			Conditions []struct {
				Type, Status string
			} `json:"conditions"`
			ContainerStatuses []struct {
				State struct {
					Waiting *struct {
						Reason string `json:"reason"`
					} `json:"waiting"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

// Status reports each platform pod by its component: ready, and its phase or the reason a
// container is waiting. A running worker's Detail is its readiness JSON.
func (t *Target) Status(ctx context.Context) (deploy.Status, error) {
	out, err := t.kubectl(ctx, nil, "get", "pods", "-n", Namespace, "-l", podSelector, "-o", "json")
	if err != nil {
		return deploy.Status{}, err
	}
	var pods podList
	if err := json.Unmarshal(out, &pods); err != nil {
		return deploy.Status{}, fmt.Errorf("parsing kubectl get pods: %w", err)
	}
	var st deploy.Status
	for _, p := range pods.Items {
		s := deploy.ServiceStatus{
			Name:  p.Metadata.Labels["app.kubernetes.io/name"],
			State: strings.ToLower(p.Status.Phase),
		}
		for _, c := range p.Status.Conditions {
			s.Healthy = s.Healthy || (c.Type == "Ready" && c.Status == "True")
		}
		for _, c := range p.Status.ContainerStatuses {
			if c.State.Waiting != nil && c.State.Waiting.Reason != "" {
				s.State = c.State.Waiting.Reason
			}
		}
		if s.Name == "worker" && s.State == "running" {
			s.Detail, _ = t.healthcheck(ctx)
		}
		st.Services = append(st.Services, s)
	}
	sort.SliceStable(st.Services, func(i, j int) bool { return st.Services[i].Name < st.Services[j].Name })
	return st, nil
}

// kubectl runs kubectl on the target's context, whatever the shell's current one is.
func (t *Target) kubectl(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	return t.Run.Run(ctx, stdin, "kubectl", append([]string{"--context", t.kubeContext()}, args...)...)
}

// helm runs helm on the target's context.
func (t *Target) helm(ctx context.Context, args ...string) ([]byte, error) {
	return t.Run.Run(ctx, nil, "helm", append([]string{"--kube-context", t.kubeContext()}, args...)...)
}

func (t *Target) kubeContext() string {
	if t.Context == "" {
		return DefaultContext
	}
	return t.Context
}

func (t *Target) envFile() string { return filepath.Join(t.Dir, ".env") }

func (t *Target) configFile() string {
	if t.Config != "" {
		return t.Config
	}
	return filepath.Join(t.Dir, "satat.yaml")
}

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

// namespaceManifest is the platform namespace. It enforces Pod Security baseline, not restricted
// as the lane namespaces do: Postgres and hatchet-lite need writable root filesystems.
func namespaceManifest(name string) []byte {
	return []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: " + name + "\n" +
		"  labels:\n    pod-security.kubernetes.io/enforce: baseline\n")
}

// secretManifest is a Secret with base64 data, for kubectl's stdin only. Not stringData: the API
// server turns it into data, so server-side apply's field manager would never own data.<KEY>, and
// a key dropped from the manifest (a name removed from pass_env) would stay in the Secret.
func secretManifest(ns, name string, values map[string]string) ([]byte, error) {
	data := make(map[string]string, len(values))
	for k, v := range values {
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"type":       "Opaque",
		"data":       data,
	})
}
