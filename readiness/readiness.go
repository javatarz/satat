// Package readiness checks that a repo meets Satat's prerequisites before an issue on it is
// started (#175, ADR 0025).
//
// The check is pure: it reads GitHub through the GitHub interface and decides. It never writes;
// commenting and relabelling is the pusher's job (pusher.NotReady). Every blocking check runs
// and all failures are returned together, so one comment lists everything to fix.
//
// What a GET-only check cannot prove is stated per check, and in docs/design.md: it cannot show
// which permissions a token was granted, only what the machine user is allowed to do on the repo
// and what the token can see.
package readiness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/javatarz/satat/pipeline"
)

const (
	// CommandsFile is the repo's commands file, read from the base branch. The worker's clone
	// reads the same file, and pipeline.ParseCommands decides what is valid, so the check and the
	// run cannot disagree.
	CommandsFile = pipeline.CommandsFile
	// ReadyLabel and NeedsInfoLabel are the labels the check's outcome moves between.
	ReadyLabel     = "ready-for-agent"
	NeedsInfoLabel = "needs-info"
	// MaxReposShown bounds how many unlisted repos a failure names.
	MaxReposShown = 10
)

// ErrNotFound and ErrForbidden are what a GitHub implementation returns for a 404 and a 403 on
// one read. The check turns them into findings; any other error means the check could not run.
var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
)

// Viewer is the identity a token belongs to.
type Viewer struct {
	Login string
	// ScopesKnown is true for a classic token, whose responses carry X-OAuth-Scopes. A
	// fine-grained token's responses do not, so ScopesKnown false means fine-grained, and Scopes
	// is empty. An installation token is not supported: GET /user is refused for it.
	ScopesKnown bool
	Scopes      []string
}

// Repo is what GET /repos/{repo} says about the machine user's access.
type Repo struct {
	FullName      string
	DefaultBranch string
	Archived      bool
	// Role flags are the machine user's role on the repo, not the token's grants.
	Admin, Maintain, Push, Pull bool
}

// Rule is one active ruleset rule that applies to a branch.
type Rule struct {
	Type      string
	RulesetID int
}

// Ruleset is the part of a ruleset the check reads. CurrentUserCanBypass is GitHub's own answer
// for the calling user: "always", "pull_requests_only", "never" or "exempt".
type Ruleset struct {
	ID                   int
	Name                 string
	Enforcement          string
	CurrentUserCanBypass string
}

// GitHub is everything the check reads. Every method is a GET. The real one is HTTP; tests use a
// fake.
type GitHub interface {
	Viewer(ctx context.Context) (Viewer, error)
	Repo(ctx context.Context, repo string) (Repo, error)
	// File returns the file's bytes at ref, or ErrNotFound.
	File(ctx context.Context, repo, path, ref string) ([]byte, error)
	BranchRules(ctx context.Context, repo, branch string) ([]Rule, error)
	// BranchProtected reports whether the branch is protected at all, by a ruleset or by classic
	// branch protection (GET /repos/{r}/branches/{b}, readable with read access).
	BranchProtected(ctx context.Context, repo, branch string) (bool, error)
	Ruleset(ctx context.Context, repo string, id int) (Ruleset, error)
	// LabeledBy returns who most recently applied label to the issue. found is false when no
	// such event exists.
	LabeledBy(ctx context.Context, repo string, issue int, label string) (login string, found bool, err error)
	// AccessibleRepos lists the repos the token's user has explicit access to (owner,
	// collaborator or organisation member). truncated is true when the list was cut short.
	AccessibleRepos(ctx context.Context) (repos []string, truncated bool, err error)
}

// Failure is one blocking problem and what to do about it.
type Failure struct {
	Check, Problem, Fix string
}

// Result is the outcome of one check run.
type Result struct {
	Repo     string
	Failures []Failure
	// Notes are things the check could not prove or chose not to check. They never block.
	Notes []string
}

// Ready is true when nothing blocks.
func (r Result) Ready() bool { return len(r.Failures) == 0 }

