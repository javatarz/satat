package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/javatarz/satat/config"
)

var intakeCfg = config.Config{
	Accounts:    map[string]string{"claude": "claude-subscription"},
	LaneMaxRuns: map[string]int{"claude": 1},
}

// labeled is GitHub's `issues.labeled` body, trimmed to what matters.
const labeled = `{"action":"labeled","label":{"name":"ready-for-agent"},` +
	`"issue":{"number":7,"title":"Add greet","body":"do it","state":"open"},` +
	`"repository":{"full_name":"javatarz/satat-sandbox","default_branch":"trunk"}}`

func payload(t *testing.T, raw string, edit ...func(map[string]any)) WebhookPayload {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	for _, e := range edit {
		e(m)
	}
	b, _ := json.Marshal(m)
	var p WebhookPayload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMapWebhookBuildsRunInput(t *testing.T) {
	in, ok, reason, err := MapWebhook(intakeCfg, []string{"javatarz/satat-sandbox"}, "claude", payload(t, labeled))
	if err != nil || !ok {
		t.Fatalf("ok=%v reason=%q err=%v", ok, reason, err)
	}
	if in.Repo != "javatarz/satat-sandbox" || in.Base != "trunk" || in.Lane != "claude" ||
		in.Account != "claude-subscription" || in.LaneCap != 1 ||
		in.Issue.Number != 7 || in.Issue.Title != "Add greet" || in.Issue.Body != "do it" {
		t.Errorf("input: %+v", in)
	}
}

func TestMapWebhookSkipsWhatIsNotARequest(t *testing.T) {
	allowed := []string{"javatarz/satat-sandbox"}
	cases := map[string]struct {
		edit allowedEdit
		want string
	}{
		"other action":   {func(m map[string]any) { m["action"] = "opened" }, "not labeled"},
		"other label":    {func(m map[string]any) { m["label"] = map[string]any{"name": "bug"} }, "not " + ReadyLabel},
		"no label":       {func(m map[string]any) { delete(m, "label") }, "not " + ReadyLabel},
		"pull request":   {func(m map[string]any) { m["issue"].(map[string]any)["pull_request"] = map[string]any{"url": "x"} }, "pull request"},
		"closed":         {func(m map[string]any) { m["issue"].(map[string]any)["state"] = "closed" }, "closed"},
		"unlisted repo":  {func(m map[string]any) { m["repository"].(map[string]any)["full_name"] = "someone/else" }, "not an allowed repo"},
		"case-folded ok": {nil, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var p WebhookPayload
			if c.edit == nil {
				p = payload(t, labeled, func(m map[string]any) { m["repository"].(map[string]any)["full_name"] = "JavaTarz/Satat-Sandbox" })
			} else {
				p = payload(t, labeled, c.edit)
			}
			_, ok, reason, err := MapWebhook(intakeCfg, allowed, "claude", p)
			if err != nil {
				t.Fatal(err)
			}
			if c.want == "" {
				if !ok {
					t.Fatalf("want queued, got %q", reason)
				}
				return
			}
			if ok || !strings.Contains(reason, c.want) {
				t.Errorf("ok=%v reason=%q, want skip containing %q", ok, reason, c.want)
			}
		})
	}
}

type allowedEdit = func(map[string]any)

func TestMapWebhookFailsClosed(t *testing.T) {
	if _, _, _, err := MapWebhook(intakeCfg, nil, "claude", payload(t, labeled)); err == nil {
		t.Error("an empty allow list must be an error, not a pass")
	}
	if _, _, _, err := MapWebhook(intakeCfg, []string{"javatarz/satat-sandbox"}, "nope", payload(t, labeled)); err == nil {
		t.Error("an unknown lane must be an error")
	}
	if _, _, _, err := MapWebhook(intakeCfg, []string{"a/b"}, "claude", WebhookPayload{}); err == nil {
		t.Error("an empty payload must be an error")
	}
}

type fakeEvents struct {
	keys []string
	got  []Input
	err  error
}

func (f *fakeEvents) Push(_ context.Context, key string, v any) error {
	f.keys = append(f.keys, key)
	f.got = append(f.got, v.(Input))
	return f.err
}

func TestIntakeQueuesOneEventPerRequest(t *testing.T) {
	ev := &fakeEvents{}
	i := Intake{Config: intakeCfg, Allowed: []string{"javatarz/satat-sandbox"}, Events: ev}
	r, err := i.Handle(context.Background(), payload(t, labeled))
	if err != nil || !r.Queued || r.Issue != 7 {
		t.Fatalf("%+v %v", r, err)
	}
	if len(ev.keys) != 1 || ev.keys[0] != EventIssue || ev.got[0].Lane != DefaultLane {
		t.Errorf("pushed %v %+v", ev.keys, ev.got)
	}
	r, err = i.Handle(context.Background(), payload(t, labeled, func(m map[string]any) { m["action"] = "unlabeled" }))
	if err != nil || r.Queued || len(ev.keys) != 1 {
		t.Errorf("a skipped delivery pushed an event: %+v %v", r, err)
	}
}

func TestIntakeReportsAFailedPush(t *testing.T) {
	ev := &fakeEvents{err: errors.New("hatchet down")}
	i := Intake{Config: intakeCfg, Allowed: []string{"javatarz/satat-sandbox"}, Events: ev}
	if _, err := i.Handle(context.Background(), payload(t, labeled)); err == nil {
		t.Error("a failed push must fail the intake run so Hatchet retries it")
	}
}
