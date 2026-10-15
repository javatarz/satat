# Research: full-platform candidates for Satat

Status: scratch, 2026-10-15. Scope: the "full platforms" group. Measured against
`docs/research/v2/platform-requirements.md`: the 11 non-negotiables (NN1–NN11) act as hard
filters, and the "How candidates will be judged" rubric gives the score. Out of scope:
GitHub Actions + ARC, Argo Workflows, Hermes Agent.

Conventions: **UNVERIFIED** marks a claim not confirmed against a primary source. Star counts
and dates come from the GitHub API on 2026-10-15. "≥100 commits/30d" means the API page cap
was hit.

## Summary table

| Candidate | What it is | Per-task isolation (runtimes) | Drives Claude Code / opencode / pi | Queue + caps + control plane | Workflows in git | License (SPDX) | Activity | Verdict |
|---|---|---|---|---|---|---|---|---|
| **OpenHands** (Agent Canvas + agent-server + automation, OSS) | Coding-agent platform: UI, agent-server, cron/webhook automations | **New (2026-09-16):** `OH_CONVERSATION_RUNTIME=docker` gives one hardened container per conversation on one Docker host. K8s via the separate MIT `sandbox-server` + kubernetes-sigs/agent-sandbox. No ECS/Cloud Run/ACA driver. | Yes, all three over ACP (pi and opencode only on self-hosted backends) | Global run cap only (`max_concurrent_runs`, over-cap create returns **429, not queued**). Automation runs queue as PENDING. No per-lane/per-repo caps and no backlog view. | Partly: one `automation.yaml` per automation through Git Sync. No multi-step graph and no shared-workflow reference. | MIT (Canvas, SDK, agent-server, automation, sandbox-server) | Very active: Canvas v1.27.1 (2026-10-12), SDK v1.55.0 (2026-10-15), ~91k stars | **Partial pass.** Best fit as the **harness adapter + workload runtime** layer; the scheduler, lanes and backlog control plane must be built |
| **OpenHands Enterprise** (reference) | Commercial self-hosted OpenHands with auth/RBAC/multi-tenancy/runtime-api | Per-run pods via closed `runtime-api` service | Claude Code, Codex, Gemini on Cloud (pi and opencode hidden) | Multi-user, budgets | Same as OSS | **PolyForm-Free-Trial-1.0.0** (30 days/yr) | Active | **Fail NN11** (not redistributable in a public recipe) |
| **Multica** | Kanban-style issue board where agents are assignees. A daemon on each machine runs agent CLIs. | **No.** Per-run *working directory* on the daemon host. "Treat every run as unsandboxed." Daemons can be placed in containers/VMs. | Yes: 26 CLIs including Claude Code, OpenCode, Pi | Yes: queue, per-daemon (20) and per-agent (6) caps, retries, board, live steering | No: runbooks/skills live in the DB, not git | Custom "Multica License" (Apache-2.0 + extra conditions). **Not OSI.** | Very active: v0.6.2 (2026-10-13), ~53k stars | **Fail NN1, NN6, NN7, NN11 (OSI bar).** Best ready-made control-plane UX, but the wrong isolation model |
| **Coder Tasks + AgentAPI** | Tasks ran CLIs inside Coder workspaces via AgentAPI | Workspaces via Terraform templates (Docker/K8s/ECS/VMs) | **Tasks removed; AgentAPI deprecated (repo archived).** Successor "Coder Agents" runs Coder's own Go agent loop, not Claude Code. | Community: 5 concurrent agents, queued. Premium: metered Agent Time reported to Coder. | No | AGPL-3.0 core; `enterprise/` under a proprietary licence | Active (v2.38.0, 2026-10-06), ~17k stars | **Fail NN2** (as orchestrator). Possible but heavy as a runtime substrate only |
| **Vibe Kanban** | Local kanban + worktree UI for agent CLIs | Git worktrees on the host | Claude Code, OpenCode and others (no pi listed) | Local UI only | No | Apache-2.0 | **Sunsetting** (company shut down 2026-04-10). Last release v0.1.44, 2026-04-24. | **Fail** (NN1, NN4, NN5, NN8; maintenance) |
| **AgentBox** (madarco/agentbox; name is ambiguous, see below) | Developer CLI that "teleports" a repo into a sandbox box and launches an agent | One container/VM per box: local Docker, remote Docker over SSH, Hetzner, DO, Daytona, Vercel, E2B; provider plugin SDK | Claude Code, Codex, OpenCode, Pi | Basic `agentbox queue`. Interactive dashboard/tray app, no server control plane. | `agentbox.yaml` (environment setup, not workflows) | MIT | Active, v0.4.3 (2026-09-21), ~540 stars | **Fail** as a platform. Its provider abstraction is a useful reference for a runtime driver. |
| **claw-orchestrator** (Enderfga) | Headless runtime wrapping CLIs as persistent sessions, plus a durable workflow kernel | Git worktrees on one host. No container per task. | Claude Code, Codex, OpenCode, Antigravity, Grok, custom CLI (pi only as a custom engine, UNVERIFIED) | Session cap, spend caps, run ledger, small dashboard | Declarative WorkflowSpec with `human_gate`, retries, timeouts (JSON, stored per run) | MIT | Active, v7.11.0 (2026-10-11), ~590 stars, single maintainer | **Fail NN1, NN3, NN4, NN9.** Workflow kernel worth studying |
| **OpenAI Symphony** (addition) | Spec + Elixir reference: tracker-polling scheduler, per-issue workspace, in-repo `WORKFLOW.md` | Per-issue directory on the host. Sandboxing is out of scope. | Codex app-server only | `max_concurrent_agents` (global + per state) | **Yes:** `WORKFLOW.md` in the repo | Apache-2.0 | "Engineering preview", nightly only, last push 2026-09-15, ~28k stars | **Fail NN1, NN2, NN4.** Design reference for the workflows-as-code and intake layers |
| **Orchestrator.inc agent-orchestrator** (addition) | Desktop app + local daemon supervising many agent CLIs, kanban from PR/CI state | Worktrees per worker (local) | "Claude Code, Codex, +25 more" (pi UNVERIFIED) | Kanban, but on the desktop | No (UNVERIFIED) | Apache-2.0 | Very active, v0.14.2 (2026-10-15), ~13k stars | **Fail NN1, NN4 (desktop), NN9** |
| **Open-Inspect / background-agents** (addition) | Ramp-Inspect-style hosted background agent | Per-session cloud sandbox (Modal, Daytona, E2B, OpenComputer). No local Docker backend listed. | OpenCode and Claude Agent harness (Claude subscription supported). Pi not listed. | Web UI, Slack/GitHub/Linear intake, automations | No | MIT | Very active, ~3.4k stars | **Fail NN9, NN11:** the control plane *requires* Cloudflare Workers/Durable Objects |

