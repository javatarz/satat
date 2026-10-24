// Package kubernetes is the Kubernetes backend of launcher.Launcher (ADR 0024). Each step runs as
// a one-shot agent-sandbox SandboxClaim in its lane's namespace. This file builds the per-lane
// objects a claim needs and holds the template floor; the launcher itself is separate.
//
// Import it as k8slauncher where client-go's kubernetes package is also in scope.
package kubernetes

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/javatarz/satat/launcher"
)

const (
	sandboxGroup   = "extensions.agents.x-k8s.io"
	sandboxVersion = "v1beta1"

	// DefaultNamespacePrefix names a lane's namespace: satat-lane-<lane>.
	DefaultNamespacePrefix = "satat-lane-"

	resourcePrefix = "satat-"
	laneSecret     = "satat-lane-secrets"
	workerRole     = "satat-worker"

	entryContainer = "entry"
	stepContainer  = "step"

	labelManaged    = "satat.dev/managed"
	annotationRun   = "satat.dev/run"
	annotationStep  = "satat.dev/step"
	annotationInput = "satat.dev/input"

	// Pod paths, the same as Docker's where Docker has them.
	inputDir    = "/etc/satat"
	outputDir   = inputDir + "/out"
	workspace   = "/workspace"
	tmpDir      = "/tmp"
	podinfoDir  = "/etc/podinfo"
	secretsDir  = "/run/satat/secrets"
	entryDir    = "/satat"
	entryBinary = entryDir + "/satat-entry"
	// platformEntry is where the platform image keeps the wrapper.
	platformEntry = "/usr/local/bin/satat-entry"

	// The lane defaults, as on Docker.
	defaultUser    = "10001:10001"
	defaultTimeout = 30 * time.Minute

	// Memory-backed volume sizes. /tmp matches Docker's tmpfs.
	tmpBytes   = 64 << 20
	entryBytes = 32 << 20
	inputBytes = 16 << 20

	// deadlineGrace is the probe wait (30 s) plus 60 s on top of the lane timeout before the
	// kubelet kills the pod, a backstop to the wrapper's own timeout.
	deadlineGrace = 90 * time.Second
)

var (
	// ClaimGVR, TemplateGVR and WarmPoolGVR are the agent-sandbox v1.0.7 resources.
	ClaimGVR    = schema.GroupVersionResource{Group: sandboxGroup, Version: sandboxVersion, Resource: "sandboxclaims"}
	TemplateGVR = schema.GroupVersionResource{Group: sandboxGroup, Version: sandboxVersion, Resource: "sandboxtemplates"}
	WarmPoolGVR = schema.GroupVersionResource{Group: sandboxGroup, Version: sandboxVersion, Resource: "sandboxwarmpools"}

	sandboxAPIVersion = sandboxGroup + "/" + sandboxVersion

	digestRE  = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
	uidgidRE  = regexp.MustCompile(`^([1-9][0-9]*):([1-9][0-9]*)$`)
	envNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

	// privateRanges are the IPv4 ranges an open lane still can't reach.
	privateRanges = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}
)

// LaneOptions are the platform-wide settings a lane's objects depend on.
type LaneOptions struct {
	// NamespacePrefix defaults to DefaultNamespacePrefix.
	NamespacePrefix string
	// PlatformImage is the image whose init container copies satat-entry into the pod.
	PlatformImage string
	// Probe is the host:port every lane's network policy blocks (satat-entry --probe).
	Probe string
	// Worker is the ServiceAccount the lane's RoleBinding binds.
	Worker rbacv1.Subject
}

// Namespace is the namespace of lane: prefix (or DefaultNamespacePrefix) followed by the lane.
func Namespace(prefix, lane string) string {
	if prefix == "" {
		prefix = DefaultNamespacePrefix
	}
	return prefix + lane
}

func templateName(lane string) string { return resourcePrefix + lane }

