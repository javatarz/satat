# ADR 0022: v2 platform requirement decisions

## Status

Accepted (2026-10-15). Settles the requirement rewordings and open questions Q1 to Q7 in
[platform requirements](../research/v2/platform-requirements.md). It does not adopt a
runtime or an engine: Kubernetes with agent-sandbox and Hatchet stay proposals, each needing
its own ADR after its spike.

## Context

[ADR 0021](0021-adopt-platform-plumbing.md) moved Satat from per-backend workload drivers to
adopted frameworks. The research then proposed Kubernetes everywhere as the runtime API. That
conflicts with two non-negotiables as first written, and seven open questions were still
unanswered.

## Decision

### Requirement changes

- **NN1 (workload runtime):** every task runs in its own isolated workload on Kubernetes: k3d
  on a laptop, k3s on a single VM, EKS, GKE or AKS in the clouds. Satat no longer targets ECS,
  Cloud Run jobs or Azure Container Apps jobs.
- **NN9 (local install):** a local single-node install needs Docker plus the k3d CLI, not
  Docker alone.
- **Laptop isolation:** plain container isolation on the laptop. gVisor is unproven on k3d and
  absent on macOS. The laptop runs only the owner's repos. gVisor stays on k3s and the clouds.
- **k3s overhead:** accepted on the current t4g.xlarge. Measured in #104: k3s, Hatchet,
  Postgres, LiteLLM and a worker use about 2.4 GiB with nothing running, leaving about 13 GiB
  of the 16 for step pods (`docs/research/v2/platform-ram-spike.md`). Concurrency is then set
  by the real harness's memory and CPU per step, which is not yet measured. A larger instance
  is the fallback if that leaves too few steps.

### Open questions

| Q | Decision |
|---|---|
| Q1 merge authority | Draft PRs only. A human merges. A gated merge step may come later. |
| Q2 build vs adopt | Settled by ADR 0021. |
| Q3 tracker scope | GitHub only. The event schema stays tracker-neutral so GitLab, Linear and Jira are later mappers. |
| Q4 licence bar | OSI-approved open source for every adopted component. Claude Code is a named exception: a proprietary CLI run unmodified. |
| Q5 live steering | Not in v2. Logs, cancel and retry are enough. The runner stays one-shot. |
| Q6 tenancy | Single operator, but several credential accounts. See below. |
| Q7 language | Go for the runner, launcher, mappers and glue. |

### Credential accounts (new requirement, NN12)

Repos can belong to different organisations, so each needs its own credentials. A repo maps to
a **credential account**: an org's Claude token, or a LiteLLM key or team on the gateway.

- Caps and back-off apply per account, in addition to global, per-lane and per-repo caps.
- A Claude token expiry alert exists per token.
- Usage is visible per account: LiteLLM spend per key or team for the gateway lane, and the
  best available signal for each Claude token.
- Conversations are captured for later analysis and cost optimisation. Gateway lanes already
  pass through LiteLLM. For the Claude lane, a spike tests whether `claude -p` can run through
  a proxy on an OAuth token, and whether Anthropic's terms allow it. Spike #102 found the
  technical path works (`claude-lane-spike.md`); the terms question is still open. If not allowed, the runner
  captures stream-json transcripts and usage events and ships them to storage.

## Consequences

- The credential lane becomes a lane type plus an account. Hatchet concurrency keys gain an
  account dimension, so the three-key design needs re-checking in the Hatchet spike.
- The launcher mounts an account's secret per step, so secrets are keyed by account, not lane.
- Go runner: one static binary per lane image. Harness images stay large because they carry
  Node and the harness itself.
- Skipping live steering keeps the runner simple. Revisit it through ACP if it is wanted.
- Open issues #59, #75, #80, #81 and #84 overlap with v2 and need a supersede decision once the
  map exists.
