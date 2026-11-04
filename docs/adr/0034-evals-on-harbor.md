# ADR 0034: Evals on Harbor, with Satat as the agent and Claude Code alone as the baseline

## Status

Accepted (2026-11-04). Decides #172's tool question. Research in
[docs/research/v2/evals.md](../research/v2/evals.md).

## Context

When we change Satat's model, Claude Code version, hooks or container, nothing measures whether
it got better or worse. `eval/` is a quick Go harness for one case (`sandbox-greet`): it runs the
whole pipeline on a GitHub issue and grades the result with held-out tests. It has no repeated
tries, no stored results to compare against and no baseline, and it needs the case's repo on
GitHub. The roadmap named two tools to pick from: Harbor (a task format and runner) and Inspect AI
(a parallel runner). The owner chose Harbor.

## Decision

**Tasks use Harbor's format**, in `evals/tasks/`: `instruction.md` (the issue), `environment/`
(the repo, committed at `/app`), `tests/test.sh` (hidden grading) and `solution/solve.sh` (a
reference answer the oracle agent must pass). The verifier runs in a separate sandbox with no
network and gets only `/app` from the agent.

**Satat is the main agent**, plugged in as a Harbor external agent (`evals/agents/satat_agent.py`).
It hands the task's repo to Satat as a snapshot (`satat-step --snapshot`, `pipeline.SnapshotCloner`)
and applies Satat's commits back. Satat runs unchanged from the clone onwards: the same lane
container, runner, prompt and push rehearsal as a real issue. A snapshot works the same on a laptop
or a cluster, and needs no GitHub repo or token per task.

**Harbor's `claude-code` agent is the baseline**, pinned to the Claude Code version in Satat's
image and kept on the subscription token. It runs once now and again when the model or the Claude
Code version changes, so a drop can be put down to the model or to Satat.

**Scoring is pass/fail plus a report card.** An attempt passes when the hidden tests pass and the
repo's original tests still pass (`reward` in `reward.json`). Pass rate is passes over attempts,
with system errors counted apart. Reliability over 3 tries, tokens, turns, minutes and rate-limit
hits are reported beside it, never weighted into it. `evals/summarize.py` writes a short summary
per run to `evals/results/` and flags any report-card number more than 25% worse than the
previous run.

**Harbor is pinned** in `mise.toml` (`pipx:harbor`), so `just setup` installs it.

## Consequences

- Evals run by hand (`just eval-satat`, `just eval-baseline`), not on real PRs, one trial at a time
  because the subscription allows one run at a time. A batch uses the same quota as real work.
- Tasks are public in this repo, hidden tests included. The agent never sees this repo, but the
  answers could reach training data. They can move to a private repo later without changing the
  format.
- Adding a task means writing a Harbor folder and checking it with `just eval-check`. No Go code.
- The adapter depends on Harbor's `BaseAgent` and `BaseEnvironment` (`exec`, `download_dir`,
  `upload_file`), which a Harbor upgrade could change. The pin keeps that a deliberate step.
- `eval/` and `cmd/satat-eval` overlap with this. They are removed once the Harbor tasks also
  check scope, commit style and that the agent ran the tests.