// LaneObjects returns, in apply order, the lane's Namespace, SandboxTemplate, SandboxWarmPool and
// the worker's Role and RoleBinding. The lane Secret is not one of them: it holds secret values.
// The template passes ValidateTemplate.
func LaneObjects(l launcher.Lane, o LaneOptions) ([]*unstructured.Unstructured, error) {
	l, err := normalizeLane(l)
	if err != nil {
		return nil, err
	}
	ns := Namespace(o.NamespacePrefix, l.Name)
	if errs := validation.IsDNS1123Label(ns); len(errs) != 0 {
		return nil, fmt.Errorf("lane %q: namespace %q is not valid: %s", l.Name, ns, strings.Join(errs, "; "))
	}
	if o.PlatformImage == "" {
		return nil, errors.New("a platform image is required")
	}
	if o.Probe == "" {
		return nil, errors.New("a network policy probe address is required")
	}
	if o.Worker.Kind == "" || o.Worker.Name == "" {
		return nil, errors.New("the worker subject needs a kind and a name")
	}

	tmpl, err := template(l, o, ns)
	if err != nil {
		return nil, err
	}
	if err := ValidateTemplate(tmpl); err != nil {
		return nil, fmt.Errorf("lane %q: %w", l.Name, err)
	}

	namespace, err := typed(&corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: ns, Labels: map[string]string{
			"pod-security.kubernetes.io/enforce": "restricted",
			labelManaged:                         "true",
		}},
	})
	if err != nil {
		return nil, err
	}
	pool := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": sandboxAPIVersion,
		"kind":       "SandboxWarmPool",
		"metadata":   map[string]interface{}{"name": templateName(l.Name), "namespace": ns},
		"spec": map[string]interface{}{
			// The default is 1; every claim cold-starts.
			"replicas":           int64(0),
			"sandboxTemplateRef": map[string]interface{}{"name": templateName(l.Name)},
		},
	}}
	role, err := typed(&rbacv1.Role{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role"},
		ObjectMeta: metav1.ObjectMeta{Name: workerRole, Namespace: ns},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{sandboxGroup}, Resources: []string{"sandboxclaims"}, Verbs: []string{"create", "get", "watch", "delete"}},
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"pods/log"}, Verbs: []string{"get"}},
		},
	})
	if err != nil {
		return nil, err
	}
	binding, err := typed(&rbacv1.RoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: workerRole, Namespace: ns},
		Subjects:   []rbacv1.Subject{o.Worker},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: workerRole},
	})
	if err != nil {
		return nil, err
	}
	return []*unstructured.Unstructured{namespace, tmpl, pool, role, binding}, nil
}

// normalizeLane fills the defaults Docker fills and rejects a lane that can't run on Kubernetes.
func normalizeLane(l launcher.Lane) (launcher.Lane, error) {
	if errs := validation.IsDNS1123Label(l.Name); len(errs) != 0 {
		return l, fmt.Errorf("lane name %q is not a valid Kubernetes name: %s", l.Name, strings.Join(errs, "; "))
	}
	if l.User == "" {
		l.User = defaultUser
	}
	if l.Timeout == 0 {
		l.Timeout = defaultTimeout
	}
	if !uidgidRE.MatchString(l.User) {
		return l, fmt.Errorf("lane %q: user %q must be a non-root numeric uid:gid", l.Name, l.User)
	}
	if l.Timeout < 0 {
		return l, fmt.Errorf("lane %q: timeout must be positive", l.Name)
	}
	if l.MemoryBytes <= 0 || l.CPUs <= 0 || l.WorkspaceBytes <= 0 {
		return l, fmt.Errorf("lane %q needs positive memory, cpus and workspace limits", l.Name)
	}
	if math.Round(l.CPUs*1000) < 1 {
		return l, fmt.Errorf("lane %q: cpus %v is below 1m", l.Name, l.CPUs)
	}
	// Memory-backed emptyDirs count against the pod's memory.
	if used := l.WorkspaceBytes + tmpBytes + inputBytes + entryBytes; used >= l.MemoryBytes {
		return l, fmt.Errorf("lane %q: workspace, /tmp, %s and %s (%d bytes) must be smaller than its memory limit", l.Name, inputDir, entryDir, used)
	}
	if len(l.Command) == 0 {
		return l, fmt.Errorf("lane %q needs a command: on Kubernetes the wrapper runs it", l.Name)
	}
	if l.Runtime != "" && l.Runtime != "runsc" {
		return l, fmt.Errorf("lane %q: runtime %q is not allowed", l.Name, l.Runtime)
	}
	for _, n := range l.PassEnv {
		if !envNameRE.MatchString(n) {
			return l, fmt.Errorf("lane %q: env name %q is not allowed", l.Name, n)
		}
	}
	return l, nil
}

