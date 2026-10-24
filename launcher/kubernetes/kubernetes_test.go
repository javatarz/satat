package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/javatarz/satat/entry"
	"github.com/javatarz/satat/launcher"
)

const (
	laneX    = "x"
	testNS   = "satat-lane-x"
	testRun  = "run1"
	testStep = "step1"
	testName = "satat-run1-step1"
)

func laneXDef() launcher.Lane {
	return launcher.Lane{
		Name:  laneX,
		Image: testImage,
		// The wrapper runs this.
		Command:     []string{"sh", "-c", "true"},
		MemoryBytes: 256 << 20, CPUs: 1, PidsLimit: 128, WorkspaceBytes: 32 << 20,
		Timeout: time.Minute,
	}
}

type harness struct {
	l    *Launcher
	dyn  *dynamicfake.FakeDynamicClient
	core *k8sfake.Clientset

	mu  sync.Mutex
	log []byte
}

func newHarness(t *testing.T, existing ...runtime.Object) *harness {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{ClaimGVR: "SandboxClaimList"}, existing...)
	core := k8sfake.NewClientset()
	l, err := New(dyn, core, []launcher.Lane{laneXDef()}, WithPoll(10*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := &harness{l: l, dyn: dyn, core: core}
	l.logs = func(ctx context.Context, ns, pod, container string) ([]byte, error) {
		if container != stepContainer {
			return nil, errors.New("wrong container " + container)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.log != nil {
			return h.log, nil
		}
		// The fake clientset's own pods/log body.
		return l.core.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{Container: container}).DoRaw(ctx)
	}
	return h
}

func (h *harness) setLog(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = b
}

func (h *harness) claims() interface {
	Get(context.Context, string, metav1.GetOptions, ...string) (*unstructured.Unstructured, error)
	UpdateStatus(context.Context, *unstructured.Unstructured, metav1.UpdateOptions) (*unstructured.Unstructured, error)
} {
	return h.dyn.Resource(ClaimGVR).Namespace(testNS)
}

// waitClaim waits for a claim without a deletionTimestamp to appear and returns it.
func (h *harness) waitClaim(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := h.claims().Get(context.Background(), testName, metav1.GetOptions{})
		if err == nil && c.GetDeletionTimestamp() == nil {
			return c
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("claim never appeared")
	return nil
}

type podEnd struct {
	exit      int32
	reason    string // container termination reason
	podReason string // pod status.reason
	finished  bool   // set the claim's Finished condition too
}

// controller plays agent-sandbox for one claim: once it appears it creates the terminated pod
// named after it, then names the sandbox in the claim's status (and sets Finished, if asked).
func (h *harness) controller(t *testing.T, end podEnd) {
	t.Helper()
	go func() {
		c := h.waitClaim(t)
		if c == nil {
			return
		}
		phase := corev1.PodSucceeded
		if end.exit != 0 || end.podReason != "" {
			phase = corev1.PodFailed
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNS},
			Status: corev1.PodStatus{
				Phase:  phase,
				Reason: end.podReason,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:  stepContainer,
					State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: end.exit, Reason: end.reason}},
				}},
			},
		}
		if _, err := h.core.CoreV1().Pods(testNS).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
			t.Errorf("create pod: %v", err)
			return
		}
		status := map[string]interface{}{"sandbox": map[string]interface{}{"name": testName}}
		if end.finished {
			reason := "PodSucceeded"
			if phase == corev1.PodFailed {
				reason = "PodFailed"
			}
			status["conditions"] = []interface{}{
				map[string]interface{}{"type": "Finished", "status": "True", "reason": reason},
			}
		}
		c.Object["status"] = status
		if _, err := h.claims().UpdateStatus(context.Background(), c, metav1.UpdateOptions{}); err != nil {
			t.Errorf("update claim status: %v", err)
		}
	}()
}

