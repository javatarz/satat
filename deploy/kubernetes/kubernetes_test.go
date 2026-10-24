package kubernetes

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/deploy"
)

const (
	kubeContext = "k3d-satat"
	registry    = "localhost:5111"
	digest      = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	token       = "eyJhbGciOiJ0ZXN0In0.payload.sig"
	fooValue    = "foo-secret-value"
	pushValue   = "push-secret-value"
	readValue   = "read-secret-value"
	pushed      = "The push refers to repository [localhost:5111/satat-platform]\n" +
		"5f70bf18a086: Pushed\ndev: digest: " + digest + " size: 1234\n"
	crdList = "customresourcedefinition.apiextensions.k8s.io/sandboxclaims.extensions.agents.x-k8s.io\n" +
		"customresourcedefinition.apiextensions.k8s.io/sandboxtemplates.extensions.agents.x-k8s.io\n" +
		"customresourcedefinition.apiextensions.k8s.io/sandboxwarmpools.extensions.agents.x-k8s.io\n"
)

type call struct {
	name  string
	args  []string
	stdin []byte
}

// fakeRunner records every command and answers it from script, keyed by the call's summary.
type fakeRunner struct {
	calls  []call
	script map[string]string
	fail   map[string]error
}

func (f *fakeRunner) Run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	c := call{name: name, args: slices.Clone(args), stdin: slices.Clone(stdin)}
	f.calls = append(f.calls, c)
	s := summarize(c)
	if err := f.fail[s]; err != nil {
		return []byte(f.script[s]), err
	}
	return []byte(f.script[s]), nil
}

// summarize is a call without its context flag, shortened where the args vary.
func summarize(c call) string {
	var args []string
	for i := 0; i < len(c.args); i++ {
		if c.args[i] == "--context" || c.args[i] == "--kube-context" {
			i++
			continue
		}
		args = append(args, c.args[i])
	}
	s := c.name + " " + strings.Join(args, " ")
	switch {
	case strings.HasPrefix(s, "docker build "):
		return "docker build"
	case strings.HasPrefix(s, "helm upgrade --install"):
		for _, a := range args {
			if strings.HasPrefix(a, "worker.replicas=") {
				return "helm upgrade " + a
			}
		}
		return "helm upgrade"
	case strings.HasPrefix(s, "kubectl exec ") && strings.Contains(s, " statefulset/postgres "):
		return "exec psql"
	case strings.HasPrefix(s, "kubectl exec ") && strings.Contains(s, " deploy/hatchet-lite "):
		return "exec hatchet-admin"
	case strings.HasPrefix(s, "kubectl exec ") && strings.Contains(s, " deploy/satat-worker "):
		return "exec healthcheck"
	case strings.HasPrefix(s, "kubectl apply -f -"), strings.HasPrefix(s, "kubectl create -f -"),
		strings.HasPrefix(s, serverSide):
		return s + " " + manifestName(c.stdin)
	}
	return s
}

// manifestName names what a manifest on stdin holds: kind/namespace/name of its first object, or
// "lanes" for the lane objects.
func manifestName(stdin []byte) string {
	if bytes.Contains(stdin, []byte("kind: SandboxTemplate")) {
		return "lanes"
	}
	var obj struct {
		Kind     string
		Metadata struct{ Name, Namespace string }
	}
	if err := yaml.Unmarshal(stdin, &obj); err != nil {
		return "unparsable"
	}
	return obj.Kind + "/" + obj.Metadata.Namespace + "/" + obj.Metadata.Name
}

func (f *fakeRunner) summary() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, summarize(c))
	}
	return out
}

func (f *fakeRunner) find(summary string) *call {
	for i := range f.calls {
		if summarize(f.calls[i]) == summary {
			return &f.calls[i]
		}
	}
	return nil
}

func loadConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load("testdata/satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// setup writes the render and satat.yaml into a temp dir and scripts a fresh cluster.
func setup(t *testing.T) (*Target, *fakeRunner, *bytes.Buffer, config.Config) {
	t.Helper()
	cfg := loadConfig(t)
	dir := t.TempDir()
	files, err := (&Target{}).Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src, err := os.ReadFile("testdata/satat.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "satat.yaml"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	run := &fakeRunner{script: map[string]string{
		"kubectl config get-contexts -o name": "other\n" + kubeContext + "\n",
		getCRDs:                               crdList,
		"docker push " + registry + "/satat-platform:dev": pushed,
		"exec psql":          "707d0855-80ab-4e1f-a156-f1c4546cbf52\n",
		"exec hatchet-admin": token + "\n",
		"exec healthcheck":   `{"status":"ready"}` + "\n",
	}}
	out := &bytes.Buffer{}
	target := &Target{
		Dir: dir, Root: "/repo", Context: kubeContext, Registry: registry,
		Run: run, Out: out, poll: time.Millisecond, timeout: 50 * time.Millisecond,
	}
	return target, run, out, cfg
}

var secrets = deploy.Secrets{"FOO": fooValue, "SATAT_GITHUB_TOKEN": readValue, "SATAT_PUSH_TOKEN": pushValue}

const (
	getManaged  = "kubectl get namespace -l satat.dev/managed=true -o name"
	getPassword = "kubectl get secret satat-postgres -n satat --ignore-not-found -o name"
	getToken    = "kubectl get secret satat-hatchet-token -n satat --ignore-not-found -o name"
	getCRDs     = "kubectl get crd sandboxclaims.extensions.agents.x-k8s.io sandboxtemplates.extensions.agents.x-k8s.io " +
		"sandboxwarmpools.extensions.agents.x-k8s.io --ignore-not-found -o name"
	getPods = "kubectl get pods -n satat -l app.kubernetes.io/instance=satat -o json"
	// serverSide is how every Secret is applied, so its values stay out of last-applied.
	serverSide = "kubectl apply --server-side --field-manager=satat -f -"
	readToken  = "kubectl get secret satat-hatchet-token -n satat -o jsonpath={.data.HATCHET_CLIENT_TOKEN}"
)

