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

- Every change goes through a pull request, not straight to `main`. Satat's agents learn from
  the same PRs, tests and checks we write.
- Humans may merge their own PRs. Tag a reviewer when you want one.
- Agent PRs need a human's approval before they merge, at least until our evals are strong
  enough to trust them. Satat only opens draft PRs
  ([ADR 0022](docs/adr/0022-v2-platform-requirement-decisions.md)).
- Update the docs in the same PR as the change they describe. Docs aren't a separate task.

## Developer experience

We follow these
[developer experience non-negotiables](https://karun.me/blog/2025/06/23/what-makes-developer-experience-world-class/):

1. A short README that says what the code does and how to get started.
2. One command to set up a workstation and one to run the system. We use `just` for these; the
   justfile is still to come.
3. Fast feedback: linting and tests quick enough to run on every commit.
4. Pre-commit checks that run the CI safety net locally.
5. Everything runs locally, without the internet where possible.

## Setup

Install [pre-commit](https://pre-commit.com/) and the hooks, including the commit message check:

```sh
pre-commit install --hook-type pre-commit --hook-type commit-msg
```

The hooks also need `actionlint` on your path, and Terraform at the version pinned in
`mise.toml` (`mise install`).

## Commit messages

`scripts/check-commit-msg.sh` enforces the mechanical rules. The rest is judgement.

- Imperative mood: "Add feature", not "Added feature".
- Start with a verb for the kind of change: `Add`, `Fix`, `Update`, `Remove`, `Refactor`,
  `Rename`, `Move`, `Docs`, `Test` or `Config`.
- Capitalise the subject, keep it to 50 characters (72 at most), and leave off the full stop.
- Append the issue number (`#N`) when there is one.
- One logical change per commit.
- The body explains why. The code already shows how. Don't wrap lines by hand.
