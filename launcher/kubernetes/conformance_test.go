package kubernetes

import (
	"context"
	"os"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/launcher/conformance"
)

// The end-to-end tests run against a live cluster only with SATAT_K8S_E2E=1. They always use the
// k3d-satat context, never the kubeconfig's current one.
const (
	e2eContext = "k3d-satat"
	// e2eImage is the busybox lanes run in satat.yaml.
	e2eImage = "busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"

	confNamespace = "satat-conf"
	confPrefix    = "satat-conf-"
	confWorker    = "satat-conf-worker"
	confProbe     = "probe." + confNamespace + ".svc.cluster.local:5432"
	fieldManager  = "satat-conformance"
)

// e2eEnv is the admin connection to the test cluster and what the lanes need.
type e2eEnv struct {
	admin         *rest.Config
	dyn           dynamic.Interface
	core          k8sclient.Interface
	platformImage string
}

// e2e connects to the k3d-satat cluster and sets up the shared namespace, worker
// ServiceAccount and probe, or skips without SATAT_K8S_E2E=1.
func e2e(t *testing.T) *e2eEnv {
	t.Helper()
	if os.Getenv("SATAT_K8S_E2E") != "1" {
		t.Skip("Kubernetes end-to-end test: set SATAT_K8S_E2E=1")
	}
	image := os.Getenv("SATAT_K8S_PLATFORM_IMAGE")
	if image == "" {
		t.Fatal("SATAT_K8S_PLATFORM_IMAGE must name the platform image with satat-entry")
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: e2eContext},
	).ClientConfig()
	if err != nil {
		t.Fatalf("kubeconfig context %s: %v", e2eContext, err)
	}
	env := &e2eEnv{admin: cfg, platformImage: image}
	if env.dyn, err = dynamic.NewForConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if env.core, err = k8sclient.NewForConfig(cfg); err != nil {
		t.Fatal(err)
	}
	env.setup(t)
	return env
}

// setup creates the satat-conf namespace with the worker ServiceAccount and the probe, and
// deletes the namespace when the test ends.
func (e *e2eEnv) setup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e.waitNamespaceGone(t, ctx, confNamespace)
	t.Cleanup(func() { e.deleteNamespace(t, confNamespace, true) })

	core := e.core.CoreV1()
	if _, err := core.Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   confNamespace,
		Labels: map[string]string{"pod-security.kubernetes.io/enforce": "restricted"},
	}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace %s: %v", confNamespace, err)
	}
	if _, err := core.ServiceAccounts(confNamespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: confWorker},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create service account: %v", err)
	}

	// The probe answers on 5432; every lane's network policy blocks it.
	labels := map[string]string{"app": "probe"}
	uid := int64(10001)
	if _, err := e.core.AppsV1().Deployments(confNamespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "probe"},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr(true), RunAsUser: &uid, RunAsGroup: &uid,
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:            "httpd",
						Image:           e2eImage,
						Command:         []string{"httpd", "-f", "-p", "5432", "-h", "/tmp"},
						Ports:           []corev1.ContainerPort{{ContainerPort: 5432}},
						SecurityContext: containerSecurity(),
						ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
							TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(5432)},
						}, PeriodSeconds: 1},
					}},
				},
			},
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create probe deployment: %v", err)
	}
	if _, err := core.Services(confNamespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "probe"},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    []corev1.ServicePort{{Port: 5432, TargetPort: intstr.FromInt32(5432)}},
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create probe service: %v", err)
	}
	poll(t, ctx, "probe deployment ready", func() (bool, error) {
		d, err := e.core.AppsV1().Deployments(confNamespace).Get(ctx, "probe", metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return d.Status.ReadyReplicas == 1, nil
	})
}

