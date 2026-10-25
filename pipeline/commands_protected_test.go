package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/pusher"
	"github.com/javatarz/satat/runner"
)

// The pusher reads an optional top-level `protected:` key from the same file (#17). The file stays
// version 1, so ParseCommands, and the readiness check built on it, must ignore the key.
func TestParseCommandsIgnoresTheProtectedSection(t *testing.T) {
	in := goodCommands + "protected:\n  add: [justfile]\n  allow: [.editorconfig]\n"
	cmds, err := ParseCommands([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if cmds["setup"] != "just setup" || cmds["test"] != "go test ./..." || len(cmds) != 3 {
		t.Errorf("commands = %v", cmds)
	}
}

// The pusher reads the policy from the base commit, so the base the worker passes must be its own
// clone's commit and never the step's word for it.
func TestPublishUsesTheWorkersBaseNotTheStepsReport(t *testing.T) {
	// result("changes") reports base "b", which is what fakeCloner cloned: it passes.
	l := &fakeLauncher{res: result("changes")}
	p := &fakePublisher{}
	rep, err := Run(context.Background(), opts(l, p))
	if err != nil || rep.PushErr != nil || p.req.BaseSHA != "b" {
		t.Fatalf("rep=%+v err=%v req=%+v", rep, err, p.req)
	}
	// A step that reports a different base is refused without calling the pusher.
	var r runner.Result
	json.Unmarshal(l.res.Files["result.json"], &r)
	r.BaseSHA = "forged"
	b, _ := json.Marshal(r)
	l2 := &fakeLauncher{res: launcher.Result{Files: map[string][]byte{"result.json": b, "changes.bundle": []byte("bundle")}}}
	p2 := &fakePublisher{}
	rep, err = Run(context.Background(), opts(l2, p2))
	var ref *pusher.Refusal
	if err != nil || !errors.As(rep.PushErr, &ref) || p2.n != 0 {
		t.Fatalf("rep=%+v err=%v pushes=%d, want a refusal and no push", rep, err, p2.n)
	}
}
