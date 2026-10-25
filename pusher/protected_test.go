package pusher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPolicyMatching(t *testing.T) {
	pol, err := buildPolicy(protectedSection{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want bool
	}{
		{".github/workflows/ci.yml", true},
		{".GitHub/Workflows/x.yml", true},
		{"./.github/workflows/ci.yml", true},
		{".github//workflows///ci.yml", true},
		{".github/workflows/../workflows/ci.yml", true},
		{".github/\u200bworkflows/ci.yml", true}, // zero-width space
		{".github/workflows", true},
		{".GITHUB/actions/setup/action.yml", true},
		{".gitlab-ci.yml", true},
		{".circleci/config.yml", true},
		{"sub/Jenkinsfile", true},
		{".pre-commit-config.yaml", true},
		{".editorconfig", true},
		{"pkg/.editorconfig", true},
		{".golangci.yml", true},
		{".eslintrc.json", true},
		{"web/.eslintrc.cjs", true},
		{".husky/pre-push", true},
		{"a/.githooks/commit-msg", true},
		{".gitmodules", true},
		{".satat/commands.yaml", true},
		{".satat//commands.yaml", true},
		{".SATAT/commands.yaml", true},
		{".sa\u00adtat/commands.yaml", true}, // soft hyphen
		{".git/hooks/pre-commit", true},
		{"main.go", false},
		{"README.md", false},
		{"docs/.github.md", false},
		{".github/ISSUE_TEMPLATE/bug.md", false},
		{".github", false},
		{"pkg/workflows/x.go", false},
		{".satat-not/x", false},
		{"caf\u00e9/\u00e9.go", false},
		{"\u65e5\u672c\u8a9e/.satat/x", true}, // unanchored: .satat at depth is still protected
	}
	for _, tc := range cases {
		if _, got := pol.protected(tc.name); got != tc.want {
			t.Errorf("protected(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestKelvinSignFolds(t *testing.T) {
	pol, _ := buildPolicy(protectedSection{})
	// U+212A KELVIN SIGN folds to k, so this is `.golangci.yml`-style spelling on a case-folding FS.
	if _, ok := pol.protected("Jenfins\u212A.yml"); ok {
		t.Error("unrelated name matched")
	}
	if _, ok := pol.protected(".\u212Aitlab-ci.yml"); ok {
		// .gitlab has a g, not a k: must not match.
		t.Error("unrelated name matched")
	}
	if _, ok := pol.protected(".\u212Aubernetes"); ok {
		t.Error("unrelated name matched")
	}
	if _, ok := pol.protected("\u212Aamal/Jenkinsfile"); !ok {
		t.Error("Jenkinsfile under a folded directory must match")
	}
	if _, ok := pol.protected("Jen\u212Ainsfile"); !ok {
		t.Error("Kelvin sign must fold to k")
	}
}

func TestPolicyAddAndAllow(t *testing.T) {
	pol, err := buildPolicy(protectedSection{
		Add:   []string{"justfile", "tests/golden/", "scripts/release-*.sh", ".editorconfig"},
		Allow: []string{".github/workflows/release.yml", ".pre-commit-config.yaml", ".editorconfig"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want bool
	}{
		{"justfile", true},
		{"JustFile", true},
		{"tests/golden/a.txt", true},
		{"tests/golden", true},
		{"tests/other.go", false},
		{"scripts/release-1.sh", true},
		{"scripts/other.sh", false},
		{".github/workflows/release.yml", false}, // allowed
		{".github/workflows/ci.yml", true},       // the allow is for one file
		{".pre-commit-config.yaml", false},
		{".editorconfig", true}, // add beats allow
		{".satat/commands.yaml", true},
	}
	for _, tc := range cases {
		if _, got := pol.protected(tc.name); got != tc.want {
			t.Errorf("protected(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestParseProtected(t *testing.T) {
	ok := "version: 1\ncommands:\n  setup: a\n  test: b\nfuture: [1]\nprotected:\n  add: [justfile]\n  allow: [.editorconfig]\n"
	sec, err := parseProtected([]byte(ok))
	if err != nil || len(sec.Add) != 1 || len(sec.Allow) != 1 {
		t.Fatalf("got %+v, %v", sec, err)
	}
	if sec, err := parseProtected([]byte("version: 1\ncommands: {setup: a, test: b}\n")); err != nil || len(sec.Add)+len(sec.Allow) != 0 {
		t.Errorf("no section: %+v, %v", sec, err)
	}
	for name, in := range map[string]string{
		"typo":         "protected:\n  adds: [x]\n",
		"not a map":    "protected: [x]\n",
		"not a list":   "protected:\n  add: x\n",
		"non string":   "protected:\n  add: [1]\n",
		"broken yaml":  "protected: [\n",
		"bad pattern":  "protected:\n  add: ['[']\n",
		"empty entry":  "protected:\n  add: ['']\n",
		"dot only":     "protected:\n  allow: ['./']\n",
		"bad pattern2": "protected:\n  allow: ['a[b']\n",
	} {
		sec, err := parseProtected([]byte(in))
		if err == nil {
			_, err = buildPolicy(sec)
		}
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// agent branches from the fixture's current base and lets edit change the working tree.
func (f *fixture) agent(run string, edit func(dir string)) Request {
	f.t.Helper()
	sh(f.t, f.clone, "checkout", "-q", "-B", "satat/"+run, f.baseSHA)
	edit(f.clone)
	sh(f.t, f.clone, "add", "-A")
	sh(f.t, f.clone, "commit", "-q", "-m", "Agent change")
	head := sh(f.t, f.clone, "rev-parse", "HEAD")
	bundle := filepath.Join(f.root, run+".bundle")
	sh(f.t, f.clone, "bundle", "create", "-q", bundle, f.baseSHA+"..satat/"+run)
	b, err := os.ReadFile(bundle)
	if err != nil {
		f.t.Fatal(err)
	}
	return Request{Repo: "acme/widgets", Base: "main", Run: run, BaseSHA: f.baseSHA, HeadSHA: head, Bundle: b, Issue: 7}
}

// rebase makes the repo's main carry the given files and symlinks, and points the fixture at it.
func (f *fixture) rebase(files map[string]string, links map[string]string) {
	f.t.Helper()
	sh(f.t, f.clone, "checkout", "-q", "main")
	for name, content := range files {
		p := filepath.Join(f.clone, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	for name, target := range links {
		p := filepath.Join(f.clone, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.Symlink(target, p); err != nil {
			f.t.Fatal(err)
		}
	}
	sh(f.t, f.clone, "add", "-A")
	sh(f.t, f.clone, "commit", "-q", "-m", "Base setup")
	sh(f.t, f.clone, "push", "-q", "origin", "main")
	f.baseSHA = sh(f.t, f.clone, "rev-parse", "HEAD")
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func nestedRepo(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	sh(t, dir, "init", "-q")
	write(t, dir, "f", "x\n")
	sh(t, dir, "add", "-A")
	sh(t, dir, "commit", "-q", "-m", "Nested")
}

const baseFiles = ".satat/commands.yaml"

func TestProtectedPaths(t *testing.T) {
	cmds := "version: 1\ncommands:\n  setup: s\n  test: t\n"
	withSection := cmds + "protected:\n  add: [justfile, tests/golden/]\n  allow: [.editorconfig]\n"
	type tc struct {
		base    map[string]string
		links   map[string]string
		edit    func(t *testing.T, dir string)
		refuses []string // every one must be named in the refusal; empty means the push passes
	}
	cases := map[string]tc{
		"acceptance: a CI file": {
			edit:    func(t *testing.T, d string) { write(t, d, ".gitlab-ci.yml", "x: 1\n") },
			refuses: []string{".gitlab-ci.yml"},
		},
		"acceptance: .satat/commands.yaml": {
			base:    map[string]string{baseFiles: cmds},
			edit:    func(t *testing.T, d string) { write(t, d, baseFiles, cmds+"# edited\n") },
			refuses: []string{baseFiles},
		},
		"workflow, odd case": {
			edit:    func(t *testing.T, d string) { write(t, d, ".GitHub/Workflows/x.yml", "on: push\n") },
			refuses: []string{".GitHub/Workflows/x.yml"},
		},
		"other paths are named too": {
			edit: func(t *testing.T, d string) {
				write(t, d, "ok.go", "package x\n")
				write(t, d, ".editorconfig", "root = true\n")
				write(t, d, ".husky/pre-push", "#!/bin/sh\n")
			},
			refuses: []string{".editorconfig", ".husky/pre-push"},
		},
		"unprotected change passes": {
			base: map[string]string{baseFiles: withSection},
			edit: func(t *testing.T, d string) {
				write(t, d, "pkg/a.go", "package a\n")
				write(t, d, "tests/a_test.go", "package a\n")
			},
		},
		"repo adds a path": {
			base:    map[string]string{baseFiles: withSection, "justfile": "x:\n"},
			edit:    func(t *testing.T, d string) { write(t, d, "justfile", "x:\n\techo hi\n") },
			refuses: []string{"justfile"},
		},
		"repo adds a directory": {
			base:    map[string]string{baseFiles: withSection},
			edit:    func(t *testing.T, d string) { write(t, d, "tests/golden/new.txt", "x\n") },
			refuses: []string{"tests/golden/new.txt"},
		},
		"repo allows a default path": {
			base: map[string]string{baseFiles: withSection, ".editorconfig": "root = true\n"},
			edit: func(t *testing.T, d string) { write(t, d, ".editorconfig", "root = false\n") },
		},
		"the section is read from the base, not the agent's commit": {
			base: map[string]string{baseFiles: cmds},
			edit: func(t *testing.T, d string) {
				// The agent also tries to allow itself the file. The commands.yaml edit is refused.
				write(t, d, baseFiles, cmds+"protected:\n  allow: [.editorconfig, .satat]\n")
				write(t, d, ".editorconfig", "x\n")
			},
			refuses: []string{baseFiles, ".editorconfig"},
		},
		"deletion": {
			base:    map[string]string{".circleci/config.yml": "v: 2\n"},
			edit:    func(t *testing.T, d string) { os.RemoveAll(filepath.Join(d, ".circleci")) },
			refuses: []string{".circleci/config.yml"},
		},
		"rename into a protected path": {
			base: map[string]string{"ci.yml": "x: 1\nline: two\nthree: 3\nfour: 4\n"},
			edit: func(t *testing.T, d string) {
				os.MkdirAll(filepath.Join(d, ".github/workflows"), 0o755)
				if err := os.Rename(filepath.Join(d, "ci.yml"), filepath.Join(d, ".github/workflows/ci.yml")); err != nil {
					t.Fatal(err)
				}
			},
			refuses: []string{".github/workflows/ci.yml"},
		},
		"rename out of a protected path": {
			base: map[string]string{".github/workflows/ci.yml": "x: 1\nline: two\nthree: 3\nfour: 4\n"},
			edit: func(t *testing.T, d string) {
				if err := os.Rename(filepath.Join(d, ".github/workflows/ci.yml"), filepath.Join(d, "ci.yml")); err != nil {
					t.Fatal(err)
				}
			},
			refuses: []string{".github/workflows/ci.yml"},
		},
		"mode change only": {
			base: map[string]string{".githooks/pre-push": "#!/bin/sh\n"},
			edit: func(t *testing.T, d string) {
				if err := os.Chmod(filepath.Join(d, ".githooks/pre-push"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			refuses: []string{".githooks/pre-push"},
		},
		"submodule added": {
			edit: func(t *testing.T, d string) {
				nestedRepo(t, filepath.Join(d, "vendor/lib"))
				write(t, d, ".gitmodules", "[submodule \"vendor/lib\"]\n\tpath = vendor/lib\n\turl = https://example.com/x.git\n")
			},
			refuses: []string{".gitmodules"},
		},
		"submodule at a protected path": {
			edit: func(t *testing.T, d string) {
				nestedRepo(t, filepath.Join(d, ".husky"))
			},
			refuses: []string{".husky"},
		},
		"new symlink to a protected file": {
			edit: func(t *testing.T, d string) {
				if err := os.Symlink(".github/workflows/ci.yml", filepath.Join(d, "ci-link")); err != nil {
					t.Fatal(err)
				}
			},
			refuses: []string{"ci-link"},
		},
		"new symlink, relative into .satat": {
			edit: func(t *testing.T, d string) {
				os.MkdirAll(filepath.Join(d, "a/b"), 0o755)
				if err := os.Symlink("../../.satat/commands.yaml", filepath.Join(d, "a/b/link")); err != nil {
					t.Fatal(err)
				}
			},
			refuses: []string{"a/b/link"},
		},
		"harmless symlink": {
			edit: func(t *testing.T, d string) {
				if err := os.Symlink("hello.py", filepath.Join(d, "alias.py")); err != nil {
					t.Fatal(err)
				}
			},
		},
		"edit the target of a protected symlink": {
			base:    map[string]string{"tools/lint.toml": "a = 1\n"},
			links:   map[string]string{".golangci.yml": "tools/lint.toml"},
			edit:    func(t *testing.T, d string) { write(t, d, "tools/lint.toml", "a = 2\n") },
			refuses: []string{"tools/lint.toml"},
		},
		"unicode paths": {
			edit: func(t *testing.T, d string) {
				write(t, d, "caf\u00e9/\u65e5\u672c\u8a9e.go", "package x\n")
				write(t, d, ".github/workflows/\u00fcber.yml", "on: push\n")
			},
			refuses: []string{".github/workflows/\\u00fcber.yml"}, // %q escapes nothing printable; see below
		},
		"hostile name is quoted": {
			edit: func(t *testing.T, d string) {
				write(t, d, ".github/workflows/a\nb.yml", "on: push\n")
			},
			refuses: []string{`.github/workflows/a\nb.yml`},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if c.base != nil || c.links != nil {
				f.rebase(c.base, c.links)
			}
			req := f.agent("run1", func(d string) { c.edit(t, d) })
			out, err := f.pusher().Push(context.Background(), req)
			if len(c.refuses) == 0 {
				if err != nil {
					t.Fatalf("want the push to pass: %v", err)
				}
				if out.PRNumber == 0 {
					t.Fatal("no PR opened")
				}
				return
			}
			var ref *Refusal
			if !errors.As(err, &ref) || !strings.Contains(err.Error(), "protected paths") {
				t.Fatalf("error = %v, want a protected-paths refusal", err)
			}
			for _, p := range c.refuses {
				if !strings.Contains(err.Error(), p) {
					// A printable unicode name is quoted as is by strconv.Quote.
					if alt := strings.ReplaceAll(p, `\u00fc`, "\u00fc"); strings.Contains(err.Error(), alt) {
						continue
					}
					t.Errorf("refusal does not name %q: %v", p, err)
				}
			}
			if f.remoteHas("satat/run1") != "" {
				t.Error("a refused change was pushed")
			}
			if len(f.gh.posted) != 0 {
				t.Error("a refused change opened a PR")
			}
		})
	}
}

func TestBrokenBaseConfigRefuses(t *testing.T) {
	for name, content := range map[string]string{
		"typo in key":   "version: 1\ncommands: {setup: a, test: b}\nprotected:\n  adds: [x]\n",
		"broken yaml":   "version: 1\nprotected: [\n",
		"not a mapping": "protected: nope\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.rebase(map[string]string{baseFiles: content}, nil)
			req := f.agent("run1", func(d string) { write(t, d, "a.go", "package a\n") })
			_, err := f.pusher().Push(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), "unusable protected section") {
				t.Fatalf("error = %v, want a refusal for the unusable section", err)
			}
		})
	}
}

func TestBaseConfigThatIsASymlinkFallsBackToRefusal(t *testing.T) {
	f := newFixture(t)
	f.rebase(map[string]string{"real.yaml": "version: 1\n"}, map[string]string{baseFiles: "../real.yaml"})
	req := f.agent("run1", func(d string) { write(t, d, "a.go", "package a\n") })
	_, err := f.pusher().Push(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want a refusal", err)
	}
}

// bundleOf bundles base..branch from the agent's clone.
func (f *fixture) bundleOf(base, branch string) Request {
	f.t.Helper()
	head := sh(f.t, f.clone, "rev-parse", branch)
	bundle := filepath.Join(f.root, strings.ReplaceAll(branch, "/", "-")+".bundle")
	sh(f.t, f.clone, "bundle", "create", "-q", bundle, base+".."+branch)
	b, err := os.ReadFile(bundle)
	if err != nil {
		f.t.Fatal(err)
	}
	return Request{Repo: "acme/widgets", Base: "main", Run: "run1", BaseSHA: f.baseSHA, HeadSHA: head, Bundle: b, Issue: 7}
}

func (f *fixture) commitFile(name, content string) string {
	f.t.Helper()
	write(f.t, f.clone, name, content)
	sh(f.t, f.clone, "add", "-A")
	sh(f.t, f.clone, "commit", "-q", "-m", "Agent change")
	return sh(f.t, f.clone, "rev-parse", "HEAD")
}

func wantProtectedRefusal(t *testing.T, f *fixture, req Request, named string) {
	t.Helper()
	_, err := f.pusher().Push(context.Background(), req)
	var ref *Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("error = %v, want a refusal", err)
	}
	if named != "" && !strings.Contains(err.Error(), named) {
		t.Errorf("refusal does not mention %q: %v", named, err)
	}
	if f.remoteHas("satat/run1") != "" {
		t.Error("a refused change was pushed")
	}
}

// The step reports its own base SHA. An agent that commits a workflow and reports that commit as
// the base would otherwise hide it from the diff and choose the policy.
func TestForgedBaseIsRefused(t *testing.T) {
	f := newFixture(t)
	sh(t, f.clone, "checkout", "-q", "-B", "satat/run1", f.baseSHA)
	forged := f.commitFile(".github/workflows/evil.yml", "on: push\n")
	f.commitFile("a.go", "package a\n")
	req := f.bundleOf(f.baseSHA, "satat/run1")
	req.BaseSHA = forged
	wantProtectedRefusal(t, f, req, "not on main")
}

func TestProtectedContentInHistoryIsRefused(t *testing.T) {
	f := newFixture(t)
	sh(t, f.clone, "checkout", "-q", "-B", "satat/run1", f.baseSHA)
	f.commitFile(".github/workflows/evil.yml", "on: push\n")
	sh(t, f.clone, "rm", "-q", ".github/workflows/evil.yml")
	sh(t, f.clone, "commit", "-q", "-m", "Remove it again")
	req := f.bundleOf(f.baseSHA, "satat/run1")
	wantProtectedRefusal(t, f, req, ".github/workflows/evil.yml")
}

func TestMergeCommitIsCheckedAgainstBothParents(t *testing.T) {
	f := newFixture(t)
	sh(t, f.clone, "checkout", "-q", "-B", "side", f.baseSHA)
	f.commitFile(".husky/pre-push", "#!/bin/sh\n")
	sh(t, f.clone, "checkout", "-q", "-B", "satat/run1", f.baseSHA)
	f.commitFile("a.go", "package a\n")
	sh(t, f.clone, "merge", "-q", "--no-ff", "-m", "Merge side", "side")
	sh(t, f.clone, "rm", "-q", ".husky/pre-push")
	sh(t, f.clone, "commit", "-q", "-m", "Drop it")
	req := f.bundleOf(f.baseSHA, "satat/run1")
	wantProtectedRefusal(t, f, req, ".husky/pre-push")
}

func TestMoreDefaults(t *testing.T) {
	for _, name := range []string{"CODEOWNERS", "docs/CODEOWNERS", ".github/CODEOWNERS", ".gitattributes"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			req := f.agent("run1", func(d string) { write(t, d, name, "* @me\n") })
			wantProtectedRefusal(t, f, req, name)
		})
	}
}

func TestSymlinkToAProtectedParentIsRefused(t *testing.T) {
	for name, tc := range map[string]struct{ link, target string }{
		"github dir":  {"gh", ".github"},
		"workflows":   {"wf", ".github/workflows"},
		"repo root":   {"here", "."},
		"parent hops": {"a/b/up", "../.."},
		"parent dir":  {"a/up", "../.github"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			req := f.agent("run1", func(d string) {
				os.MkdirAll(filepath.Join(d, filepath.Dir(tc.link)), 0o755)
				if err := os.Symlink(tc.target, filepath.Join(d, tc.link)); err != nil {
					t.Fatal(err)
				}
			})
			wantProtectedRefusal(t, f, req, tc.link)
		})
	}
}

func TestAllowIsAnchoredAtTheRoot(t *testing.T) {
	f := newFixture(t)
	f.rebase(map[string]string{baseFiles: "version: 1\ncommands: {setup: s, test: t}\nprotected:\n  allow: [.editorconfig]\n"}, nil)
	req := f.agent("run1", func(d string) {
		write(t, d, ".editorconfig", "root = true\n")
		write(t, d, ".satat/.editorconfig", "x\n")
	})
	_, err := f.pusher().Push(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), ".satat/.editorconfig") || strings.Contains(err.Error(), `"`+".editorconfig"+`"`) {
		t.Fatalf("error = %v, want only the nested file refused", err)
	}
}
