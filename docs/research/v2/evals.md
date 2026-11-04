# Evals for the agent and harness together (#172)

Part of #99. Answers #172's questions: which tool, which sample repos and cases, and how a run is
scored and kept so the next one can be compared with it. The plan behind it is the
[Satat Evals Plan](https://claude.ai/artifact/AkCpGXLYBYDCxCzp1CkhkX). The decision is
[ADR 0034](../../adr/0034-evals-on-harbor.md). This first iteration covers deterministic checks only.

## Short answer

- **Tool: Harbor** ([docs](https://docs.harborframework.com/), PyPI `harbor` 0.25.0, Apache-2.0),
  pinned in `mise.toml`. Inspect AI was not taken further. The Go harness in `eval/` stays
  until Harbor covers its scope, commit-style and ran-tests checks, then goes.
- **Tasks: four hand-written Python tasks** in `evals/tasks/`, one of each kind the plan names:
  new feature (`greet`, `slugify`), bug fix (`fix-duration`) and write the tests (`test-split`).
- **Two agents on the same tasks.** Satat (`evals/agents/satat_agent.py`) is the main eval. Harbor's
  own `claude-code` agent, pinned to the Claude Code version in Satat's image, is the baseline.
  Both use the Claude subscription token and Claude Code's default model.
- **Scoring: pass/fail plus a report card, never one blended score.** An attempt passes when the
  hidden tests pass and the repo's original tests still pass. Pass rate is passes over attempts.
  Reliability, tokens, turns, minutes and rate-limit hits are reported beside it.
- **Results:** Harbor's full job output stays in `docs/scratch/harbor-jobs/` (gitignored). A short
  summary per run is committed in `evals/results/`, compared with the agent's previous run.

## How a run works

```sh
just eval-check                   # oracle must pass every task, doing nothing must fail every one
just eval-baseline                # Claude Code alone, 3 tries per task
just eval-satat                   # Satat, 3 tries per task
just eval-summary docs/scratch/harbor-jobs/<job>   # summarise, save, compare
```

Pass Harbor flags through, for example `just eval-satat -i greet -k 1` for one try of one task.

**A task** is a Harbor task folder. `instruction.md` is the issue (title on the first line).
`environment/` builds the repo the agent works on: `python:3.13.16-slim` by the same digest as
Satat's `check-python` lane, with the repo committed at `/app`. The image also has Claude Code at
the version Satat's image pins, so Harbor's `claude-code` agent skips its own install: that install
took 2 to 5 minutes a trial (measured 137 to 286 seconds), against about 10 seconds of agent work. `task.toml` gives the agent the
`claude` lane's limits (20 minutes, 2 CPUs, 3 GiB). `solution/solve.sh` is the reference answer.

**The verifier** runs in a separate sandbox built from the same image, with no network. Only
`/app` is copied in from the agent's sandbox, so nothing else the agent changed can affect the
grade (`harbor lint` flags a shared verifier). `tests/test.sh` writes `reward.json` with three
numbers:

- `solved`: the hidden tests pass. For `test-split`, the agent's tests pass against the real
  `split_amount` and fail against each of four mutants, each a bug the issue asked them to catch.
- `intact`: the repo's original tests, copied into `tests/` before the agent ran, pass against the
  changed code, so deleting a test cannot hide a break. For `test-split`, `money.py` must also be
  unchanged.
- `reward`: `solved` and `intact` together. Harbor's mean of it over trials is the pass rate.

**Satat as a Harbor agent.** Satat is an external agent (`BaseAgent`): its loop runs in the Harbor
process on the host, not in the task's sandbox. Per trial it downloads `/app`, runs
`satat-step --snapshot <repo> --instruction <file>` and, when there are commits, uploads
`changes.bundle` and moves `/app` to Satat's head commit. `--snapshot` uses
`pipeline.SnapshotCloner`, a `Cloner` that clones a local repo instead of GitHub. Everything after
the clone is the path a real issue takes: the same lane container, hardening, caches, runner,
prompt and push rehearsal. The step's output, including the transcript, is kept under the trial's
`agent/satat/`.

**System errors versus agent failures.** A trial that raises is a system error and is left out of
the pass rate. Harbor's `claude-code` agent raises when `claude` exits with an error, so the
adapter raises when Satat's runner reports `failed` (after applying any commits and recording
usage), so both agents count the same failures. An agent timeout counts as a fail, not an error.

**Cost and speed** come from the claude stream-json each agent leaves in its logs: tokens (input,
cache and output) and turns summed over every session, including Satat's follow-up after a failed
push rehearsal, and rate-limit events. Minutes are Harbor's agent execution time.

## What has been checked

Run on 2026-11-04 on the owner's MacBook (colima, Docker 28.4.0), Harbor 0.25.0.

| Check | Result |
|---|---|
| `harbor lint` on each task | Clean once the verifier was made separate |
| Oracle agent, all four tasks | 4/4 pass (`solved` 1, `intact` 1) |
| Nop agent, all four tasks | 0/4 pass (`solved` 0, `intact` 1): the tests tell a right answer from no answer |
| Satat adapter, fake token | Snapshot taken, lane container started, failed at Claude auth, recorded as a system error |
| Baseline, fake token | Harbor installed Claude Code 2.1.301 in the task image, failed at Claude auth, recorded as a system error |
| Applying a bundle in a task container | `git fetch` of the bundle and `reset --hard` to its head left the change in `/app` |

The first real runs of both agents, with the subscription token, are still to do. Their summaries
go in `evals/results/`.

## Fixes found on the way

- **Cache volume adoption** (`launcher/docker`): on Docker 28.4, creating a volume that already
  exists sometimes returns it without its labels, so the launcher refused its own persistent
  caches. It now reads the volume back before refusing it.
- **Token masking** (`pipeline/clone.go`): with an empty token, masking it in git's error output
  put `***` between every character. It is now skipped.

## Open

- **One subscription for evals and real work.** Both share the quota and the one-run-at-a-time
  account limit, so a batch (4 tasks × 3 tries × 2 agents) blocks real runs while it lasts. The
  plan leaves a separate seat to the team.
- **Measurements left for later:** easy to review (diff size, out-of-scope files) and repo rules
  (ran the tests itself, commit style), which the Go harness already checks, then the v2 trap
  tasks.
- **Go tasks and tasks rebuilt from issues Satat already fixed**, towards the plan's ten.
- **A trial takes about a minute, 22 seconds of it grading.** The separate verifier sandbox
  costs about 20 seconds over a shared one (4 seconds measured). Running trials two at a time would
  halve a run too, at the cost of the laptop's memory and of the one-run-at-a-time habit.
