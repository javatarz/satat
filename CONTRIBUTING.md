# Contributing to Satat

Start with the [README](README.md), the glossary in [CONTEXT.md](CONTEXT.md) and the
[design roadmap](docs/roadmap/). Conventions for agents and contributors are in
[AGENTS.md](AGENTS.md).

## How we work

The team is spread across time zones and everyone has a day job, so we work async first.

- **Decisions go on an issue.** Write the options, their pros and cons, and your
  recommendation. Tag everyone on the team and post the issue in the team channel. Every
  contributor has 24 hours to reply with one of: I agree; I disagree, let's talk; or I forgo my
  vote. Not replying isn't one of the options.
- **Calls settle what async can't.** Keep them to the people who care about the decision, and
  post the outcome back on the issue so everyone else can catch up in their own time.
- **A weekly huddle** covers updates and open questions.
- **Keep moving.** Aim to move at least one thing forward each week, averaged over a month.
  Side projects die quietly when weeks go by with nothing done.
- **Write for humans.** Using AI to draft an issue or a PR description is fine. Read it and
  check it says what you mean before you post it.

## Picking up work

- Find an issue, or open one, and assign yourself so others know it's taken.
- Link dependencies ("Depends on #N") so people working the same weekend don't collide.
- `ready-for-agent` means an issue is fully specified and Satat may pick it up. The other
  labels are in [triage labels](docs/agents/triage-labels.md).
- Bigger efforts are planned as a wayfinder map: one `wayfinder:map` issue with child tickets.
  `scripts/next-issue.sh` lists the next unblocked, unassigned ticket on the newest map.
- Research goes in `docs/research/`, and the settled outcome becomes an ADR in `docs/adr/`.
  Don't leave findings only in an issue.

## Building opinions

The point of Satat is a body of tested opinions and a product that always works. We learn by
trying things, and we never let the product stop working while we do.

- **Adopt before you build.** If a tool already solves the problem, use it. Spend our effort on
  problems nobody has solved.
- **Experiment instead of evaluating forever.** Pick one tool, try it on a real case, and form
  an opinion on what it can and can't do. Then try a second on a branch to compare.
- **Thin slices.** Make it work, then make it right, then make it fast.
- **Write the opinion down** with the good and the bad, so the team can argue with it.
- **Keep it working.** `main` always runs. The README says what Satat can do today, and the
  [roadmap](docs/roadmap/) says what's coming. Never describe a planned feature as if it exists.

## Pull requests

### Rebase merges only

**PRs are rebase-merged. There are no merge commits and no squashing.** Every commit in your PR
lands on `main` exactly as you wrote it, so:

- Each commit must build and pass the checks on its own.
- Each commit message is written for `main`, not for your reviewer. "Fix review comments" and
  "WIP" commits have to be folded into the commits they fix before you merge.
- Keep the history in the order a reader should follow it.

### A good PR

- **One thing.** A PR does what its title says and nothing else. No drive-by refactoring: if you
  spot something worth tidying, open a separate PR or an issue.
- **Small.** Easier to review, and easier to revert.
- **Tests come with the change.** A PR that changes behaviour includes the tests that prove it.
  Run them before you push. For Go, that's `go test -p 1 ./...`: tests that start containers
  share one Docker daemon, so packages must not run in parallel, and `-short` skips them.
- **Docs come with the change.** Update the docs in the same PR. They aren't a separate task.
- **The description says what, why and how you tested it,** and links the issue (`Closes #N`).
- **Open it as a draft** when you want early feedback.

### Merging

- Every change goes through a pull request, not straight to `main`. Satat's agents learn from
  the same PRs, tests and checks we write.
- `main` requires signed commits. Set up
  [commit signing](https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification)
  before your first PR.
- Humans may merge their own PRs. Tag a reviewer when you want one.
- Agent PRs need a human's approval before they merge, at least until our evals are strong
  enough to trust them (#124). Satat only opens draft PRs
  ([ADR 0022](docs/adr/0022-v2-platform-requirement-decisions.md)).

### Secrets

Never commit secrets, tokens or keys. gitleaks and the private-key and AWS-credential checks run
in pre-commit and again in CI. Where secrets do live is in [AGENTS.md](AGENTS.md#secrets).

## Developer experience

We follow these
[developer experience non-negotiables](https://karun.me/blog/2025/06/23/what-makes-developer-experience-world-class/):

1. A short README that says what the code does and how to get started.
2. A single entry point for every task: one command to set up a workstation, one to run the
   system, and one each to lint and test. Ours is the `justfile`: `just setup`, `just doctor`,
   `just run`, `just lint`, `just test` and `just build`. New tasks get a recipe there.
3. Fast feedback: linting and tests quick enough to run on every commit.
4. Pre-commit checks that run the CI safety net locally.
5. Everything runs locally, without the internet where possible.

## Setup

Install [just](https://just.systems/), [mise](https://mise.jdx.dev/) and
[pre-commit](https://pre-commit.com/), then run `just setup`. It installs the tools pinned in
`mise.toml`, the git hooks (including the commit message check) and a `.env`, and then runs
`just doctor`, which names anything else that is missing, such as `actionlint`.

`scripts/doctor.sh` is a list of check functions. When a change adds a prerequisite, add a check
for it there.

## Commit messages

We follow [How to Write a Git Commit Message](https://cbea.ms/git-commit/), with one change:
don't wrap the body by hand. Let tools wrap it for display. With the commit-msg hook installed,
`scripts/check-commit-msg.sh` checks the mechanical rules: a capitalised subject, no full stop,
72 characters at most, and a blank line before the body.

**Subject**

- Use the imperative mood. It should complete "If applied, this commit will ...".
- Start with the verb for the kind of change: `Add`, `Fix`, `Update`, `Remove`, `Refactor`,
  `Rename`, `Move`, `Docs`, `Test` or `Config`.
- Capitalise it and leave off the full stop.
- Aim for 50 characters, 72 at most, not counting the issue reference.
- End with the issue number (`#N`) when there is one. Skip it for trivial chores.

**Body**

- Separate it from the subject with a blank line.
- Explain what changed and why. The diff already shows how. Mention trade-offs and anything
  you ruled out.
- Use a `Closes #N` footer when the commit finishes the issue.

**Scope**

- One logical change per commit, so any one can be reverted cleanly.

```text
Fix parallel Docker tests clobbering each other #112

Package-level parallelism let one package's label reap remove another's running container. Run Go tests with -p 1 in CI and document it.
```
