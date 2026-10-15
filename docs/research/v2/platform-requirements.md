# Research: Satat platform requirements (draft for confirmation)

Status: **draft, 2026-10-15.** This defines what we are looking for before evaluating
frameworks. Nothing here is decided until the owner confirms it; open questions are at
the end.

## Why this exists

The current stack (OpenHands Agent Canvas on one EC2 VM, ADR 0001/0017/0018) runs one
harness, in one process, on one host. Three things it cannot give us:

- Claude Code on the subscription alongside open harnesses (pi, opencode) on the LLM
  Gateway. OpenHands now drives these over ACP, but only inside its single pod.
- Horizontal scale. The open-source Helm chart is one pod with a shared filesystem;
  per-run containers are an OpenHands Enterprise feature.
- A backlog control plane. Canvas shows conversations, not what is incoming, queued,
  in progress, or waiting on review, and it has no claim-based concurrency limits.

## End outcome

Satat behaves like a small team of software engineers working continuously. Work arrives
from repo backlogs and schedules; Satat queues it, works several items in parallel within
set limits, and hands back pull requests for human review, then follows up on review
feedback. The operator has one place to see what is incoming, queued, in progress,
blocked, and in review, and can pause, cancel, retry, or reprioritise any of it.

The same Satat deploys unchanged in shape to a laptop, a single VM with Docker, a
Kubernetes cluster, or GCP, AWS, or Azure. Moving between them means swapping
configured plugins, not rewriting the system.

## Non-negotiables

1. **Pluggable workload runtime.** Every task runs in its own isolated workload: a Docker
   container on a single host, a pod on Kubernetes, or a cloud container service (ECS/
   Fargate, Cloud Run jobs, Azure Container Apps jobs). No shared process or shared
   filesystem between tasks. Capacity grows by adding hosts or nodes.
2. **Pluggable harness.** Claude Code, opencode, and pi are supported at launch. Adding a
   harness is an adapter plus configuration, not a fork. The harness is chosen per
   workflow step, overridable per repo.
3. **Credential lanes enforced by infrastructure.** Claude Code uses only the
   Claude subscription (OAuth token) and is never given an API key it could fall back
   to. Open harnesses use only the LLM Gateway through LiteLLM. The subscription token
   never reaches a non-Claude-Code harness (Anthropic consumer terms). This is enforced by
   which secrets each workload receives, not by prompts or conventions.
4. **Control plane.** A UI and API that show every work item's state (incoming, queued,
   running, blocked, in review, done, failed), which harness and lane it is on, and its
   logs. Operators can pause, cancel, retry, and reprioritise.
5. **Concurrency limits.** Caps at three levels: global, per lane (the Claude subscription
   lane is the tightest), and per repo. Work over the cap queues; it is never dropped.
   The Claude lane backs off when it hits subscription usage limits and resumes when the
   window resets.
6. **Workflows as code.** Workflows are declarative files, versioned in git and reviewed
   like code. A workflow is a sequence or graph of steps (for example refine → implement
   → self-review → test → open PR → address review), each naming a harness, prompt or
   skill, and limits. Human approval gates are expressible.
7. **Repos opt in to shared workflows.** A repo references one or more shared workflows
   and supplies its own settings (trigger label, branch prefix, harness override, limits)
   through a small config file. One workflow definition serves many repos.
8. **Triggers.** Cron schedules, events (webhooks such as issue labelled or PR review
   submitted), and manual dispatch from the control plane.
9. **Every infrastructure dependency is a plugin.** Workload runtime, queue/state store,
   secret store, ingress and auth, notifications, observability backend, model router,
   and issue tracker each sit behind an interface with at least a local implementation
   and one cloud or Kubernetes implementation. A local single-node install must work with
   nothing but Docker.
10. **Security posture carries over.** Secrets never in git, IaC state, or instance
    metadata (ADR 0011). Each task gets least-privilege, short-lived credentials where the
    provider supports them. Agent code is untrusted: no host access, no container
    runtime socket, restricted egress.
11. **Self-hosted, with a license that allows a public recipe.** Satat is published as a
    "deploy your own" recipe, so every component's license must allow that. No managed
    control plane we cannot run ourselves.

## Shape of the solution

We are looking for these layers with stable interfaces between them. No single framework
is likely to cover all of them, so the question is which layers we adopt and which we
build.

| Layer | Responsibility | Plug points |
|---|---|---|
| Intake | Turn tracker items, cron ticks, and webhooks into work items | Issue tracker (GitHub first), webhook sources |
| Queue and scheduler | Hold work items, enforce concurrency caps, claim and lease work | State store (Postgres, SQLite locally) |
| Workflow engine | Run a workflow's steps durably, with retries, timeouts, and human gates | Workflow definitions from git |
| Harness adapter | Run one step with a given harness: inputs (repo, task, branch, instructions), outputs (status, artifacts, PR) | Claude Code, opencode, pi, future harnesses |
| Workload runtime driver | Start, watch, and tear down the isolated workload for a step | Docker, Kubernetes, ECS, Cloud Run, Azure Container Apps |
| Model access | Route open harnesses to the gateway with budgets; give Claude Code its subscription | LiteLLM + LLM Gateway; Claude subscription |
| Control plane | UI and API over the queue, workflows, and runs | Auth provider (OAuth) |
| Cross-cutting | Secrets, notifications, telemetry | Secret store, ntfy, OTLP backend |

## How candidates will be judged

For each framework or combination:

- Which layers it covers natively, and which we would have to build.
- Whether workload isolation is per task and runtime-pluggable, or tied to one host or
  one cloud.
- Whether it can drive external harness CLIs (Claude Code, opencode, pi), or only its own
  agent loop.
- Whether concurrency limits, queueing, and the control-plane view exist or must be built.
- Whether workflows are declarative files in git that repos can reference.
- License, maintenance activity, and project maturity.
- Operational weight on a single small VM (the cheapest deployment must stay cheap).

Agent SDKs that build an agent loop (for example Strands Agents, the Claude Agent SDK, the
OpenHands SDK) belong to the harness layer, not the orchestration layers. They are
candidates for a custom harness, not substitutes for the scheduler, workflow engine, or
control plane.

## Open questions

1. **Merge authority.** Does Satat stop at draft PRs for human review, or may a workflow
   merge after gates pass (as OpenHands' "software factory" does)?
2. **Build vs adopt.** Writing adapters (harness, runtime, tracker) is expected. Are we
   willing to build the control-plane UI, the scheduler, or the workflow engine, or must
   those come from an existing project?
3. **Tracker scope.** GitHub Issues only, or must the tracker be pluggable now (Linear,
   Jira, GitLab)?
4. **License bar.** OSI-approved open source only, or is source-available acceptable if
   it permits self-hosting and a public recipe?
5. **Live steering.** Do you need to chat with or redirect a running task from the
   control plane, or are logs plus cancel and retry enough?
6. **Tenancy.** Single operator, or several people sharing one Satat with separate
   repos and limits?
7. **Extension language.** Any preference for the language adapters and plugins are
   written in (Python, Go, TypeScript)?
