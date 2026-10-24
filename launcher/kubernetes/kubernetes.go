package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	k8sclient "k8s.io/client-go/kubernetes"

	"github.com/javatarz/satat/entry"
	"github.com/javatarz/satat/launcher"
)

const (
	labelLane = "satat.dev/lane"
	labelRun  = "satat.dev/run"
	labelStep = "satat.dev/step"

	// maxInput is the largest step input the input annotation carries.
	maxInput = 200 << 10
	// claimTTL is how long a finished claim stays if nothing deletes it first.
	claimTTL = int64(300)

	// launcherGrace is added to the pod's own deadline (lane timeout + deadlineGrace) before the
	// launcher stops waiting and deletes the claim itself.
	launcherGrace = 30 * time.Second

	cleanupTimeout = 60 * time.Second
	defaultPoll    = time.Second
	// maxTransient is how many API errors in a row a wait retries before it fails the launch.
	maxTransient = 3
	// defaultStartGrace is how long a container may keep waiting for a reason in unstartable
	// before the launch fails. A pull error can be transient (a registry hiccup), and the kubelet
	// retries a pull after a 10 s back-off, then 20 s: 30 s lets one retry land.
	defaultStartGrace = 30 * time.Second
	// maxLog bounds a step log read. A frame at the caps is about 8 MiB (1 + 1 + 4 MiB as base64)
	// and kubelet rotates a container log at 10 MiB, so 12 MiB holds any whole frame.
	maxLog = 12 << 20
	// podInfoTimeout bounds reading the pod once more after the launcher's deadline.
	podInfoTimeout = 10 * time.Second
)

// unstartable are the container waiting reasons that keep a pod from ever starting. The launch
// fails once one is seen on at least two checks in a row spanning the start grace; an invalid
// image name never recovers, so it needs only the two checks.
var unstartable = map[string]bool{
	"ErrImagePull":               true,
	"ImagePullBackOff":           true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
}

// rejected are the claim Ready=False reasons that mean the claim can never run as it stands.
var rejected = map[string]bool{
	"TemplateNotFound":          true,
	"WarmPoolNotFound":          true,
	"EnvVarsInjectionRejected":  true,
	"InvalidMetadata":           true,
	"VolumeClaimTemplatesError": true,
	"InvalidConfiguration":      true,
}

// Launcher runs steps as one-shot SandboxClaims. It needs only what the worker Role grants in
// each lane namespace: sandboxclaims create, get, watch and delete; pods and pods/log get. It
// never lists.
type Launcher struct {
	dyn    dynamic.Interface
	core   k8sclient.Interface
	lanes  map[string]launcher.Lane
	prefix string
	log    *slog.Logger
	poll   time.Duration
	// logs reads a container's log; tests replace it, since the fake clientset's pods/log body is fixed.
	logs func(ctx context.Context, ns, pod, container string) ([]byte, error)
	// startGrace is how long a container may wait for a reason in unstartable before the launch
	// fails (defaultStartGrace); deadline replaces the computed launcher deadline. Tests set both.
	startGrace time.Duration
	deadline   time.Duration

	group     singleflight.Group
	mu        sync.Mutex
	cancelled map[string]bool
	// cancelMu orders Cancel's check-and-mark against a run settling its flag after cleanup.
	cancelMu sync.Mutex
}

// Option adjusts a Launcher.
type Option func(*Launcher)

// WithNamespacePrefix replaces DefaultNamespacePrefix.
func WithNamespacePrefix(p string) Option { return func(l *Launcher) { l.prefix = p } }

// WithLogger sets the logger.
func WithLogger(lg *slog.Logger) Option { return func(l *Launcher) { l.log = lg } }

// WithPoll sets how often the step's pod is checked while waiting. The default is one second.
func WithPoll(d time.Duration) Option { return func(l *Launcher) { l.poll = d } }