func template(l launcher.Lane, o LaneOptions, ns string) (*unstructured.Unstructured, error) {
	spec, err := podSpec(l, o)
	if err != nil {
		return nil, err
	}
	podSpecMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&spec)
	if err != nil {
		return nil, fmt.Errorf("convert pod spec: %w", err)
	}
	egress := []interface{}{}
	for _, r := range egressRules(l.Internal) {
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&r)
		if err != nil {
			return nil, fmt.Errorf("convert egress rule: %w", err)
		}
		egress = append(egress, m)
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": sandboxAPIVersion,
		"kind":       "SandboxTemplate",
		"metadata":   map[string]interface{}{"name": templateName(l.Name), "namespace": ns},
		"spec": map[string]interface{}{
			"envVarsInjectionPolicy":  "Disallowed",
			"networkPolicyManagement": "Managed",
			// Both lists are set: an empty list denies all. The controller adds the pod selector
			// and both policy types.
			"networkPolicy": map[string]interface{}{
				"ingress": []interface{}{},
				"egress":  egress,
			},
			"podTemplate": map[string]interface{}{
				"metadata": map[string]interface{}{},
				"spec":     podSpecMap,
			},
		},
	}}, nil
}

func podSpec(l launcher.Lane, o LaneOptions) (corev1.PodSpec, error) {
	uidS, gidS, _ := strings.Cut(l.User, ":")
	uid, err := strconv.ParseInt(uidS, 10, 64)
	if err != nil {
		return corev1.PodSpec{}, fmt.Errorf("lane %q: uid: %w", l.Name, err)
	}
	gid, err := strconv.ParseInt(gidS, 10, 64)
	if err != nil {
		return corev1.PodSpec{}, fmt.Errorf("lane %q: gid: %w", l.Name, err)
	}

	cmd := []string{entryBinary, "--probe", o.Probe, "--timeout", l.Timeout.String()}
	for _, n := range l.PassEnv {
		cmd = append(cmd, "--pass-env", n)
	}
	cmd = append(cmd, "--")
	cmd = append(cmd, l.Command...)

	stepResources := corev1.ResourceList{
		corev1.ResourceMemory: *resource.NewQuantity(l.MemoryBytes, resource.BinarySI),
		corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(math.Round(l.CPUs*1000)), resource.DecimalSI),
	}
	entryResources := corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("32Mi"),
		corev1.ResourceCPU:    resource.MustParse("100m"),
	}
	deadline := int64(math.Ceil((l.Timeout + deadlineGrace).Seconds()))
	secretsOptional := len(l.PassEnv) == 0

	spec := corev1.PodSpec{
		AutomountServiceAccountToken: ptr(false),
		RestartPolicy:                corev1.RestartPolicyNever,
		// Explicit, or the controller points the pod at public resolvers.
		DNSPolicy:             corev1.DNSClusterFirst,
		ActiveDeadlineSeconds: &deadline,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot:   ptr(true),
			RunAsUser:      &uid,
			RunAsGroup:     &gid,
			FSGroup:        &gid,
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		InitContainers: []corev1.Container{{
			Name:            entryContainer,
			Image:           o.PlatformImage,
			Command:         []string{"cp", platformEntry, entryBinary},
			SecurityContext: containerSecurity(),
			Resources:       corev1.ResourceRequirements{Requests: entryResources, Limits: entryResources.DeepCopy()},
			VolumeMounts:    []corev1.VolumeMount{{Name: "satat", MountPath: entryDir}},
		}},
		Containers: []corev1.Container{{
			Name:       stepContainer,
			Image:      l.Image,
			Command:    cmd,
			WorkingDir: workspace,
			Env: []corev1.EnvVar{
				{Name: "HOME", Value: workspace},
				{Name: "SATAT_INPUT", Value: inputDir + "/input.json"},
				{Name: "SATAT_OUTPUT", Value: outputDir},
			},
			SecurityContext: containerSecurity(),
			Resources:       corev1.ResourceRequirements{Requests: stepResources, Limits: stepResources.DeepCopy()},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "podinfo", MountPath: podinfoDir, ReadOnly: true},
				{Name: "secrets", MountPath: secretsDir, ReadOnly: true},
				{Name: "satat", MountPath: entryDir, ReadOnly: true},
				{Name: "input", MountPath: inputDir},
				{Name: "workspace", MountPath: workspace},
				{Name: "tmp", MountPath: tmpDir},
			},
		}},
		Volumes: []corev1.Volume{
			{Name: "podinfo", VolumeSource: corev1.VolumeSource{DownwardAPI: &corev1.DownwardAPIVolumeSource{
				Items: []corev1.DownwardAPIVolumeFile{
					annotationFile("run", annotationRun),
					annotationFile("step", annotationStep),
					annotationFile("input", annotationInput),
				},
			}}},
			{Name: "secrets", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: laneSecret,
				Optional:   &secretsOptional,
			}}},
			memoryDir("satat", entryBytes),
			memoryDir("input", inputBytes),
			memoryDir("workspace", l.WorkspaceBytes),
			memoryDir("tmp", tmpBytes),
		},
	}
	if l.Runtime != "" {
		spec.RuntimeClassName = ptr(l.Runtime)
	}
	return spec, nil
}

