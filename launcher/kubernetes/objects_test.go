package kubernetes

import (
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/javatarz/satat/launcher"
)

const testImage = "ghcr.io/javatarz/satat-smoke@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testLane() launcher.Lane {
	return launcher.Lane{
		Name:           "smoke",
		Image:          testImage,
		Command:        []string{"/bin/agent", "run"},
		PassEnv:        []string{"CLAUDE_CODE_OAUTH_TOKEN", "GH_TOKEN"},
		MemoryBytes:    1 << 30,
		CPUs:           1.5,
		PidsLimit:      256,
		WorkspaceBytes: 256 << 20,
		Timeout:        20 * time.Minute,
	}
}

func testOptions() LaneOptions {
	return LaneOptions{
		PlatformImage: "satat-platform",
		Probe:         "satat-postgres.satat.svc:5432",
		Worker:        rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Name: "satat-worker", Namespace: "satat"},
	}
}

func objectsFor(t *testing.T, l launcher.Lane) []*unstructured.Unstructured {
	t.Helper()
	objs, err := LaneObjects(l, testOptions())
	if err != nil {
		t.Fatalf("LaneObjects: %v", err)
	}
	return objs
}

func find(t *testing.T, objs []*unstructured.Unstructured, kind string) *unstructured.Unstructured {
	t.Helper()
	for _, o := range objs {
		if o.GetKind() == kind {
			return o
		}
	}
	t.Fatalf("no %s in %d objects", kind, len(objs))
	return nil
}

func podSpecOf(t *testing.T, tmpl *unstructured.Unstructured) corev1.PodSpec {
	t.Helper()
	m, ok, err := unstructured.NestedMap(tmpl.Object, "spec", "podTemplate", "spec")
	if err != nil || !ok {
		t.Fatalf("spec.podTemplate.spec: ok=%v err=%v", ok, err)
	}
	var spec corev1.PodSpec
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &spec); err != nil {
		t.Fatalf("convert pod spec: %v", err)
	}
	return spec
}