// Key identifies the set of failures. The pusher uses it to avoid commenting twice on the same
// failure for the same labelling.
func (r Result) Key() string {
	var parts []string
	for _, f := range r.Failures {
		parts = append(parts, f.Check+"\x00"+f.Problem)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x01")))
	return hex.EncodeToString(sum[:6])
}

// Comment is the text the pusher posts on the issue. It lists every failure with its fix.
func (r Result) Comment() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Satat did not start this issue: `%s` is not ready for it yet.\n\n", r.Repo)
	for _, f := range r.Failures {
		fmt.Fprintf(&b, "- **%s**: %s Fix: %s\n", f.Check, f.Problem, f.Fix)
	}
	fmt.Fprintf(&b, "\nI moved the label from `%s` to `%s`. Once the repo is fixed, add `%s` again and Satat checks again and picks the issue up.\n", ReadyLabel, NeedsInfoLabel, ReadyLabel)
	return b.String()
}

// String is what `just check-repo` prints.
func (r Result) String() string {
	var b strings.Builder
	if r.Ready() {
		fmt.Fprintf(&b, "%s: ready\n", r.Repo)
	} else {
		fmt.Fprintf(&b, "%s: NOT ready, %d blocking problem(s)\n", r.Repo, len(r.Failures))
	}
	for _, f := range r.Failures {
		fmt.Fprintf(&b, "  FAIL %s: %s Fix: %s\n", f.Check, f.Problem, f.Fix)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "  note %s\n", n)
	}
	return b.String()
}

// Request says what to check. Issue 0 skips the checks that need an issue.
type Request struct {
	Repo string
	// Base is the branch runs start from and PRs target. Empty means the repo's default branch.
	Base  string
	Issue int
}

// Checker runs the blocking checks.
type Checker struct {
	GH GitHub
	// Allowed lists the repos the push token may reach: the intake and poller lists.
	Allowed []string
}

// Check runs every blocking check. An error means a check could not run (GitHub unreachable, a
// 5xx); it does not mean the repo is not ready, so the caller must not relabel on it.
func (c Checker) Check(ctx context.Context, req Request) (Result, error) {
	res := Result{Repo: req.Repo}
	fail := func(check, problem, fix string) {
		res.Failures = append(res.Failures, Failure{Check: check, Problem: problem, Fix: fix})
	}

	viewer, err := c.GH.Viewer(ctx)
	if err != nil {
		return res, fmt.Errorf("read the push token's identity: %w", err)
	}

	repo, err := c.GH.Repo(ctx, req.Repo)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrForbidden):
		fail("access", fmt.Sprintf("the machine user %s cannot read the repo.", viewer.Login),
			fmt.Sprintf("invite %s as a collaborator with write access, or grant the token the repo.", viewer.Login))
		// Every check below needs the repo, so only the ones that do not are still worth running.
		return res, c.reach(ctx, viewer, &res, fail)
	case err != nil:
		return res, fmt.Errorf("read repo %s: %w", req.Repo, err)
	}

	c.access(repo, viewer, fail)
	base := req.Base
	if base == "" {
		base = repo.DefaultBranch
	}
	if err := c.commands(ctx, req.Repo, base, fail); err != nil {
		return res, err
	}
	if err := c.mergeProtection(ctx, req.Repo, repo.DefaultBranch, base, viewer, &res, fail); err != nil {
		return res, err
	}
	if req.Issue > 0 {
		if err := c.labeller(ctx, req, viewer, fail); err != nil {
			return res, err
		}
	} else {
		res.Notes = append(res.Notes, "labeller: not checked, no issue given.")
	}
	if err := c.reach(ctx, viewer, &res, fail); err != nil {
		return res, err
	}
	res.Notes = append(res.Notes,
		"access: a GET cannot show the token's granted permissions, only the machine user's role on the repo. Branch and PR creation are inferred from write access, and are proven only by the first push.")
	return res, nil
}

func (c Checker) access(repo Repo, viewer Viewer, fail func(check, problem, fix string)) {
	if repo.Archived {
		fail("access", "the repo is archived, so nothing can be pushed.", "unarchive it.")
	}
	if !repo.Push {
		fail("access", fmt.Sprintf("the machine user %s has no write access, so it cannot create satat/* branches or pull requests.", viewer.Login),
			fmt.Sprintf("give %s the Write role on the repo.", viewer.Login))
	}
	if repo.Admin || repo.Maintain {
		role := "maintain"
		if repo.Admin {
			role = "admin"
		}
		fail("access", fmt.Sprintf("the machine user %s has the %s role; it must have Write only, because %s can change rulesets and bypass protection.", viewer.Login, role, role),
			fmt.Sprintf("lower %s to the Write role.", viewer.Login))
	}
}