func egressRules(internal bool) []networkingv1.NetworkPolicyEgressRule {
	udp, tcp := corev1.ProtocolUDP, corev1.ProtocolTCP
	dnsPort := intstr.FromInt32(53)
	rules := []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
		}}},
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: &udp, Port: &dnsPort},
			{Protocol: &tcp, Port: &dnsPort},
		},
	}}
	if !internal {
		rules = append(rules, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{
				CIDR:   "0.0.0.0/0",
				Except: append([]string(nil), privateRanges...),
			}}},
		})
	}
	return rules
}

func containerSecurity() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		ReadOnlyRootFilesystem:   ptr(true),
		AllowPrivilegeEscalation: ptr(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

func annotationFile(path, annotation string) corev1.DownwardAPIVolumeFile {
	return corev1.DownwardAPIVolumeFile{
		Path:     path,
		FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.annotations['" + annotation + "']"},
	}
}

func memoryDir(name string, bytes int64) corev1.Volume {
	return corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
		Medium:    corev1.StorageMediumMemory,
		SizeLimit: resource.NewQuantity(bytes, resource.BinarySI),
	}}}
}

func typed(obj runtime.Object) (*unstructured.Unstructured, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("convert %T: %w", obj, err)
	}
	u := &unstructured.Unstructured{Object: m}
	// A typed object carries an empty creationTimestamp and status (and a Namespace an empty
	// spec); drop them so the rendered object is clean.
	unstructured.RemoveNestedField(u.Object, "metadata", "creationTimestamp")
	delete(u.Object, "status")
	if s, ok := u.Object["spec"].(map[string]interface{}); ok && len(s) == 0 {
		delete(u.Object, "spec")
	}
	return u, nil
}