func egressOf(t *testing.T, tmpl *unstructured.Unstructured) []networkingv1.NetworkPolicyEgressRule {
	t.Helper()
	np, ok, err := unstructured.NestedMap(tmpl.Object, "spec", "networkPolicy")
	if err != nil || !ok {
		t.Fatalf("spec.networkPolicy: ok=%v err=%v", ok, err)
	}
	ingress, ok := np["ingress"].([]interface{})
	if !ok || len(ingress) != 0 {
		t.Fatalf("ingress = %#v, want []", np["ingress"])
	}
	var out struct {
		Egress []networkingv1.NetworkPolicyEgressRule `json:"egress"`
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(np, &out); err != nil {
		t.Fatalf("convert network policy: %v", err)
	}
	return out.Egress
}

func TestLaneObjectsShape(t *testing.T) {
	objs := objectsFor(t, testLane())
	want := []struct{ kind, name, namespace string }{
		{"Namespace", "satat-lane-smoke", ""},
		{"SandboxTemplate", "satat-smoke", "satat-lane-smoke"},
		{"SandboxWarmPool", "satat-smoke", "satat-lane-smoke"},
		{"Role", "satat-worker", "satat-lane-smoke"},
		{"RoleBinding", "satat-worker", "satat-lane-smoke"},
	}
	if len(objs) != len(want) {
		t.Fatalf("got %d objects, want %d", len(objs), len(want))
	}
	for i, w := range want {
		o := objs[i]
		if o.GetKind() != w.kind || o.GetName() != w.name || o.GetNamespace() != w.namespace {
			t.Errorf("object %d = %s %s/%s, want %s %s/%s", i, o.GetKind(), o.GetNamespace(), o.GetName(), w.kind, w.namespace, w.name)
		}
		if o.GetAPIVersion() == "" {
			t.Errorf("object %d (%s) has no apiVersion", i, o.GetKind())
		}
	}

	ns := objs[0].GetLabels()
	if ns["pod-security.kubernetes.io/enforce"] != "restricted" || ns["satat.dev/managed"] != "true" {
		t.Errorf("namespace labels = %v", ns)
	}

	pool := objs[2]
	if pool.GetAPIVersion() != "extensions.agents.x-k8s.io/v1beta1" {
		t.Errorf("pool apiVersion = %q", pool.GetAPIVersion())
	}
	replicas, ok, _ := unstructured.NestedInt64(pool.Object, "spec", "replicas")
	if !ok || replicas != 0 {
		t.Errorf("pool replicas = %d (set %v), want an explicit 0", replicas, ok)
	}
	ref, _, _ := unstructured.NestedString(pool.Object, "spec", "sandboxTemplateRef", "name")
	if ref != "satat-smoke" {
		t.Errorf("pool template ref = %q", ref)
	}
}

func TestNamespacePrefix(t *testing.T) {
	if got := Namespace("", "smoke"); got != "satat-lane-smoke" {
		t.Errorf("Namespace default = %q", got)
	}
	o := testOptions()
	o.NamespacePrefix = "t1-"
	objs, err := LaneObjects(testLane(), o)
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range objs[1:] {
		if obj.GetNamespace() != "t1-smoke" {
			t.Errorf("%s namespace = %q", obj.GetKind(), obj.GetNamespace())
		}
	}
	if objs[0].GetName() != "t1-smoke" {
		t.Errorf("namespace name = %q", objs[0].GetName())
	}
}

func TestTemplateHardening(t *testing.T) {
	tmpl := find(t, objectsFor(t, testLane()), "SandboxTemplate")
	if p, _, _ := unstructured.NestedString(tmpl.Object, "spec", "envVarsInjectionPolicy"); p != "Disallowed" {
		t.Errorf("envVarsInjectionPolicy = %q", p)
	}
	spec := podSpecOf(t, tmpl)

	if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken must be false")
	}
	if spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy = %q", spec.RestartPolicy)
	}
	if spec.DNSPolicy != corev1.DNSClusterFirst {
		t.Errorf("dnsPolicy = %q", spec.DNSPolicy)
	}
	if spec.ActiveDeadlineSeconds == nil || *spec.ActiveDeadlineSeconds != 20*60+90 {
		t.Errorf("activeDeadlineSeconds = %v", spec.ActiveDeadlineSeconds)
	}
	if spec.RuntimeClassName != nil {
		t.Errorf("runtimeClassName = %q, want unset", *spec.RuntimeClassName)
	}
	sc := spec.SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Fatal("runAsNonRoot must be true")
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != 10001 || sc.RunAsGroup == nil || *sc.RunAsGroup != 10001 {
		t.Errorf("runAsUser/runAsGroup = %v/%v", sc.RunAsUser, sc.RunAsGroup)
	}
	if sc.FSGroup == nil || *sc.FSGroup != 10001 {
		t.Errorf("fsGroup = %v", sc.FSGroup)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("seccompProfile = %v", sc.SeccompProfile)
	}

	if len(spec.InitContainers) != 1 || spec.InitContainers[0].Name != "entry" {
		t.Fatalf("init containers = %+v", spec.InitContainers)
	}
	if len(spec.Containers) != 1 || spec.Containers[0].Name != "step" {
		t.Fatalf("containers = %+v", spec.Containers)
	}
	entry, step := spec.InitContainers[0], spec.Containers[0]
	for _, c := range []corev1.Container{entry, step} {
		csc := c.SecurityContext
		if csc == nil || csc.ReadOnlyRootFilesystem == nil || !*csc.ReadOnlyRootFilesystem {
			t.Errorf("%s: readOnlyRootFilesystem must be true", c.Name)
		}
		if csc == nil || csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation {
			t.Errorf("%s: allowPrivilegeEscalation must be false", c.Name)
		}
		if csc == nil || csc.Capabilities == nil || !reflect.DeepEqual(csc.Capabilities.Drop, []corev1.Capability{"ALL"}) {
			t.Errorf("%s: capabilities must drop ALL", c.Name)
		}
		if !reflect.DeepEqual(c.Resources.Requests, c.Resources.Limits) {
			t.Errorf("%s: requests %v != limits %v", c.Name, c.Resources.Requests, c.Resources.Limits)
		}
	}
	if entry.Image != "satat-platform" {
		t.Errorf("entry image = %q", entry.Image)
	}
	if !reflect.DeepEqual(entry.Command, []string{"cp", "/usr/local/bin/satat-entry", "/satat/satat-entry"}) {
		t.Errorf("entry command = %q", entry.Command)
	}
	if q := entry.Resources.Limits[corev1.ResourceMemory]; q.Cmp(resource.MustParse("32Mi")) != 0 {
		t.Errorf("entry memory = %s", q.String())
	}
	if q := entry.Resources.Limits[corev1.ResourceCPU]; q.Cmp(resource.MustParse("100m")) != 0 {
		t.Errorf("entry cpu = %s", q.String())
	}

	if step.Image != testImage {
		t.Errorf("step image = %q", step.Image)
	}
	if q := step.Resources.Limits[corev1.ResourceMemory]; q.Value() != 1<<30 {
		t.Errorf("step memory limit = %s", q.String())
	}
	if q := step.Resources.Limits[corev1.ResourceCPU]; q.Cmp(resource.MustParse("1500m")) != 0 {
		t.Errorf("step cpu limit = %s", q.String())
	}
	wantCmd := []string{
		"/satat/satat-entry", "--probe", "satat-postgres.satat.svc:5432", "--timeout", "20m0s",
		"--pass-env", "CLAUDE_CODE_OAUTH_TOKEN", "--pass-env", "GH_TOKEN",
		"--", "/bin/agent", "run",
	}
	if !reflect.DeepEqual(step.Command, wantCmd) {
		t.Errorf("step command = %q\nwant %q", step.Command, wantCmd)
	}
	if len(step.Args) != 0 {
		t.Errorf("step args = %q, want none", step.Args)
	}
	wantEnv := []corev1.EnvVar{
		{Name: "HOME", Value: "/workspace"},
		{Name: "SATAT_INPUT", Value: "/etc/satat/input.json"},
		{Name: "SATAT_OUTPUT", Value: "/etc/satat/out"},
	}
	if !reflect.DeepEqual(step.Env, wantEnv) || len(step.EnvFrom) != 0 {
		t.Errorf("step env = %+v envFrom = %+v", step.Env, step.EnvFrom)
	}
	mounts := map[string]string{}
	for _, m := range step.VolumeMounts {
		mounts[m.MountPath] = m.Name
	}
	for _, p := range []string{"/etc/podinfo", "/run/satat/secrets", "/satat", "/etc/satat", "/workspace", "/tmp"} {
		if mounts[p] == "" {
			t.Errorf("step has no mount at %s (mounts %v)", p, mounts)
		}
	}

	vols := map[string]corev1.Volume{}
	for _, v := range spec.Volumes {
		vols[v.Name] = v
	}
	pi := vols["podinfo"].DownwardAPI
	if pi == nil {
		t.Fatal("no podinfo downward API volume")
	}
	gotFiles := map[string]string{}
	for _, it := range pi.Items {
		if it.FieldRef != nil {
			gotFiles[it.Path] = it.FieldRef.FieldPath
		}
	}
	wantFiles := map[string]string{
		"run":   "metadata.annotations['satat.dev/run']",
		"step":  "metadata.annotations['satat.dev/step']",
		"input": "metadata.annotations['satat.dev/input']",
	}
	if !reflect.DeepEqual(gotFiles, wantFiles) {
		t.Errorf("podinfo files = %v", gotFiles)
	}
	sec := vols["secrets"].Secret
	if sec == nil || sec.SecretName != "satat-lane-secrets" {
		t.Fatalf("secrets volume = %+v", vols["secrets"])
	}
	// Optional, so a key missing from the Secret reaches satat-entry, which fails the step with 125,
	// instead of the kubelet failing the mount and the pod never starting.
	if sec.Optional == nil || !*sec.Optional {
		t.Error("secrets must be optional")
	}
	wantItems := []corev1.KeyToPath{
		{Key: "CLAUDE_CODE_OAUTH_TOKEN", Path: "CLAUDE_CODE_OAUTH_TOKEN"},
		{Key: "GH_TOKEN", Path: "GH_TOKEN"},
	}
	if !reflect.DeepEqual(sec.Items, wantItems) {
		t.Errorf("secrets items = %+v, want only the pass_env keys %+v", sec.Items, wantItems)
	}
	wantSizes := map[string]string{"satat": "32Mi", "input": "16Mi", "workspace": "256Mi", "tmp": "64Mi"}
	for name, size := range wantSizes {
		ed := vols[name].EmptyDir
		if ed == nil || ed.Medium != corev1.StorageMediumMemory || ed.SizeLimit == nil || ed.SizeLimit.Cmp(resource.MustParse(size)) != 0 {
			t.Errorf("emptyDir %s = %+v, want memory %s", name, ed, size)
		}
	}
}