// New builds a Launcher for the given lanes, checking each the way LaneObjects does.
func New(dyn dynamic.Interface, core k8sclient.Interface, lanes []launcher.Lane, opts ...Option) (*Launcher, error) {
	l := &Launcher{
		dyn:       dyn,
		core:      core,
		lanes:     map[string]launcher.Lane{},
		prefix:    DefaultNamespacePrefix,
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		poll:      defaultPoll,
		cancelled: map[string]bool{},
	}
	l.logs = func(ctx context.Context, ns, pod, container string) ([]byte, error) {
		return l.core.CoreV1().Pods(ns).GetLogs(pod, logOptions(container)).DoRaw(ctx)
	}
	for _, o := range opts {
		o(l)
	}
	if l.poll <= 0 {
		return nil, errors.New("poll interval must be positive")
	}
	for _, lane := range lanes {
		n, err := normalizeLane(lane)
		if err != nil {
			return nil, err
		}
		if _, dup := l.lanes[n.Name]; dup {
			return nil, fmt.Errorf("lane %q is defined twice", n.Name)
		}
		ns := Namespace(l.prefix, n.Name)
		if errs := validation.IsDNS1123Label(ns); len(errs) != 0 {
			return nil, fmt.Errorf("lane %q: namespace %q is not valid: %s", n.Name, ns, strings.Join(errs, "; "))
		}
		l.lanes[n.Name] = n
	}
	return l, nil
}

// Setup does nothing: deploy applies each lane's template, pool and Role.
func (l *Launcher) Setup(context.Context) error { return nil }

// Ping checks the API server answers.
func (l *Launcher) Ping(context.Context) error {
	if _, err := l.core.Discovery().ServerVersion(); err != nil {
		return fmt.Errorf("kubernetes api: %w", err)
	}
	return nil
}

func claimName(run, step string) string { return resourcePrefix + run + "-" + step }

func claimKey(ns, name string) string { return ns + "/" + name }

func (l *Launcher) claims(ns string) dynamic.ResourceInterface {
	return l.dyn.Resource(ClaimGVR).Namespace(ns)
}

// Launch implements launcher.Launcher.
func (l *Launcher) Launch(ctx context.Context, step launcher.Step) (launcher.Result, error) {
	lane, ok := l.lanes[step.Lane]
	if !ok {
		return launcher.Result{}, fmt.Errorf("unknown lane %q", step.Lane)
	}
	name := claimName(step.Run, step.Name)
	for _, id := range []string{step.Run, step.Name} {
		if errs := validation.IsValidLabelValue(id); len(errs) != 0 || id == "" {
			return launcher.Result{}, fmt.Errorf("step id %q is not valid on Kubernetes", id)
		}
	}
	if errs := validation.IsDNS1123Label(name); len(errs) != 0 {
		return launcher.Result{}, fmt.Errorf("claim name %q is not valid: %s", name, strings.Join(errs, "; "))
	}
	if len(step.Input) > maxInput {
		return launcher.Result{}, fmt.Errorf("step input is %d bytes, over the %d byte limit", len(step.Input), maxInput)
	}
	ns := Namespace(l.prefix, lane.Name)
	key := claimKey(ns, name)
	// Callers for one step in this process share one claim. The first caller's context governs
	// the run; a joined caller sees its outcome.
	v, err, _ := l.group.Do(key, func() (any, error) {
		return l.run(ctx, lane, step, ns, name)
	})
	res, _ := v.(launcher.Result)
	return res, err
}