// commands checks the commands file with pipeline.ParseCommands: version 1, string values, and a
// non-empty commands.setup and commands.test. Other keys are hints and are ignored.
func (c Checker) commands(ctx context.Context, repo, base string, fail func(check, problem, fix string)) error {
	raw, err := c.GH.File(ctx, repo, CommandsFile, base)
	switch {
	case errors.Is(err, ErrNotFound):
		fail("commands", fmt.Sprintf("%s does not exist on %s.", CommandsFile, base),
			fmt.Sprintf("add it on %s with `version: 1` and `commands:` holding `setup` and `test`.", base))
		return nil
	case errors.Is(err, ErrForbidden):
		fail("commands", fmt.Sprintf("the token cannot read %s on %s.", CommandsFile, base), "grant the token Contents: read on the repo.")
		return nil
	case err != nil:
		return fmt.Errorf("read %s: %w", CommandsFile, err)
	}
	if _, err := pipeline.ParseCommands(raw); err != nil {
		fail("commands", fmt.Sprintf("%s on %s is invalid: %s.", CommandsFile, base, sanitize(err.Error())),
			"set `version: 1` and give `commands.setup` and `commands.test` a non-empty command each.")
	}
	return nil
}

// mergeProtection wants an active ruleset rule "pull_request" on the default branch (and the
// base, when it differs), and that GitHub says the machine user cannot bypass the ruleset that
// holds it. With no such rule, a branch that is not protected at all is a failure. A branch with
// classic branch protection passes with a note, because its settings need admin to read.
func (c Checker) mergeProtection(ctx context.Context, repo, def, base string, viewer Viewer, res *Result, fail func(check, problem, fix string)) error {
	branches := []string{def}
	if base != def {
		branches = append(branches, base)
	}
	for _, br := range branches {
		rules, err := c.GH.BranchRules(ctx, repo, br)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
			fail("merge protection", fmt.Sprintf("the rules on %s cannot be read with the push token.", br), "grant the token Metadata: read; rulesets are readable with it.")
			continue
		} else if err != nil {
			return fmt.Errorf("read rules for %s: %w", br, err)
		}
		var ids []int
		for _, r := range rules {
			if r.Type == "pull_request" && !slices.Contains(ids, r.RulesetID) {
				ids = append(ids, r.RulesetID)
			}
		}
		if len(ids) == 0 {
			prot, err := c.GH.BranchProtected(ctx, repo, br)
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
				fail("merge protection", fmt.Sprintf("whether %s is protected cannot be read with the push token.", br), "grant the token Metadata: read.")
				continue
			} else if err != nil {
				return fmt.Errorf("read branch %s: %w", br, err)
			}
			if prot {
				// Classic branch protection: its settings (a PR required, who may bypass) are
				// readable only with admin, which the machine user must not have, so they cannot be
				// proven. Conservative but not blocking: protection that exists passes with a note,
				// because blocking would fail every repo that has not moved to rulesets.
				res.Notes = append(res.Notes, fmt.Sprintf("merge protection: %s has classic branch protection, not a ruleset. Whether it requires a pull request and whether %s can bypass it cannot be read without admin, so it is not verified. Rulesets are preferred (ADR 0025).", br, viewer.Login))
				continue
			}
			fail("merge protection", fmt.Sprintf("%s is not protected: no ruleset requires a pull request and there is no branch protection.", br),
				fmt.Sprintf("add a ruleset on %s with \"Require a pull request before merging\" and no bypass entry for %s.", br, viewer.Login))
			continue
		}
		for _, id := range ids {
			rs, err := c.GH.Ruleset(ctx, repo, id)
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
				fail("merge protection", fmt.Sprintf("ruleset %d on %s cannot be read, so bypass rights cannot be checked.", id, br), "grant the token Metadata: read.")
				continue
			} else if err != nil {
				return fmt.Errorf("read ruleset %d: %w", id, err)
			}
			if rs.CurrentUserCanBypass != "never" {
				fail("merge protection", fmt.Sprintf("%s can bypass ruleset %q on %s (GitHub says: %s).", viewer.Login, sanitize(rs.Name), br, sanitize(rs.CurrentUserCanBypass)),
					fmt.Sprintf("remove %s, and any team or role it holds, from the ruleset's bypass list.", viewer.Login))
			}
		}
	}
	return nil
}

