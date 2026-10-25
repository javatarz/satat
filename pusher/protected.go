package pusher

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// The files that judge the agent are off limits to it: CI, lint and format config, hooks and
// Satat's own `.satat/`. A prompt asking the agent to leave them alone is not enough, so the
// pusher refuses a bundle that changes one (#17). The check is on the diff between the base and
// head commits, runs no repo code, and reads the repo's own additions from the BASE commit, which
// the agent cannot change.

// defaultProtected is the safe default. A pattern without a slash matches any path segment at any
// depth (`.husky` is protected in a subfolder too). A pattern with a slash is anchored at the repo
// root and matches that path and everything under it. `*`, `?` and `[...]` work within a segment.
// Matching ignores case.
var defaultProtected = []string{
	// CI
	".github/workflows", ".github/actions", "codeowners",
	".gitlab-ci.yml", ".gitlab-ci.yaml", ".gitlab", ".circleci", ".travis.yml", ".drone.yml",
	".woodpecker", ".woodpecker.yml", ".buildkite", "azure-pipelines.yml", ".azure-pipelines",
	"bitbucket-pipelines.yml", "appveyor.yml", ".appveyor.yml", "jenkinsfile", "cloudbuild.yaml",
	// Lint, format and secret-scan config
	".pre-commit-config.yaml", ".editorconfig", ".golangci.*", ".eslintrc*", "eslint.config.*",
	".prettierrc*", "prettier.config.*", ".stylelintrc*", "ruff.toml", ".ruff.toml", ".flake8",
	".pylintrc", "mypy.ini", ".rubocop.yml", ".markdownlint*", ".yamllint*", "rustfmt.toml",
	".rustfmt.toml", "clippy.toml", ".shellcheckrc", ".gitleaks.toml", ".gitleaksignore",
	// Hooks, and the files that make git run or fetch things
	".githooks", ".husky", ".git-hooks", ".lefthook", "lefthook.yml", "lefthook.yaml",
	"lefthook-local.yml", ".gitmodules", ".gitattributes", ".git",
	// Satat's own config
	".satat",
}

// protectedSection is the optional `protected:` key of `.satat/commands.yaml`. The file stays
// `version: 1`: pipeline.ParseCommands reads only `version` and `commands` and ignores other
// top-level keys, so a repo adds this section without breaking any reader.
//
//	protected:
//	  add: [justfile, tests/golden]   # more protected paths, on top of the default
//	  allow: [.editorconfig]          # default paths the agent may change in this repo
//
// `add` always wins over `allow`: a path matching both is protected.
type protectedSection struct{ Add, Allow []string }

const (
	maxProtectedConfig = 64 << 10
	maxProtectedRules  = 200
	maxNamed           = 10
)

type protectedPolicy struct{ add, def, allow []rule }

type rule struct {
	text     string // as written, for messages
	segs     []string
	anchored bool
}

func newRule(s string, forceAnchored bool) (rule, error) {
	text := strings.TrimSpace(s)
	anchored := forceAnchored || strings.Contains(strings.Trim(text, "/"), "/") || strings.HasPrefix(text, "/")
	segs := foldSegments(text)
	if len(segs) == 0 {
		return rule{}, fmt.Errorf("empty path %q", s)
	}
	for _, g := range segs {
		if _, err := path.Match(g, ""); err != nil {
			return rule{}, fmt.Errorf("pattern %q: %v", s, err)
		}
	}
	return rule{text: text, segs: segs, anchored: anchored}, nil
}

func (r rule) matches(segs []string) bool {
	if !r.anchored {
		for _, s := range segs {
			if ok, _ := path.Match(r.segs[0], s); ok {
				return true
			}
		}
		return false
	}
	if len(segs) < len(r.segs) {
		return false
	}
	for i, g := range r.segs {
		if ok, _ := path.Match(g, segs[i]); !ok {
			return false
		}
	}
	return true
}

// foldSegments normalises a path the way a case-insensitive filesystem would see it: empty and `.`
// segments dropped (`.satat//commands.yaml`, `./x`), `..` resolved, invisible format characters
// removed and every rune folded to one case.
func foldSegments(name string) []string {
	var out []string
	for _, s := range strings.Split(strings.ReplaceAll(name, "\\", "/"), "/") {
		s = strings.Map(func(r rune) rune {
			if unicode.Is(unicode.Cf, r) {
				return -1
			}
			return foldRune(r)
		}, s)
		switch s {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, s)
		}
	}
	return out
}

// foldRune maps a rune to the smallest member of its case-folding orbit, so `K` (Kelvin sign) and
// `ſ` land on the same rune as `k` and `s`.
func foldRune(r rune) rune {
	m := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < m {
			m = f
		}
	}
	return m
}

