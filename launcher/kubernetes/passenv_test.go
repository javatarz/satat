package kubernetes

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	k8sclient "k8s.io/client-go/kubernetes"

	"github.com/javatarz/satat/launcher"
)

// TestPassEnvOnKubernetes runs a lane with pass_env against a lane Secret: the value reaches the
// step and is scrubbed from its output, and a run whose Secret lacks the key fails with 125.
func TestPassEnvOnKubernetes(t *testing.T) {
	env := e2e(t)
	const (
		name   = "passenv"
		key    = "SATAT_MARKER"
		marker = "satat-e2e-marker-7f3a"
	)
	lane := launcher.Lane{
		Name: name, Image: e2eImage, Command: []string{"sh", "-c", `echo "got $` + key + `"`},
		PassEnv:     []string{key},
		MemoryBytes: 256 << 20, CPUs: 1, PidsLimit: 128, WorkspaceBytes: 32 << 20,
		Timeout: time.Minute,
	}
	env.applyLane(t, lane)
	ns := Namespace(confPrefix, name)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	secrets := env.core.CoreV1().Secrets(ns)
	if _, err := secrets.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: laneSecret},
		Data:       map[string][]byte{key: []byte(marker), "STRAY": []byte("not passed")},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create lane Secret: %v", err)
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
	l, err := New(dyn, core, []launcher.Lane{lane}, WithNamespacePrefix(confPrefix))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := l.Launch(ctx, launcher.Step{Run: "pe1", Name: "marker", Lane: name})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.ExitCode != 0 || string(res.Stdout) != "got ***\n" {
		t.Errorf("exit=%d stdout=%q stderr=%q, want exit 0 and the marker scrubbed", res.ExitCode, res.Stdout, res.Stderr)
	}
	if strings.Contains(string(res.Stdout)+string(res.Stderr), marker) {
		t.Error("the marker value reached the result")
	}

	// The key is gone from the Secret: the step fails with 125 and never runs the command.
	if err := secrets.Delete(ctx, laneSecret, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("delete lane Secret: %v", err)
	}
	if _, err := secrets.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: laneSecret},
		Data:       map[string][]byte{"STRAY": []byte("not passed")},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("recreate lane Secret: %v", err)
	}
	res, err = l.Launch(ctx, launcher.Step{Run: "pe2", Name: "missing", Lane: name})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.ExitCode != 125 || !strings.Contains(string(res.Stderr), key) || strings.Contains(string(res.Stdout), "got") {
		t.Errorf("exit=%d stdout=%q stderr=%q, want 125 naming %s", res.ExitCode, res.Stdout, res.Stderr, key)
	}
}
