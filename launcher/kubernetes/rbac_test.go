package kubernetes

import (
	"context"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"

	"github.com/javatarz/satat/launcher"
)

// The worker's Role in a lane namespace grants exactly what the launcher uses, and nothing that
// would let a worker run a pod of its own choosing or read a secret.
func TestWorkerRBACOnKubernetes(t *testing.T) {
	env := e2e(t)
	lane := launcher.Lane{
		Name: "rbac", Image: e2eImage, Command: []string{"true"},
		MemoryBytes: 256 << 20, CPUs: 1, WorkspaceBytes: 32 << 20, Timeout: time.Minute,
	}
	env.applyLane(t, lane)
	ns := Namespace(confPrefix, lane.Name)
	worker, err := k8sclient.NewForConfig(env.worker())
	if err != nil {
		t.Fatal(err)
	}

	type access struct{ verb, group, resource, sub string }
	claims := func(verb string) access { return access{verb, sandboxGroup, "sandboxclaims", ""} }
	allowed := []access{
		claims("create"), claims("get"), claims("watch"), claims("delete"),
		{"get", "", "pods", ""},
		{"get", "", "pods", "log"},
	}
	denied := []access{
		{"create", "", "pods", ""},
		{"create", sandboxGroup, "sandboxtemplates", ""},
		{"create", sandboxGroup, "sandboxwarmpools", ""},
		{"create", sandboxGroup, "sandboxes", ""},
		{"get", "", "secrets", ""},
		{"list", "", "secrets", ""},
		{"create", "", "serviceaccounts", ""},
		claims("list"),
	}
	check := func(a access, want bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r, err := worker.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: ns, Verb: a.verb, Group: a.group, Resource: a.resource, Subresource: a.sub,
			}},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("access review %+v: %v", a, err)
		}
		if r.Status.Allowed != want {
			t.Errorf("worker %s %s/%s (group %q) allowed = %v, want %v", a.verb, a.resource, a.sub, a.group, r.Status.Allowed, want)
		}
	}
	for _, a := range allowed {
		check(a, true)
	}
	for _, a := range denied {
		check(a, false)
	}
}