func ptr[T any](v T) *T { return &v }

// ValidateTemplate is the hardening floor for a lane's SandboxTemplate, the way docker's
// ValidateRequest is for a create request: it checks the finished object, not the lane that
// produced it, and returns every violation joined.
func ValidateTemplate(t *unstructured.Unstructured) error {
	if t == nil {
		return errors.New("template is nil")
	}
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if t.GetAPIVersion() != sandboxAPIVersion || t.GetKind() != "SandboxTemplate" {
		bad("object is %s %s, not a %s SandboxTemplate", t.GetAPIVersion(), t.GetKind(), sandboxAPIVersion)
	}
	spec, _, _ := unstructured.NestedMap(t.Object, "spec")
	if p, _ := spec["envVarsInjectionPolicy"].(string); p != "Disallowed" {
		bad("spec.envVarsInjectionPolicy is %q, must be Disallowed", p)
	}
	if p, _ := spec["volumeClaimTemplatesPolicy"].(string); p != "" && p != "Disallowed" {
		bad("spec.volumeClaimTemplatesPolicy is %q, must be Disallowed", p)
	}
	if v, ok := spec["volumeClaimTemplates"]; ok && v != nil {
		bad("spec.volumeClaimTemplates are not allowed")
	}
	if v, ok := spec["service"].(bool); ok && v {
		bad("spec.service is not allowed")
	}
	validateNetworkPolicy(spec, bad)

	podMap, ok, err := unstructured.NestedMap(t.Object, "spec", "podTemplate", "spec")
	if err != nil || !ok {
		bad("spec.podTemplate.spec is missing")
		return errors.Join(errs...)
	}
	var pod corev1.PodSpec
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(podMap, &pod); err != nil {
		bad("spec.podTemplate.spec is not a pod spec: %v", err)
		return errors.Join(errs...)
	}
	validatePod(pod, bad)
	return errors.Join(errs...)
}

func validateNetworkPolicy(spec map[string]interface{}, bad func(string, ...any)) {
	if m, _ := spec["networkPolicyManagement"].(string); m != "" && m != "Managed" {
		bad("spec.networkPolicyManagement is %q, must be Managed", m)
	}
	np, ok := spec["networkPolicy"].(map[string]interface{})
	if !ok {
		// Omitted, the controller's default allows ingress from its router and sets public resolvers.
		bad("spec.networkPolicy is required")
		return
	}
	if in, ok := np["ingress"].([]interface{}); ok && len(in) != 0 {
		bad("spec.networkPolicy.ingress must be empty")
	} else if !ok && np["ingress"] != nil {
		bad("spec.networkPolicy.ingress must be a list")
	}
	var parsed struct {
		Egress []networkingv1.NetworkPolicyEgressRule `json:"egress"`
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(np, &parsed); err != nil {
		bad("spec.networkPolicy is not valid: %v", err)
		return
	}
	for i, r := range parsed.Egress {
		if !isDNSRule(r) && !isInternetRule(r) {
			bad("spec.networkPolicy.egress[%d] is neither cluster DNS nor public internet", i)
		}
	}
}

func isDNSRule(r networkingv1.NetworkPolicyEgressRule) bool {
	if len(r.To) != 1 || r.To[0].IPBlock != nil || r.To[0].PodSelector != nil || r.To[0].NamespaceSelector == nil {
		return false
	}
	sel := r.To[0].NamespaceSelector
	if len(sel.MatchExpressions) != 0 || len(sel.MatchLabels) != 1 || sel.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" {
		return false
	}
	if len(r.Ports) == 0 {
		return false
	}
	for _, p := range r.Ports {
		if p.Port == nil || p.Port.String() != "53" || p.EndPort != nil {
			return false
		}
	}
	return true
}

