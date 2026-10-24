package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
)

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

	group     singleflight.Group
	mu        sync.Mutex
	cancelled map[string]bool
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
		return l.core.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{Container: container}).DoRaw(ctx)
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

func (l *Launcher) run(ctx context.Context, lane launcher.Lane, step launcher.Step, ns, name string) (launcher.Result, error) {
	var res launcher.Result
	key := claimKey(ns, name)

	// Delete the claim on every exit path, even when ctx is already done, but only a claim this
	// call created or adopted.
	owned := false
	defer func() {
		if !owned {
			return
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		l.cleanup(cctx, ns, name)
	}()

	adopted, err := l.createOrAdopt(ctx, lane, step, ns, name)
	if err != nil {
		return res, err
	}
	owned = true
	res.Adopted = adopted

	deadline := lane.Timeout + deadlineGrace + launcherGrace
	out, err := l.wait(ctx, ns, name, deadline)
	if err != nil {
		return res, err
	}
	switch {
	case out.cancelled:
		res.Cancelled = true
	case out.timedOut:
		res.TimedOut = true
	default:
		if err := l.result(ctx, ns, out.pod, &res); err != nil {
			return res, err
		}
	}

	l.mu.Lock()
	res.Cancelled = res.Cancelled || l.cancelled[key]
	delete(l.cancelled, key)
	l.mu.Unlock()
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
// rerun after cleanup is a fresh execution.
func (l *Launcher) createOrAdopt(ctx context.Context, lane launcher.Lane, step launcher.Step, ns, name string) (bool, error) {
	obj := l.claimObject(lane, step, ns, name)
	for attempt := 0; attempt < 3; attempt++ {
		_, err := l.claims(ns).Create(ctx, obj, metav1.CreateOptions{})
		if err == nil {
			return false, nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return false, fmt.Errorf("create claim %s/%s: %w", ns, name, err)
		}
		found, err := l.claims(ns).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("adopt claim %s/%s: %w", ns, name, err)
		}
		if found.GetDeletionTimestamp() != nil {
			l.log.Info("waiting for a deleted claim to go", "claim", name, "namespace", ns)
			wctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
			err := l.waitGone(wctx, ns, name)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				return false, fmt.Errorf("claim %s/%s is still being deleted: %w", ns, name, err)
			}
			continue
		}
		lb := found.GetLabels()
		if lb[labelManaged] != "true" || lb[labelLane] != lane.Name || lb[labelRun] != step.Run || lb[labelStep] != step.Name {
			return false, fmt.Errorf("claim %s/%s exists and is not this Satat step's claim", ns, name)
		}
		l.log.Info("adopted existing claim", "claim", name, "namespace", ns)
		return true, nil
	}
	return false, fmt.Errorf("create claim %s/%s: it kept reappearing", ns, name)
}

// outcome is how waiting for a claim ended without an error.
type outcome struct {
	pod       string
	cancelled bool
	timedOut  bool
}

// wait blocks until the claim's pod is finished, the step is cancelled, or the launcher's
// deadline passes. It watches the one claim from a Get's resourceVersion and re-establishes the
// watch when it closes. The pod is also polled: the claim's Finished condition can lag it, and
// the worker can get pods but not watch them.
func (l *Launcher) wait(ctx context.Context, ns, name string, deadline time.Duration) (outcome, error) {
	dctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	tick := time.NewTicker(l.poll)
	defer tick.Stop()
	pod := name

	ended := func() (outcome, error) {
		if ctx.Err() != nil {
			return outcome{}, ctx.Err()
		}
		l.log.Warn("step outlived the launcher deadline", "claim", name, "namespace", ns, "deadline", deadline)
		return outcome{pod: pod, timedOut: true}, nil
	}

	for {
		claim, err := l.claims(ns).Get(dctx, name, metav1.GetOptions{})
		if dctx.Err() != nil {
			return ended()
		}
		if err != nil {
			if apierrors.IsNotFound(err) {
				return l.gone(ns, name)
			}
			return outcome{}, fmt.Errorf("get claim %s/%s: %w", ns, name, err)
		}
		if out, done, err := l.check(dctx, ns, claim, &pod, true); done || err != nil {
			return out, err
		}
		w, err := l.claims(ns).Watch(dctx, metav1.ListOptions{
			FieldSelector:   "metadata.name=" + name,
			ResourceVersion: claim.GetResourceVersion(),
		})
		if dctx.Err() != nil {
			return ended()
		}
		if err != nil {
			if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
				continue
			}
			return outcome{}, fmt.Errorf("watch claim %s/%s: %w", ns, name, err)
		}
		out, done, err := l.watch(dctx, ns, name, w, tick.C, &pod)
		w.Stop()
		if dctx.Err() != nil && !done && err == nil {
			return ended()
		}
		if done || err != nil {
			return out, err
		}
		// Pace re-watching, so a watch that keeps failing doesn't spin.
		select {
		case <-dctx.Done():
			return ended()
		case <-tick.C:
		}
	}
}

