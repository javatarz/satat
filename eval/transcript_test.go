package eval

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

var testPatterns = []*regexp.Regexp{
	regexp.MustCompile(`python3? -m unittest`),
	regexp.MustCompile(`\bpytest\b`),
}

func use(id, cmd string) string {
	return `{"type":"assistant","message":{"content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"` + cmd + `"}}]}}`
}

func result(id string, isErr bool) string {
	e := "false"
	if isErr {
		e = "true"
	}
	return `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","is_error":` + e + `,"content":"out"}]}}`
}

func TestRanTests(t *testing.T) {
	const init = `{"type":"system","subtype":"init"}`
	cases := []struct {
		name  string
		lines []string
		want  bool
		sub   string
	}{
		{"ran and succeeded", []string{init, use("t1", "python -m unittest -v"), result("t1", false)}, true, "successfully"},
		{"python3 and pytest match", []string{use("t1", "cd x && pytest -q"), result("t1", false)}, true, "pytest"},
		{"ran and failed", []string{use("t1", "python -m unittest"), result("t1", true)}, false, "failed"},
		{"failed then passed", []string{use("t1", "python -m unittest"), result("t1", true), use("t2", "python3 -m unittest"), result("t2", false)}, true, "successfully"},
		{"never ran", []string{init, use("t1", "ls -la"), result("t1", false)}, false, "never ran"},
		{"empty transcript", nil, false, "never ran"},
		{"no result recorded", []string{use("t1", "pytest")}, false, "no result"},
		{"result pairs by id, not order", []string{use("t1", "pytest"), use("t2", "ls"), result("t2", false), result("t1", true)}, false, "failed"},
		{"other tool's success is not the tests'", []string{use("t1", "pytest"), result("other", false)}, false, "no result"},
		{"non-Bash tool is ignored", []string{`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Write","input":{"command":"pytest"}}]}}`, result("t1", false)}, false, "never ran"},
		{"malformed and odd lines ignored", []string{
			"not json", "{", `{"type":"assistant"}`, `{"type":"assistant","message":{"content":"plain text"}}`,
			`{"type":"user","message":{"content":[42,"x"]}}`, `[]`, "",
			use("t1", "python -m unittest"), "garbage", result("t1", false),
		}, true, "successfully"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, detail := ranTests([]byte(strings.Join(c.lines, "\n")), testPatterns)
			if got != c.want || !strings.Contains(detail, c.sub) {
				t.Errorf("got %v %q, want %v containing %q", got, detail, c.want, c.sub)
			}
		})
	}
}

// A change that passes the held-out tests still fails when the agent never ran tests itself.
func TestGradeFlagsARunThatNeverRanTests(t *testing.T) {
	f := newFixture(t)
	for name, tc := range map[string]struct {
		transcript []string
		pass, skip bool
	}{
		"ran and passed": {[]string{use("t1", "python -m unittest"), result("t1", false)}, true, false},
		"ran and failed": {[]string{use("t1", "python -m unittest"), result("t1", true)}, false, false},
		"never ran":      {[]string{use("t1", "ls"), result("t1", false)}, false, false},
		"no transcript":  {nil, false, true},
	} {
		s := f.subject("r1", map[string]string{"hello.py": goodHello})
		s.Transcript = []byte(strings.Join(tc.transcript, "\n"))
		res, err := f.grader().Grade(context.Background(), theCase(t), s)
		if err != nil {
			t.Fatal(err)
		}
		c := byName(res)["ran-tests"]
		if c.Pass != tc.pass || c.Skip != tc.skip {
			t.Errorf("%s: ran-tests = %+v", name, c)
		}
		if got := res.Passed(); got != (tc.pass || tc.skip) {
			t.Errorf("%s: Passed() = %v, checks %+v", name, got, res.Checks)
		}
	}
}

func TestRanTestsCaseValidation(t *testing.T) {
	dir := t.TempDir()
	write := func(extra string) {
		y := "name: n\nrepo: o/r\nbase: main\nissue: 1\nchecks:\n  ran_tests:\n" + extra
		if err := os.WriteFile(dir+"/case.yaml", []byte(y), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("    commands: ['(']\n")
	if _, err := LoadCase(dir); err == nil {
		t.Error("an invalid regex should be rejected")
	}
	write("    commands: []\n")
	if _, err := LoadCase(dir); err == nil {
		t.Error("an empty command list should be rejected")
	}
	write("    commands: ['pytest']\n")
	if _, err := LoadCase(dir); err != nil {
		t.Errorf("valid case rejected: %v", err)
	}
}
