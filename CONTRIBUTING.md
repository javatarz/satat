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

### Linear history

**There are no merge commits and no squashing.** PRs are rebase-merged, so every commit in your
PR lands on `main`, in order, so:

- Each commit must build and pass the checks on its own.
- Each commit message is written for `main`, not for your reviewer. "Fix review comments" and
  "WIP" commits have to be folded into the commits they fix before you merge.
- Keep the history in the order a reader should follow it.

Merge with GitHub's "Rebase and merge" button or from the command line. We prefer the command
line, because it keeps your commits and their signatures as they are, but neither is required.
GitHub's button creates new commits, so they are not signed:

```sh
git fetch origin
git rebase origin/main        # only if main moved
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
- **Exposure test.** `launcher/docker/exposure_test.go` starts a probe process in a real step
  container and lists every credential it can see (environment, files, mounts) and every address
  it can reach (the secret store's network, the Docker API, hosts outside the allowlist). It
  fails on any finding that is not in `knownGaps`, the holes the harness has today: the Claude
  and GitHub tokens in `pass_env` and open egress on `internal: false` lanes. Those show as
  skips with the reason. When you close one, the test fails until you delete its entry. It needs
  Docker and is skipped with `-short`.
- **Docs come with the change.** Update the docs in the same PR. They aren't a separate task.
- **The description says what, why and how you tested it,** and links the issue (`Closes #N`).
- **Open it as a draft** when you want early feedback.

### Merging

- Every change goes through a pull request, not straight to `main`. Satat's agents learn from
  the same PRs, tests and checks we write.
- Signing your commits is encouraged, not required. See [signed commits](#signed-commits).
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
   `just run`, `just up`, `just status`, `just down`, `just lint`, `just test` and `just build`. New tasks get a recipe there.
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

### `.satat/commands.yaml`

Satat's agent reads the repo's commands from `.satat/commands.yaml` on the base branch. It has
`version: 1` and a `commands:` map. `setup` and `test` are required; any other key is an optional,
free-form hint. The harness runs none of them as a gate: `setup` installs the git hooks (commit,
commit-msg and pre-push), and those hooks are the gate. `test` is a fast loop, here
`go test -p 1 -short ./...` (the Docker tests need `just test`). See
[per-codebase workflows](docs/roadmap/per-codebase-workflows.md).

The same file can hold an optional top-level `protected:` key (the file stays `version: 1`, and
readers of `commands:` ignore other keys). The pusher refuses a bundle that changes a protected
path, so the agent cannot edit what judges it. The default list covers CI config
(`.github/workflows`, `.gitlab-ci.yml`, `.circleci`, ...), lint and format config
(`.pre-commit-config.yaml`, `.editorconfig`, `.golangci.*`, `.eslintrc*`, ...), hook directories,
`.gitmodules`, `.gitattributes`, CODEOWNERS and `.satat/`. Tests are not protected by default; add the ones that judge your
code.

```yaml
protected:
  add: [justfile, tests/golden/]   # more protected paths
  allow: [.editorconfig]           # default paths the agent may change in this repo
```

An `add` entry without a slash matches that name at any depth; one with a slash is anchored at the
repo root and covers everything under it. `allow` entries are always anchored at the root, so
`.editorconfig` frees the root file only; list a nested one by its path. `*`, `?` and `[...]` work within a name, and case is ignored.
`add` wins over `allow`. The pusher reads this from the base commit, so a change in the agent's own
branch never widens what it may touch. A malformed `protected:` section makes the pusher refuse
every push until it is fixed.

## Signed commits

We recommend signing your commits, but `main` doesn't require it. Signed commits show as Verified
on GitHub and tell a reader who wrote them. `just doctor` warns if git isn't set up to sign.
Nothing blocks an unsigned commit, and GitHub's rebase button creates unsigned ones anyway.

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

If you want to sign commits you already made, re-signing rewrites the commit ids but keeps your changes, messages and authors. From your branch:

```sh
git fetch origin
git rebase --exec 'git commit --amend --no-edit -S' origin/main
git push --force-with-lease
```

`git rebase --force-rebase -S origin/main` does the same in one step. Without `--force-rebase`,
git keeps commits that are already on top of `origin/main` as they are, unsigned. If GitHub
doesn't show them as Verified because of `bad_email`, fix `user.email` and add `--reset-author` to
the amend so the commits take the new email.

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