func isInternetRule(r networkingv1.NetworkPolicyEgressRule) bool {
	if len(r.To) != 1 || r.To[0].IPBlock == nil || r.To[0].PodSelector != nil || r.To[0].NamespaceSelector != nil {
		return false
	}
	ip := r.To[0].IPBlock
	if ip.CIDR != "0.0.0.0/0" {
		return false
	}
	for _, want := range privateRanges {
		found := false
		for _, e := range ip.Except {
			if e == want {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func validatePod(pod corev1.PodSpec, bad func(string, ...any)) {
	const p = "spec.podTemplate.spec."
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		bad(p + "automountServiceAccountToken must be false")
	}
	if pod.ServiceAccountName != "" || pod.DeprecatedServiceAccount != "" {
		bad(p + "serviceAccountName must not be set")
	}
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		bad(p+"restartPolicy is %q, must be Never", pod.RestartPolicy)
	}
	if pod.DNSPolicy != corev1.DNSClusterFirst {
		bad(p+"dnsPolicy is %q, must be ClusterFirst", pod.DNSPolicy)
	}
	if pod.DNSConfig != nil || len(pod.HostAliases) != 0 {
		bad(p + "dnsConfig and hostAliases are not allowed")
	}
	if pod.ActiveDeadlineSeconds == nil || *pod.ActiveDeadlineSeconds <= 0 {
		bad(p + "activeDeadlineSeconds is required")
	}
	if pod.HostNetwork || pod.HostPID || pod.HostIPC || (pod.HostUsers != nil && *pod.HostUsers) {
		bad(p + "hostNetwork, hostPID, hostIPC and hostUsers are not allowed")
	}
	if pod.ShareProcessNamespace != nil && *pod.ShareProcessNamespace {
		bad(p + "shareProcessNamespace is not allowed")
	}
	if pod.RuntimeClassName != nil && *pod.RuntimeClassName != "runsc" {
		bad(p+"runtimeClassName %q is not allowed", *pod.RuntimeClassName)
	}
	if len(pod.EphemeralContainers) != 0 {
		bad(p + "ephemeralContainers are not allowed")
	}

	sc := pod.SecurityContext
	if sc == nil {
		bad(p + "securityContext is required")
		sc = &corev1.PodSecurityContext{}
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		bad(p + "securityContext.runAsNonRoot must be true")
	}
	if sc.RunAsUser == nil || *sc.RunAsUser <= 0 {
		bad(p + "securityContext.runAsUser must be a non-root uid")
	}
	if sc.RunAsGroup == nil || *sc.RunAsGroup <= 0 {
		bad(p + "securityContext.runAsGroup must be a non-root gid")
	}
	if sc.FSGroup == nil || *sc.FSGroup <= 0 {
		bad(p + "securityContext.fsGroup must be a non-root gid")
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		bad(p + "securityContext.seccompProfile must be RuntimeDefault")
	}
	if len(sc.Sysctls) != 0 || len(sc.SupplementalGroups) != 0 {
		bad(p + "securityContext.sysctls and supplementalGroups are not allowed")
	}

	if len(pod.InitContainers) != 1 || pod.InitContainers[0].Name != entryContainer {
		bad(p+"initContainers must be exactly %q", entryContainer)
	}
	if len(pod.Containers) != 1 || pod.Containers[0].Name != stepContainer {
		bad(p+"containers must be exactly %q", stepContainer)
	}
	for i, c := range pod.InitContainers {
		f := fmt.Sprintf(p+"initContainers[%d].", i)
		if c.Image == "" {
			bad(f + "image is required")
		}
		validateContainer(c, f, bad)
	}
	for i, c := range pod.Containers {
		f := fmt.Sprintf(p+"containers[%d].", i)
		if !digestRE.MatchString(c.Image) {
			bad(f+"image %q is not pinned by digest", c.Image)
		}
		for _, e := range c.Env {
			if e.Name != "HOME" && e.Name != "SATAT_INPUT" && e.Name != "SATAT_OUTPUT" {
				bad(f+"env %q is not allowed", e.Name)
			}
			if e.ValueFrom != nil {
				bad(f+"env %q must be a literal", e.Name)
			}
		}
		validateContainer(c, f, bad)
	}

	for i, v := range pod.Volumes {
		f := fmt.Sprintf(p+"volumes[%d]", i)
		switch {
		case v.EmptyDir != nil:
			if v.EmptyDir.Medium != corev1.StorageMediumMemory || v.EmptyDir.SizeLimit == nil || v.EmptyDir.SizeLimit.Sign() <= 0 {
				bad(f+" (%s): emptyDir must be memory-backed with a size limit", v.Name)
			}
		case v.DownwardAPI != nil:
			for _, it := range v.DownwardAPI.Items {
				if it.ResourceFieldRef != nil || it.FieldRef == nil || !strings.HasPrefix(it.FieldRef.FieldPath, "metadata.annotations[") {
					bad(f+" (%s): downwardAPI may expose annotations only", v.Name)
				}
			}
		case v.Secret != nil:
			if v.Secret.SecretName != laneSecret {
				bad(f+" (%s): only Secret %s may be mounted", v.Name, laneSecret)
			}
		default:
			bad(f+" (%s): volume type is not allowed", v.Name)
		}
	}
}

func validateContainer(c corev1.Container, f string, bad func(string, ...any)) {
	sc := c.SecurityContext
	if sc == nil {
		bad(f + "securityContext is required")
		sc = &corev1.SecurityContext{}
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		bad(f + "securityContext.readOnlyRootFilesystem must be true")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		bad(f + "securityContext.allowPrivilegeEscalation must be false")
	}
	if sc.Privileged != nil && *sc.Privileged {
		bad(f + "securityContext.privileged is not allowed")
	}
	if sc.Capabilities == nil || !hasAll(sc.Capabilities.Drop) {
		bad(f + "securityContext.capabilities.drop must include ALL")
	}
	if sc.Capabilities != nil && len(sc.Capabilities.Add) != 0 {
		bad(f+"securityContext.capabilities.add is not allowed: %v", sc.Capabilities.Add)
	}
	if sc.RunAsUser != nil && *sc.RunAsUser <= 0 {
		bad(f + "securityContext.runAsUser must be a non-root uid")
	}
	if sc.RunAsGroup != nil && *sc.RunAsGroup <= 0 {
		bad(f + "securityContext.runAsGroup must be a non-root gid")
	}
	if sc.RunAsNonRoot != nil && !*sc.RunAsNonRoot {
		bad(f + "securityContext.runAsNonRoot must not be false")
	}
	if sc.SeccompProfile != nil && sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		bad(f + "securityContext.seccompProfile must be RuntimeDefault")
	}
	if sc.ProcMount != nil && *sc.ProcMount != corev1.DefaultProcMount {
		bad(f + "securityContext.procMount must be Default")
	}
	if len(c.Ports) != 0 {
		bad(f + "ports are not allowed")
	}
	if len(c.EnvFrom) != 0 {
		bad(f + "envFrom is not allowed")
	}
	if c.Stdin || c.TTY {
		bad(f + "stdin and tty are not allowed")
	}
	for _, r := range []corev1.ResourceName{corev1.ResourceMemory, corev1.ResourceCPU} {
		lim, ok := c.Resources.Limits[r]
		if !ok || lim.Sign() <= 0 {
			bad(f+"resources.limits.%s is required", r)
			continue
		}
		if req, ok := c.Resources.Requests[r]; !ok || req.Cmp(lim) != 0 {
			bad(f+"resources.requests.%s must equal the limit", r)
		}
	}
}

func hasAll(caps []corev1.Capability) bool {
	for _, c := range caps {
		if c == "ALL" {
			return true
		}
	}
	return false
}