func (l *Launcher) run(ctx context.Context, lane launcher.Lane, step launcher.Step, ns, name string) (res launcher.Result, err error) {
	key := claimKey(ns, name)

	// On every exit path, even when ctx is already done, delete the claim this call created or
	// adopted, or one a failed create may still have stored. Only then settle the cancel flag:
	// once the claim is gone a late Cancel finds nothing to mark, so no flag outlives this run.
	var owned, maybeStored bool
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		switch {
		case owned:
			l.cleanup(cctx, ns, name)
		case maybeStored:
			l.cleanupIfOurs(cctx, lane, step, ns, name)
		}
		l.cancelMu.Lock()
		defer l.cancelMu.Unlock()
		l.mu.Lock()
		defer l.mu.Unlock()
		if err == nil && l.cancelled[key] {
			res.Cancelled = true
		}
		delete(l.cancelled, key)
	}()

	adopted, uncertain, err := l.createOrAdopt(ctx, lane, step, ns, name)
	if err != nil {
		maybeStored = uncertain
		return res, err
	}
	owned = true
	res.Adopted = adopted

	deadline := lane.Timeout + deadlineGrace + launcherGrace
	if l.deadline > 0 {
		deadline = l.deadline
	}
	out, err := l.wait(ctx, ns, name, deadline)
	if err != nil {
		return res, err
	}
	switch {
	case out.cancelled:
		res.Cancelled = true
	case out.timedOut:
		res.TimedOut = true
		if out.pending != "" {
			res.Stderr = fmt.Appendf(nil, "satat: the launcher deadline passed with pod %s/%s still Pending: %s\n", ns, out.pod, out.pending)
		}
	default:
		if err := l.result(ctx, ns, out.pod, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}
func (l *Launcher) claimObject(lane launcher.Lane, step launcher.Step, ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": sandboxAPIVersion,
		"kind":       "SandboxClaim",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": ns,
			"labels": map[string]interface{}{
				labelManaged: "true",
				labelLane:    lane.Name,
				labelRun:     step.Run,
				labelStep:    step.Name,
			},
		},
		"spec": map[string]interface{}{
			"warmPoolRef": map[string]interface{}{"name": templateName(lane.Name)},
			"lifecycle": map[string]interface{}{
				"ttlSecondsAfterFinished": claimTTL,
				"shutdownPolicy":          "Delete",
			},
			"additionalPodMetadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					annotationRun:   step.Run,
					annotationStep:  step.Name,
					annotationInput: string(step.Input),
				},
			},
		},
	}}
}

// createOrAdopt creates the step's claim. A claim of that name an earlier attempt left is adopted
// if its labels say it is this step's; one still being deleted is waited out and replaced, so a
// rerun after cleanup is a fresh execution. uncertain is set when a create failed in a way that
// may still have stored the claim.
func (l *Launcher) createOrAdopt(ctx context.Context, lane launcher.Lane, step launcher.Step, ns, name string) (adopted, uncertain bool, err error) {
	obj := l.claimObject(lane, step, ns, name)
	for attempt := 0; attempt < 3; attempt++ {
		_, err := l.claims(ns).Create(ctx, obj, metav1.CreateOptions{})
		if err == nil {
			return false, false, nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return false, createUncertain(ctx, err), fmt.Errorf("create claim %s/%s: %w", ns, name, err)
		}
		found, err := l.claims(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, false, fmt.Errorf("adopt claim %s/%s: %w", ns, name, err)
		}
		if found.GetDeletionTimestamp() != nil {
			l.log.Info("waiting for a deleted claim to go", "claim", name, "namespace", ns)
			wctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
			err := l.waitGone(wctx, ns, name)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return false, false, ctx.Err()
				}
				return false, false, fmt.Errorf("claim %s/%s is still being deleted: %w", ns, name, err)
			}
			continue
		}
		if !ours(found, lane, step) {
			return false, false, fmt.Errorf("claim %s/%s exists and is not this Satat step's claim", ns, name)
		}
		l.log.Info("adopted existing claim", "claim", name, "namespace", ns)
		return true, false, nil
	}
	return false, false, fmt.Errorf("create claim %s/%s: it kept reappearing", ns, name)
}

// createUncertain reports whether a failed create may still have stored the claim: the request
// got no API answer (the context ended, or a transport error), or the answer was a timeout.
func createUncertain(ctx context.Context, err error) bool {
	var st apierrors.APIStatus
	return ctx.Err() != nil || !errors.As(err, &st) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err)
}