// protected reports the rule that covers name. A path is protected when it matches an `add` rule,
// or matches a default rule and no `allow` rule: allowing `.github/workflows/release.yml` frees
// that file only, and allowing `.github/workflows` frees the directory. Allow rules are always
// anchored at the repo root, so `.editorconfig` frees the root file and not `pkg/.editorconfig`.
func (p *protectedPolicy) protected(name string) (string, bool) {
	segs := foldSegments(name)
	if len(segs) == 0 {
		return "", false
	}
	for _, r := range p.add {
		if r.matches(segs) {
			return r.text, true
		}
	}
	for _, r := range p.def {
		if r.matches(segs) {
			for _, a := range p.allow {
				if a.matches(segs) {
					return "", false
				}
			}
			return r.text, true
		}
	}
	return "", false
}

// covers reports whether dir is the repo root or a parent directory of something protected, such
// as `.github` for `.github/workflows`. A symlink to it reaches protected paths.
func (p *protectedPolicy) covers(dir []string) bool {
	if len(dir) == 0 {
		return true
	}
	for _, rs := range [][]rule{p.add, p.def} {
		for _, r := range rs {
			if !r.anchored || len(r.segs) <= len(dir) {
				continue
			}
			ok := true
			for i, d := range dir {
				if m, _ := path.Match(r.segs[i], d); !m {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}

// buildPolicy applies the repo's section to the default.
func buildPolicy(sec protectedSection) (*protectedPolicy, error) {
	if len(sec.Add)+len(sec.Allow) > maxProtectedRules {
		return nil, fmt.Errorf("protected has more than %d entries", maxProtectedRules)
	}
	p := &protectedPolicy{}
	for _, c := range []struct {
		from []string
		to   *[]rule
		key  string
		anch bool
	}{{defaultProtected, &p.def, "default", false}, {sec.Add, &p.add, "protected.add", false}, {sec.Allow, &p.allow, "protected.allow", true}} {
		for _, s := range c.from {
			r, err := newRule(s, c.anch)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", c.key, err)
			}
			*c.to = append(*c.to, r)
		}
	}
	return p, nil
}

// parseProtected reads the `protected:` section from the raw file. Unknown keys inside it are an
// error, so a typo such as `adds:` cannot silently drop protection. Other top-level keys are left
// to their own readers.
func parseProtected(b []byte) (protectedSection, error) {
	var sec protectedSection
	var top struct {
		Protected yaml.Node `yaml:"protected"`
	}
	if err := yaml.Unmarshal(b, &top); err != nil {
		return sec, err
	}
	if top.Protected.Kind == 0 {
		return sec, nil
	}
	n := &top.Protected
	if n.Kind != yaml.MappingNode {
		return sec, fmt.Errorf("protected must be a mapping with add and allow lists")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, n.Content[i+1]
		var dst *[]string
		switch key {
		case "add":
			dst = &sec.Add
		case "allow":
			dst = &sec.Allow
		default:
			return sec, fmt.Errorf("protected.%s is not a known key (add, allow)", key)
		}
		if val.Kind != yaml.SequenceNode {
			return sec, fmt.Errorf("protected.%s must be a list of paths", key)
		}
		for _, item := range val.Content {
			if item.Kind != yaml.ScalarNode || item.ShortTag() != "!!str" {
				return sec, fmt.Errorf("protected.%s entries must be strings", key)
			}
			*dst = append(*dst, item.Value)
		}
	}
	return sec, nil
}

// loadPolicy reads the policy from `.satat/commands.yaml` at the base commit. A missing file means
// the default; a file the pusher cannot read as a policy is a refusal, never a silent fallback.
func (p *Pusher) loadPolicy(ctx context.Context, repoDir, baseSHA string) (*protectedPolicy, error) {
	const file = ".satat/commands.yaml"
	entry, err := p.gitOut(ctx, repoDir, "ls-tree", baseSHA, "--", file)
	if err != nil {
		return nil, err
	}
	if entry == "" {
		return buildPolicy(protectedSection{})
	}
	meta, _, _ := strings.Cut(entry, "\t")
	f := strings.Fields(meta)
	if len(f) != 3 || f[1] != "blob" || (f[0] != "100644" && f[0] != "100755") {
		return nil, refuse("%s on the base commit is not a regular file; fix it before Satat publishes", file)
	}
	size, err := p.gitOut(ctx, repoDir, "cat-file", "-s", f[2])
	if err != nil {
		return nil, err
	}
	if n, _ := strconv.Atoi(size); n > maxProtectedConfig {
		return nil, refuse("%s on the base commit is over %d bytes", file, maxProtectedConfig)
	}
	raw, err := p.gitOut(ctx, repoDir, "cat-file", "blob", f[2])
	if err != nil {
		return nil, err
	}
	sec, err := parseProtected([]byte(raw))
	if err != nil {
		return nil, refuse("%s on the base commit has an unusable protected section: %v", file, err)
	}
	pol, err := buildPolicy(sec)
	if err != nil {
		return nil, refuse("%s on the base commit has an unusable protected section: %v", file, err)
	}
	return pol, nil
}

// checkProtected refuses a bundle that touches a protected path, naming the paths. It looks at
// every path in the diff with renames off, so a rename shows as a delete of the old path and an
// add of the new one, and deletions, mode changes and submodule (gitlink) changes show too.
// Symlinks are followed one hop: a symlink added at a path pointing into a protected path is
// refused, and so is a change to the target of a protected path that the base makes a symlink.
func (p *Pusher) checkProtected(ctx context.Context, repoDir string, req Request) error {
	pol, err := p.loadPolicy(ctx, repoDir, req.BaseSHA)
	if err != nil {
		return err
	}
	type change struct{ newMode, newSHA, name string }
	var changes []change
	diff := func(from, to string) error {
		raw, err := p.gitOut(ctx, repoDir, "diff", "--raw", "--no-abbrev", "--no-renames", "-z", from, to)
		if err != nil {
			return err
		}
		toks := strings.Split(raw, "\x00")
		for i := 0; i+1 < len(toks); i += 2 {
			f := strings.Fields(strings.TrimPrefix(toks[i], ":"))
			if len(f) < 5 {
				return fmt.Errorf("unexpected git diff output %q", toks[i])
			}
			changes = append(changes, change{newMode: f[1], newSHA: f[3], name: toks[i+1]})
		}
		return nil
	}
	// The net diff, and each commit against each of its parents: a commit that adds a protected
	// file and a later one that removes it still ships the content in history.
	if err := diff(req.BaseSHA, req.HeadSHA); err != nil {
		return err
	}
	revs, err := p.gitOut(ctx, repoDir, "rev-list", "--parents", req.BaseSHA+".."+req.HeadSHA)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(revs, "\n") {
		f := strings.Fields(line)
		for _, parent := range f[min(1, len(f)):] {
			if err := diff(parent, f[0]); err != nil {
				return err
			}
		}
	}

	// Paths the base already has as symlinks at a protected location: their targets are protected
	// too, because editing the target edits the protected file.
	linked, err := p.protectedSymlinkTargets(ctx, repoDir, req.BaseSHA, pol)
	if err != nil {
		return err
	}

	var hits []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			hits = append(hits, strconv.Quote(s))
		}
	}
	for _, c := range changes {
		if _, ok := pol.protected(c.name); ok {
			add(c.name)
			continue
		}
		if underAny(linked, foldSegments(c.name)) {
			add(c.name + " (the target of a protected symlink)")
			continue
		}
		if c.newMode == "120000" {
			target, err := p.gitOut(ctx, repoDir, "cat-file", "blob", c.newSHA)
			if err != nil {
				return err
			}
			if resolved, ok := resolveLink(c.name, target); ok {
				if _, hit := pol.protected(resolved); hit || pol.covers(foldSegments(resolved)) {
					add(c.name + " (a symlink to " + resolved + ")")
				}
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	more := ""
	if len(hits) > maxNamed {
		more = fmt.Sprintf(" and %d more", len(hits)-maxNamed)
		hits = hits[:maxNamed]
	}
	return refuse("the change touches protected paths: %s%s. Satat does not edit CI, lint, hook or .satat/ files; a repo can allow a default path or protect more under `protected:` in .satat/commands.yaml on its base branch", strings.Join(hits, ", "), more)
}

// resolveLink returns the repo-relative path a symlink at name points to, or false when the target
// is absolute or leaves the repo.
func resolveLink(name, target string) (string, bool) {
	if strings.HasPrefix(target, "/") || target == "" {
		return "", false
	}
	joined := path.Join(path.Dir(strings.ReplaceAll(name, "\\", "/")), target)
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return "", false
	}
	return joined, true
}

// protectedSymlinkTargets lists, folded and slash-joined, the targets of base symlinks that sit at
// protected paths.
func (p *Pusher) protectedSymlinkTargets(ctx context.Context, repoDir, baseSHA string, pol *protectedPolicy) (map[string]bool, error) {
	out, err := p.gitOut(ctx, repoDir, "ls-tree", "-r", "-z", baseSHA)
	if err != nil {
		return nil, err
	}
	targets := map[string]bool{}
	for _, e := range strings.Split(out, "\x00") {
		meta, name, ok := strings.Cut(e, "\t")
		if !ok || !strings.HasPrefix(meta, "120000 ") {
			continue
		}
		if _, hit := pol.protected(name); !hit {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			continue
		}
		target, err := p.gitOut(ctx, repoDir, "cat-file", "blob", f[2])
		if err != nil {
			return nil, err
		}
		if resolved, ok := resolveLink(name, target); ok {
			targets[strings.Join(foldSegments(resolved), "/")] = true
		}
	}
	return targets, nil
}

// underAny reports whether segs is, or is inside, one of the folded, slash-joined paths in set.
func underAny(set map[string]bool, segs []string) bool {
	for i := 1; i <= len(segs); i++ {
		if set[strings.Join(segs[:i], "/")] {
			return true
		}
	}
	return false
}