// TestNoSecretsWithoutPassEnv checks that a lane that passes nothing mounts no Secret, so a stray
// satat-lane-secrets left from an older config is never visible to its steps.
func TestNoSecretsWithoutPassEnv(t *testing.T) {
	l := testLane()
	l.PassEnv = nil
	spec := podSpecOf(t, find(t, objectsFor(t, l), "SandboxTemplate"))
	for _, v := range spec.Volumes {
		if v.Secret != nil {
			t.Errorf("volume %s mounts Secret %s", v.Name, v.Secret.SecretName)
		}
	}
	for _, m := range spec.Containers[0].VolumeMounts {
		if m.MountPath == "/run/satat/secrets" {
			t.Errorf("step mounts %s", m.MountPath)
		}
	}
}

func TestLaneDefaults(t *testing.T) {
	l := testLane()
	l.User, l.Timeout = "", 0
	spec := podSpecOf(t, find(t, objectsFor(t, l), "SandboxTemplate"))
	if *spec.SecurityContext.RunAsUser != 10001 || *spec.ActiveDeadlineSeconds != 30*60+90 {
		t.Errorf("defaults: uid %d deadline %d", *spec.SecurityContext.RunAsUser, *spec.ActiveDeadlineSeconds)
	}
	if !strings.Contains(strings.Join(spec.Containers[0].Command, " "), "--timeout 30m0s") {
		t.Errorf("command = %q", spec.Containers[0].Command)
	}
}