**Bottom line:** no full platform passes all 11 non-negotiables. OpenHands OSS comes closest at
the *execution* layers. It is MIT, has per-conversation Docker isolation shipped in
2026-09, has K8s through sandbox-server/agent-sandbox, and drives all three harnesses over
ACP. It does not give Satat the scheduler, lane caps, workflows-as-code or a backlog control
plane. Multica has the best control-plane and queue UX but fails isolation and the OSI bar.

---

## 1. OpenHands (open source)

### What it is (as of 2026-10)

The `OpenHands/OpenHands` repo now **is Agent Canvas**. The old monorepo was split:
`OpenHands/legacy` (archived snapshot), `OpenHands/enterprise` (proprietary backend),
`OpenHands/sandbox-server` (the extracted `app_server`), and Canvas history replayed into
`OpenHands/OpenHands`. Sources: tracker issue OpenHands/OpenHands#15396; `OpenHands/agent-canvas`
is archived and says "moved to OpenHands/OpenHands".

Components:

- **Agent Canvas** (TS UI + launcher; MIT), v1.27.1, 2026-10-12.
- **agent-server** (in `OpenHands/software-agent-sdk`, MIT), SDK v1.55.0, 2026-10-15. It runs conversations.
- **automation service** (`OpenHands/automation`, MIT, "Beta"), v1.20.0, 2026-10-15. It handles cron, webhooks, run history, dispatch and Git Sync.
- **sandbox-server** (`OpenHands/sandbox-server`, MIT outside an `enterprise/` carve-out, which this repo does not contain). It is the multi-sandbox app server with Docker, Kubernetes, Remote and Process sandbox services. 4 stars, no releases, last commit 2026-09-28.

### Layers covered

| Layer | Native? | Notes |
|---|---|---|
| Intake | Partial | Automations take cron and webhooks (GitHub events through webhook filters). There is no backlog sync from issues as work items. |
| Queue/scheduler | Weak | agent-server `max_concurrent_runs` (default 10) rejects over-cap creates with HTTP 429 "Conversation run limit reached. Retry the request later." Automation runs sit in `PENDING` until the dispatcher picks them up. There are no per-lane or per-repo caps and no reprioritisation. |
| Workflow engine | No | An automation is one prompt/script (tarball plus bash chain) per run. There are no multi-step graphs, per-step harness choice or human gates. |
| Harness adapter | **Yes** | Built-in OpenHands agent, plus ACP: Claude Code (`claude-agent-acp`), Codex, Gemini CLI, Pi (`pi-acp`), OpenCode (`opencode acp`). |
| Workload runtime driver | **Partial, improving** | agent-server: `local` or `docker` (`Literal["local","docker"]`, no plugin point). sandbox-server: Docker, Kubernetes (agent-sandbox), Remote (runtime API), Process. SDK workspaces: Docker, Apptainer, APIRemote, AgentSandbox (K8s), Cloud. |
| Model access | Yes | LLM profiles; ACP agents bring their own credentials. |
| Control plane | Partial | Canvas shows conversations, automations and runs (cancel, "Run now"). It has no queue or backlog view, no lane view, and no reprioritisation. |
| Cross-cutting | Partial | Encrypted secrets store, OTel tracing in the SDK (`acp_tracing.py`). Notifications are not native. |

