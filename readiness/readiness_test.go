package readiness

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const goodCommands = "version: 1\ncommands:\n  setup: just setup\n  test: go test ./...\n  lint: just lint\n"

// fake is an in-memory GitHub. A zero fake is a repo that passes every check.
type fake struct {
	viewer    Viewer
	repo      Repo
	repoErr   error
	file      string
	fileErr   error
	rules     map[string][]Rule
	rulesets  map[int]Ruleset
	labeler   string
	labelErr  error
	protected map[string]bool
	protErr   error
	reach     []string
	trunc     bool
	reachErr  error
	calls     []string
}

func good() *fake {
	return &fake{
		viewer: Viewer{Login: "satat-bot"},
		repo:   Repo{FullName: "o/r", DefaultBranch: "main", Push: true, Pull: true},
		file:   goodCommands,
		rules: map[string][]Rule{"main": {
			{Type: "deletion", RulesetID: 9},
			{Type: "pull_request", RulesetID: 7},
		}},
		rulesets: map[int]Ruleset{7: {ID: 7, Name: "main", Enforcement: "active", CurrentUserCanBypass: "never"}},
		labeler:  "karun",
		reach:    []string{"o/r"},
	}
}

func (f *fake) Viewer(context.Context) (Viewer, error) {
	f.calls = append(f.calls, "viewer")
	return f.viewer, nil
}
func (f *fake) Repo(context.Context, string) (Repo, error) {
	f.calls = append(f.calls, "repo")
	return f.repo, f.repoErr
}
func (f *fake) File(_ context.Context, _, path, ref string) ([]byte, error) {
	f.calls = append(f.calls, "file "+path+"@"+ref)
	return []byte(f.file), f.fileErr
}
func (f *fake) BranchRules(_ context.Context, _, branch string) ([]Rule, error) {
	f.calls = append(f.calls, "rules "+branch)
	return f.rules[branch], nil
}
func (f *fake) BranchProtected(_ context.Context, _, branch string) (bool, error) {
	return f.protected[branch], f.protErr
}
func (f *fake) Ruleset(_ context.Context, _ string, id int) (Ruleset, error) {
	rs, ok := f.rulesets[id]
	if !ok {
		return Ruleset{}, ErrNotFound
	}
	return rs, nil
}
func (f *fake) LabeledBy(context.Context, string, int, string) (string, bool, error) {
	return f.labeler, f.labeler != "", f.labelErr
}
func (f *fake) AccessibleRepos(context.Context) ([]string, bool, error) {
	return f.reach, f.trunc, f.reachErr
}