// labeller refuses an issue Satat's own machine user labelled ready: that identity has write
// access, so it is the one labeller GitHub's triage-access rule does not exclude (#175).
func (c Checker) labeller(ctx context.Context, req Request, viewer Viewer, fail func(check, problem, fix string)) error {
	who, found, err := c.GH.LabeledBy(ctx, req.Repo, req.Issue, ReadyLabel)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrForbidden):
		fail("labeller", "who labelled the issue cannot be read.", "grant the token Issues: read.")
	case err != nil:
		return fmt.Errorf("read who labelled #%d: %w", req.Issue, err)
	case !found || strings.TrimSpace(who) == "":
		// An unknown labeller (no event, or an event whose actor GitHub no longer shows, such as
		// a deleted account) is not trusted.
		fail("labeller", fmt.Sprintf("no record of who added %s (not labelled, the event history is too long, or the account is gone).", ReadyLabel),
			fmt.Sprintf("remove %s and add it again by hand.", ReadyLabel))
	case strings.EqualFold(who, viewer.Login):
		fail("labeller", fmt.Sprintf("%s was added by Satat's own machine user (%s), not by a person.", ReadyLabel, who),
			fmt.Sprintf("a person with triage access removes %s and adds it again.", ReadyLabel))
	}
	return nil
}

// reach fails when the push token's user can reach a repo that is not listed: the owner's other
// repos, such as the private javatarz/satat-automations, must stay out of its reach.
//
// What this proves. A classic token (ScopesKnown) can do anything its user can on every repo the
// user can access, so the user's repo list is exactly its reach and a pass is a real guarantee,
// but only while the machine user's memberships stay as they are. A fine-grained token is limited
// to the repos chosen when it was made, and GitHub limits this listing to them, so a pass shows
// the token reaches only the listed repos. It does not show the token's permission level on each
// one. Neither case covers public repos, which anyone can read, nor access that does not appear
// in the list (such as a stale classic grant to an organisation that requires SSO).
func (c Checker) reach(ctx context.Context, viewer Viewer, res *Result, fail func(check, problem, fix string)) error {
	repos, truncated, err := c.GH.AccessibleRepos(ctx)
	if err != nil {
		return fmt.Errorf("list the repos the push token reaches: %w", err)
	}
	var extra []string
	for _, r := range repos {
		if !slices.ContainsFunc(c.Allowed, func(a string) bool { return strings.EqualFold(a, r) }) {
			extra = append(extra, r)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		shown := extra
		more := ""
		if len(shown) > MaxReposShown {
			shown, more = shown[:MaxReposShown], fmt.Sprintf(" and %d more", len(extra)-MaxReposShown)
		}
		fail("token reach", fmt.Sprintf("the push token (machine user %s) can reach repos that are not listed in the deploy config: %s%s.", viewer.Login, strings.Join(shown, ", "), more),
			"remove the machine user from those repos, or recreate the token as a fine-grained token limited to the listed repos.")
	} else if truncated {
		fail("token reach", "the push token reaches more repos than the check can list, so it cannot show the unlisted ones are out of reach.",
			"use a fine-grained token limited to the listed repos.")
	}
	if viewer.ScopesKnown {
		res.Notes = append(res.Notes, fmt.Sprintf("token reach: a classic token (scopes: %s). Its reach is everything the machine user can access, which is what was listed. A fine-grained token limited to the listed repos is stronger, because it does not depend on the machine user's memberships.", scopeList(viewer.Scopes)))
	} else {
		res.Notes = append(res.Notes, "token reach: not a classic token (taken to be fine-grained). The listing is limited to the repos the token was granted. It cannot show the token's permission level on each, and public repos are not counted.")
	}
	return nil
}

func scopeList(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// sanitize makes repo-controlled text safe to put in a comment: one short line, with no code
// fences or @-mentions.
func sanitize(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.NewReplacer("`", "'", "@", "(at)").Replace(s)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