// ours reports whether claim carries this step's labels.
func ours(claim *unstructured.Unstructured, lane launcher.Lane, step launcher.Step) bool {
	lb := claim.GetLabels()
	return lb[labelManaged] == "true" && lb[labelLane] == lane.Name && lb[labelRun] == step.Run && lb[labelStep] == step.Name
}

// outcome is how waiting for a claim ended without an error. pending says why the pod had not
// started when the deadline passed, if it was still Pending.
type outcome struct {
	pod       string
	cancelled bool
	timedOut  bool
	pending   string
}

// waiter is one wait for one claim. It counts transient API errors in a row, and the checks in a
// row that found a container waiting for an unstartable reason, since stuckSince.
type waiter struct {
	l          *Launcher
	ns, name   string
	pod        string
	fails      int
	stuck      int
	stuckSince time.Time
}

// tolerate records a transient API error. It returns the error once more than maxTransient have
// come in a row; until then the wait goes on.
func (w *waiter) tolerate(what string, err error) error {
	w.fails++
	if w.fails > maxTransient {
		return fmt.Errorf("%s %s/%s: %w", what, w.ns, w.name, err)
	}
	w.l.log.Warn("api error while waiting, retrying", "op", what, "claim", w.name, "namespace", w.ns, "err", err)
	return nil
}

// wait blocks until the claim's pod is finished, the step is cancelled, or the launcher's
// deadline passes. It watches the one claim from a Get's resourceVersion and re-establishes the
// watch when it closes. The pod is also polled: the claim's Finished condition can lag it, and
// the worker can get pods but not watch them. Up to maxTransient API errors in a row are retried.
func (l *Launcher) wait(ctx context.Context, ns, name string, deadline time.Duration) (outcome, error) {
	dctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	tick := time.NewTicker(l.poll)
	defer tick.Stop()
	w := &waiter{l: l, ns: ns, name: name, pod: name}

	ended := func() (outcome, error) {
		if ctx.Err() != nil {
			return outcome{}, ctx.Err()
		}
		l.log.Warn("step outlived the launcher deadline", "claim", name, "namespace", ns, "deadline", deadline)
		out := outcome{pod: w.pod, timedOut: true}
		pctx, cancel := context.WithTimeout(ctx, podInfoTimeout)
		defer cancel()
		if p, err := l.core.CoreV1().Pods(ns).Get(pctx, w.pod, metav1.GetOptions{}); err == nil {
			out.pending = pendingReason(p)
		}
		return out, nil
	}
	// pause paces retries and re-watches, so a failing call doesn't spin.
	pause := func() bool {
		select {
		case <-dctx.Done():
			return false
		case <-tick.C:
			return true
		}
	}

	for {
		claim, err := l.claims(ns).Get(dctx, name, metav1.GetOptions{})
		if dctx.Err() != nil {
			return ended()
		}
		if apierrors.IsNotFound(err) {
			return l.gone(ns, name)
		}
		if err != nil {
			if err := w.tolerate("get claim", err); err != nil {
				return outcome{}, err
			}
			if !pause() {
				return ended()
			}
			continue
		}
		w.fails = 0
		if out, done, err := w.check(dctx, claim, true); done || err != nil {
			return out, err
		}
		wi, err := l.claims(ns).Watch(dctx, metav1.ListOptions{
			FieldSelector:   "metadata.name=" + name,
			ResourceVersion: claim.GetResourceVersion(),
		})
		if dctx.Err() != nil {
			return ended()
		}
		if err != nil {
			if !apierrors.IsResourceExpired(err) && !apierrors.IsGone(err) {
				if err := w.tolerate("watch claim", err); err != nil {
					return outcome{}, err
				}
			}
			if !pause() {
				return ended()
			}
			continue
		}
		out, done, err := w.watch(dctx, wi, tick.C)
		wi.Stop()
		if done || err != nil {
			return out, err
		}
		if !pause() {
			return ended()
		}
	}
}