func run(t *testing.T, f *fake, req Request) Result {
	t.Helper()
	if req.Repo == "" {
		req.Repo = "o/r"
	}
	res, err := Checker{GH: f, Allowed: []string{"O/R"}}.Check(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func checks(r Result) string {
	var c []string
	for _, f := range r.Failures {
		c = append(c, f.Check)
	}
	return strings.Join(c, ",")
}

func TestAGoodRepoIsReady(t *testing.T) {
	res := run(t, good(), Request{Issue: 5})
	if !res.Ready() {
		t.Fatalf("want ready, got %s", res)
	}
}

func TestEachBlockingCheckFails(t *testing.T) {
	cases := map[string]struct {
		mutate func(*fake)
		want   string // check name
		text   string // in the problem or fix
	}{
		"no commands file": {func(f *fake) { f.fileErr = ErrNotFound }, "commands", "does not exist"},
		"bad version":      {func(f *fake) { f.file = "version: 2\ncommands: {setup: a, test: b}\n" }, "commands", "version is 2"},
		"no test":          {func(f *fake) { f.file = "version: 1\ncommands:\n  setup: a\n" }, "commands", "commands.test is required"},
		"blank setup":      {func(f *fake) { f.file = "version: 1\ncommands:\n  setup: ' '\n  test: a\n" }, "commands", "commands.setup is required"},
		"not yaml":         {func(f *fake) { f.file = "version: [\n" }, "commands", "yaml:"},
		"no write":         {func(f *fake) { f.repo.Push = false }, "access", "no write access"},
		"admin":            {func(f *fake) { f.repo.Admin = true }, "access", "admin role"},
		"maintain":         {func(f *fake) { f.repo.Maintain = true }, "access", "maintain role"},
		"archived":         {func(f *fake) { f.repo.Archived = true }, "access", "archived"},
		"no PR rule":       {func(f *fake) { f.rules["main"] = []Rule{{Type: "deletion", RulesetID: 9}} }, "merge protection", "is not protected"},
		"no rules at all":  {func(f *fake) { f.rules = nil }, "merge protection", "is not protected"},
		"can bypass":       {func(f *fake) { f.rulesets[7] = Ruleset{ID: 7, Name: "main", CurrentUserCanBypass: "always"} }, "merge protection", "can bypass"},
		"bypass PRs only": {func(f *fake) {
			f.rulesets[7] = Ruleset{ID: 7, Name: "main", CurrentUserCanBypass: "pull_requests_only"}
		}, "merge protection", "can bypass"},
		"exempt":                {func(f *fake) { f.rulesets[7] = Ruleset{ID: 7, Name: "main", CurrentUserCanBypass: "exempt"} }, "merge protection", "can bypass"},
		"labelled by satat":     {func(f *fake) { f.labeler = "Satat-Bot" }, "labeller", "Satat's own machine user"},
		"labeller account gone": {func(f *fake) { f.labeler = " " }, "labeller", "no record"},
		"labeller unknown":      {func(f *fake) { f.labeler = "" }, "labeller", "no record"},
		"unlisted repo":         {func(f *fake) { f.reach = []string{"o/r", "javatarz/satat-automations"} }, "token reach", "javatarz/satat-automations"},
		"reach truncated":       {func(f *fake) { f.trunc = true }, "token reach", "more repos than the check can list"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := good()
			tc.mutate(f)
			res := run(t, f, Request{Issue: 5})
			if checks(res) != tc.want || !strings.Contains(res.String(), tc.text) {
				t.Fatalf("want one %q failure mentioning %q, got:\n%s", tc.want, tc.text, res)
			}
			if res.Ready() {
				t.Error("a failure must make the repo not ready")
			}
		})
	}
}

func TestAllFailuresAreListedTogether(t *testing.T) {
	f := good()
	f.fileErr = ErrNotFound
	f.repo.Admin = true
	f.rules = nil
	f.labeler = "satat-bot"
	f.reach = []string{"o/r", "o/secret"}
	res := run(t, f, Request{Issue: 5})
	if checks(res) != "access,commands,merge protection,labeller,token reach" {
		t.Fatalf("failures: %s", checks(res))
	}
	c := res.Comment()
	for _, want := range []string{"access", "commands", "merge protection", "labeller", "token reach", "ready-for-agent", "needs-info"} {
		if !strings.Contains(c, want) {
			t.Errorf("the comment lacks %q:\n%s", want, c)
		}
	}
	if strings.Count(c, "\n- ") != 5 {
		t.Errorf("want one bullet per failure:\n%s", c)
	}
}

func TestUnreadableRepoIsOneFailureAndStillChecksReach(t *testing.T) {
	f := good()
	f.repoErr = ErrNotFound
	f.reach = []string{"o/secret"}
	res := run(t, f, Request{Issue: 5})
	if checks(res) != "access,token reach" {
		t.Fatalf("failures: %s", checks(res))
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "file") || strings.HasPrefix(c, "rules") {
			t.Errorf("made call %q after the repo could not be read", c)
		}
	}
}