// watch handles one watch until it closes (done false), or the wait is over.
func (l *Launcher) watch(ctx context.Context, ns, name string, w watch.Interface, tick <-chan time.Time, pod *string) (outcome, bool, error) {
	for {
		select {
		case <-ctx.Done():
			return outcome{}, false, nil
		case <-tick:
			if l.isCancelled(claimKey(ns, name)) {
				return outcome{pod: *pod, cancelled: true}, true, nil
			}
			finished, err := l.podFinished(ctx, ns, *pod)
			if err != nil {
				return outcome{}, true, err
			}
			if finished {
				return outcome{pod: *pod}, true, nil
			}
		case ev, ok := <-w.ResultChan():
			if !ok {
				return outcome{}, false, nil
			}
			switch ev.Type {
			case watch.Error:
				// Too old a resourceVersion, or any other watch error: Get and watch again.
				l.log.Info("claim watch error, re-watching", "claim", name, "err", apierrors.FromObject(ev.Object))
				return outcome{}, false, nil
			case watch.Deleted:
				if u, ok := ev.Object.(*unstructured.Unstructured); ok && u.GetName() != name {
					continue
				}
				out, err := l.gone(ns, name)
				return out, true, err
			case watch.Added, watch.Modified:
				u, ok := ev.Object.(*unstructured.Unstructured)
				if !ok || u.GetName() != name {
					continue
				}
				if out, done, err := l.check(ctx, ns, u, pod, false); done || err != nil {
					return out, done || err != nil, err
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
func (l *Launcher) check(ctx context.Context, ns string, claim *unstructured.Unstructured, pod *string, checkPod bool) (outcome, bool, error) {
	name := claim.GetName()
	if claim.GetDeletionTimestamp() != nil {
		out, err := l.gone(ns, name)
		return out, true, err
	}
	if sb, _, _ := unstructured.NestedString(claim.Object, "status", "sandbox", "name"); sb != "" {
		*pod = sb
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
			return outcome{pod: *pod}, true, nil
		case typ == "Ready" && status == string(metav1.ConditionFalse) && rejected[reason]:
			return outcome{}, true, fmt.Errorf("claim %s/%s was rejected: %s: %s", ns, name, reason, msg)
		}
	}
	if !checkPod {
		return outcome{}, false, nil
	}
	finished, err := l.podFinished(ctx, ns, *pod)
	if err != nil {
		return outcome{}, true, err
	}
	if finished {
		return outcome{pod: *pod}, true, nil
	}
	return outcome{}, false, nil
}

func (l *Launcher) podFinished(ctx context.Context, ns, name string) (bool, error) {
	p, err := l.core.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) || ctx.Err() != nil {
			return false, nil
		}
		return false, fmt.Errorf("get pod %s/%s: %w", ns, name, err)
	}
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed, nil
}

func (l *Launcher) isCancelled(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cancelled[key]
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
// then returns Cancelled. A step with no claim, or whose claim has finished, is not running.
func (l *Launcher) Cancel(ctx context.Context, run, name string) error {
	cname := claimName(run, name)
	for _, lane := range l.lanes {
		ns := Namespace(l.prefix, lane.Name)
		claim, err := l.claims(ns).Get(ctx, cname, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cancel %s/%s: %w", ns, cname, err)
		}
		if claim.GetLabels()[labelManaged] != "true" {
			return fmt.Errorf("claim %s/%s is not a Satat step claim", ns, cname)
		}
		if claim.GetDeletionTimestamp() != nil || finished(claim) {
			continue
		}
		key := claimKey(ns, cname)
		l.mu.Lock()
		l.cancelled[key] = true
		l.mu.Unlock()
		if err := l.deleteClaim(ctx, ns, cname); err != nil {
			return fmt.Errorf("cancel %s/%s: %w", ns, cname, err)
		}
	}
	return nil
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