func assertDNS(t *testing.T, r networkingv1.NetworkPolicyEgressRule) {
	t.Helper()
	if len(r.To) != 1 || r.To[0].NamespaceSelector == nil || r.To[0].PodSelector != nil || r.To[0].IPBlock != nil ||
		!reflect.DeepEqual(r.To[0].NamespaceSelector.MatchLabels, map[string]string{"kubernetes.io/metadata.name": "kube-system"}) {
		t.Errorf("dns rule peers = %+v", r.To)
	}
	var got []string
	for _, p := range r.Ports {
		if p.Protocol == nil || p.Port == nil {
			t.Errorf("dns port %+v must name a protocol and port", p)
			continue
		}
		got = append(got, string(*p.Protocol)+"/"+p.Port.String())
	}
	if !reflect.DeepEqual(got, []string{"UDP/53", "TCP/53"}) {
		t.Errorf("dns ports = %v", got)
	}
}

func TestNetworkPolicyInternal(t *testing.T) {
	l := testLane()
	l.Internal = true
	egress := egressOf(t, find(t, objectsFor(t, l), "SandboxTemplate"))
	if len(egress) != 1 {
		t.Fatalf("internal lane egress = %+v, want only DNS", egress)
	}
	assertDNS(t, egress[0])
}

func TestNetworkPolicyOpen(t *testing.T) {
	egress := egressOf(t, find(t, objectsFor(t, testLane()), "SandboxTemplate"))
	if len(egress) != 2 {
		t.Fatalf("open lane egress = %+v, want DNS and internet", egress)
	}
	assertDNS(t, egress[0])
	r := egress[1]
	if len(r.Ports) != 0 || len(r.To) != 1 || r.To[0].IPBlock == nil {
		t.Fatalf("internet rule = %+v", r)
	}
	ip := r.To[0].IPBlock
	want := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}
	if ip.CIDR != "0.0.0.0/0" || !reflect.DeepEqual(ip.Except, want) {
		t.Errorf("ipBlock = %+v", ip)
	}
}

