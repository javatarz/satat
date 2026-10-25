# ADR 0026: Docker first, behind a launcher interface; Kubernetes stays the target

## Status

Accepted (2026-10-18). Defers [ADR 0024](0024-kubernetes-agent-sandbox-runtime.md): its
decision is not reversed, but it is no longer the first runtime. Amends the laptop line of
[ADR 0022](0022-v2-platform-requirement-decisions.md) (NN1, "k3d on a laptop") and the
"Provision-deploy split" in `AGENTS.md`. [ADR 0023](0023-hatchet-queue-and-workflow-engine.md)
is unchanged, with one clarification below. Where the Docker API is exposed to is decided in
[ADR 0027](0027-docker-api-access.md). Amended 2026-10-25: the laptop runs trusted
repos, not only the owner's, and the egress proxy is a hostname allowlist with no secrets (see
ADR 0025 and #99).

## Context

ADR 0024 runs every step as a one-shot `SandboxClaim` on Kubernetes. The spikes (#103, #104)
showed it works, but it puts a cluster between Satat and its first working step: k3d or k3s,
agent-sandbox, per-lane `SandboxTemplate`s, a gVisor install and a network-policy start-up
workaround. None of that is what is uncertain about Satat. What is uncertain is whether the
loop works end to end: a labeled issue, queued by Hatchet, run by a harness in an isolated
container, and returned as a draft PR.

Docker is already on the owner's laptop, and one container per step gives the same
isolation unit. The v0.1 stack ran on Docker Compose (ADR 0009).

## Decision

Satat starts on Docker, on the owner's laptop, and moves to Kubernetes later.

- **Runtime.** Each step is one container, created, started, waited on and removed by the
  Satat worker through the Docker Engine API. The platform (Hatchet, Postgres, the worker
  and a Docker socket proxy) runs in one Docker Compose project. LiteLLM joins when a lane that
  uses it exists (build step 5). The Claude Code lane talks to Anthropic directly on the
  subscription token and does not go through it.
- **Launcher interface.** The worker calls a small Go interface (`Launch(step) -> exit code,
  logs`, plus `Cancel`). Docker is the first implementation. The `SandboxClaim` flow from ADR
  0024 is the second, written when Kubernetes is the next step. Workflows, the runner and the
  drivers never see which backend is behind the interface. This replaces ADR 0021's "no
  per-backend drivers" for the launcher only, and only for these two backends. Cloud Run, ACA
  jobs and ECS stay out of scope.
- **Hatchet Lite.** The prototype runs `hatchet-lite` and Postgres. The full Hatchet
  deployment (separate engine, API, frontend and optional RabbitMQ) is a later test once
  load makes it relevant. Nothing in Satat's code depends on which one runs, because workflows
  and the SDK are the same.
- **Host.** The laptop only, for now. There is no VM, Terraform or provisioning work in
  this phase.

How the Docker launcher meets what ADR 0024 required of the Kubernetes one:

- **Idempotent.** The container name is deterministic from the run and step. A name conflict
  (HTTP 409) means adopt: inspect it and, if it has exited, read its result. Hatchet
  re-delivers tasks when the platform stalls (#104), so this is still required.
- **Cleanup.** No `AutoRemove` (it races the log and exit-code read). The launcher removes the
  container on every exit path. Every container carries `satat.run`, `satat.step` and
  `satat.lane` labels, and the worker reaps labelled containers older than a limit at start
  and on a timer.
- **Inputs.** Non-secret inputs are copied into the created container as a file
  (`PutArchive` into a per-step volume mounted at `/etc/satat`, because a read-only root refuses the copy)
  before start. No host bind mounts. The one secret the Claude lane needs,
  `CLAUDE_CODE_OAUTH_TOKEN`, is passed as an env var by name (the form the #102 spike verified).
  That is a prototype shortcut: this step container holds that token, unlike GitHub credentials
  (ADR 0025), and a later card replaces it with a proxy that injects it.
- **Hardening floor** (replaces the Pod Security `restricted` floor of ADR 0024): non-root
  user, `cap-drop ALL`, `no-new-privileges`, read-only root filesystem with a tmpfs `/tmp` and
  a tmpfs `/workspace` (memory-backed, so it counts against the lane's memory limit; a disk volume
  replaces it if real repos need more), default seccomp, `--memory`, `--cpus` and `--pids-limit` from
  the lane config, no Docker socket and no host path in any step container.
  `runtime: runsc` is allowed per lane where installed. It is not required (#110 is not on
  the critical path for a laptop that runs only trusted repos, per ADR 0022).
- **Lane = config, not a CRD.** A lane is an image, limits, a network and an allowed-backends
  list in `satat.yaml`. It is the equivalent of a `SandboxTemplate`.
- **Network.** One user-defined Docker network per lane, marked internal, so a step reaches
  only the containers attached to it: an egress proxy, and LiteLLM once a lane uses it. The proxy is a
  hostname allowlist only: no TLS interception, no secrets and no token injection (ADR 0025). It
  is part of v0.2 (#99). Until it exists, steps get a plain bridge network with no hostname
  allowlist, and only the owner's repos may be pointed at it.
- **Concurrency cap.** A deploy-time value: the Docker VM's memory, minus the platform share
  (measured in the prototype, expected below ADR 0024's 2.4 GiB because k3s is gone), divided
  by the per-step memory limit. #109 calibrates the per-step values on real steps.

## Consequences

- The first build steps change: build step 2 becomes "Docker launcher and lane config", and
  its gVisor acceptance condition moves to the Kubernetes step. Build step 9 becomes
  "Kubernetes launcher, then managed clusters". #99 is updated to say so.
- Two launcher implementations to keep behaviourally equal. A shared conformance test (start,
  exit code, logs, cancel, adopt after re-delivery, cleanup after a crash) runs against both.
- Docker gives weaker isolation than gVisor on Kubernetes: steps share the Docker VM's
  kernel. Acceptable because the laptop runs only trusted repos and Docker Desktop (or
  colima) puts a VM between the containers and macOS. Not acceptable for untrusted
  repos: they need gVisor or Kata (#166, #110).
- No network-policy start-up gap and no Kubernetes RBAC to design. A Docker API path to the
  worker is a larger single point of trust than the claim-only RBAC in ADR 0024, which is why
  ADR 0027 exists.
- ADR 0025 (broker, proxy, pusher) is unaffected and easier to tie to a step, since each step
  container has its own address on its lane network.
- Image digests, Postgres snapshots before Hatchet upgrades and the long `schedule_timeout`,
  pause TTL and retention settings from ADR 0023 apply as written.
- `AGENTS.md` and `CONTEXT.md` stop describing v0.2 as "the Kubernetes platform". They now say
  v0.2 is Hatchet, one-shot steps and a launcher interface, Docker first.

## Not verified

- That an internal lane network plus a proxy container works the same on Docker Desktop and
  colima as on Linux.
- The real harness's RAM and CPU per step, and the platform's share under Compose (#109).
- That the Hatchet worker keeps its heartbeat while a step container runs and the Docker VM
  is under memory pressure (the #104 stall happened under memory exhaustion).
- Hatchet's full deployment, and what load makes Lite insufficient. Tested after the prototype.
- `runsc` on Docker Desktop (absent on macOS) and on a Linux host.

Revisit this ADR when the first step runs on someone else's repo, when the laptop is no longer
the host, or when two simultaneous steps cannot fit the Docker VM.