// watch handles one watch until it closes (done false), or the wait is over.
func (w *waiter) watch(ctx context.Context, wi watch.Interface, tick <-chan time.Time) (outcome, bool, error) {
	for {
		select {
		case <-ctx.Done():
			return outcome{}, false, nil
		case <-tick:
			if w.l.isCancelled(claimKey(w.ns, w.name)) {
				return outcome{pod: w.pod, cancelled: true}, true, nil
			}
			finished, err := w.podFinished(ctx)
			if err != nil {
				return outcome{}, true, err
			}
			if finished {
				return outcome{pod: w.pod}, true, nil
			}
		case ev, ok := <-wi.ResultChan():
			if !ok {
				return outcome{}, false, nil
			}
			switch ev.Type {
			case watch.Error:
				// Too old a resourceVersion, or any other watch error: Get and watch again.
				w.l.log.Info("claim watch error, re-watching", "claim", w.name, "err", apierrors.FromObject(ev.Object))
				return outcome{}, false, nil
			case watch.Deleted:
				if u, ok := ev.Object.(*unstructured.Unstructured); ok && u.GetName() != w.name {
					continue
				}
				out, err := w.l.gone(w.ns, w.name)
				return out, true, err
			case watch.Added, watch.Modified:
				u, ok := ev.Object.(*unstructured.Unstructured)
				if !ok || u.GetName() != w.name {
					continue
				}
				if out, done, err := w.check(ctx, u, false); done || err != nil {
					return out, true, err
				}
			}
		}
	}
}

// gone is the outcome when the claim disappeared while the step was running.
func (l *Launcher) gone(ns, name string) (outcome, error) {
	if l.isCancelled(claimKey(ns, name)) {
		return outcome{pod: name, cancelled: true}, nil
	}
	return outcome{}, fmt.Errorf("claim %s/%s was deleted before the step finished", ns, name)
}

// check reads one state of the claim. It reports done when the step's pod has finished or the
// step was cancelled, and an error when the claim was rejected. The pod is checked once the
// claim names its sandbox, or always when checkPod is set.
func (w *waiter) check(ctx context.Context, claim *unstructured.Unstructured, checkPod bool) (outcome, bool, error) {
	if claim.GetDeletionTimestamp() != nil {
		out, err := w.l.gone(w.ns, w.name)
		return out, true, err
	}
	if sb := sandboxName(claim); sb != "" {
		w.pod = sb
		checkPod = true
	}
	conds, _, _ := unstructured.NestedSlice(claim.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		status, _ := m["status"].(string)
		reason, _ := m["reason"].(string)
		msg, _ := m["message"].(string)
		switch {
		case typ == "Finished" && status == string(metav1.ConditionTrue):
			return outcome{pod: w.pod}, true, nil
		case typ == "Ready" && status == string(metav1.ConditionFalse) && rejected[reason]:
			return outcome{}, true, fmt.Errorf("claim %s/%s was rejected: %s: %s", w.ns, w.name, reason, msg)
		}
	}
	if !checkPod {
		return outcome{}, false, nil
	}
	finished, err := w.podFinished(ctx)
	if err != nil {
		return outcome{}, true, err
	}
	if finished {
		return outcome{pod: w.pod}, true, nil
	}
	return outcome{}, false, nil
}

// podFinished reports whether the step's pod is terminal. A missing pod is not; an API error
// counts against the transient budget. A pod that can't start is an error naming the container
// and the reason, once seen on two checks in a row spanning the start grace.
func (w *waiter) podFinished(ctx context.Context) (bool, error) {
	p, err := w.l.core.CoreV1().Pods(w.ns).Get(ctx, w.pod, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) || ctx.Err() != nil {
			return false, nil
		}
		return false, w.tolerate("get pod", err)
	}
	w.fails = 0
	if terminal(p) {
		return true, nil
	}
	why, reason := cannotStart(p)
	if why == "" {
		w.stuck = 0
		return false, nil
	}
	now := time.Now()
	if w.stuck == 0 {
		w.stuckSince = now
	}
	w.stuck++
	grace := w.l.startGrace
	if grace <= 0 {
		grace = defaultStartGrace
	}
	if reason == "InvalidImageName" {
		grace = 0
	}
	if w.stuck >= 2 && now.Sub(w.stuckSince) >= grace {
		return false, fmt.Errorf("pod %s/%s can't start: %s", w.ns, w.pod, why)
	}
	return false, nil
}

