# Research: agent platform options for Satat

Status: **research, 2026-10-15. Partly superseded.** Stack A here still has Satat-built drivers and an intake service; [ADR 0021](../../adr/0021-adopt-platform-plumbing.md) and [ADR 0022](../../adr/0022-v2-platform-requirement-decisions.md) replace them, and the current design is in the [How Satat works](https://claude.ai/code/artifact/ad820f93-494e-403a-a790-ed353561669e) doc. No decision on the stack itself. Input for the owner. The owner will capture
the outcome in an ADR after reading this.

This document evaluates frameworks and combinations against the confirmed requirements in
[`platform-requirements.md`](platform-requirements.md). The 11 non-negotiables (NN1-NN11) are
hard filters. The "How candidates will be judged" list is the scoring rubric. Where an open
question (Q1-Q7 in that doc) changes a verdict, the verdict is given under each answer.

Out of scope, as already rejected by the owner: GitHub Actions + ARC, Argo Workflows, and
Hermes Agent.

Method: one research pass per layer:

- full platforms
- durable workflow and queue engines
- workload runtime and sandboxes
- harnesses

Every claim was checked against primary sources: GitHub API metadata, LICENSE files, source
code, official docs, and Anthropic legal pages. Repo facts were fetched on 2026-10-15. Claims
not confirmed against a primary source are marked **UNVERIFIED**. The highest-impact claims
were spot-checked a second time:

- Daytona and coder/agentapi are archived.
- OpenHands added per-conversation Docker containers in PR #3403 on 2026-09-16.
- Hatchet is MIT-licensed, supports multiple concurrency keys, and has a schedule timeout.
- Prefect's work pool types, and its webhooks being Cloud-only.

## Summary

- No single project passes all eleven non-negotiables, so every viable answer combines
  layers. Satat builds the parts no project provides:
  - workflows as code with repo opt-in (NN6, NN7)
  - credential-lane wiring (NN3)
  - a backlog view of work items (NN4)
- Agent SDKs belong in the harness layer, as the requirements doc assumed. Strands ships in-process multi-agent graphs, A2A, deploy guides,
  and experimental durable checkpoints, which it delegates to Temporal. None of that is a work
  queue, keyed concurrency caps, workflows-as-files, or a control plane. Strands also cannot
  drive Claude Code, opencode, or pi. It belongs in the harness layer, and only as a possible
  future custom harness.
- One premise in the requirements doc is out of date. Per-conversation containers are no
  longer Enterprise-only in OpenHands:
  - Since 2026-09-16, `OH_CONVERSATION_RUNTIME=docker` runs one hardened container per
    conversation on one Docker host.
  - Kubernetes is reachable through the separate, MIT-licensed `OpenHands/sandbox-server`,
    which is new: 4 stars and no releases.
  - The OSS Helm chart is still one shared pod.
- **Recommended direction (Stack A):**
  - **Hatchet** as the queue, scheduler, and durable workflow engine.
  - A **Satat runner** inside every workload that drives Claude Code, opencode, and pi
    directly.
  - A **thin, Satat-owned workload-driver interface**: Docker first, then Kubernetes, then
    cloud jobs.
  - A **YAML workflow interpreter** that Satat writes.
  - This holds under the likely answers to the open questions: OSI licences, adopt the
    engine, single operator, one small VM.
- Stack B keeps OpenHands as the execution layer. It is the better choice if live chat
  steering (Q5) matters more than independence from a fast-moving upstream.
- Stack C (Prefect 3) is the only free engine that natively launches each run on Docker,
  Kubernetes, ECS, Cloud Run, and Azure Container Instances. It is held back by a security
  concern: the workload needs a control-plane credential (see Stack C).

## Layer comparison

### Full platforms

| Candidate | Per-task isolation | Drives Claude Code / opencode / pi | Queue, caps, control plane | Workflows in git | Licence | Activity (2026-10) | Verdict |
|---|---|---|---|---|---|---|---|
| **OpenHands OSS** (Canvas + agent-server + automation + sandbox-server) | Docker: one container per conversation since 2026-09-16 (cap-drop ALL, no-new-privileges, no socket). Kubernetes via sandbox-server + agent-sandbox. No ECS, Cloud Run, or ACA. `/api/bash` and automation setup scripts still run on the outer host. | Yes, all three over ACP (`claude-agent-acp`, `opencode acp`, `pi-acp`) | One global `max_concurrent_runs`. Over-cap requests get **HTTP 429 and are not queued**. No lane or repo caps. Canvas shows conversations, not a backlog. | One `automation.yaml` per automation via Git Sync. Single step, no graph. | MIT | Very active: SDK v1.55.0 (2026-10-15), Canvas v1.27.1 | **Partial.** Strong execution layer; scheduler, lanes, workflows, and backlog view must be built |
| OpenHands Enterprise (reference) | Per-run pods via a closed `runtime-api` service | Yes | Multi-user, budgets | Same as OSS | PolyForm Free Trial 1.0.0 (30 days a year) | Active | **Fails NN11** |
| Multica | None. A per-run working directory on the daemon host; its docs say "Treat every run as unsandboxed" | Yes, 26 CLIs | Best of the group: queue, per-daemon and per-agent caps, board, live steering | No. Runbooks and skills live in its database | "Multica License": Apache-2.0 plus no hosted service and branding conditions. Not OSI. | v0.6.2, ~53k stars | **Fails NN1, NN6, NN7, NN10** (and NN11 if the bar is OSI) |
| Coder Tasks + AgentAPI | Workspaces from Terraform templates | **Tasks removed after v2.34; AgentAPI archived.** Successor Coder Agents runs Coder's own loop | Community licence: 5 concurrent agents | No | AGPL-3.0 core, proprietary `enterprise/` | v2.38.0 | **Fails NN2** |
| Vibe Kanban | Git worktrees on the host | Claude Code and opencode; no pi | Local UI only | No | Apache-2.0 | Company shut down 2026-04-10; v0.1.44 (2026-04-24) | **Fails**; unmaintained |
| AgentBox (three unrelated projects; madarco/agentbox is the likely one) | A container or VM per "box", provider plugin SDK | Claude Code, Codex, opencode, pi | Basic queue, desktop UI | No | MIT | v0.4.3 | **Fails** as a platform. Reference for a runtime-provider SDK |
| claw-orchestrator (Enderfga) | Git worktrees on one host | Claude Code, opencode, others; pi unverified | Session cap, spend caps, minimal dashboard | `WorkflowSpec` JSON with `human_gate` and verifier nodes | MIT | v7.11.0, single maintainer | **Fails NN1, NN3, NN4, NN9.** Reference for the workflow layer |
| OpenAI Symphony (added) | Per-issue directory; sandboxing out of scope | Codex only | `max_concurrent_agents` | **In-repo `WORKFLOW.md`** | Apache-2.0 | Engineering preview | **Fails NN1, NN2, NN4.** Reference for repo-owned workflow files |
| Open-Inspect / background-agents (added) | Cloud sandboxes (Modal, Daytona, E2B) | opencode, Claude | Web UI, intake | No | MIT | Active | **Fails NN9, NN11** (control plane needs Cloudflare Workers) |

### Durable workflow / queue engines

None of these knows what a work item or a repo is, and none launches harnesses. Satat builds
intake, harness adapters, and runtime drivers on top of whichever is chosen.

| Engine | Licence | Latest | Per-task container launch | Declarative files in git | Global / lane / repo caps | UI: pause, cancel, retry, reprioritise | Single-VM weight | Verdict |
|---|---|---|---|---|---|---|---|---|
| **Hatchet** | MIT | v0.110.5 (2026-10-06) | No; a task calls our driver | No (Python, TS, Go, Ruby code) | **Yes, natively.** Multiple CEL-keyed concurrency expressions per task, all enforced together, shareable across workflows, dynamic `max_runs`, `GROUP_ROUND_ROBIN` queues instead of dropping | Workflow pause, cancel, replay, bulk operations. Priority 1-3 is set at trigger; changing it on a queued run is UNVERIFIED | `hatchet-lite` (engine + API + UI) + Postgres. RAM UNVERIFIED | **Best fit** |
| **Temporal** | MIT | Server v1.32.1 (2026-10-08) | No; an activity calls our driver | No (8 SDKs) | Partial. Task-queue-per-lane plus worker slots. Fairness keys "do not provide … concurrency limits", so per-repo caps need a semaphore workflow we build | Cancel, terminate, reset, signal. Workflow pause is pre-release | Server + UI + Postgres; Elasticsearch optional | Strong alternative; more of the queue semantics to build |
| **Kestra** | Apache-2.0 (Enterprise Edition proprietary) | v2.0.6 (2026-10-13) | Docker runner in OSS. **Kubernetes, AWS Batch, Cloud Run, and Azure Batch runners are Enterprise** | **Yes, YAML flows** | Flow, namespace, and tenant limits with QUEUE behaviour; not keyed by input. Whether namespace and tenant limits are in OSS is UNVERIFIED | Pause/approval, kill, replay. Maintenance mode is Enterprise | JVM, ≥4 GiB RAM and 2 vCPU, Postgres | Best declarative fit; cloud runtimes are paywalled |
| **Prefect 3** | Apache-2.0 | 3.8.8 (2026-10-06) | **Yes, natively.** Docker, Kubernetes, ECS (EC2 and Fargate), Cloud Run v2, Azure Container Instances (not ACA jobs) | Partial: `prefect.yaml` deployments; flows are Python | Work-pool and work-queue limits, queue priority, named global concurrency limits, rate limits | Cancel, retry, pause/suspend (UI details UNVERIFIED) | Server + SQLite or Postgres | Viable (Stack C), with a security caveat |
| Restate | BUSL-1.1 (Apache-2.0 after 4 years) | v1.8.0 (2026-10-14) | No | No | Yes but **experimental**: scope plus two key levels | Cancel, kill, pause | Single binary, no external database | Fails OSI bar; passes source-available |
| Inngest | SSPL-1.0 (Apache-2.0 after 3 years) | v1.46.0 | No | No | At most two limits per function | UNVERIFIED for self-host | Single binary; self-host still "[Beta]" | Not recommended |
| Windmill | AGPLv3 + proprietary; Community Edition image may not be modified or wrapped | v1.835.0 | Only `nsjail` on the worker | Yes (OpenFlow YAML) | **Concurrency limits are Enterprise-only** | Approvals in OSS | Rust + Postgres | Not recommended |
| Trigger.dev | Apache-2.0 | v4.7.4 | Yes, on Docker and Kubernetes | No (TypeScript only) | Queue limit + `concurrencyKey` | UNVERIFIED | ~10 services (Postgres, Redis, ClickHouse, MinIO, …) | Too heavy for one VM |
| Conductor OSS | Apache-2.0 | v3.32.5 | No | Yes (JSON) | Per-task-definition limit only | UNVERIFIED | JVM + Postgres | Fallback if declarative JSON is wanted |
| DBOS / Flyte 2 / Dagger | MIT / Apache-2.0 / Apache-2.0 | n/a | n/a | n/a | n/a | DBOS's UI is a managed service. Flyte needs Kubernetes. Dagger has no queue or scheduler | n/a | Not a fit |

### Workload runtime / sandbox layer

| Candidate | Docker | Kubernetes | ECS / Cloud Run / ACA | Isolation | Licence | Latest | Verdict |
|---|---|---|---|---|---|---|---|
| **Satat thin driver** (Docker Engine API, Kubernetes Job/Pod, ECS `RunTask`, Cloud Run jobs, ACA jobs) | Yes | Yes | Yes, all three | runc, gVisor (`runsc`), or Kata on Docker and Kubernetes; a VM per task on Fargate; microVM on Cloud Run jobs | Ours | n/a | **Recommended abstraction** |
| kubernetes-sigs/agent-sandbox | No | Yes (CRDs, warm pools, default-deny egress NetworkPolicy, env-injection lock) | No | RuntimeClass (gVisor, Kata) | Apache-2.0 | v1.0.6 (2026-10-08); APIs at v1beta1 since v1.0.0 | Use **inside** the Kubernetes driver |
| OpenSandbox (opensandbox-group) | Yes | Yes (own controller or agent-sandbox) | No | runc, gVisor, Kata; Firecracker on Kubernetes only | Apache-2.0 | release-1.1.1 (2026-10-09) | Strongest adoptable project. Fail-closed egress sidecar; "Credential Vault" injects secrets on egress. Its egress filtering does not work under gVisor. Candidate later backend |
| E2B runtime / Embed | Single node only | One node | No | Firecracker | Apache-2.0 | 2026.30 (2026-09-10) | Reject: needs KVM and ~12 GiB RAM; multi-node is Enterprise |
| Daytona | n/a | n/a | n/a | n/a | AGPL-3.0 (frozen); Helm charts Elastic-2.0 | **Archived June 2026** | Reject |
| Modal, Cloudflare Sandbox | n/a | n/a | n/a | n/a | Managed only | n/a | Reject (NN11) |
| OpenKruise Agents, microsandbox | No / single host | Yes / No | No | RuntimeClass / libkrun microVM | Apache-2.0 | v0.6.0 / v0.7.8 (beta) | Watch |
| Nomad, Virtual Kubelet, Kestra runners | n/a | n/a | Partial | n/a | BUSL / Apache-2.0 / Enterprise-gated | Fargate provider dead since 2023-12 | Reject for this layer |

No project spans Docker, Kubernetes, and all three cloud job services. Credential lanes work
the same way on every backend: **one fixed identity per lane**. That is a task definition and
role on ECS, a job and service account on Cloud Run, a job and managed identity on ACA, a
ServiceAccount and Secret on Kubernetes, and a secret set on Docker. Only the Claude-lane
identity can read the OAuth token.

One Azure caveat: anyone allowed to start an ACA job can override its env and reference any
secret on that job. The Claude token must therefore live on a job that open-harness steps
never start.

### Harness layer

| Candidate | Licence | Latest | Headless | Events | Cancel / steer | Credentials | Role |
|---|---|---|---|---|---|---|---|
| **Claude Code CLI** (`claude -p`) | Proprietary; must stay unmodified | v2.1.300 (2026-10-14) | `--permission-mode`, `--permission-prompts none`, `--max-turns` | `stream-json` NDJSON, including `system/api_retry{error: rate_limit}` | SIGINT. Steering via `--input-format stream-json` | `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`. **`ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` silently take precedence over it; `--bare` ignores it** | Launch harness; drive directly |
| Claude Agent SDK / `claude-agent-acp` | Python MIT, TS proprietary; adapter Apache-2.0 but bundles the SDK | py 0.2.167 / ts 0.3.300 / adapter 0.91.0 | Yes | Typed messages / ACP | `interrupt()` / ACP cancel | Same as the CLI | Not needed. **Terms grey zone on OAuth** (below) |
| **opencode** | MIT | v1.19.0 (2026-10-11) | `run --format json --auto` | JSONL (schema undocumented) | **Native ACP** (`opencode acp`). v1 has cancel only | `OPENCODE_CONFIG_CONTENT` with `options.baseURL` pointed at LiteLLM. Any other provider key in env enables that provider | Launch harness; drive over ACP |
| **pi** (earendil-works/pi) | MIT | v1.2.0 (2026-10-12) | `--mode json`, `--mode rpc`; no permission prompts at all | JSONL; wait for `agent_settled` | RPC `steer`, `abort`, `follow_up` (best native steering) | `models.json` (`baseUrl`, `apiKey: "$VAR"`) in `PI_CODING_AGENT_DIR` | Launch harness; drive over native RPC |
| pi-acp | MIT | v0.0.34, one maintainer, "MVP" | | ACP | ACP | | Optional |
| **ACP** (Agent Client Protocol) | Apache-2.0; governed by Zed and JetBrains, foundation planned | Protocol v1 stable, schema v1.25.0; v2 draft since 2026-07-20 | Yes, with a policy client answering `session/request_permission` | `session/update` | v1: `session/cancel` only. v2 is meant to add steering | Out of scope of the spec, which suits infrastructure-enforced lanes | Shape of Satat's event schema; generic driver for future harnesses |
| Goose (AAIF / Linux Foundation) | Apache-2.0 | v1.54.0 | `run --recipe … --output-format stream-json` | stream-json | `goose acp` | `GOOSE_PROVIDER__*` | Possible fourth harness; not an orchestrator |
| OpenHands SDK (`ACPAgent`) | MIT | v1.55.0 | Yes | REST/WebSocket events | ACP cancel | Provider presets already strip `ANTHROPIC_API_KEY` when the OAuth token is present | Reference, or the runner core if extensions are in Python |
| Strands Agents (`strands-agents/harness-sdk`) | Apache-2.0 | py v1.61.0 (2026-10-14) | Own loop | Own events | Own | Model-provider keys | **Harness layer only.** Can be served over ACP (`strands --acp-server`); cannot drive CLIs |
| coder/agentapi, A2A | MIT / Apache-2.0 | agentapi **archived** | | | | | Reject (deprecated / wrong layer) |

**Claude subscription terms** (from the Claude Code legal page, authentication docs, and
Consumer Terms; this is a reading, not legal advice):

- **Clearly allowed:** the operator mints a token with `claude setup-token`, and Satat runs
  the **unmodified `claude` CLI headless**. This is the same pattern as the official GitHub
  Action, and the legal page allows it "including where a platform hosts Claude Code".
- **Grey:** driving Claude through the Agent SDK or `claude-agent-acp` on an OAuth token. The
  SDK docs tell third-party developers to use API keys. This is the path OpenHands uses for
  Claude Code over ACP.
- **Not allowed:**
  - several people sharing one token (matters for Q6)
  - sending the token to any other harness (NN3)
- **Volume risk:** subscription limits "assume ordinary, individual usage". Keep the Claude
  lane's cap low, and use `system/api_retry{error: rate_limit}` as the back-off signal.

## Viable stacks

All three stacks share the same lower layers:

- **The Satat runner and harness drivers.** The runner is the entrypoint of every workload.
  It:
  1. clones with a read-only token
  2. preflights the lane, failing closed. Claude: `claude auth status` must report
     `oauth_token`, with no `ANTHROPIC_*` env. Open harnesses: no Claude token, only the
     LiteLLM key.
  3. drives the harness and streams normalised, ACP-shaped events
  4. **commits, pushes, and opens the PR itself** with a short-lived GitHub App token injected
     only after the harness exits

  Drivers: `claude -p` stream-json, `opencode acp`, and `pi --mode rpc`.
- **Per-lane credentials.** Lane secrets come from the workload's identity, never from the
  caller.
- **A small work-item view.** Every stack needs one: incoming, queued, running, blocked, in
  review, done, and failed, derived from GitHub plus engine state. No engine or platform
  models PR review states.

### Stack A: Hatchet + Satat runner + Satat workload drivers (recommended)

| Layer | Source |
|---|---|
| Intake | **Build**: GitHub webhook and label sync into Hatchet events. Hatchet has webhooks, cron, and events natively |
| Queue and scheduler | **Hatchet**: global, lane, and repo caps as three concurrency expressions |
| Workflow engine | **Hatchet** durable tasks + **build** a YAML interpreter workflow (one shared workflow file plus a per-repo `satat.yaml`) |
| Harness adapter | **Build**: the runner + three drivers |
| Workload runtime | **Build**: driver interface. Docker first (Engine API, optional gVisor, `--internal` network with per-lane egress proxy). Kubernetes next (optionally agent-sandbox). ECS, Cloud Run, and ACA as needed |
| Model access | Existing LiteLLM + LLM Gateway; Claude subscription token |
| Control plane | Hatchet dashboard (runs, pause, cancel, replay) + **build** the work-item view |
| Cross-cutting | Existing ntfy, OTLP, secrets in GitHub or the cloud secret store |

What we build, roughly in order:

1. Runner and drivers. Claude Code first, because the lane check matters most.
2. Docker workload driver.
3. YAML interpreter and repo config.
4. GitHub intake.
5. Claude-lane back-off. Lower the lane's dynamic `max_runs`, or pause, until the window
   resets. Whether a zero limit is accepted is UNVERIFIED.
6. Work-item view.
7. Further runtime drivers.

Settings that must not be left at defaults:

- `schedule_timeout` defaults to **5 minutes**, after which queued tasks are cancelled. Set it
  to days, or NN5's "never dropped" fails.
- Paused workflows drop queued runs after a TTL. Set it long.
- `execution_timeout` must cover runs of 30-120 minutes.

Why it is recommended:

- It is the only engine whose native caps match NN5 directly.
- It is MIT, so it passes NN11 under either licence bar.
- It is light: one container plus Postgres, and Postgres is a pluggable state store (NN9).
- It supports Go, Python, and TypeScript, so it fits any answer to Q7.
- Hatchet tenants cover Q6 if tenancy grows.

Alternative inside Stack A: **Temporal** in place of Hatchet, if durability track record and
typed live-steering channels (Updates) outweigh building the lane and repo semaphores
ourselves. The layer boundaries are the same.

### Stack B: Satat orchestration on top of OpenHands execution

| Layer | Source |
|---|---|
| Intake, queue, workflows, work-item view | **Build** on Hatchet (as in Stack A) or on Temporal |
| Harness adapter | **OpenHands agent-server + ACP** (all three harnesses already wired) |
| Workload runtime | OpenHands `docker` runtime per conversation (single host). Kubernetes via `sandbox-server` + agent-sandbox. **Build** a server for the 7-endpoint remote runtime API to reach ECS, Cloud Run, and ACA |
| Control plane | Canvas for live chat with a running conversation + the work-item view |

What the remote runtime API takes:

- The server (`ghcr.io/openhands/runtime-api`) is closed. There is no public spec. The
  contract is read off two MIT clients: `APIRemoteWorkspace`, and `RemoteSandboxService` in
  sandbox-server.
- Endpoints, all authenticated with `X-API-Key`:
  - `POST /start` with image, command, env, session id, run-as user, runtime class, and
    resource factor; returns `url` and `session_api_key`
  - `GET /sessions/{id}`
  - `GET /sessions/batch`
  - `GET /list`
  - `POST /pause`, `POST /resume`, `POST /stop`
- The returned URL must route to the agent-server inside the workload.
- Estimated size: roughly 1-2k lines (UNVERIFIED).
- Pause and resume must be emulated on Cloud Run and ACA jobs.

What we build: everything Stack A builds above the harness layer, plus:

- the runtime-API server
- **one OpenHands backend per lane**, because the secret store is per backend
- isolation for automation bash, which still runs on the outer host

Strengths:

- It reuses today's stack and OpenHands' adapters.
- It already has live steering through Canvas (Q5).

Risks:

- **Claude Code runs through `claude-agent-acp`, which is the terms grey zone.**
- The upstream changes weekly: the repo split and the Canvas rename both happened in 2026.
- `sandbox-server` has no releases.
- Over-cap requests return 429.
- No egress restriction in the OSS Docker runtime.
- The OSS stack is explicitly single-tenant.

### Stack C: Prefect 3 + Satat runner

| Layer | Source |
|---|---|
| Queue and scheduler | **Prefect**: one work pool per lane (the pool's base job template carries only that lane's secrets and identity), a pool concurrency limit per lane, named global concurrency limits for the global cap and `repo:<name>` |
| Workload runtime | **Prefect work pools**: Docker, Kubernetes, ECS/Fargate, Cloud Run v2, Azure Container Instances. ACA jobs are not supported; ACI is the Azure option |
| Workflow engine | Prefect flows + **build** the YAML interpreter (Python) |
| Intake | **Build** a webhook receiver. Prefect webhooks are **Cloud-only** |
| Control plane | Prefect UI + work-item view |

What we build: the runner, packaged as a Prefect flow in the workload image; the YAML
interpreter; the webhook intake; and the work-item view. We do not build runtime drivers.
That is the main saving.

Risks:

- **The workload is a Prefect flow run, so it calls the Prefect API from inside the
  sandbox.** Agent code in the same container could reach the orchestrator with that
  credential. This conflicts with NN10 unless the runner and harness are split, or the API
  token is scoped. Self-hosted Prefect server auth is coarse (basic-auth string, UNVERIFIED);
  RBAC is a Cloud feature.
- Extensions are Python only (Q7).
- `prefect.yaml` is deployment config, not a workflow graph.
- Fewer of the stack-specific claims were verified than for Hatchet.

### Considered and not proposed as a stack

- **Kestra.** It fits NN6 best with native YAML flows. It loses on two points: its Kubernetes
  and cloud task runners are Enterprise-only, and it needs at least 4 GiB of RAM. It only
  becomes the pick if declarative YAML outweighs both.
- **Multica.** It is the only option if the control plane must be adopted (Q2) rather than
  built. It fails NN1, NN6, NN7, and NN10 for architectural reasons, not for lack of adapters.

## Verdicts under the open questions

| Question | Answer | Effect |
|---|---|---|
| Q1 Merge authority | Draft PRs only, or merge after gates | No effect on the choice. Gates are expressible in Hatchet (durable event waits), Temporal (signals), and Prefect (pause/suspend). Merging needs a write-scoped token only in the runner's finish phase |
| Q2 Build vs adopt | Building the scheduler, workflow engine, and UI is acceptable | Stack A with Temporal, or Stack B, become more attractive |
| | Engine and scheduler must be adopted; work-item view may be built | **Stack A (Hatchet)**, then C, then Kestra |
| | Control plane must also be adopted | **Nothing passes.** No engine shows work items or review state; Multica comes closest and fails NN1, NN6, NN7, NN10. The owner would need to relax this or accept a built view |
| Q3 Tracker scope | GitHub only | Intake is built in every stack; small |
| | Pluggable now | Same stacks; add a tracker interface to intake. No candidate removes this work (Multica uses its own tracker) |
| Q4 Licence bar | OSI only | Restate, Inngest, Windmill CE, Multica, and Nomad are out. Hatchet, Temporal, Prefect, Kestra OSS, and OpenHands OSS pass. **Claude Code itself is proprietary**: the bar must apply to Satat's components, with Claude Code installed unmodified at runtime and never redistributed, or Claude Code cannot be a launch harness |
| | Source-available OK | Restate becomes a light Stack A engine (keyed caps are experimental, no cron). Recommendation unchanged |
| Q5 Live steering | Logs + cancel + retry are enough | Stack A with one-shot drivers: the simplest path |
| | Steering required | Stack B (Canvas chat) has it today. In Stack A, use two-way drivers (`claude --input-format stream-json`, `pi --mode rpc`; opencode limited to cancel and re-prompt until ACP v2) plus an outbound channel from the runner to the control plane. Runtime-level attach is uneven, and unverified for Cloud Run and ACA jobs |
| Q6 Tenancy | Single operator | Any stack |
| | Several people | Hatchet tenants and Temporal namespaces fit. OpenHands OSS is single-tenant (pushes towards Enterprise, which fails NN11). **Each person needs their own Claude token and lane**: sharing one subscription is not permitted |
| Q7 Extension language | Go | Hatchet or Temporal; runner as a static binary; ACP client hand-written (no official Go SDK) |
| | TypeScript | Hatchet or Temporal; official ACP TS SDK 1.0 and pi/opencode clients: least runner effort |
| | Python | Hatchet, Temporal, or Prefect; the OpenHands SDK `ACPAgent` could be the runner core (Stack B) |

## Risks

- **Claude subscription enforcement.** Around-the-clock automated use may hit limits or draw
  enforcement "without prior notice". Mitigations:
  - a low Claude-lane cap
  - back-off on `rate_limit`
  - the unmodified CLI only
  - a gateway-lane fallback per workflow step
- **Credential leakage between lanes.** A stray `ANTHROPIC_API_KEY` silently overrides the
  OAuth token. A stray provider key enables that provider in opencode. Mitigations:
  - lane identities that cannot read the other lane's secrets
  - the runner's fail-closed preflight
  - a test in CI for each driver
  - Managed `forceLoginMethod` as a second guard is UNVERIFIED.
- **Satat becomes a product to maintain.** Every stack builds the runner, the YAML
  interpreter, intake, and the work-item view. The estimate for these is UNVERIFIED; size them
  before committing.
- **Upstream churn.**
  - OpenHands restructured its repos in 2026.
  - ACP v2 will break v1 clients.
  - pi and opencode release weekly, and opencode's JSON event schema is undocumented.

  Mitigations: pin harness versions in workload images, and qualify each upgrade with the ACP
  test kit (`acp-tck`) and driver tests.
- **Engine defaults that drop work.** Hatchet's 5-minute schedule timeout and pause TTL could
  silently cancel queued work if left at defaults (Stack A).
- **Egress control on the cheapest tier.** Docker has no FQDN egress policy. The Docker
  driver needs an egress proxy on an internal network. gVisor conflicts with sidecar-style
  nftables filtering, such as OpenSandbox's.
- **The orchestrator holds the container runtime socket.** In every stack, the worker that
  launches Docker workloads holds the socket. Workloads never get it, but the worker becomes
  a high-value target. Mitigations: a socket proxy or rootless Docker.
- **Small-VM footprint.** Hatchet-lite, Prefect, and OpenSandbox RAM figures are UNVERIFIED.
  Measure before sizing against [`vm-sizing.md`](../vm-sizing.md).

## Recommendation

Under the most likely answers (OSI licences for Satat's own components, adopt rather than
build the engine, one operator, logs plus cancel being enough), adopt **Stack A**:

- **Hatchet** for the queue, caps, and durable steps.
- A Satat-owned **runner** that drives the unmodified `claude` CLI, `opencode acp`, and
  `pi --mode rpc`, with lane preflight and runner-owned pushes.
- A Satat-owned **workload-driver interface**: Docker first, then Kubernetes (optionally on
  agent-sandbox), then cloud jobs.
- A Satat-owned **YAML workflow interpreter** with a per-repo `satat.yaml`. Use Symphony's
  `WORKFLOW.md` and claw-orchestrator's `WorkflowSpec` as design references.

Keep Temporal as the drop-in alternative engine. Revisit Stack B only if live chat steering
becomes required.

Before committing, run two spikes:

1. **Hatchet caps and back-off.** Three concurrency keys with a long `schedule_timeout`. Check
   that over-cap runs queue for hours without cancellation, and that a lane cap can be lowered
   to zero and restored.
2. **Claude lane in a container.** Run `claude -p` with only `CLAUDE_CODE_OAUTH_TOKEN`:
   - `auth status` reports `oauth_token`
   - a planted `ANTHROPIC_API_KEY` is caught by the preflight
   - check `forceLoginMethod` behaviour
   - check rate-limit events

The owner decides; the next step after that decision is an ADR, per the research flow in
`AGENTS.md`.

## Sources

Full per-layer notes with every citation are in [`notes/`](notes/). The primary sources behind
the claims above:

**Platforms**

- OpenHands:
  - https://github.com/OpenHands/OpenHands (`helm/agent-canvas/README.md`, `docs/ACP_AGENTS.md`)
  - https://github.com/OpenHands/software-agent-sdk:
    - `openhands-agent-server/openhands/agent_server/docker_runtime/registry.py` (PR #3403, 2026-09-16)
    - `openhands-workspace/openhands/workspace/remote_api/workspace.py`
    - `openhands-sdk/openhands/sdk/settings/acp_providers.py`
  - https://github.com/OpenHands/sandbox-server (`remote_sandbox_service.py`, `kubernetes_sandbox_service.py`)
  - https://github.com/OpenHands/automation
  - https://github.com/OpenHands/OpenHands-Cloud and https://github.com/OpenHands/enterprise (LICENSE: PolyForm Free Trial)
  - https://github.com/OpenHands/OpenHands/issues/15396 (repo split)
- Multica:
  - https://github.com/multica-ai/multica (`LICENSE`)
  - `apps/docs/content/docs/security-model.mdx`
  - `apps/docs/content/docs/daemon-runtimes.mdx`
- Coder:
  - https://github.com/coder/coder (`LICENSE`, `LICENSE.enterprise`)
  - `docs/ai-coder/agents/tasks-to-chats-migration.md`
  - `docs/ai-coder/agents/licensing-usage.md`
  - https://github.com/coder/agentapi (archived)
- Vibe Kanban: https://github.com/BloopAI/vibe-kanban, https://www.vibekanban.com/blog/shutdown
- AgentBox: https://github.com/madarco/agentbox, https://github.com/devilcoolyue/agentbox, https://github.com/fkeil/agentbox
- https://github.com/Enderfga/claw-orchestrator (`skills/references/workflow.md`)
- https://github.com/openai/symphony (`SPEC.md`)
- https://github.com/ColeMurray/background-agents

**Engines**

- Hatchet:
  - https://github.com/hatchet-dev/hatchet/blob/main/LICENSE
  - https://docs.hatchet.run/v1/concurrency
  - https://docs.hatchet.run/v1/timeouts
  - https://docs.hatchet.run/v1/pausing-workflows
  - https://docs.hatchet.run/v1/priority
  - https://docs.hatchet.run/self-hosting/hatchet-lite
- Temporal:
  - https://docs.temporal.io/develop/task-queue-priority-fairness
  - https://docs.temporal.io/design-patterns/fairness
  - https://docs.temporal.io/encyclopedia/workflow/workflow-pause
  - https://docs.temporal.io/temporal-service/persistence
- Kestra:
  - https://kestra.io/docs/task-runners/types
  - https://kestra.io/docs/workflow-components/concurrency
  - https://kestra.io/docs/administrator-guide/requirements
  - https://kestra.io/docs/oss-vs-paid
- Prefect:
  - https://docs.prefect.io/v3/concepts/work-pools
  - https://docs.prefect.io/v3/concepts/global-concurrency-limits
  - https://docs.prefect.io/v3/concepts/webhooks
  - https://docs.prefect.io/v3/concepts/server
- Restate:
  - https://github.com/restatedev/restate/blob/main/LICENSE
  - https://docs.restate.dev/services/flow-control
- Inngest:
  - https://github.com/inngest/inngest/blob/main/LICENSE.md
  - https://www.inngest.com/docs/guides/concurrency
- Windmill:
  - https://github.com/windmill-labs/windmill/blob/main/LICENSE
  - https://www.windmill.dev/docs/script_editor/concurrency_limit
- Trigger.dev: https://trigger.dev/docs/self-hosting/overview
- Conductor: https://conductor-oss.github.io/conductor/documentation/configuration/taskdef.html
- DBOS: https://docs.dbos.dev/production/self-hosting/conductor

**Runtime**

- agent-sandbox:
  - https://github.com/kubernetes-sigs/agent-sandbox (v1.0.0 release notes)
  - `extensions/api/v1beta1/sandboxtemplate_types.go`
- OpenSandbox:
  - https://github.com/opensandbox-group/OpenSandbox
  - `docs/architecture/network/egress.md`
  - `docs/guides/credential-vault.md`
- https://github.com/e2b-dev/runtime (`embed/README.md`)
- https://github.com/daytonaio/daytona (archived)
- https://modal.com/docs/guide/sandboxes
- https://github.com/cloudflare/sandbox-sdk
- https://gvisor.dev/docs/user_guide/quick_start/docker/
- https://github.com/kata-containers/kata-containers
- https://github.com/openkruise/agents
- https://github.com/superradcompany/microsandbox
- https://github.com/hashicorp/nomad/blob/main/LICENSE
- https://github.com/virtual-kubelet/aws-fargate
- Cloud job services:
  - https://docs.aws.amazon.com/AmazonECS/latest/developerguide/fargate-security-considerations.html
  - https://docs.aws.amazon.com/AmazonECS/latest/developerguide/specifying-sensitive-data-secrets.html
  - https://cloud.google.com/run/docs/configuring/task-timeout
  - https://cloud.google.com/run/docs/about-execution-environments
  - https://learn.microsoft.com/en-us/azure/container-apps/jobs

**Harness**

- Claude Code:
  - https://code.claude.com/docs/en/legal-and-compliance
  - https://code.claude.com/docs/en/authentication
  - https://code.claude.com/docs/en/agent-sdk/overview
  - https://code.claude.com/docs/en/headless
  - https://code.claude.com/docs/en/cli-reference
  - https://www.anthropic.com/legal/consumer-terms
  - https://github.com/anthropics/claude-code-action
- ACP:
  - https://github.com/agentclientprotocol/agent-client-protocol
  - https://agentclientprotocol.com/protocol/v1/prompt-turn
  - https://agentclientprotocol.com/protocol/v1/cancellation
  - https://agentclientprotocol.com/announcements/acp-v2-draft
  - https://agentclientprotocol.com/community/governance
  - https://github.com/agentclientprotocol/registry
  - https://github.com/agentclientprotocol/acp-tck
  - https://github.com/agentclientprotocol/claude-agent-acp
- opencode:
  - https://opencode.ai/docs/cli/
  - https://opencode.ai/docs/providers/
  - https://github.com/anomalyco/opencode
- pi:
  - https://github.com/earendil-works/pi (`packages/coding-agent/docs/cli.md`, `json.md`, `rpc-commands.md`, `models.md`, `security.md`)
  - https://github.com/svkozak/pi-acp
- Goose:
  - https://github.com/aaif-goose/goose (`documentation/docs/guides/goose-cli-commands.md`)
  - `documentation/docs/guides/acp-providers.md`
- Strands:
  - https://github.com/strands-agents/harness-sdk (`strands-cli/README.md`, `strands-py/src/strands/multiagent`)
  - `strands-py/src/strands/experimental/checkpoint/__init__.py`
- https://github.com/a2aproject/A2A