func (h *harness) launch(ctx context.Context, input []byte) (launcher.Result, error) {
	return h.l.Launch(ctx, launcher.Step{Run: testRun, Name: testStep, Lane: laneX, Input: input})
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func (h *harness) assertGone(t *testing.T) {
	t.Helper()
	if _, err := h.claims().Get(context.Background(), testName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("claim still there after Launch: err=%v", err)
	}
}

func (h *harness) actions(verb string) []k8stesting.Action {
	var out []k8stesting.Action
	for _, a := range h.dyn.Actions() {
		if a.GetVerb() == verb && a.GetResource() == ClaimGVR {
			out = append(out, a)
		}
	}
	return out
}

func frame(t *testing.T, r entry.Result) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("some wrapper noise\n")
	if err := entry.Write(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func existingClaim(labels map[string]interface{}, deleting bool) *unstructured.Unstructured {
	meta := map[string]interface{}{"name": testName, "namespace": testNS}
	if labels != nil {
		meta["labels"] = labels
	}
	if deleting {
		meta["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
		meta["finalizers"] = []interface{}{"foregroundDeletion"}
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": sandboxAPIVersion, "kind": "SandboxClaim", "metadata": meta,
		"spec": map[string]interface{}{"warmPoolRef": map[string]interface{}{"name": "satat-x"}},
	}}
}

func ourLabels() map[string]interface{} {
	return map[string]interface{}{labelManaged: "true", labelLane: laneX, labelRun: testRun, labelStep: testStep}
}

func TestLaunchHappyPath(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	tb, _, err := entry.PackDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	h.setLog(frame(t, entry.Result{ExitCode: 3, Stdout: []byte("out\n"), Stderr: []byte("err\n"), Output: tb}))
	h.controller(t, podEnd{exit: 3, finished: true})

	input := []byte(`{"issue":112}`)
	res, err := h.launch(ctxT(t), input)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.ExitCode != 3 || string(res.Stdout) != "out\n" || string(res.Stderr) != "err\n" || string(res.Files["a.txt"]) != "one" {
		t.Errorf("result = %+v", res)
	}
	if res.Adopted || res.Cancelled || res.TimedOut || res.OOMKilled {
		t.Errorf("unexpected flags: %+v", res)
	}

	creates := h.actions("create")
	if len(creates) != 1 {
		t.Fatalf("creates = %d, want 1", len(creates))
	}
	ca := creates[0].(k8stesting.CreateAction)
	if ca.GetNamespace() != testNS {
		t.Errorf("namespace = %q", ca.GetNamespace())
	}
	c := ca.GetObject().(*unstructured.Unstructured)
	if c.GetName() != testName || c.GetKind() != "SandboxClaim" {
		t.Errorf("claim = %s %s", c.GetKind(), c.GetName())
	}
	for k, v := range ourLabels() {
		if c.GetLabels()[k] != v {
			t.Errorf("label %s = %q, want %q", k, c.GetLabels()[k], v)
		}
	}
	pool, _, _ := unstructured.NestedString(c.Object, "spec", "warmPoolRef", "name")
	ttl, _, _ := unstructured.NestedInt64(c.Object, "spec", "lifecycle", "ttlSecondsAfterFinished")
	policy, _, _ := unstructured.NestedString(c.Object, "spec", "lifecycle", "shutdownPolicy")
	ann, _, _ := unstructured.NestedStringMap(c.Object, "spec", "additionalPodMetadata", "annotations")
	if pool != "satat-x" || ttl != 300 || policy != "Delete" {
		t.Errorf("pool=%q ttl=%d policy=%q", pool, ttl, policy)
	}
	if ann[annotationInput] != string(input) || ann[annotationRun] != testRun || ann[annotationStep] != testStep {
		t.Errorf("annotations = %v", ann)
	}
	if env, found, _ := unstructured.NestedSlice(c.Object, "spec", "env"); found {
		t.Errorf("claim sets env %v", env)
	}

	deletes := h.actions("delete")
	if len(deletes) != 1 {
		t.Fatalf("deletes = %d, want 1", len(deletes))
	}
	opts := deletes[0].(k8stesting.DeleteActionImpl).DeleteOptions
	if opts.PropagationPolicy == nil || *opts.PropagationPolicy != metav1.DeletePropagationForeground {
		t.Errorf("delete propagation = %v, want Foreground", opts.PropagationPolicy)
	}
	h.assertGone(t)
	for _, a := range append(h.dyn.Actions(), h.core.Actions()...) {
		if a.GetVerb() == "list" {
			t.Errorf("launcher listed %s: no list permission", a.GetResource())
		}
	}
}

func TestLaunchNoFrameUsesPodStatus(t *testing.T) {
	h := newHarness(t)
	h.controller(t, podEnd{exit: 137, reason: "OOMKilled", finished: true})
	res, err := h.launch(ctxT(t), nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !res.OOMKilled || res.ExitCode != 137 || len(res.Stdout) != 0 || len(res.Stderr) != 0 {
		t.Errorf("result = %+v, want OOMKilled exit 137 and no logs", res)
	}
	h.assertGone(t)
}

func TestLaunchRejectedClaim(t *testing.T) {
	h := newHarness(t)
	go func() {
		c := h.waitClaim(t)
		if c == nil {
			return
		}
		c.Object["status"] = map[string]interface{}{"conditions": []interface{}{map[string]interface{}{
			"type": "Ready", "status": "False", "reason": "EnvVarsInjectionRejected", "message": "env is not allowed",
		}}}
		if _, err := h.claims().UpdateStatus(context.Background(), c, metav1.UpdateOptions{}); err != nil {
			t.Errorf("update status: %v", err)
		}
	}()
	_, err := h.launch(ctxT(t), nil)
	if err == nil || !strings.Contains(err.Error(), "EnvVarsInjectionRejected") {
		t.Fatalf("Launch err = %v, want the rejection reason", err)
	}
	h.assertGone(t)
}

func TestLaunchAdoptsMatchingClaim(t *testing.T) {
	h := newHarness(t, existingClaim(ourLabels(), false))
	h.setLog(frame(t, entry.Result{ExitCode: 0, Stdout: []byte("hi\n")}))
	h.controller(t, podEnd{finished: true})
	res, err := h.launch(ctxT(t), nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !res.Adopted || string(res.Stdout) != "hi\n" {
		t.Errorf("result = %+v, want Adopted", res)
	}
	h.assertGone(t)
}

func TestLaunchRefusesForeignClaim(t *testing.T) {
	h := newHarness(t, existingClaim(map[string]interface{}{"app": "other"}, false))
	_, err := h.launch(ctxT(t), nil)
	if err == nil || !strings.Contains(err.Error(), "not this Satat step") {
		t.Fatalf("Launch err = %v, want a refusal", err)
	}
	if _, err := h.claims().Get(context.Background(), testName, metav1.GetOptions{}); err != nil {
		t.Errorf("foreign claim was touched: %v", err)
	}
	if n := len(h.actions("delete")); n != 0 {
		t.Errorf("deletes = %d, want 0", n)
	}
}

func TestLaunchWaitsOutTerminatingClaim(t *testing.T) {
	h := newHarness(t, existingClaim(ourLabels(), true))
	h.setLog(frame(t, entry.Result{Stdout: []byte("fresh\n")}))
	// The old claim goes once its pod and sandbox would have; then the new one runs.
	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := h.dyn.Tracker().Delete(ClaimGVR, testNS, testName); err != nil {
			t.Errorf("delete old claim: %v", err)
		}
	}()
	h.controller(t, podEnd{finished: true})
	res, err := h.launch(ctxT(t), nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.Adopted || string(res.Stdout) != "fresh\n" {
		t.Errorf("result = %+v, want a fresh, not adopted, run", res)
	}
	if n := len(h.actions("create")); n != 2 {
		t.Errorf("creates = %d, want 2 (AlreadyExists, then the fresh claim)", n)
	}
	h.assertGone(t)
}

func TestCancel(t *testing.T) {
	h := newHarness(t)
	if err := h.l.Cancel(context.Background(), testRun, testStep); err != nil {
		t.Fatalf("Cancel with no claim: %v", err)
	}
	done := make(chan launcher.Result, 1)
	errc := make(chan error, 1)
	go func() {
		res, err := h.launch(ctxT(t), nil)
		done <- res
		errc <- err
	}()
	h.waitClaim(t)
	if err := h.l.Cancel(context.Background(), testRun, testStep); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	select {
	case res := <-done:
		if err := <-errc; err != nil {
			t.Fatalf("Launch: %v", err)
		}
		if !res.Cancelled {
			t.Errorf("Cancelled = false: %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Launch did not return after Cancel")
	}
	h.assertGone(t)
}

func TestInputTooLarge(t *testing.T) {
	h := newHarness(t)
	_, err := h.launch(ctxT(t), make([]byte, 200<<10+1))
	if err == nil {
		t.Fatal("Launch accepted an input over 200 KiB")
	}
	if n := len(h.dyn.Actions()) + len(h.core.Actions()); n != 0 {
		t.Errorf("%d API calls before refusing", n)
	}
}

func TestTimeoutFromDeadline(t *testing.T) {
	h := newHarness(t)
	// The claim's Finished condition lags: the terminal pod alone ends the wait.
	h.controller(t, podEnd{exit: 137, reason: "Error", podReason: "DeadlineExceeded"})
	res, err := h.launch(ctxT(t), nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("TimedOut = false: %+v", res)
	}
	h.assertGone(t)
}

func TestContextCancelReturnsError(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := h.launch(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Launch err = %v, want the context's error", err)
	}
	h.assertGone(t)
}

func TestNewRejectsLaneWithoutCommand(t *testing.T) {
	lane := laneXDef()
	lane.Command = nil
	if _, err := New(nil, nil, []launcher.Lane{lane}); err == nil {
		t.Fatal("New accepted a lane without a command")
	}
}
