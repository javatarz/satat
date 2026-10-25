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

### Linear history, merged from the command line

**There are no merge commits and no squashing.** Every commit in your PR lands on `main` exactly
as you signed it, so:

- Each commit must build and pass the checks on its own.
- Each commit message is written for `main`, not for your reviewer. "Fix review comments" and
  "WIP" commits have to be folded into the commits they fix before you merge.
- Keep the history in the order a reader should follow it.

Merge from the command line, not with GitHub's merge button. The button's "Rebase and merge"
creates new, unsigned commits, which `main` rejects. Rebase your branch onto `origin/main`, make
sure every commit is signed, wait for the checks, then fast-forward `main`:

```sh
git fetch origin
git rebase origin/main        # only if main moved; then re-sign (see Signed commits)
git push origin <branch>:main
```

The PR closes by itself once its commits are on `main`.

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
- `main` requires signed commits that GitHub shows as Verified. Set up
  [signing](#signed-commits) before your first commit.
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
`mise.toml`, the git hooks (including the commit message and [signing](#signed-commits)
checks) and a `.env`, and then runs
`just doctor`, which names anything else that is missing, such as `actionlint`.

`scripts/doctor.sh` is a list of check functions. When a change adds a prerequisite, add a check
for it there.

## Signed commits

`main` only accepts commits GitHub shows as Verified. Your PR's commits land on `main` as they
are, so every one has to be signed, not just the last one. Three checks tell you early:

- **On commit**, the pre-commit hook fails if git isn't set up to sign
  (`scripts/check-signing-config.sh`). `just doctor` warns about the same thing.
- **On push**, the pre-push hook fails if a commit you're about to push is unsigned or has a bad
  signature (`scripts/check-signed-push.sh`).
- **On the PR**, the `signed-commits` check fails for any commit GitHub doesn't show as Verified
  and says why: `unsigned`, `unknown_key` (the key isn't on your GitHub account) or `bad_email`
  and similar (the commit email isn't a verified email on your GitHub account).

`just setup` installs the hooks. If you set up before the push hook existed, run it again.

### Set up signing

With an SSH key (the simplest if you already push over SSH):

```sh
git config --global commit.gpgsign true
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519.pub
```

With a GPG key:

```sh
git config --global commit.gpgsign true
git config --global user.signingkey <key id>   # gpg --list-secret-keys --keyid-format=long
```

Then add the key to GitHub under Settings > SSH and GPG keys. An SSH key has to be added as a
**signing key**, separately from adding it as an authentication key. Make sure `user.email` is a
verified email on your GitHub account. Check with `git log --show-signature -1` after your next
commit. GitHub's
[signing guide](https://docs.github.com/en/authentication/managing-commit-signature-verification)
covers the details.

### Re-sign commits you already made

Re-signing rewrites the commit ids but keeps your changes, messages and authors. From your branch:

```sh
git fetch origin
git rebase --exec 'git commit --amend --no-edit -S' origin/main
git push --force-with-lease
```

`git rebase --force-rebase -S origin/main` does the same in one step. Without `--force-rebase`,
git keeps commits that are already on top of `origin/main` as they are, unsigned. If the check
says `bad_email`, fix `user.email` and add `--reset-author` to the amend so the commits take the
new email.

Both commands re-sign **every** commit in the range as you, replacing any existing signature and
making you the committer. That is right for your own commits. If the PR also has commits by other
people, don't re-sign those for them: your signature on their work says you wrote it, and
`--reset-author` would overwrite their authorship. Ask them to re-sign their own commits, or
re-sign only yours with an interactive rebase (`git rebase -i origin/main`, then `exec git commit
--amend --no-edit -S` after each of your commits).

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