// cannotStart describes the first container, init containers first, that waits for a reason in
// unstartable, and returns that reason; both are empty when there is none.
func cannotStart(p *corev1.Pod) (why, reason string) {
	for _, cs := range append(slices.Clone(p.Status.InitContainerStatuses), p.Status.ContainerStatuses...) {
		if wt := cs.State.Waiting; wt != nil && unstartable[wt.Reason] {
			return waitingText(cs.Name, wt), wt.Reason
		}
	}
	return "", ""
}

// pendingReason says why a Pending pod has not started: each waiting container's reason and
// message, else why it is unscheduled. It is empty for a pod past Pending.
func pendingReason(p *corev1.Pod) string {
	if p.Status.Phase != corev1.PodPending {
		return ""
	}
	var parts []string
	for _, cs := range append(slices.Clone(p.Status.InitContainerStatuses), p.Status.ContainerStatuses...) {
		if wt := cs.State.Waiting; wt != nil && wt.Reason != "" {
			parts = append(parts, waitingText(cs.Name, wt))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "; ")
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
			return "not scheduled: " + c.Reason + ": " + c.Message
		}
	}
	return "no container has started"
}

func waitingText(container string, wt *corev1.ContainerStateWaiting) string {
	s := "container " + container + " waiting: " + wt.Reason
	if wt.Message != "" {
		s += ": " + wt.Message
	}
	return s
}

func terminal(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}

// sandboxName is the claim's status.sandbox.name, which names its pod.
func sandboxName(claim *unstructured.Unstructured) string {
	sb, _, _ := unstructured.NestedString(claim.Object, "status", "sandbox", "name")
	return sb
}

func (l *Launcher) isCancelled(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cancelled[key]
}

// logOptions reads one container's log, at most maxLog bytes.
func logOptions(container string) *corev1.PodLogOptions {
	return &corev1.PodLogOptions{Container: container, LimitBytes: ptr(int64(maxLog))}
}

// result fills res from the finished pod: its step container's status and the last result frame
// in that container's log. Without a frame, the exit code and the OOM flag come from the pod and
// the result has no logs.
func (l *Launcher) result(ctx context.Context, ns, pod string, res *launcher.Result) error {
	p, err := l.core.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get pod %s/%s: %w", ns, pod, err)
	}
	var term *corev1.ContainerStateTerminated
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == stepContainer {
			term = cs.State.Terminated
		}
	}
	res.TimedOut = p.Status.Reason == "DeadlineExceeded"

	log, err := l.logs(ctx, ns, pod, stepContainer)
	if err != nil {
		l.log.Warn("read step log", "pod", pod, "namespace", ns, "err", err)
		log = nil
	}
	frame, ok, err := entry.Parse(log)
	if err != nil {
		return fmt.Errorf("pod %s/%s: %w", ns, pod, err)
	}
	if !ok {
		if term == nil && !res.TimedOut {
			return fmt.Errorf("pod %s/%s: step container has no exit status and no result frame", ns, pod)
		}
		if term != nil {
			res.ExitCode = int(term.ExitCode)
			res.OOMKilled = term.Reason == "OOMKilled"
		}
		return nil
	}
	files, err := entry.UnpackFiles(frame.Output)
	if err != nil {
		return fmt.Errorf("pod %s/%s: read output: %w", ns, pod, err)
	}
	res.ExitCode = frame.ExitCode
	res.OOMKilled = frame.OOMKilled || (term != nil && term.Reason == "OOMKilled")
	res.TimedOut = res.TimedOut || frame.TimedOut
	res.Stdout, res.Stderr = frame.Stdout, frame.Stderr
	res.LogsTruncated = frame.LogsTruncated
	res.Files = files
	res.FilesTruncated = frame.FilesTruncated
	return nil
}