// applyLane applies lane's objects and waits until a claim can use them; the lane namespace is
// deleted when the test ends.
func (e *e2eEnv) applyLane(t *testing.T, lane launcher.Lane) {
	t.Helper()
	objs, err := LaneObjects(lane, LaneOptions{
		NamespacePrefix: confPrefix,
		PlatformImage:   e.platformImage,
		Probe:           confProbe,
		Worker:          rbacv1.Subject{Kind: "ServiceAccount", Name: confWorker, Namespace: confNamespace},
	})
	if err != nil {
		t.Fatalf("LaneObjects(%s): %v", lane.Name, err)
	}
	ns := Namespace(confPrefix, lane.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e.waitNamespaceGone(t, ctx, ns)
	t.Cleanup(func() { e.deleteNamespace(t, ns, false) })
	for _, o := range objs {
		gvr, ok := laneGVRs[o.GetKind()]
		if !ok {
			t.Fatalf("no resource for kind %s", o.GetKind())
		}
		var ri dynamic.ResourceInterface = e.dyn.Resource(gvr)
		if o.GetNamespace() != "" {
			ri = e.dyn.Resource(gvr).Namespace(o.GetNamespace())
		}
		if _, err := ri.Apply(ctx, o.GetName(), o, metav1.ApplyOptions{FieldManager: fieldManager, Force: true}); err != nil {
			t.Fatalf("apply %s %s: %v", o.GetKind(), o.GetName(), err)
		}
	}
	// A pod can't be created before the namespace's default ServiceAccount exists.
	poll(t, ctx, "lane "+lane.Name+" ready", func() (bool, error) {
		if _, err := e.dyn.Resource(TemplateGVR).Namespace(ns).Get(ctx, templateName(lane.Name), metav1.GetOptions{}); err != nil {
			return false, ignoreNotFound(err)
		}
		_, err := e.core.CoreV1().ServiceAccounts(ns).Get(ctx, "default", metav1.GetOptions{})
		return err == nil, ignoreNotFound(err)
	})
}

var laneGVRs = map[string]schema.GroupVersionResource{
	"Namespace":       {Version: "v1", Resource: "namespaces"},
	"SandboxTemplate": TemplateGVR,
	"SandboxWarmPool": WarmPoolGVR,
	"Role":            {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
	"RoleBinding":     {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"},
}

// worker is the rest config of the worker ServiceAccount, by impersonation.
func (e *e2eEnv) worker() *rest.Config {
	c := rest.CopyConfig(e.admin)
	c.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:" + confNamespace + ":" + confWorker}
	return c
}

func (e *e2eEnv) deleteNamespace(t *testing.T, ns string, wait bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := e.core.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Logf("delete namespace %s: %v", ns, err)
		return
	}
	if wait {
		e.waitNamespaceGone(t, ctx, ns)
	}
}

// waitNamespaceGone waits out a namespace an earlier run is still deleting.
func (e *e2eEnv) waitNamespaceGone(t *testing.T, ctx context.Context, ns string) {
	t.Helper()
	poll(t, ctx, "namespace "+ns+" gone", func() (bool, error) {
		n, err := e.core.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if n.DeletionTimestamp == nil {
			// Left by a run that died: delete it.
			if err := e.core.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return false, err
			}
		}
		return false, nil
	})
}

func poll(t *testing.T, ctx context.Context, what string, cond func() (bool, error)) {
	t.Helper()
	for {
		ok, err := cond()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if ok {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v", what, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func ignoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func TestConformanceOnKubernetes(t *testing.T) {
	env := e2e(t)
	conformance.Run(t, conformance.Config{
		Image: e2eImage,
		New: func(t *testing.T, lanes []launcher.Lane) launcher.Launcher {
			for _, lane := range lanes {
				env.applyLane(t, lane)
			}
			w := env.worker()
			dyn, err := dynamic.NewForConfig(w)
			if err != nil {
				t.Fatal(err)
			}
			core, err := k8sclient.NewForConfig(w)
			if err != nil {
				t.Fatal(err)
			}
			l, err := New(dyn, core, lanes, WithNamespacePrefix(confPrefix))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			return l
		},
	})
}
