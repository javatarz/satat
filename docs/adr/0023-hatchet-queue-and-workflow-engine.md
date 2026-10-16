# ADR 0023: Hatchet as queue and workflow engine

## Status

Accepted (2026-10-16). Settles the engine proposal left open in
[ADR 0021](0021-adopt-platform-plumbing.md) and
[ADR 0022](0022-v2-platform-requirement-decisions.md). Based on the Hatchet caps spike (#101,
[findings](../research/v2/hatchet-caps-spike.md)) and the Claude lane spike (#102,
[findings](../research/v2/claude-lane-spike.md)). The case and the alternatives are in
[why Hatchet](../research/v2/why-hatchet.md).

## Context

ADR 0021 says Satat adopts its queue and intake layers. The engine must enforce four caps
together: global, per lane, per repo and per credential account (NN12). It must hold queued
work while a lane backs off, accept GitHub webhooks and cron, run on one VM beside Postgres,
and meet the OSI licence bar (Q4). Hatchet was the only candidate where the caps are
configuration, not code. The fallback if its caps or timeouts failed was Temporal.

## Decision

Satat adopts Hatchet (self-hosted, MIT) as the queue, scheduler and workflow engine. Satat
keeps its own workflow YAML; only the interpreter and the cap wiring are Hatchet-specific.

How Satat uses it, as verified in the spike:

- **Caps.** One workflow carries four `GROUP_ROUND_ROBIN` concurrency expressions: `'global'`,
  `input.lane`, `input.repo` and `input.account`. Per-lane and per-account limits that differ
  use a CEL `max_runs` (for example `input.cap`). The mapper looks each cap up in `satat.yaml`
  and passes it in the run input.
- **Claude-lane back-off.** A live rate limit on a lane key (`rate_limits.put(key, 0, ...)`),
  restored with a nonzero limit when the token recovers. Queued work waits and resumes at the
  next refill window. Pausing a lane's workflow is the second option and needs each lane to
  be its own workflow. A `max_runs` of zero holds work but cannot change live, so it is not
  used for back-off.
- **Intake.** Hatchet webhooks with the `GITHUB` source and HMAC verification. The event key
  and the concurrency keys are read from the payload. Cron triggers are created per repo
  with an explicit input.
- **Timeouts.** Every task sets a long `schedule_timeout` (720 h worked), because the 5-minute
  default cancels runs waiting behind a cap or a rate limit. Every pause sets a long
  `queue_ttl`. The data-retention period is set long (`SERVER_LIMITS_DEFAULT_TENANT_RETENTION_PERIOD`)
  if run history matters.
- **Language.** Go SDK, in line with Q7. The spike used the Python SDK; the options exist in Go.
- **Deployment.** `hatchet-lite` and Postgres on the VM, pinned by image digest
  (`scripts/image-digest.sh`). Postgres snapshot before every upgrade.

## Consequences

- Temporal stays the fallback. Moving to it means rewriting the interpreter and the cap
  wiring, and building the caps as semaphore workflows.
- Pre-1.0 and frequent releases (39 between 2026-07-15 and 2026-10-15): upgrades are
  deliberate, pinned and preceded by a snapshot.
- Three defaults drop work silently: `schedule_timeout`, the pause TTL and retention. The
  mapper and launcher code must set them explicitly, with a test that no task is registered
  without them.
- A cron on a workflow whose concurrency key reads from input produces no runs and no error.
  Cron creation always passes input, with a test.
- Hatchet's dashboard gives a first control plane. Whether a purpose-built work-item board is
  still needed stays open (#84, build step 8).
- Satat still builds the YAML interpreter, the workload launcher, the drivers and the
  Claude-lane back-off logic.

## Not verified

- A queue held for hours (longest wait was about 6 minutes) and exact cancel timing under the
  5-minute default on an unpaused queue.
- Behaviour under real load (tests ran 36 runs at a time), on k3s and on arm64.
- RAM of Hatchet with k3s, Postgres and LiteLLM on the VM (#104).
- Tenant-scoped concurrency across workflows and the retention variable.

Revisit this ADR if a multi-hour queue loses work or the VM cannot hold the stack (#104).