func TestCheckErrorsAreNotFindings(t *testing.T) {
	boom := errors.New("502")
	for name, mutate := range map[string]func(*fake){
		"repo":  func(f *fake) { f.repoErr = boom },
		"file":  func(f *fake) { f.fileErr = boom },
		"label": func(f *fake) { f.labelErr = boom },
		"reach": func(f *fake) { f.reachErr = boom },
	} {
		t.Run(name, func(t *testing.T) {
			f := good()
			mutate(f)
			_, err := Checker{GH: f, Allowed: []string{"o/r"}}.Check(context.Background(), Request{Repo: "o/r", Issue: 5})
			if !errors.Is(err, boom) {
				t.Fatalf("err = %v; want the transport error, so nothing is relabelled", err)
			}
		})
	}
}

func TestWithoutAnIssueTheLabellerIsNotChecked(t *testing.T) {
	f := good()
	f.labeler = "satat-bot"
	res := run(t, f, Request{})
	if !res.Ready() || !strings.Contains(res.String(), "labeller: not checked") {
		t.Fatalf("got %s", res)
	}
}

func TestABaseBranchOtherThanDefaultIsProtectedToo(t *testing.T) {
	f := good()
	res := run(t, f, Request{Base: "release"})
	if checks(res) != "merge protection" || !strings.Contains(res.String(), "release") {
		t.Fatalf("got %s", res)
	}
	f = good()
	f.rules["release"] = f.rules["main"]
	if res := run(t, f, Request{Base: "release"}); !res.Ready() {
		t.Fatalf("got %s", res)
	}
	found := false
	for _, c := range f.calls {
		found = found || c == "file .satat/commands.yaml@release"
	}
	if !found {
		t.Errorf("commands.yaml must be read from the base branch: %v", f.calls)
	}
}

func TestReachNotesSayWhatWasProven(t *testing.T) {
	f := good()
	if res := run(t, f, Request{}); !strings.Contains(res.String(), "not a classic token") {
		t.Errorf("fine-grained note missing:\n%s", res)
	}
	f = good()
	f.viewer.ScopesKnown, f.viewer.Scopes = true, []string{"repo"}
	if res := run(t, f, Request{}); !strings.Contains(res.String(), "classic token (scopes: repo)") {
		t.Errorf("classic note missing:\n%s", res)
	}
}

func TestRepoControlledTextCannotInjectIntoTheComment(t *testing.T) {
	f := good()
	f.file = "version: 1\ncommands:\n  setup: a\n  test: ''\n"
	f.rulesets[7] = Ruleset{ID: 7, Name: "x`\n@everyone look", CurrentUserCanBypass: "always"}
	c := run(t, f, Request{}).Comment()
	if strings.Contains(c, "@everyone") || strings.Contains(c, "x`") {
		t.Errorf("unsanitised repo text in the comment:\n%s", c)
	}
}

func TestKeyIdentifiesTheFailureSet(t *testing.T) {
	a := Result{Failures: []Failure{{Check: "a", Problem: "p"}, {Check: "b", Problem: "q"}}}
	b := Result{Failures: []Failure{{Check: "b", Problem: "q"}, {Check: "a", Problem: "p"}}}
	c := Result{Failures: []Failure{{Check: "a", Problem: "p"}}}
	if a.Key() != b.Key() || a.Key() == c.Key() || len(a.Key()) != 12 {
		t.Errorf("keys %s %s %s", a.Key(), b.Key(), c.Key())
	}
}

func TestClassicBranchProtectionPassesWithANote(t *testing.T) {
	f := good()
	f.rules = nil
	f.protected = map[string]bool{"main": true}
	res := run(t, f, Request{})
	if !res.Ready() || !strings.Contains(res.String(), "classic branch protection") || !strings.Contains(res.String(), "not verified") {
		t.Fatalf("got %s", res)
	}
	f.protected = nil
	if res := run(t, f, Request{}); checks(res) != "merge protection" {
		t.Fatalf("no protection at all must fail: %s", res)
	}
}

func TestUnreadableProtectionIsAFinding(t *testing.T) {
	f := good()
	f.rules = nil
	f.protErr = ErrForbidden
	if res := run(t, f, Request{}); checks(res) != "merge protection" || !strings.Contains(res.String(), "cannot be read") {
		t.Fatalf("got %s", res)
	}
}
