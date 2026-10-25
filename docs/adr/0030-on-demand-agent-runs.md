# ADR 0030: On-demand one-shot agent runs through Hatchet

## Status

Proposed (2026-10-23). Based on the [infrastructure adapter design](../research/v2/deploy-targets.md).
Extends [ADR 0023](0023-hatchet-queue-and-workflow-engine.md) (one more trigger event, the same
caps). Keeps [ADR 0027](0027-docker-api-access.md) and the one-shot runner of
[ADR 0022](0022-v2-platform-requirement-decisions.md) (Q5).

## Context

Today an agent run starts only from an issue, through the `satat-issue` workflow. The platform
also needs one-shot runs on demand, from a lane's config, in the running platform: for platform
smoke tests, and perhaps for eval lanes. Each does one task and exits. Agent work itself starts
only from an issue a human labelled `ready-for-agent`, so these runs are allowed only on lanes
that run no agent ([design.md](../design.md#lane-started-runs)). Agents must not register with Hatchet themselves:
the worker stays the only Hatchet worker and the only process that starts containers.

Hatchet's concurrency groups belong to a workflow. ADR 0023 verified the four caps within one
workflow only, so a second workflow could run an on-demand `claude` agent next to an issue run on
the same subscription with neither the lane cap nor the account cap holding.

## Decision

- **One workflow, two events.** The existing workflow gains `satat:agent` next to `satat:issue`, with
  the same four caps and timeouts. Its input gains `kind` (`issue` or `agent`; empty means issue) and
  `step_input`. Agent runs key the repo cap on a fixed `adhoc` value, and look up the lane and account
  caps from `satat.yaml` as issue runs do. So both kinds share one set of groups.
- **Only on lanes that say `agent: false`.** A lane without the key runs an agent and takes issue
  runs only. The worker refuses an agent run on such a lane at run start, whoever pushed the event:
  it launches nothing and records the run as `refused` with the reason. `satat agent run` refuses
  it too, before pushing, to fail early. `smoke`, `sleep` and `check-python` say `agent: false`.
- **An agent run is one launcher step** on the named lane, with `step_input` placed at
  `/etc/satat/input.json`. Nothing in it reaches a container flag. It never pushes. Publishing stays
  with issue runs.
- **The result is small and scrubbed:** exit code, a status (`ok`, `failed`, `timed_out`, `oom`,
  `cancelled`), output file names and sizes, and the last 4096 bytes of stdout and stderr. Every
  secret value the worker holds is replaced with `***` across the whole output **before** it's cut,
  so neither a secret nor part of one is stored in Hatchet's 720 h run history. File contents aren't
  returned. A non-zero exit is a result, not a failed task, so it stays readable.
- **The operator reaches it through Hatchet, never Docker:**
  - `satat agent run <lane> [--input] [--wait]` pushes the event, and `--wait` prints the result.
  - `satat agent ls` lists queued and running runs of both kinds.
  - `satat agent rm <run>` cancels the run. The worker's task context ends, and the launcher's
    `Cancel` path stops and removes the container.

  Because these go through Hatchet, they work unchanged when the launcher behind the worker becomes
  Kubernetes.
- **No compose labels on agent containers.** They carry `satat.*` labels only, so `docker compose up
  --remove-orphans` and `satat deploy up` never mistake a running agent for an orphan. A unit test
  asserts it.
- **No health check on agents** ([ADR 0029](0029-worker-health.md)).

## Consequences

- On-demand runs queue behind issue runs, and the reverse, on a shared lane or account. That is the
  point, and it can surprise someone expecting `agent run` to start at once. `agent ls` shows the
  queue.
- Outputs beyond the 4 KiB tail aren't kept. Someone who needs the files runs `satat-step` by hand,
  as today.
- A `sleep` test lane joins `satat.yaml` for the cancel test, next to `smoke`.
- **Agent runs share one repo key, `adhoc`**, so `limits.per_repo` caps all on-demand agents
  together. With the shipped defaults (`per_repo` 1, `per_account` 1, lane `max_runs` 1) only one
  on-demand agent runs at a time. An operator who wants more raises those limits in `satat.yaml`.
  A separate per-repo default for `adhoc` is not decided.
- The CLI finds the run it started by a random nonce in the run input, not by timestamp, so
  two operators pushing to the same lane at once don't see each other's run.
- The workflow name stays `satat-issue` for now, though it carries both kinds. Renaming it would
  orphan run history. Revisit when the workflow interpreter (#108) replaces the hard-coded workflow.

## Verified

On the Compose stack on colima (scratch config with the limits raised to 3, `smoke` and `sleep`
lanes, placeholder secrets, [docker-prototype.md](../research/v2/docker-prototype.md#deploy-adapter-2026-10-23)):

- `agent rm` cancels a running agent and its container is gone within 4 s.
- With the global cap left at 2 and `per_repo`, `per_account` and the `sleep` lane's `max_runs`
  raised to 3, three agents gave two RUNNING and one QUEUED.
- `docker compose up --remove-orphans` leaves running agents alone.

## Not verified

- Shared caps across the two events with a real `claude` run and an issue run at the same time (the
  `claude` lane and real secrets were not used).
- A failed run whose output Hatchet dropped, against live Hatchet: it makes `agent run --wait`
  print the run's error and exit 1. It is unit-tested only.
- `agent ls` paging and the run lookup against a busy tenant. They are tested against a fake only.
- That a worker recreated while agents run adopts or stops them. They are orphaned until the reaper
  or `deploy down`.

Revisit this ADR if on-demand runs need their own caps, longer-lived agents, or full output
retention.