// cleanup deletes the claim with foreground propagation and waits until it, its Sandbox and its
// pod are gone, so the next launch of the step starts fresh. Errors are logged; the claim's TTL
// and deploy down catch what this misses.
func (l *Launcher) cleanup(ctx context.Context, ns, name string) {
	if err := l.deleteClaim(ctx, ns, name); err != nil {
		l.log.Warn("delete claim", "claim", name, "namespace", ns, "err", err)
		return
	}
	if err := l.waitGone(ctx, ns, name); err != nil {
		l.log.Warn("wait for claim deletion", "claim", name, "namespace", ns, "err", err)
	}
}

// cleanupIfOurs is cleanup for a claim a failed create may have stored: it deletes the claim only
// if it carries this step's labels.
func (l *Launcher) cleanupIfOurs(ctx context.Context, lane launcher.Lane, step launcher.Step, ns, name string) {
	claim, err := l.claims(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			l.log.Warn("get claim after a failed create", "claim", name, "namespace", ns, "err", err)
		}
		return
	}
	if ours(claim, lane, step) {
		l.cleanup(ctx, ns, name)
	}
}

func (l *Launcher) deleteClaim(ctx context.Context, ns, name string) error {
	fg := metav1.DeletePropagationForeground
	err := l.claims(ns).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &fg})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// waitGone polls until the claim is NotFound or ctx ends.
func (l *Launcher) waitGone(ctx context.Context, ns, name string) error {
	t := time.NewTicker(l.poll)
	defer t.Stop()
	for {
		_, err := l.claims(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			l.log.Warn("get claim", "claim", name, "namespace", ns, "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// Cancel implements launcher.Launcher. It marks the step cancelled and deletes its claim; Launch
// then returns Cancelled. A step with no claim, or whose claim or pod has finished, is not
// running.
func (l *Launcher) Cancel(ctx context.Context, run, name string) error {
	cname := claimName(run, name)
	for _, lane := range l.lanes {
		ns := Namespace(l.prefix, lane.Name)
		marked, err := l.markCancelled(ctx, ns, cname)
		if err != nil {
			return fmt.Errorf("cancel %s/%s: %w", ns, cname, err)
		}
		if !marked {
			continue
		}
		if err := l.deleteClaim(ctx, ns, cname); err != nil {
			return fmt.Errorf("cancel %s/%s: %w", ns, cname, err)
		}
	}
	return nil
}

// markCancelled sets the cancel flag when the claim exists and its step is still running. It
// holds cancelMu from the check to the mark, so a run that has deleted its claim and settled its
// flag can't be marked afterwards.
func (l *Launcher) markCancelled(ctx context.Context, ns, cname string) (bool, error) {
	l.cancelMu.Lock()
	defer l.cancelMu.Unlock()
	claim, err := l.claims(ns).Get(ctx, cname, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if claim.GetLabels()[labelManaged] != "true" {
		return false, errors.New("the claim is not a Satat step claim")
	}
	if claim.GetDeletionTimestamp() != nil || finished(claim) {
		return false, nil
	}
	pod := sandboxName(claim)
	if pod == "" {
		pod = cname
	}
	p, err := l.core.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	switch {
	case err == nil && terminal(p):
		return false, nil
	case err != nil && !apierrors.IsNotFound(err):
		return false, err
	}
	l.mu.Lock()
	l.cancelled[claimKey(ns, cname)] = true
	l.mu.Unlock()
	return true, nil
}

func finished(claim *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(claim.Object, "status", "conditions")
	for _, c := range conds {
		if m, ok := c.(map[string]interface{}); ok && m["type"] == "Finished" && m["status"] == string(metav1.ConditionTrue) {
			return true
		}
	}
	return false
}

var _ launcher.Launcher = (*Launcher)(nil)