func TestUpOrder(t *testing.T) {
	target, run, _, cfg := setup(t)
	target.Config = filepath.Join(target.Dir, "other.yaml") // --config other.yaml
	if err := target.Up(context.Background(), cfg, secrets); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"kubectl config get-contexts -o name",
		getCRDs,
		getManaged,
		"docker build",
		"docker push " + registry + "/satat-platform:dev",
		"kubectl apply -f - Namespace//satat",
		"kubectl apply -f - lanes",
		getPassword,
		"kubectl create -f - Secret/satat/satat-postgres",
		serverSide + " Secret/satat-lane-open/satat-lane-secrets",
		serverSide + " Secret/satat/satat-worker-secrets",
		getToken,
		"helm upgrade worker.replicas=0",
		"exec psql",
		"exec hatchet-admin",
		serverSide + " Secret/satat/satat-hatchet-token",
		"helm upgrade worker.replicas=1",
		"exec healthcheck",
	}
	if got := run.summary(); !slices.Equal(got, want) {
		t.Fatalf("calls:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}

	// The password is generated here: find it, to check it never reaches argv either.
	password := secretData(t, run.find("kubectl create -f - Secret/satat/satat-postgres").stdin)["POSTGRES_PASSWORD"]
	if len(password) < 32 {
		t.Fatalf("password secret has a %d-character POSTGRES_PASSWORD", len(password))
	}
	secretApplies := 0
	for _, c := range run.calls {
		if c.name == "kubectl" && !hasPair(c.args, "--context", kubeContext) {
			t.Errorf("kubectl %q has no --context %s", c.args, kubeContext)
		}
		if bytes.Contains(c.stdin, []byte("kind: Secret")) && slices.Contains(c.args, "apply") {
			secretApplies++
			if !slices.Contains(c.args, "--server-side") || !slices.Contains(c.args, "--field-manager=satat") {
				t.Errorf("Secret applied client-side, so its values land in last-applied: %q", c.args)
			}
		}
		if c.name == "helm" && !hasPair(c.args, "--kube-context", kubeContext) {
			t.Errorf("helm %q has no --kube-context %s", c.args, kubeContext)
		}
		for _, a := range c.args {
			for _, secret := range []string{fooValue, pushValue, token, password} {
				if strings.Contains(a, secret) {
					t.Errorf("%s %q has a secret value on its command line", c.name, c.args)
				}
			}
		}
	}

	if secretApplies != 3 {
		t.Errorf("%d Secret applies, want the lane, worker and token Secrets", secretApplies)
	}

	build := run.find("docker build").args
	for _, want := range [][2]string{
		{"-f", "/repo/images/worker/Dockerfile"},
		{"--build-arg", "GO_IMAGE=" + deploy.GoImage},
		{"--build-arg", "BASE_IMAGE=" + deploy.BaseImage},
		{"-t", registry + "/satat-platform:dev"},
	} {
		if !hasPair(build, want[0], want[1]) {
			t.Errorf("docker build %q has no %s %s", build, want[0], want[1])
		}
	}
	if build[len(build)-1] != "/repo" {
		t.Errorf("docker build context = %q, want /repo", build[len(build)-1])
	}

	var ns struct {
		Metadata struct{ Labels map[string]string }
	}
	if err := yaml.Unmarshal(run.find("kubectl apply -f - Namespace//satat").stdin, &ns); err != nil {
		t.Fatal(err)
	}
	if got := ns.Metadata.Labels["pod-security.kubernetes.io/enforce"]; got != "baseline" {
		t.Errorf("namespace satat enforces Pod Security %q, want baseline", got)
	}

	lanes := string(run.find("kubectl apply -f - lanes").stdin)
	ref := registry + "/satat-platform@" + digest
	if strings.Contains(lanes, "image: "+PlatformPlaceholder+"\n") || strings.Count(lanes, "image: "+ref+"\n") != 2 {
		t.Errorf("lanes were applied without the pushed image %s in place of the placeholder", ref)
	}

	for summary, want := range map[string]map[string]string{
		serverSide + " Secret/satat-lane-open/satat-lane-secrets": {"FOO": fooValue},
		serverSide + " Secret/satat/satat-worker-secrets":         {"SATAT_GITHUB_TOKEN": readValue, "SATAT_PUSH_TOKEN": pushValue},
		serverSide + " Secret/satat/satat-hatchet-token":          {"HATCHET_CLIENT_TOKEN": token},
	} {
		got := secretData(t, run.find(summary).stdin)
		if len(got) != len(want) {
			t.Errorf("%s: %d keys, want %d", summary, len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s is not the resolved value", summary, k)
			}
		}
	}

	upgrade := run.find("helm upgrade worker.replicas=1").args
	for _, want := range [][2]string{
		{"--namespace", "satat"},
		{"--values", filepath.Join(target.Dir, "satat.k8s", "values.yaml")},
		{"--set-file", "config=" + filepath.Join(target.Dir, "other.yaml")},
		{"--set", "image.ref=" + ref},
	} {
		if !hasPair(upgrade, want[0], want[1]) {
			t.Errorf("helm %q has no %s %s", upgrade, want[0], want[1])
		}
	}
	if !slices.Contains(upgrade, "--wait") {
		t.Errorf("helm %q does not wait", upgrade)
	}

	if v, ok, _ := deploy.ReadKey(filepath.Join(target.Dir, ".env"), "HATCHET_CLIENT_TOKEN"); !ok || v != token {
		t.Error("the minted token is not in the env file")
	}
}

// secretData decodes a Secret manifest's data. Secrets carry base64 data, never stringData: the
// API server turns stringData into data, so server-side apply's field manager never owns a
// data key and a key dropped from the manifest would stay in the Secret.
func secretData(t *testing.T, manifest []byte) map[string]string {
	t.Helper()
	var s struct {
		StringData map[string]string `yaml:"stringData"`
		Data       map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal(manifest, &s); err != nil {
		t.Fatal(err)
	}
	if s.StringData != nil {
		t.Errorf("Secret manifest has stringData")
	}
	out := map[string]string{}
	for k, v := range s.Data {
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			t.Fatalf("data %s is not base64", k)
		}
		out[k] = string(b)
	}
	return out
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestUpIdempotent(t *testing.T) {
	target, run, _, cfg := setup(t)
	run.script[getPassword] = "secret/satat-postgres\n"
	run.script[getToken] = "secret/satat-hatchet-token\n"
	if err := os.WriteFile(filepath.Join(target.Dir, ".env"), []byte("HATCHET_CLIENT_TOKEN="+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run.script[readToken] = base64.StdEncoding.EncodeToString([]byte(token))
	if err := target.Up(context.Background(), cfg, secrets); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := deploy.ReadKey(filepath.Join(target.Dir, ".env"), "HATCHET_CLIENT_TOKEN"); v != token {
		t.Error("the env file's token changed although it matches the Secret")
	}
	var helm []string
	for _, s := range run.summary() {
		if strings.HasPrefix(s, "kubectl create") || s == "exec psql" || s == "exec hatchet-admin" {
			t.Errorf("unexpected %s on a second up", s)
		}
		if strings.HasPrefix(s, "helm") {
			helm = append(helm, s)
		}
	}
	if !slices.Equal(helm, []string{"helm upgrade worker.replicas=1"}) {
		t.Errorf("helm calls = %q, want one at replicas 1", helm)
	}
}

// A token Secret with no token in the env file (a fresh checkout) is copied back into the file,
// read from kubectl's stdout only.
func TestUpRestoresToken(t *testing.T) {
	for _, existing := range []string{"", "OTHER=1\nHATCHET_CLIENT_TOKEN=\n"} {
		target, run, _, cfg := setup(t)
		run.script[getPassword] = "secret/satat-postgres\n"
		run.script[getToken] = "secret/satat-hatchet-token\n"
		run.script[readToken] = base64.StdEncoding.EncodeToString([]byte(token))
		envFile := filepath.Join(target.Dir, ".env")
		if existing != "" {
			if err := os.WriteFile(envFile, []byte(existing), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := target.Up(context.Background(), cfg, secrets); err != nil {
			t.Fatal(err)
		}
		if v, ok, _ := deploy.ReadKey(envFile, "HATCHET_CLIENT_TOKEN"); !ok || v != token {
			t.Errorf("env file %q: token not restored", existing)
		}
		if run.find(readToken) == nil || run.find("exec hatchet-admin") != nil {
			t.Errorf("calls = %q, want the Secret read and no mint", run.summary())
		}
		for _, c := range run.calls {
			if slices.ContainsFunc(c.args, func(a string) bool { return strings.Contains(a, token) }) {
				t.Errorf("%s %q has the token on its command line", c.name, c.args)
			}
		}
	}
}

// An env file token that differs from the Secret's (a recreated cluster, say) is replaced, and
// neither value is printed.
func TestUpReplacesStaleToken(t *testing.T) {
	const stale = "eyJzdGFsZSJ9.old.sig"
	target, run, out, cfg := setup(t)
	run.script[getPassword] = "secret/satat-postgres\n"
	run.script[getToken] = "secret/satat-hatchet-token\n"
	run.script[readToken] = base64.StdEncoding.EncodeToString([]byte(token))
	envFile := filepath.Join(target.Dir, ".env")
	if err := os.WriteFile(envFile, []byte("OTHER=1\nHATCHET_CLIENT_TOKEN="+stale+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := target.Up(context.Background(), cfg, secrets); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := deploy.ReadKey(envFile, "HATCHET_CLIENT_TOKEN"); v != token {
		t.Error("the stale token was not replaced with the Secret's")
	}
	if v, _, _ := deploy.ReadKey(envFile, "OTHER"); v != "1" {
		t.Error("other env file lines were lost")
	}
	if strings.Contains(out.String(), token) || strings.Contains(out.String(), stale) {
		t.Error("a token was printed")
	}
}

func TestUpRestoreTokenErrorHidesValue(t *testing.T) {
	target, run, _, cfg := setup(t)
	run.script[getPassword] = "secret/satat-postgres\n"
	run.script[getToken] = "secret/satat-hatchet-token\n"
	run.script[readToken] = "not base64 " + token
	err := target.Up(context.Background(), cfg, secrets)
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "not base64") {
		t.Fatalf("err = %v, want a failure that names no content", err)
	}
}

func TestUpRefusesStaleRender(t *testing.T) {
	target, run, _, cfg := setup(t)
	if err := os.WriteFile(filepath.Join(target.Dir, "satat.k8s", "lanes.yaml"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := target.Up(context.Background(), cfg, secrets)
	if err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("err = %v, want out of date", err)
	}
	if len(run.calls) != 0 {
		t.Errorf("ran %q on a stale render", run.summary())
	}
}

func TestUpRefusesWrongContext(t *testing.T) {
	target, run, _, cfg := setup(t)
	target.Context = "k3d-elsewhere"
	err := target.Up(context.Background(), cfg, secrets)
	if err == nil || !strings.Contains(err.Error(), "k3d-elsewhere") {
		t.Fatalf("err = %v, want it to name the context", err)
	}
	if len(run.calls) != 1 {
		t.Errorf("calls = %q, want only the context check", run.summary())
	}
}

func TestUpNeedsCRDs(t *testing.T) {
	target, run, _, cfg := setup(t)
	run.script[getCRDs] = "customresourcedefinition.apiextensions.k8s.io/sandboxclaims.extensions.agents.x-k8s.io\n"
	err := target.Up(context.Background(), cfg, secrets)
	if err == nil || !strings.Contains(err.Error(), "agent-sandbox") {
		t.Fatalf("err = %v, want it to say agent-sandbox is missing", err)
	}
	if len(run.calls) != 2 {
		t.Errorf("calls = %q, want only the two checks", run.summary())
	}
}

func TestUpNeedsRegistry(t *testing.T) {
	target, run, _, cfg := setup(t)
	target.Registry = ""
	err := target.Up(context.Background(), cfg, secrets)
	if err == nil || !strings.Contains(err.Error(), "SATAT_REGISTRY") {
		t.Fatalf("err = %v, want it to name SATAT_REGISTRY", err)
	}
	if len(run.calls) != 0 {
		t.Errorf("calls = %q, want none", run.summary())
	}
}

func TestUpFailsWithoutPushDigest(t *testing.T) {
	target, run, _, cfg := setup(t)
	run.script["docker push "+registry+"/satat-platform:dev"] = "pushed, somehow\n"
	err := target.Up(context.Background(), cfg, secrets)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err = %v, want a missing digest", err)
	}
	if s := run.summary(); s[len(s)-1] != "docker push "+registry+"/satat-platform:dev" {
		t.Errorf("went on after the push: %q", s)
	}
}

func TestUpWarnsOnRemovedLane(t *testing.T) {
	target, run, out, cfg := setup(t)
	run.script[getManaged] = "namespace/satat-lane-closed\nnamespace/satat-lane-old\nnamespace/satat-lane-open\n"
	if err := target.Up(context.Background(), cfg, secrets); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "satat-lane-old") || strings.Contains(out.String(), "satat-lane-open") {
		t.Errorf("output does not warn of satat-lane-old alone:\n%s", out)
	}
	for _, c := range run.calls {
		if slices.Contains(c.args, "delete") {
			t.Errorf("up deleted something: %s %q", c.name, c.args)
		}
	}
}

func TestDownOrder(t *testing.T) {
	for _, volumes := range []bool{false, true} {
		target, run, _, _ := setup(t)
		run.script[getManaged] = "namespace/satat-lane-closed\nnamespace/satat-lane-open\n"
		envFile := filepath.Join(target.Dir, ".env")
		if err := os.WriteFile(envFile, []byte("HATCHET_CLIENT_TOKEN="+token+"\nOTHER=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := target.Down(context.Background(), volumes); err != nil {
			t.Fatal(err)
		}
		want := []string{
			getManaged,
			"kubectl delete sandboxclaims.extensions.agents.x-k8s.io --all -n satat-lane-closed --ignore-not-found --wait=true",
			"kubectl delete sandboxclaims.extensions.agents.x-k8s.io --all -n satat-lane-open --ignore-not-found --wait=true",
			"helm uninstall satat --namespace satat --ignore-not-found --wait",
			"kubectl delete namespace satat-lane-closed satat-lane-open --ignore-not-found --wait=true",
		}
		if volumes {
			want = append(want,
				"kubectl delete persistentvolumeclaim --all -n satat --ignore-not-found --wait=true",
				"kubectl delete secret satat-hatchet-token -n satat --ignore-not-found",
			)
		}
		if got := run.summary(); !slices.Equal(got, want) {
			t.Errorf("volumes=%v calls:\n  %s\nwant:\n  %s", volumes, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
		}
		for _, c := range run.calls {
			if slices.Contains(c.args, "satat-postgres") {
				t.Errorf("down touched the password: %q", c.args)
			}
			if (c.name == "kubectl" && !hasPair(c.args, "--context", kubeContext)) ||
				(c.name == "helm" && !hasPair(c.args, "--kube-context", kubeContext)) {
				t.Errorf("%s %q has no context", c.name, c.args)
			}
		}
		_, ok, _ := deploy.ReadKey(envFile, "HATCHET_CLIENT_TOKEN")
		if ok == volumes {
			t.Errorf("volumes=%v: token in the env file = %v", volumes, ok)
		}
		if _, ok, _ := deploy.ReadKey(envFile, "OTHER"); !ok {
			t.Error("down removed another env file line")
		}
	}
}

func TestDownKeepsDataWhenUninstallFails(t *testing.T) {
	target, run, _, _ := setup(t)
	run.fail = map[string]error{"helm uninstall satat --namespace satat --ignore-not-found --wait": errors.New("boom")}
	if err := target.Down(context.Background(), true); err == nil {
		t.Fatal("down succeeded with a failed uninstall")
	}
	for _, s := range run.summary() {
		if strings.Contains(s, "persistentvolumeclaim") || strings.Contains(s, "satat-hatchet-token") {
			t.Errorf("deleted data after a failed uninstall: %s", s)
		}
	}
}

const pods = `{"items": [
{"metadata": {"labels": {"app.kubernetes.io/name": "worker"}},
"status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}]}},
{"metadata": {"labels": {"app.kubernetes.io/name": "postgres"}},
"status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}]}},
{"metadata": {"labels": {"app.kubernetes.io/name": "hatchet-lite"}},
"status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "False"}],
"containerStatuses": [{"state": {"waiting": {"reason": "CrashLoopBackOff"}}}]}}
]}`

func TestStatusParses(t *testing.T) {
	target, run, _, _ := setup(t)
	run.script[getPods] = pods
	st, err := target.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []deploy.ServiceStatus{
		{Name: "hatchet-lite", State: "CrashLoopBackOff", Healthy: false},
		{Name: "postgres", State: "running", Healthy: true},
		{Name: "worker", State: "running", Healthy: true, Detail: `{"status":"ready"}`},
	}
	if !slices.Equal(st.Services, want) {
		t.Errorf("status = %+v, want %+v", st.Services, want)
	}
	for _, c := range run.calls {
		if !hasPair(c.args, "--context", kubeContext) {
			t.Errorf("%s %q has no context", c.name, c.args)
		}
	}
}

func TestStatusEmpty(t *testing.T) {
	target, run, _, _ := setup(t)
	run.script[getPods] = `{"items": []}`
	st, err := target.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Services) != 0 || len(run.calls) != 1 {
		t.Errorf("status = %+v after %q, want nothing", st, run.summary())
	}
}