func TestWorkerRoleExact(t *testing.T) {
	objs := objectsFor(t, testLane())
	var role rbacv1.Role
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(find(t, objs, "Role").Object, &role); err != nil {
		t.Fatal(err)
	}
	want := []rbacv1.PolicyRule{
		{APIGroups: []string{"extensions.agents.x-k8s.io"}, Resources: []string{"sandboxclaims"}, Verbs: []string{"create", "get", "watch", "delete"}},
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
		{APIGroups: []string{""}, Resources: []string{"pods/log"}, Verbs: []string{"get"}},
	}
	if !reflect.DeepEqual(role.Rules, want) {
		t.Errorf("rules = %+v\nwant %+v", role.Rules, want)
	}
	var rb rbacv1.RoleBinding
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(find(t, objs, "RoleBinding").Object, &rb); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rb.Subjects, []rbacv1.Subject{testOptions().Worker}) {
		t.Errorf("subjects = %+v", rb.Subjects)
	}
	if rb.RoleRef != (rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "satat-worker"}) {
		t.Errorf("roleRef = %+v", rb.RoleRef)
	}
}

func TestRunscRuntime(t *testing.T) {
	l := testLane()
	l.Runtime = "runsc"
	spec := podSpecOf(t, find(t, objectsFor(t, l), "SandboxTemplate"))
	if spec.RuntimeClassName == nil || *spec.RuntimeClassName != "runsc" {
		t.Errorf("runtimeClassName = %v", spec.RuntimeClassName)
	}
}

func TestValidateTemplateAcceptsBuilt(t *testing.T) {
	if err := ValidateTemplate(find(t, objectsFor(t, testLane()), "SandboxTemplate")); err != nil {
		t.Fatalf("built template fails the floor: %v", err)
	}
}

func TestValidateTemplateRejects(t *testing.T) {
	podPath := []string{"spec", "podTemplate", "spec"}
	step := func(o map[string]interface{}) map[string]interface{} {
		cs, _, _ := unstructured.NestedSlice(o, append(podPath, "containers")...)
		return cs[0].(map[string]interface{})
	}
	setStep := func(o map[string]interface{}, c map[string]interface{}) {
		cs, _, _ := unstructured.NestedSlice(o, append(podPath, "containers")...)
		cs[0] = c
		_ = unstructured.SetNestedSlice(o, cs, append(podPath, "containers")...)
	}
	cases := []struct {
		name   string
		mutate func(o map[string]interface{})
		field  string
	}{
		{"root uid", func(o map[string]interface{}) {
			_ = unstructured.SetNestedField(o, int64(0), append(podPath, "securityContext", "runAsUser")...)
		}, "securityContext.runAsUser"},
		{"writable root", func(o map[string]interface{}) {
			c := step(o)
			_ = unstructured.SetNestedField(c, false, "securityContext", "readOnlyRootFilesystem")
			setStep(o, c)
		}, "readOnlyRootFilesystem"},
		{"no drop ALL", func(o map[string]interface{}) {
			c := step(o)
			unstructured.RemoveNestedField(c, "securityContext", "capabilities", "drop")
			setStep(o, c)
		}, "capabilities.drop"},
		{"token automount", func(o map[string]interface{}) {
			_ = unstructured.SetNestedField(o, true, append(podPath, "automountServiceAccountToken")...)
		}, "automountServiceAccountToken"},
		{"image not pinned", func(o map[string]interface{}) {
			c := step(o)
			c["image"] = "ghcr.io/javatarz/satat-smoke:latest"
			setStep(o, c)
		}, "image"},
		{"no memory limit", func(o map[string]interface{}) {
			c := step(o)
			unstructured.RemoveNestedField(c, "resources", "limits", "memory")
			setStep(o, c)
		}, "resources.limits.memory"},
		{"env policy allowed", func(o map[string]interface{}) {
			_ = unstructured.SetNestedField(o, "Allowed", "spec", "envVarsInjectionPolicy")
		}, "envVarsInjectionPolicy"},
		{"whole Secret mounted", func(o map[string]interface{}) {
			setSecretItems(o, nil)
		}, "secrets"},
		{"key not passed", func(o map[string]interface{}) {
			setSecretItems(o, []interface{}{
				map[string]interface{}{"key": "CLAUDE_CODE_OAUTH_TOKEN", "path": "CLAUDE_CODE_OAUTH_TOKEN"},
				map[string]interface{}{"key": "GH_TOKEN", "path": "GH_TOKEN"},
				map[string]interface{}{"key": "OTHER", "path": "OTHER"},
			})
		}, "OTHER"},
		{"key at another path", func(o map[string]interface{}) {
			setSecretItems(o, []interface{}{
				map[string]interface{}{"key": "CLAUDE_CODE_OAUTH_TOKEN", "path": "GH_TOKEN"},
				map[string]interface{}{"key": "GH_TOKEN", "path": "CLAUDE_CODE_OAUTH_TOKEN"},
			})
		}, "path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := find(t, objectsFor(t, testLane()), "SandboxTemplate").DeepCopy()
			tc.mutate(tmpl.Object)
			err := ValidateTemplate(tmpl)
			if err == nil {
				t.Fatal("mutated template passed the floor")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error %q does not name %s", err, tc.field)
			}
		})
	}
}

