package workflows

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher"
)

// noAgent marks the test lanes that run no agent, as `agent: false` does in satat.yaml.
var noAgent = map[string]bool{"sleep": true, "smoke": true}

func agentInput() Input {
	return Input{Kind: KindAgent, Repo: AdhocRepo, Lane: "sleep", StepInput: []byte(`{"x":1}`)}
}

func TestDefineListensToBothEvents(t *testing.T) {
	s := Define(limits, time.Hour)
	if len(s.Events) != 2 || s.Events[0] != EventIssue || s.Events[1] != EventAgent {
		t.Errorf("events: %v", s.Events)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

// laneCfg has an agent lane, claude, and a lane that runs no agent, smoke.
func laneCfg() config.Config {
	c := cfg()
	c.Accounts["smoke"], c.LaneMaxRuns["smoke"] = "none", 2
	c.NoAgent = map[string]bool{"smoke": true}
	return c
}

func TestMapAgentUsesAdhocRepoAndLaneCaps(t *testing.T) {
	in, err := MapAgent(laneCfg(), "smoke", []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != KindAgent || in.Repo != AdhocRepo || in.Account != "none" || in.LaneCap != 2 || in.Lane != "smoke" {
		t.Errorf("input: %+v", in)
	}
	if string(in.StepInput) != `{"a":1}` {
		t.Errorf("step input: %s", in.StepInput)
	}
	if _, err := MapAgent(cfg(), "nope", nil); err == nil || !strings.Contains(err.Error(), "unknown lane") {
		t.Errorf("unknown lane: %v", err)
	}
}

func TestAgentRunNeverPushes(t *testing.T) {
	l, p := &fakeLauncher{res: result("changes")}, &fakePub{}
	out, err := Handler{Launcher: l, Pusher: p, NoAgent: noAgent}.Run(context.Background(), "run1", agentInput())
	if err != nil {
		t.Fatal(err)
	}
	if p.n != 0 {
		t.Errorf("pusher called %d times", p.n)
	}
	want := launcher.Step{Run: "run1", Name: "agent", Lane: "sleep", Input: []byte(`{"x":1}`)}
	if l.got.Run != want.Run || l.got.Name != want.Name || l.got.Lane != want.Lane || string(l.got.Input) != string(want.Input) {
		t.Errorf("step: %+v", l.got)
	}
	if out.Kind != KindAgent || out.Run != "run1" || out.Status != "ok" {
		t.Errorf("output: %+v", out)
	}
}

func TestAgentRunReportsTailAndFiles(t *testing.T) {
	stdout := bytes.Repeat([]byte("a"), 10000-3)
	stdout = append(stdout, "xyz"...)
	l := &fakeLauncher{res: launcher.Result{Stdout: stdout, Stderr: []byte("err"), Files: map[string][]byte{"a.txt": []byte("abc")}}}
	out, err := Handler{Launcher: l, NoAgent: noAgent}.Run(context.Background(), "r", agentInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stdout) != TailBytes || !strings.HasSuffix(out.Stdout, "xyz") || out.Stderr != "err" {
		t.Errorf("tails: %d %q", len(out.Stdout), out.Stderr)
	}
	if len(out.Files) != 1 || out.Files["a.txt"] != 3 {
		t.Errorf("files: %v", out.Files)
	}
}

func TestScrubBeforeTail(t *testing.T) {
	secret := "SECRETSECRETSECRET12" // 20 bytes
	buf := bytes.Repeat([]byte("."), 9000)
	at := len(buf) - TailBytes - 4 // the secret straddles the cut
	copy(buf[at:], secret)
	got := scrubTail(buf, TailBytes, []string{secret, ""})
	if strings.Contains(got, secret) {
		t.Fatal("secret survived")
	}
	for i := 0; i+8 <= len(secret); i++ {
		if strings.Contains(got, secret[i:i+8]) {
			t.Fatalf("fragment %q survived", secret[i:i+8])
		}
	}
	if len(got) > TailBytes {
		t.Errorf("len %d", len(got))
	}
}

func TestAgentRunScrubsHandlerSecrets(t *testing.T) {
	l := &fakeLauncher{res: launcher.Result{Stdout: []byte("token=hunter2hunter2 end"), Stderr: []byte("hunter2hunter2")}}
	out, err := Handler{Launcher: l, Secrets: []string{"hunter2hunter2"}, NoAgent: noAgent}.Run(context.Background(), "r", agentInput())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Stdout, "hunter2") || strings.Contains(out.Stderr, "hunter2") {
		t.Errorf("leaked: %q %q", out.Stdout, out.Stderr)
	}
}

func TestAgentNonZeroExitIsAResult(t *testing.T) {
	l := &fakeLauncher{res: launcher.Result{ExitCode: 2}}
	out, err := Handler{Launcher: l, NoAgent: noAgent}.Run(context.Background(), "r", agentInput())
	if err != nil || out.Status != "failed" || out.ExitCode != 2 {
		t.Errorf("out %+v err %v", out, err)
	}
}

func TestAgentStatusFlagsOutrankExitCode(t *testing.T) {
	cases := map[string]launcher.Result{
		"cancelled": {ExitCode: 137, Cancelled: true, TimedOut: true, OOMKilled: true},
		"timed_out": {ExitCode: 137, TimedOut: true, OOMKilled: true},
		"oom":       {ExitCode: 137, OOMKilled: true},
		"ok":        {},
	}
	for want, res := range cases {
		out, err := Handler{Launcher: &fakeLauncher{res: res}, NoAgent: noAgent}.Run(context.Background(), "r", agentInput())
		if err != nil || out.Status != want {
			t.Errorf("%s: out %+v err %v", want, out, err)
		}
		if out.TimedOut != res.TimedOut || out.OOMKilled != res.OOMKilled || out.Cancelled != res.Cancelled {
			t.Errorf("%s: flags %+v", want, out)
		}
	}
}

func TestAgentLaunchErrorIsAnError(t *testing.T) {
	l := &fakeLauncher{err: errors.New("docker down")}
	out, err := Handler{Launcher: l, NoAgent: noAgent}.Run(context.Background(), "r", agentInput())
	if err == nil || !strings.Contains(err.Error(), "docker down") {
		t.Fatalf("err %v", err)
	}
	if out.Run != "r" || out.Kind != KindAgent {
		t.Errorf("output: %+v", out)
	}
}

func TestEmptyKindIsIssue(t *testing.T) {
	l, p := &fakeLauncher{res: result("changes")}, &fakePub{}
	in := input // Kind unset
	out, err := Handler{Cloner: fakeCloner{}, Launcher: l, Pusher: p, Issues: ready(), Allowed: listed}.Run(context.Background(), "r", in)
	if err != nil {
		t.Fatal(err)
	}
	if p.n != 1 || l.got.Name == "agent" || out.Kind != "" {
		t.Errorf("not the issue path: pushes %d step %+v out %+v", p.n, l.got, out)
	}
}

func TestMapAgentRefusesAnAgentLane(t *testing.T) {
	_, err := MapAgent(laneCfg(), "claude", nil)
	if err == nil {
		t.Fatal("MapAgent accepted the claude lane")
	}
	for _, want := range []string{`"claude"`, ReadyLabel, "agent: false"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

// The worker checks again, whoever pushed the event: an agent lane launches nothing.
func TestAgentLaneIsRefusedAndLaunchesNothing(t *testing.T) {
	for name, h := range map[string]Handler{
		"claude not in NoAgent": {NoAgent: noAgent},
		"NoAgent not set":       {},
	} {
		t.Run(name, func(t *testing.T) {
			l, p := &fakeLauncher{res: result("changes")}, &fakePub{}
			h.Launcher, h.Pusher = l, p
			in := agentInput()
			in.Lane = "claude"
			if name == "NoAgent not set" {
				in.Lane = "sleep"
			}
			out, err := h.Run(context.Background(), "r", in)
			if err != nil {
				t.Fatal(err)
			}
			if l.got.Run != "" || p.n != 0 {
				t.Errorf("launched %+v, pushed %d", l.got, p.n)
			}
			if out.Status != StatusRefused || out.Kind != KindAgent || !strings.Contains(out.Reason, in.Lane) || !strings.Contains(out.Reason, ReadyLabel) {
				t.Errorf("output: %+v", out)
			}
		})
	}
}

func TestLaneStartedRunsOnSmokeAndSleep(t *testing.T) {
	for _, lane := range []string{"smoke", "sleep"} {
		l := &fakeLauncher{res: result("changes")}
		in := agentInput()
		in.Lane = lane
		out, err := Handler{Launcher: l, NoAgent: noAgent}.Run(context.Background(), "r", in)
		if err != nil {
			t.Fatal(err)
		}
		if l.got.Lane != lane || out.Status != "ok" {
			t.Errorf("%s: step %+v, output %+v", lane, l.got, out)
		}
	}
}