### Per-conversation isolation: what exists now

1. **agent-server Docker runtime (OSS, MIT, shipped 2026-09-16, PR #3403).**
   `openhands-agent-server/openhands/agent_server/docker_runtime/registry.py` is described as
   "Own one hardened agent-server container per conversation". The parent agent-server shells
   out to `docker run -d --rm --user <uid> --cap-drop ALL --security-opt no-new-privileges
   --ulimit core=0 [--memory/--cpus/--pids-limit]` with three bind mounts per conversation
   (conversation dir, persistence/HOME, `/workspace`). It publishes only on `127.0.0.1` and
   proxies `/api/conversations/{id}/...` into the container. The child gets a per-conversation
   session key and encryption key. `HOME` is the per-conversation persistence dir, so
   **host CLI logins are not visible inside**. Later work added runtime archive, a disk budget
   and cache reclaim (PRs #5263, #5407, #5429, 2026-10-06).
   Caveats:
   - The parent needs the Docker CLI and socket. The children do not get it.
   - It is single-host only.
   - `/api/bash` "still runs on the outer host" in docker mode (agent-server feature map F13),
     and automation runs execute their setup bash chain through `/api/bash`. So **automation
     scripts are not isolated** unless they start a conversation that itself runs in a
     container. That second part is UNVERIFIED end-to-end.
   - ACP model discovery returns 501 in docker mode (`acp_router.py`). It is UNVERIFIED that
     ACP conversations run correctly inside the per-conversation container. The design implies
     they do, because the child is a `local`-mode agent-server that spawns the ACP subprocess.
2. **sandbox-server Kubernetes backend (MIT, merged 2026-09-24).**
   `kubernetes_sandbox_service.py`: each sandbox is a `SandboxClaim` against
   kubernetes-sigs/agent-sandbox (Apache-2.0, v0.5.x documented; repo latest v1.0.6). Pod-level
   policy lives in the cluster `SandboxTemplate`: image, CPU and memory, `runtimeClassName`
   (gVisor/Kata), NetworkPolicy. `max_num_sandboxes` defaults to 10, and the oldest sandboxes
   are *paused* beyond that, not queued. Known limitation: warm pools are never used because
   env injection forces cold starts (`deploy/kubernetes/README.md`). Whether Canvas works
   against a self-hosted sandbox-server is UNVERIFIED. Its README says to use Agent Canvas as
   the web client, but Canvas's "Cloud" backend tab targets an OpenHands Cloud host.
3. **OSS Helm chart** (`helm/agent-canvas`): an experimental single-replica StatefulSet with one
   PVC holding `~/.openhands` and `~/workspace`. Its README says: "all agents are comingled on
   the same pod and PVC", "unauthenticated, single-tenant", and per-run isolated sandboxes are
   an OHE feature. The chart does **not** use the agent-sandbox path. RBAC is off by default
   (opt-in namespace `admin` or `cluster-admin`).

So the requirements doc's premise ("per-run containers are an OpenHands Enterprise
feature") is **out of date for Docker**. It still holds for the turnkey Helm/Canvas K8s path.

### The "remote runtime API": what implementing it takes

- **Spec status:** no public OpenAPI spec found. The server image is
  `ghcr.io/openhands/runtime-api`, deployed by the `runtime-api` subchart in
  `OpenHands/OpenHands-Cloud` (PolyForm Free Trial). No public source repo was found in the
  OpenHands org. Whether a private one exists is UNVERIFIED. The contract has to be
  reverse-engineered from two MIT clients: `APIRemoteWorkspace`
  (`openhands-workspace/openhands/workspace/remote_api/workspace.py`) and
  `RemoteSandboxService` (`sandbox-server/openhands/app_server/sandbox/remote_sandbox_service.py`).
- **Endpoints used (header `X-API-Key`):**
  - `POST /start`, body `{image, command, working_dir, environment, session_id, run_as_user,
    run_as_group?, fs_group, image_pull_policy?, runtime_class? ("sysbox-runc"),
    resource_factor? (1|2|4|8)}`, returns `{runtime_id|id, url, session_api_key, ...}`.
  - `GET /sessions/{session_id}`, returns `{pod_status: pending|running|ready|failed|unknown|crashloopbackoff|"not found", container_statuses, runtime_id, url, session_api_key}`.
  - `GET /sessions/batch?ids=...`, returns a list keyed by `session_id`.
  - `GET /list`, returns `{runtimes: [{session_id, ...}]}`.
  - `POST /pause`, `POST /resume` (issues a new `session_api_key`), `POST /stop`, each with
    body `{runtime_id}`.
  - The returned `url` must route to the agent-server inside the workload (port 60000 in the
    SDK client). Per-sandbox host/path routing is in `RemoteSandboxService` URL helpers.
- **Effort estimate (UNVERIFIED):** a small service (~1–2k LOC) mapping these seven calls onto
  Docker, K8s, ECS/Fargate, Cloud Run jobs or ACA would give OpenHands a pluggable runtime.
  It would also make that service Satat's NN1 runtime-driver plugin point, since sandbox-server
  already has a `RemoteSandboxService` client for it. Pause/resume semantics on Cloud Run or
  ACA jobs would need emulation (stop plus volume snapshot).

### Driving Claude Code, opencode and pi (ACP)

- The agent-server spawns the ACP CLI as a subprocess and relays turns over JSON-RPC/stdio.
  Providers are defined in `openhands.sdk.settings.acp_providers`, with commands
  `npx -y @agentclientprotocol/claude-agent-acp`, `npx -y opencode-ai acp`, and
  `npx ... pi-acp` (`docs/ACP_AGENTS.md`).
- Pi and OpenCode are offered only on self-hosted/local backends, not OpenHands Cloud. They are
  not pre-installed, so the first run downloads them via `npx`.
- Credentials: subscription logins are auto-detected from `~/.claude/.credentials.json` and
  similar. When `CLAUDE_CODE_OAUTH_TOKEN` is present, the SDK *strips* `ANTHROPIC_API_KEY` and
  `ANTHROPIC_BASE_URL` for Claude Code (`acp_providers.py` ~L585). Pi reads the
  `ANTHROPIC_API_KEY` secret slot or its own `models.json`, and ignores `ANTHROPIC_BASE_URL`.
- **Credential lanes (NN3):**
  - In single-process/Helm mode this **fails**: every harness shares one HOME, so pi or
    opencode can read Claude's credential file.
  - In docker-runtime mode each conversation gets its own HOME, and secrets are injected per
    conversation. That is closer, but the secret store is per backend and selection is
    configuration, not infrastructure.
  - Infrastructure-level enforcement is achievable by running **two backends (two
    agent-servers or sandbox-server specs)**: a Claude lane backend holding only the OAuth
    token, and an open lane backend holding only the gateway key. The workload gets only its
    lane's secrets. This needs to be built (routing by lane) on top.
- Per-conversation sandbox for ACP agents: implied in docker mode (see the caveat above).
  Not available in the OSS Helm chart.

### Automations, Git Sync, concurrency, queue view

- The automation service owns definitions, cron, webhooks (GitHub dedupe by delivery),
  run history, dispatch, and "sandbox lifecycle orchestration". Runs go
  `PENDING → RUNNING → COMPLETED/FAILED/CANCELLED`. Cancelling a RUNNING run in local mode
  leaves the bash chain running (feature map F07).
- **Git Sync** is bidirectional, through `openhands/automation/git_sync/*`, with failure
  backoff (migration 029). This is what Satat uses today with `satat-automations`.
- Concurrency:
  - agent-server global `max_concurrent_runs` (default 10, 429 over cap).
  - Per-automation `max_concurrent_runs` is referenced in `docs/kv-store-client-guide.md`;
    its semantics are UNVERIFIED.
  - sandbox-server `max_num_sandboxes` pauses the oldest sandboxes.
  - None of these are lane- or repo-aware, and the 429 path does not queue.
- Queue view: run lists per automation (`GET /v1/{id}/runs` with `status_counts`). There is no
  cross-automation backlog or "waiting on review" state.

### Workflows in git

`automation.yaml` per automation, synced from a git repo. One automation is one prompt/preset
with a trigger. There are no step graphs, no per-step harness and no shared-workflow
reference from repos. **NN6/NN7: fail** (Satat would supply this layer).

### Operational weight

The all-in-one image (Canvas + agent-server + automation, SQLite) runs today on Satat's single
VM. Docker runtime mode adds one agent-server container per live conversation, with
idle-stop and retention reclaim. The K8s path adds the agent-sandbox controller plus
sandbox-server plus Postgres (UNVERIFIED sizing).

### Pass/fail against the non-negotiables (OSS OpenHands as-is)

| NN | Result | Why |
|---|---|---|
| 1 Pluggable workload runtime | **Partial** | Docker per conversation (agent-server). K8s via sandbox-server. ECS/Cloud Run/ACA only by building a runtime-API server. Automation bash runs on the host. |
| 2 Pluggable harness | **Pass** (adapter level) | ACP covers all three. A new harness means an ACP adapter plus a provider entry upstream in the SDK. Harness is per backend/conversation; per-*step* choice needs a workflow layer. |
| 3 Credential lanes by infra | **Fail as shipped / Pass if built** | Needs separate backends per lane, or a runtime driver that injects lane-scoped secrets. |
| 4 Control plane | **Partial** | Conversations and runs, cancel, live chat. No backlog states, lanes, reprioritise or pause-queue. |
| 5 Concurrency limits (global/lane/repo, queue never drops, backoff) | **Fail** | One global cap, over-cap rejects with 429, no lane or repo caps, no subscription-window backoff. |
| 6 Workflows as code | **Fail** | Single-step automations. |
| 7 Repos opt in to shared workflows | **Fail** | |
| 8 Triggers (cron, webhook, manual) | **Pass** | |
| 9 Every infra dependency a plugin | **Partial** | Runtime: Docker/K8s/remote. State: SQLite or Postgres (automation). No tracker, notification or secret-store plugin points. |
| 10 Security posture | **Partial** | Docker mode: cap-drop ALL, no-new-privileges, no socket in children, loopback ports. No egress restriction in the OSS Docker runtime (K8s can add NetworkPolicy). |
| 11 Self-hosted, licence allows public recipe | **Pass** | MIT throughout the OSS stack. |

**Verdict:** adopt OpenHands as the **harness adapter + workload runtime** layer (ACP plus
per-conversation containers), not as the whole platform. Satat would build intake, the
scheduler (lanes, per-repo caps, backoff), workflows-as-code and the backlog control plane,
calling agent-server/sandbox-server through their APIs.

Under the open questions:

- **Build vs adopt.** If the scheduler and control plane must come from an existing project,
  OpenHands alone fails. If building them is acceptable, it is the strongest execution layer
  in this group.
- **Live steering = required.** OpenHands is strong here (chat into a running conversation).
- **Tenancy = multi-user.** OSS is explicitly single-tenant, which pushes towards OHE (fails
  the licence bar).
- **Merge authority.** Neutral; the workflow layer decides.
- **Extension language.** Python (SDK, agent-server, automation) plus TS (Canvas).

---

## 2. OpenHands Enterprise (reference only)

- Licence: **PolyForm Free Trial License 1.0.0** for `OpenHands/enterprise` and
  `OpenHands/OpenHands-Cloud` (Helm charts). "Usage is limited to 30 days per calendar year
  without a commercial license."
- Gated features (OSS Helm README "Relationship to OpenHands Enterprise", and the enterprise
  README):
  - SSO/SAML/OIDC authentication.
  - RBAC.
  - Multi-tenancy.
  - Isolated per-run agent sandboxes. The `runtime-api` subchart creates sandbox pods, with
    HPA, cleanup cron jobs, `runtime_class` sysbox.
  - Slack, Jira, GitHub, GitLab and Bitbucket integrations.
  - "Unlimited parallel agent conversations".
  - Budgets and usage monitoring.
  - Install is preferred via Replicated.
- **Fails NN11** under both licence bars: it is not redistributable as a public "deploy your
  own" recipe beyond a trial. It is still useful as the reference design for the runtime-API
  contract above.

---

## 3. Multica

- **What:** a "source-available workspace where you assign work to AI coding agents".
  Agents are board assignees. A per-machine **daemon** claims runs and invokes local CLIs.
  The stack is a Go server, a TS web/desktop/mobile frontend, and Postgres, deployed with
  Docker Compose or Helm (`deploy/helm/multica`: backend, frontend, Postgres, ingress; no
  daemon chart).
- **Layers:**
  - Intake: its own issue tracker. Autopilots (cron, webhook with GitHub header and dedupe
    support, manual "Run now"). GitHub/GitLab/Gitea/Forgejo integration only *links PRs* and
    mirrors CI; "it never pushes commits, comments, or status checks". GitHub Issues are not a
    native intake source; a webhook autopilot can create Multica issues from GitHub events.
  - Queue/scheduler: yes. Runs move `deferred → queued → running`. Queued work never expires
    while the runtime heartbeats. Automatic retries and timeouts.
  - Workflow engine: no graph. "Squads" route work between agents, and a runbook per autopilot.
  - Harness adapter: yes, 26 CLIs including `claude`, `opencode`, `pi`. Custom runtime
    profiles allow wrapper commands within a supported protocol family.
  - Runtime driver: **none.** The daemon runs on a machine.
  - Control plane: strong. Board, execution log replay, usage/cost per run, inbox, roles,
    workspaces, CLI/API. **Live steering** into a running Claude Code/Codex/Grok run.
- **Isolation:** "Multica makes no filesystem-sandbox guarantee… Treat every run as
  unsandboxed and put the boundary outside the daemon." Each run gets its own workdir under
  `~/multica_workspaces/`. Claude Code runs with `--permission-mode bypassPermissions`. Child
  processes inherit `MULTICA_TOKEN`. Recommended boundaries are a dedicated user, a container
  or a VM *for the daemon*, not per run (`security-model.mdx`, `daemon-runtimes.mdx`).
- **Concurrency:** daemon default 20 (`MULTICA_DAEMON_MAX_CONCURRENT_TASKS`) and 6 per agent.
  Effective cap is the minimum of the two. Over-cap runs queue. There are no per-repo caps and
  no subscription-window backoff (UNVERIFIED beyond the docs read).
- **Credential lanes:** achievable by infrastructure only at daemon granularity. Run one
  daemon container with Claude Code plus the OAuth token, and another with opencode/pi plus
  the gateway key, each registered as separate runtimes. Caveat: agent custom env vars are
  stored server-side and sent to whichever runtime runs the agent, so an operator can misroute
  a secret.
- **Workflows in git:** no. Agents, skills, runbooks and autopilots live in the database.
- **Licence:** `LICENSE` is the "Multica License": Apache-2.0 plus Part I conditions. The
  GitHub API reports `NOASSERTION`.
  - (a) No hosted service for third parties without a commercial licence, "even when it is
    offered free of charge". Internal use within one organisation is fine. Publishing source
    or a fork is allowed.
  - (b) The Multica logo, name and copyright must stay in any UI.
  - (c) Attribution is required for backend-only use.
  - **Not OSI-approved.**
- **Maintenance:** v0.6.2 (2026-10-13), ≥100 commits in 30 days, ~52.9k stars. Pre-1.0.
  Telemetry is on by default (`DO_NOT_TRACK=1`).
- **Ops weight:** Go API + Next.js frontend + Postgres + daemon(s). Fine on a small VM
  (sizing UNVERIFIED).

| NN | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Multica | Fail | Pass | Partial (daemon-level) | Pass | Partial (no lane/repo caps) | Fail | Fail | Pass | Fail | Fail (unsandboxed runs) | OSI: Fail / source-available: Pass |

**Verdict:** reject as the platform. Isolation (NN1/NN10) and workflows-as-code (NN6/NN7) are
architectural gaps, not missing adapters.

Under the open questions:

- **Licence bar = source-available OK.** A public Satat recipe that has each operator
  self-host Multica internally is permitted, but the branding conditions apply.
- **Tenancy = multi-user.** It is the best in this group (workspaces, roles, scopes).
- **Live steering.** Strong.
- **Tracker scope.** Its own tracker. GitHub/GitLab only for PR linkage.
- **Build vs adopt.** If the control plane must be adopted, Multica is the only candidate here
  with a production-grade one, but it would need a per-run container wrapper via a custom
  runtime profile. That approach is UNVERIFIED and fights its "workdir on host" design.

---

## 4. Coder Tasks + AgentAPI

- **Status change:** "Coder Tasks was deprecated in Coder v2.34 and has been removed,
  including the Tasks API (`/api/v2/tasks`)" (`docs/ai-coder/agents/tasks-to-chats-migration.md`).
  `coder/agentapi` is **archived**, and its README says it is "deprecated and no longer
  maintained" (last release v0.12.2, 2026-05-27).
- **Successor, Coder Agents:** "includes its own self-hosted AI coding agent that runs the
  agent loop directly within the Coder control plane… It is not a wrapper around third-party
  agent tools like Claude Code or Codex." Workspaces are only where tool calls execute.
  "Agent Relay" (early access, closed preview) connects *cloud-hosted* Claude Code/Cursor
  sessions to Coder workspaces. It is not self-hosted orchestration.
- **Isolation/runtimes:** strong. A workspace per task is provisioned from Terraform templates
  (Docker, Kubernetes, ECS, VMs on any cloud), and there is an "agent firewall"
  (nsjail/landjail) for Docker/ECS/K8s.
- **Concurrency:** Community licence allows "up to five concurrently active agents
  deployment-wide", with the rest queued. Premium sells "Agent Time", and usage is reported
  hourly to Coder's Tallyman (`docs/ai-coder/agents/licensing-usage.md`).
- **Licence:** `LICENSE` is AGPL-3.0. `LICENSE.enterprise` is a proprietary Coder licence for
  the `enterprise/` directory. Chat features have code under `enterprise/coderd/`; exactly
  which chat features need Premium is UNVERIFIED.
- **Maintenance:** v2.38.0 (2026-10-06), very active, ~17k stars.
- **Ops weight:** coderd + Postgres + provisioners. This is heavier than Satat's VM budget
  suggests (UNVERIFIED sizing).

| NN | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Coder Agents | Pass (workspaces) | **Fail** (own agent only) | Fail (LLM creds in control plane; no subscription lane) | Pass | Partial (5-agent community cap) | Fail | Fail | Partial (API) | Partial | Pass | AGPL Pass (OSI); enterprise parts not |

**Verdict:** reject as orchestrator (NN2). Coder workspaces could still serve as a *runtime
substrate* with Satat running CLIs in them through Registry modules, but that adds a full
Coder deployment just to get a Terraform-template runtime driver. It is not justified for
Satat's cheapest tier.

---

## 5. Vibe Kanban (BloopAI)

- Local kanban + worktree workspaces + diff review for 10+ agent CLIs: Claude Code, Codex,
  Gemini, Copilot, Amp, Cursor, OpenCode, Droid, CCR, Qwen. Pi is not listed.
- **Sunsetting:** "Today we're shutting down bloop, the company behind Vibe Kanban… The Vibe
  Kanban project will live on, open source and community maintained." (2026-04-10). Remote
  services were removed after 30 days, and it moved to a fully local architecture.
- Licence: Apache-2.0. Last release v0.1.44 (2026-04-24). 10 commits in 30 days. ~28k stars.
- Isolation is git worktrees on the host. There is no server control plane, queue or triggers.
- **Verdict:** fail (NN1, NN4, NN5, NN8, maintenance).

---

## 6. AgentBox

The name is **ambiguous**. Three unrelated GitHub projects match:

1. **madarco/agentbox** (most likely intended; MIT; ~540 stars; v0.4.3, 2026-09-21; created
   2026-05).
   - A developer CLI plus macOS tray app. It "teleports" a project into a box (a container
     with in-box dockerd, or a cloud VM) and launches Claude Code, Codex, OpenCode or Pi.
   - Providers: local Docker, remote Docker over SSH, Hetzner, DigitalOcean, Daytona, Vercel,
     E2B. There is a `@madarco/agentbox-provider-sdk` for third-party providers.
   - Git credentials stay on the host, and pushes go through a host relay. However, Claude/Codex
     auth is copied into the box ("carries… your Claude Code and Codex auth").
   - It has `agentbox queue` (start agents as boxes go idle). There is no server, API control
     plane, triggers or workflows. In-box dockerd means privileged-ish containers (UNVERIFIED
     flags).
   - Sources: README, https://agent-box.sh, `docs/architecture.md`.
2. **devilcoolyue/agentbox** (Apache-2.0; ~200 stars; v0.1.14, 2026-10-15; created
   2026-09-28). A self-hosted Go + SQLite + Docker *browser workspace* for Claude Code and
   Codex CLI. It has one container per workspace, account pools and credit limits.
   It is interactive, not backlog-driven. It supports neither opencode nor pi.
3. **fkeil/agentbox** (MIT; 5 stars). A single-box CLI/TUI with an egress allowlist; supports
   Claude Code, OpenCode, Pi and Codex.

**Verdict:** none of these is a Satat platform. All fail NN4, NN5, NN6, NN7 and NN8.
madarco/agentbox's provider-plugin model, and fkeil/agentbox's drop-by-default egress plus
YAML agent manifests, are reference designs for a Satat runtime driver.

---

## 7. claw-orchestrator (Enderfga/claw-orchestrator)

- A TypeScript runtime that wraps Claude Code (`stream-json`), Codex, OpenCode
  (`opencode run --format json`), Antigravity, Grok and custom CLIs as persistent sessions.
  It exposes them through a CLI, MCP server, ACP agent, an OpenAI-compatible endpoint and an
  OpenClaw plugin. Several GitHub forks exist (Seagull619, metahacker, WillieOng-HK); Enderfga
  is upstream.
- **Durable run kernel:** declarative `WorkflowSpec` with nodes `agent`, `fanout`, `council`,
  `verifier`, `human_gate`, `router`, `subflow`, `autoloop`. Every transition is checkpointed
  to `~/.claw-orchestrator/wf/<runId>/`. Execution is at-least-once, resume is explicit, and
  the kernel has retries and timeouts (`skills/references/workflow.md`).
- **Verification plane:** acceptance contracts that the runtime executes itself.
- **Spend caps and run ledger.**
- **Isolation:** git worktrees on one host. Docker is used only for `ultraapp` app deploys
  (`src/ultraapp/docker.ts`). No per-task container and no runtime plugins.
- **Concurrency:** `maxConcurrentSessions` (example 5). No lanes.
- Licence MIT. v7.11.0 (2026-10-11). 77 commits in 30 days. ~590 stars. Effectively a single
  maintainer.
- Pi: not a built-in engine; possible as a custom CLI engine (UNVERIFIED).
- **Verdict:** fail NN1, NN3, NN4 (dashboard is minimal), NN8 and NN9. Its workflow kernel
  (human gates, verifier nodes, checkpointed runs) is a good reference for Satat's workflow
  layer.

---

## 8. Additions (credible full platforms found)

The search surfaced many small GitHub-issue → PR orchestrators (orch, baton, drydock,
patchwork-agents, kalos, codeforge, orclaw; each 0–20 stars). They are omitted as not
credible on maturity.

### 8a. OpenAI Symphony (openai/symphony)

- Apache-2.0, ~27.7k stars, "low-key engineering preview for testing in trusted
  environments". Only a `nightly` pre-release; last push 2026-09-15.
- `SPEC.md` describes a tracker-polling scheduler (Linear adapter), a per-issue isolated
  *workspace directory*, an in-repo **`WORKFLOW.md`** (YAML front matter plus prompt) holding
  tracker config, hooks and runtime settings, and `max_concurrent_agents` (global and per
  tracker state).
- It requires the **Codex app-server** protocol. Sandboxing is an explicit non-goal.
- **Verdict:** fail NN1, NN2, NN4. A strong reference for intake plus "workflow file lives in
  the repo" (NN6/NN7 shape).

### 8b. Orchestrator.inc agent-orchestrator (OrchestratorInc/agent-orchestrator, formerly ComposioHQ)

- Apache-2.0, ~13k stars, v0.14.2 (2026-10-15).
- A desktop app plus local daemon. Each worker gets its own worktree/branch. It has a kanban
  derived from PR, CI and review state, and "any harness (Claude Code, codex, +25 more)".
- The description mentions "cloud agents"; the runtime plugins were not verified (UNVERIFIED).
- **Verdict:** fail NN1, NN4 (desktop-first) and NN9. Useful as a UX reference for a
  PR-state-derived board ("Needs you / In review / Ready to merge").

### 8c. Open-Inspect (ColeMurray/background-agents)

- MIT, ~3.4k stars, very active.
- Control plane on **Cloudflare Workers + Durable Objects + D1**. Per-session sandboxes on
  Modal, Daytona, E2B or OpenComputer.
- Harness: OpenCode or the Claude Agent harness (Claude subscription supported).
- Intake from Slack, GitHub, Linear and webhooks. Automations. Explicitly single-tenant.
- **Verdict:** fail NN9 (no Docker-only local install) and NN11 ("no managed control plane we
  cannot run ourselves"). It is architecturally the closest to the Satat target, but it is
  bound to Cloudflare.

---

## Cross-cutting conclusions for the build-vs-adopt question

1. **No candidate in this group passes NN1 + NN5 + NN6 + NN7 together.** Workflows-as-code
   with repo opt-in, and lane-aware queues, are absent everywhere except as single-file
   designs (Symphony `WORKFLOW.md`, claw-orchestrator `WorkflowSpec`).
2. **OpenHands OSS is the best execution substrate.** It has MIT licensing, ACP adapters for
   all three harnesses, per-conversation hardened Docker containers (new), a K8s path via
   sandbox-server/agent-sandbox, and a reverse-engineerable 7-endpoint runtime API that could
   become Satat's runtime-driver plugin point for ECS, Cloud Run and ACA.
3. **If the control plane must be adopted** (open question 2), Multica is the only mature
   option. Its isolation model and non-OSI licence make it a poor fit, and it would still need
   per-run container wrapping.

## Sources

OpenHands:
- https://github.com/OpenHands/OpenHands (README; `helm/agent-canvas/README.md`, `values.yaml`; `docs/ACP_AGENTS.md`; `.agents/skills/verify-openhands/references/feature-map/F25-backends-and-cloud.md`, `F26-runtime-variants.md`)
- https://github.com/OpenHands/OpenHands/issues/15396 (monorepo split)
- https://github.com/OpenHands/agent-canvas (archived, moved notice)
- https://github.com/OpenHands/software-agent-sdk: `openhands-agent-server/openhands/agent_server/docker_runtime/registry.py`; `acp_router.py`; `openhands-workspace/openhands/workspace/remote_api/workspace.py`; `openhands-workspace/openhands/workspace/agent_sandbox/workspace.py`; `openhands-sdk/openhands/sdk/settings/acp_providers.py`; `.agents/skills/verify-agent-server/references/feature-map/F13-bash.md`, `F31-persistence-and-limits.md`; commit history of `docker_runtime/registry.py` (PR #3403, 2026-09-16)
- https://github.com/OpenHands/sandbox-server: `README.md`, `LICENSE`, `openhands/app_server/sandbox/README.md`, `kubernetes_sandbox_service.py`, `remote_sandbox_service.py`, `deploy/kubernetes/README.md`
- https://github.com/OpenHands/automation: README, `LICENSE`, `.agents/skills/verify-automation/references/feature-map/F07-dispatch-and-runs.md`, `openhands/automation/git_sync/`, `docs/kv-store-client-guide.md`
- https://github.com/OpenHands/OpenHands-Cloud: README, `LICENSE`, `charts/openhands/charts/runtime-api/`
- https://github.com/OpenHands/enterprise: README, `LICENSE`
- https://github.com/kubernetes-sigs/agent-sandbox

Multica:
- https://github.com/multica-ai/multica: README, `LICENSE`, `deploy/helm/multica/`
- `apps/docs/content/docs/daemon-runtimes.mdx`, `security-model.mdx`, `tasks.mdx`, `autopilots.mdx`, `github-integration.mdx`

Coder:
- https://github.com/coder/coder: `LICENSE`, `LICENSE.enterprise`
- `docs/ai-coder/agents/tasks-to-chats-migration.md`, `docs/ai-coder/agents/index.md`, `docs/ai-coder/agents/licensing-usage.md`, `docs/ai-coder/agent-relay/index.md`, `docs/ai-coder/agent-firewall/`
- https://github.com/coder/agentapi (README deprecation notice; archived)

Vibe Kanban:
- https://github.com/BloopAI/vibe-kanban
- https://www.vibekanban.com/blog/shutdown

AgentBox:
- https://github.com/madarco/agentbox
- https://agent-box.sh/
- https://agent-box.sh/docs/local-docker
- https://github.com/madarco/agentbox/blob/main/docs/architecture.md
- https://github.com/devilcoolyue/agentbox
- https://github.com/fkeil/agentbox

claw-orchestrator:
- https://github.com/Enderfga/claw-orchestrator (README, `CLAUDE.md`, `skills/references/workflow.md`, `src/ultraapp/docker.ts`)

Additions:
- https://github.com/openai/symphony (README, `SPEC.md`)
- https://github.com/OrchestratorInc/agent-orchestrator
- https://github.com/ColeMurray/background-agents

Repo metadata (stars, licence SPDX, releases, 30-day commit counts) comes from `gh api repos/...` and `gh release list` on 2026-10-15.