// setSecretItems replaces the items of the template's Secret volume; nil removes them.
func setSecretItems(o map[string]interface{}, items []interface{}) {
	path := []string{"spec", "podTemplate", "spec", "volumes"}
	vols, _, _ := unstructured.NestedSlice(o, path...)
	for i, v := range vols {
		m := v.(map[string]interface{})
		if _, ok := m["secret"]; !ok {
			continue
		}
		if items == nil {
			unstructured.RemoveNestedField(m, "secret", "items")
		} else {
			_ = unstructured.SetNestedSlice(m, items, "secret", "items")
		}
		vols[i] = m
	}
	_ = unstructured.SetNestedSlice(o, vols, path...)
}

func TestValidateTemplateJoinsViolations(t *testing.T) {
	tmpl := find(t, objectsFor(t, testLane()), "SandboxTemplate").DeepCopy()
	_ = unstructured.SetNestedField(tmpl.Object, "Allowed", "spec", "envVarsInjectionPolicy")
	_ = unstructured.SetNestedField(tmpl.Object, true, "spec", "podTemplate", "spec", "automountServiceAccountToken")
	err := ValidateTemplate(tmpl)
	if err == nil || !strings.Contains(err.Error(), "envVarsInjectionPolicy") || !strings.Contains(err.Error(), "automountServiceAccountToken") {
		t.Errorf("err = %v, want both violations", err)
	}
}

func TestLaneObjectsRejects(t *testing.T) {
	cases := map[string]func(l *launcher.Lane, o *LaneOptions){
		"bad name":       func(l *launcher.Lane, _ *LaneOptions) { l.Name = "Bad_Name" },
		"root user":      func(l *launcher.Lane, _ *LaneOptions) { l.User = "0:0" },
		"unpinned image": func(l *launcher.Lane, _ *LaneOptions) { l.Image = "alpine:3" },
		"no command":     func(l *launcher.Lane, _ *LaneOptions) { l.Command = nil },
		"no probe":       func(_ *launcher.Lane, o *LaneOptions) { o.Probe = "" },
		"no platform":    func(_ *launcher.Lane, o *LaneOptions) { o.PlatformImage = "" },
		"no worker":      func(_ *launcher.Lane, o *LaneOptions) { o.Worker = rbacv1.Subject{} },
		"bad env name":   func(l *launcher.Lane, _ *LaneOptions) { l.PassEnv = []string{"lower"} },
		"no memory":      func(l *launcher.Lane, _ *LaneOptions) { l.MemoryBytes = 0 },
		"tmpfs too big":  func(l *launcher.Lane, _ *LaneOptions) { l.WorkspaceBytes = l.MemoryBytes },
		"bad runtime":    func(l *launcher.Lane, _ *LaneOptions) { l.Runtime = "kata" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			l, o := testLane(), testOptions()
			mutate(&l, &o)
			if _, err := LaneObjects(l, o); err == nil {
				t.Error("LaneObjects accepted the lane")
			}
		})
	}
}
