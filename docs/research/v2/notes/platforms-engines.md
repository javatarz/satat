# Platform research: durable workflow / queue engines with UIs

Status: scratch research, 2026-10-15. Input to the platform-candidates evaluation against
`docs/research/v2/platform-requirements.md`. Claims carry a source number (see Sources at the
end). Anything not checked against a primary source is marked **UNVERIFIED**.

Scope: engines that would supply the **queue/scheduler + workflow engine + part of the
control plane**. In every case Satat still builds the harness adapters (Claude Code,
opencode, pi), the workload runtime drivers, intake from GitHub, and the credential-lane
wiring. None of these engines knows what a "work item" or "repo" is; that view would be
derived from run metadata or built.

## Summary table

| Engine | License (SPDX, LICENSE file) | Latest release | Layers native | Per-task container launch | Declarative files in git | 3-level caps (global / lane / repo) native? | UI: pause / cancel / retry / reprioritise | Single-VM weight | NN11 (OSI bar) | Verdict |
|---|---|---|---|---|---|---|---|---|---|---|
| **Hatchet** | MIT [H1] | v0.110.5, 2026-10-06 [G] | Queue, scheduler, durable engine, run UI, cron, webhooks, events | No. We write a task that calls Docker/K8s/ECS/Cloud Run/ACA APIs | No (code: Py/TS/Go/Ruby). Build a YAML interpreter | **Yes**: chained concurrency keys (CEL), shared across workflows, dynamic `max_runs` [H2] | Pause workflow, cancel, replay, bulk ops; priority set at trigger, no reprioritise seen [H3][H4][H5] | Postgres + one `hatchet-lite` container [H6] | Pass | **Best fit for queue + caps** |
| **Temporal** | MIT [T0] | Server v1.32.1, 2026-10-08; UI v2.56.0, 2026-10-13 [G] | Durable engine, task queues, schedules, signals/updates, UI | No. Activity calls runtime API | No (code, 8 SDKs). Build DSL interpreter | **Partly**: per-worker slots, queue RPS limit, priority (1-5), fairness keys. No keyed concurrency cap [T1][T2] | Workflow pause (pre-release), cancel, terminate, reset, signal; activity pause [T3][T4] | Server + Postgres + UI; ES optional since 1.20 [T5] | Pass | Strong engine, but per-repo/lane caps must be built |
| **Kestra** | Apache-2.0 [G] (EE proprietary) | v2.0.6, 2026-10-13 (2.0.0 on 2026-09-07) [G] | Engine, scheduler, triggers, UI, YAML flows | **Docker runner OSS**; K8s/AWS Batch/Cloud Run/Azure Batch runners **EE** [K1]. OSS `PodCreate` and Azure Batch job tasks exist [K6] | **Yes**, YAML flows | Flow / namespace / tenant limits, QUEUE behaviour [K2]. No input-keyed limit | Pause task (approval), kill, replay, restart; maintenance mode + kill switch are EE [K5] | JVM, ≥4 GiB RAM + 2 vCPU, Postgres [K3] | Pass for OSS core; cloud runners fail | Best declarative fit; cloud runtimes gated |
| **Restate** | `BUSL-1.1` (GitHub reports NOASSERTION) [R1] | v1.8.0, 2026-10-14 [G] | Durable engine, built-in UI, invocation admin API | No. Handler calls runtime API | No (code) | **Yes, experimental**: scope + 2-level limit keys, rule book [R2] | Cancel, kill, pause, restart-as-new invocation [R3] | Single binary, embedded storage, no external DB [R4] | **Fail** (BSL); pass if source-available OK | Elegant and light, license + experimental flow control |
| **Inngest** | SSPL-1.0 with Apache-2.0 after 3 years [I1] | v1.46.0, 2026-10-06 [G] | Queue, flow control, durable steps, UI | No. Step calls runtime API | No (code) | Concurrency keys + account scope, **max 2 limits per function** [I2] | Cancel, replay **UNVERIFIED** for self-host UI | Single binary, SQLite + in-memory Redis default; Postgres/Redis external [I3] | **Fail** (SSPL not OSI); arguably fail even for source-available "public recipe" | Not recommended |
| **Windmill** | AGPLv3 + Apache-2.0 + proprietary; CE binary is proprietary-licensed [W1] | v1.835.0, 2026-10-14 [G] | Engine, UI, triggers, approvals, OpenFlow YAML | `# sandbox <image>` runs image under nsjail on the worker (OSS) [W4]; no K8s/ECS job launch natively | Yes (OpenFlow YAML + `wmill` CLI); Git Sync EE beyond 2 users [W3] | **Concurrency limits are EE** [W2] | Approvals OSS [W5]; cancel/restart in UI | Rust server + workers + Postgres | Pass only for self-built AGPL binary; CE image fails ("not modify or wrap") | Not recommended |
| **Trigger.dev** | Apache-2.0 [G] | v4.7.4, 2026-10-14 [G] | Queue, concurrency keys, UI, waitpoints, schedules, **per-run container** (Docker/K8s) | **Yes on Docker and K8s** via supervisor [D1]; no ECS/Cloud Run/ACA | No (TypeScript only) | Queue limit + `concurrencyKey` per queue [D2]; nested 3-level **UNVERIFIED** | Cancel/replay in dashboard **UNVERIFIED** | Heavy: webapp, Postgres, Redis, Electric, ClickHouse, registry, MinIO, S2, docker-socket-proxy [D3] | Pass | Interesting isolation, too heavy and TS-only |
| **Prefect 3** | Apache-2.0 [G] | 3.8.8, 2026-10-06 [G] | Scheduler, work pools, UI, automations, pause/suspend | **Yes**: Docker, K8s, ECS (incl. Fargate), Cloud Run v2, Azure Container Instances work pools [P1] | Partly: `prefect.yaml` deployments; flows are Python | Work pool / work queue / deployment limits + named global concurrency limits [P2] | Cancel, retry, pause/resume in UI (**UNVERIFIED** detail) | Server + SQLite or Postgres | Pass | **Worth a closer look**, see below |
| **Conductor OSS** | Apache-2.0 [G] | v3.32.5, 2026-09-25 [G] | Engine, JSON workflow defs, UI, HUMAN/WAIT tasks | No. Worker calls runtime API | **Yes** (JSON) | Per task-def `concurrentExecLimit`; rate limit Redis-only [C1] | **UNVERIFIED** | JVM + Postgres (or Redis + ES) [C2] | Pass | Declarative, but per-repo caps missing; heavier |
| **DBOS** | MIT (transact-py) [G] | 3.2.0, 2026-09-29 [G] | Library: durable workflows + Postgres queues | No | No (code) | Queue + worker concurrency, rate limits [B1] | Conductor UI is a **managed service** [B2] | Postgres only | Library MIT; UI fails NN11 | Library only; no self-hosted UI |
| **Flyte 2** | Apache-2.0 [G] | v2.0.51, 2026-10-08 [G] | K8s-native orchestration | K8s pods only | No (Python) | **UNVERIFIED** | **UNVERIFIED** | Requires Kubernetes | Pass | Fails NN9 (no Docker-only local) |
| **Dagger** | Apache-2.0 [G] | v0.21.10, 2026-09-30 [G] | Container pipeline engine, not a durable queue | Runs steps in containers via its engine | Code modules (8 SDKs) | None (no queue) | No control plane UI | Engine container | Pass | Not a workflow engine for this layer |

[G] = `gh api repos/<owner>/<repo>` and `.../releases/latest`, run 2026-10-15. All repos show
pushes on 2026-10-15, so all are actively maintained.

## Satat-specific requirement mapping

The three caps Satat needs: **global** (all tasks), **per lane** (Claude subscription lane
tightest; gateway lane), **per repo**. Plus: over-cap work queues and is never dropped, and
the Claude lane backs off on a usage-limit hit until the window resets.

### Hatchet

- **Layers native:** queue + scheduler (Postgres-backed), durable tasks and DAG workflows,
  cron, scheduled runs, events, incoming webhooks (GitHub cookbook exists) [H7], run
  dashboard with filters, cancel, replay, bulk cancel/replay [H4], workflow pause [H3],
  Prometheus metrics, OTel traces.
- **Workload launch:** no runtime drivers. A Hatchet task (in our worker) would call the
  Docker API, Kubernetes Jobs API, ECS `RunTask`, Cloud Run Jobs `run`, or ACA Jobs `start`,
  then poll/stream until exit. The worker holds the container-runtime credential; the agent
  container never does, which satisfies NN10 if workers and agent workloads are separate.
- **Workflows as code:** workflows are code (Python, TypeScript, Go, Ruby SDKs). Repo
  YAML would be read by a generic "interpreter" workflow we write.
- **Concurrency mapping:** strongest of the group. Concurrency keys are CEL expressions
  over input/metadata; a workflow can carry **multiple** strategies and "every limit in the
  chain applies at once"; shared strategies can span workflows; `max_runs` can be a CEL
  expression (engine ≥ 0.106.0) [H2]. So: `'global'` max N, `input.lane` max per lane,
  `input.repo` max per repo, all with `GROUP_ROUND_ROBIN` (queues, never cancels).
  Rate limits (static and dynamic keyed) exist and are visible in the dashboard [H8].
- **Never drop caveat:** queued tasks have a **schedule timeout, default 5 m**, after which
  they are cancelled [H9]. Satat must set it long (for example `720h`). Workflow pause
  requires a queue TTL (dashboard default 24 h) after which queued runs are cancelled [H3].
- **Claude lane back-off:** options are (a) pause a dedicated Claude-lane workflow via API
  until reset (TTL caveat above), (b) a static rate limit on the Claude-lane key adjusted
  via `put_rate_limit`, or (c) the Claude-step task sleeps durably until reset. (b)/(c)
  behaviour under a zero limit is **UNVERIFIED**.
- **Human gates:** durable event waits with CEL filters; events pushed via SDK or webhook
  [H10]. No documented wait timeout on that page.
- **Priority:** 1-3, set at trigger, only orders runs of the same workflow [H5]. Changing
  priority of a queued run: **UNVERIFIED** (not found).
- **License:** MIT for the whole repo [H1]. SSO, user groups, tenant tags are Hatchet Cloud
  "Custom plan" features [H11]; GitHub/Google OAuth is available. No `ee/` directory in
  the OSS tree.
- **Ops weight:** `hatchet-lite` (engine + API + UI in one image) plus Postgres; RabbitMQ
  optional [H6]. Docs call Lite "for development and low-volume use-cases" [H6]; Satat's
  volume is low. RAM footprint **UNVERIFIED**.
- **NN pass/fail (this layer):** NN4 partial (runs, not work items; no reprioritise),
  NN5 pass (with timeout settings), NN6 fail natively / pass with interpreter, NN8 pass,
  NN9 pass (Postgres local; Postgres managed in cloud), NN11 pass.
- **Verdict:** best candidate in this group for the queue + scheduler layer. Satat would
  build the YAML interpreter, runtime drivers, and a work-item view (or accept Hatchet's
  run view).

### Temporal

- **Layers native:** durable workflows, task queues, Schedules, Signals/Queries/Updates
  [T6], Web UI (separate binary) [T5], batch operations, CLI dev server.
- **Workload launch:** activity calls runtime API (same as Hatchet). Temporal "Serverless
  Workers" (Lambda, Cloud Run, AgentCore) run *workers*, not per-task sandboxes [T7].
- **Workflows as code:** 8 SDKs (Go, Java, TypeScript, Python, .NET, PHP, Ruby, Rust)
  [T8]. A DSL-interpreter workflow is a well-known pattern; we would write it.
- **Concurrency mapping:**
  - Global / per lane: separate task queue per lane; cap = workers x
    `MaxConcurrentActivityExecutionSize` slots, plus server-side `queue-rps-limit` [T1][T9].
  - Per repo: **no native cap**. Fairness keys give weighted round-robin dispatch, and
    docs state fairness "does not provide exact dispatch ratios, concurrency limits, or
    compute isolation" [T2]. Per-key limits are RPS, not concurrency [T1]. Per-repo caps
    need a semaphore/mutex workflow we build.
  - Priority keys 1-5 and fairness on by default / via `matching.enableFairness` on
    self-hosted [T1].
- **Never drop:** tasks persist in queues until a worker polls [T9]. Pass.
- **Claude lane back-off:** pause individual workflows (pre-release; server ≥ 1.30 with
  `frontend.WorkflowPauseEnabled`) [T3] or activities [T4]; no bulk/queue-level pause.
  A lane-gate workflow holding permits is the clean build.
- **Human gates:** Signals/Updates with durable timers. Pass.
- **UI:** list/filter by search attributes, cancel, terminate, reset, signal, pause
  (UI ≥ 2.47.2) [T3]. Reprioritise a running workflow: `update-options` documented for
  versioning overrides; priority change **UNVERIFIED**.
- **Ops weight:** Postgres (12+), MySQL, Cassandra; SQL advanced visibility since 1.20,
  so Elasticsearch is optional; SQLite is dev/test only [T5]. Server (frontend, history,
  matching, worker roles in one process is possible) + UI + Postgres. RAM **UNVERIFIED**.
- **NN:** NN4 partial, NN5 partial (repo/lane caps built), NN6 fail natively, NN8 pass
  (Schedules; webhook needs our receiver), NN9 pass, NN11 pass.
- **Verdict:** most mature durable engine with the widest SDK choice, but Satat would
  build more of the queue semantics than with Hatchet. Prefer if live steering (Updates)
  and long-lived reliability matter more than built-in keyed caps.

### Kestra

- **Layers native:** YAML flows, scheduler (Schedule trigger), Webhook and Flow triggers,
  Pause task for approvals, replay/restart, UI with execution list, logs, Concurrency tab
  [K2][K4].
- **Workload launch:** Docker task runner (OSS) gives a container per script task; the
  Kestra worker needs the Docker daemon (Docker-in-Docker requirement; Fargate not
  supported for the server) [K3]. Kubernetes, AWS Batch (ECS/Fargate), Google Cloud Run,
  Google Batch, Azure Batch task runners are **Enterprise** [K1]. OSS plugins still offer a
  Kubernetes `PodCreate` task and Azure Batch `job.Create` (Apache-2.0) [K6]; ECS and Cloud
  Run jobs would need a CLI step in a Docker container or a custom Java plugin.
  Azure Container Apps jobs: no runner found.
- **Workflows as code:** **native YAML**. A shared flow is a subflow; a per-repo flow in
  its own namespace can call it. GitOps sync from git exists (`version-control-cicd`)
  [K4]; exact OSS/EE split of SyncFlows **UNVERIFIED**.
- **Concurrency mapping:** limits at flow, namespace (including children) and tenant
  level with `QUEUE` (default), `CANCEL`, `FAIL` [K2]. Evaluated most-specific first; "When a
  more specific limit is reached, no slot is consumed at higher levels" [K2]. Mapping: tenant
  = global, a per-lane step flow = lane, a per-repo namespace = repo. Not keyed by input.
  Whether namespace/tenant limits are OSS: **UNVERIFIED** (page has no edition badge;
  multi-tenancy is EE [K5]). Paused (approval-waiting) executions **hold their slot** [K2].
- **Claude lane back-off:** Pause task with a duration, or retry with delay; maintenance
  mode (queue all new executions) is EE [K5].
- **License:** Apache-2.0; EE features listed in [K5] (SSO/RBAC, multi-tenancy, worker
  groups, cloud task runners, kill switch, maintenance mode). OSS auth is basic auth only.
- **Ops weight:** Java 25 runtime, Postgres or MySQL, internal storage (local, MinIO, S3,
  GCS, Azure Blob); standalone needs **≥ 4 GiB RAM and 2 vCPU** [K3].
- **NN:** NN1 pass on Docker, fail on cloud runtimes without EE or custom plugins; NN6
  pass; NN7 pass via subflows + namespace config; NN8 pass; NN9 partial; NN11 pass for OSS.
- **Verdict:** best match for "workflows as declarative files" and approval gates, but
  the cloud runtime layer is the paid layer and the VM floor is ~4 GiB.

### Restate

- **License:** Business Source License 1.1, change to Apache-2.0 four years after release;
  additional use grant bars a "Public Restate Platform Service" (third parties registering
  their own Restate services). Self-hosting for your own workflows, and platforms whose
  users do not call Restate APIs directly, are explicitly permitted [R1]. A public "deploy
  your own" recipe appears permitted, but it is not OSI.
- **Layers native:** durable handlers, virtual objects (per-key serial queues), workflows,
  awakeables/signals for human gates, Kafka ingress, built-in admin API (cancel, kill,
  pause, restart-as-new) [R3]. UI port not documented on the overview page; **UNVERIFIED**.
- **Cron:** no native cron; implement via durable timers / virtual objects [R5].
- **Concurrency mapping (experimental, opt-in):** scope + up to two limit-key levels
  (`scope/L1/L2`); an invocation is admitted only when all levels have a slot [R2]. Map
  scope=`satat`, L1=lane, L2=repo. But "A `*` limit is not a global pool": limits are per
  scope, so one scope per Satat install is needed for a global cap, and per-repo caps are
  nested under lane (repo across lanes is not capped) [R2]. Requires
  `experimental-enable-vqueues`; "configuration and APIs may change" [R2]. Priorities and
  rate limits are listed as planned [R2].
- **Ops weight:** single binary, embedded Raft metadata + RocksDB, persistent volume;
  S3/MinIO snapshots only for multi-node [R4]. Lightest of the group.
- **Verdict:** fails under an OSI-only bar. Under a source-available bar, attractive for
  footprint but flow control is experimental and no cron or work-item UI.

### Inngest (self-hosted)

- **License:** `LICENSE.md` is **SSPL v1** with a grant of Apache-2.0 on the third
  anniversary of each release [I1]. Not OSI. SSPL section 13 obligations make a public
  recipe legally awkward. Fails NN11 under the OSI bar; borderline under source-available.
- **Self-host:** `inngest start` single binary; SQLite + in-memory Redis by default, or
  external Postgres/Redis; the CLI help still labels it "[Beta]" [I3].
- **Concurrency:** step-level (not run-level) limits with keys and `fn`/`env`/`account`
  scope; "Each SDK's concurrency option supports up to two limits" [I2], so three levels do
  not fit natively. Throttling, priority, debounce, rate limit exist [I3].
- **Gates/triggers:** `waitForEvent`, `cancelOn`, cron triggers [I3]. Webhook transforms
  in self-host **UNVERIFIED**.
- **Verdict:** not recommended (license + 2-limit ceiling).

### Windmill

- **License:** mixed. Source is AGPLv3 (backend/frontend) with Apache-2.0 clients and
  OpenFlow spec, plus proprietary code behind the `enterprise` flag. The published CE
  Docker image "includes proprietary and non-public code" and may be redistributed "as is
  but not to ... modify or wrap" [W1]. A public recipe would have to build AGPL binaries
  from source.
- **Concurrency limits are Cloud/EE only** [W2]. Worker groups via `WORKER_TAGS` in CE,
  UI-managed groups EE [W6]. Git Sync free only up to 2 users in CE [W3].
- **Runtime:** workers run jobs under nsjail; `# sandbox <image>` runs a container image
  daemonless on the worker [W4]. Isolation is per job, but on the worker host, not a
  separate pod/cloud job.
- **Gates:** suspend/approval steps with resume URLs [W5]. Triggers: schedules, webhooks,
  many event sources [W7].
- **Verdict:** fails NN5 without EE and NN11 under OSI for the CE image. Not recommended.

### Prefect 3 (added candidate)

- Apache-2.0; 3.8.8 on 2026-10-06 [G]. Self-hosted server with UI; SQLite or Postgres.
- **Work pools launch per-run infrastructure natively:** Process, Docker, Kubernetes, ECS
  (EC2 and Fargate), Azure Container Instances, Cloud Run / Cloud Run v2, Vertex AI;
  push pools for ECS/ACI/Cloud Run without a worker [P1]. Azure Container *Apps* jobs not
  listed. This is the only OSS engine here with a native runtime plug-point across Docker,
  K8s and the clouds.
- **Concurrency:** work-pool and work-queue limits (queue priority), deployment
  concurrency limits, named global concurrency limits with leases, rate limits via slot
  decay, active/inactive toggle [P2]. A Claude-lane global limit can be set inactive/0 or a
  queue paused; per-repo via a named limit `repo:<name>` acquired in the flow.
- **Caveat:** the unit launched per run is a Prefect *flow run* inside the container,
  so the harness container image must include Prefect and the flow code (the flow would be
  our harness adapter). Webhooks are a Prefect Cloud feature [P3]; self-host needs our own
  receiver. Python only for extensions.
- **Verdict:** worth a deeper pass by whoever evaluates "runtime-pluggable orchestrators";
  closest OSS match to NN1 + NN5 + NN9 in this group. **Most claims here beyond [P1]-[P3]
  are UNVERIFIED.**

### Trigger.dev (added candidate)

- Apache-2.0; v4.7.4 on 2026-10-14 [G]. Each run executes in its own container launched
  by the supervisor (Docker via docker-socket-proxy, or Kubernetes) [D1][D3].
- Queues with `concurrencyLimit`, per-key queue copies via `concurrencyKey` [D2].
  Self-hosted lacks checkpoints, so waits keep holding concurrency [D1][D2].
- Tasks are TypeScript; runtimes Node/Bun images [D1]. Compose stack: webapp, Postgres,
  Redis, Electric, ClickHouse, registry, MinIO, S2, supervisor, docker-socket-proxy [D3].
- **Verdict:** heavy for one small VM, TS-only, no ECS/Cloud Run/ACA. Not recommended.

### Conductor OSS, DBOS, Flyte, Dagger (brief)

- **Conductor OSS** (Apache-2.0, v3.32.5): JSON workflow definitions (declarative), HUMAN
  and WAIT tasks, UI. Per task-definition `concurrentExecLimit`; rate limits only on the
  Redis persistence module [C1]. Production stack: Postgres-only is supported, otherwise
  Redis + Elasticsearch [C2]. Per-repo caps would need task domains. JVM weight. Fallback
  if Kestra's EE gating is unacceptable but declarative JSON is wanted.
- **DBOS** (MIT library, 3.2.0): durable workflows and queues inside your app on Postgres,
  with worker/global concurrency and rate limits [B1]. Its UI, Conductor, is a managed
  service the app connects to [B2]; fails NN11 for the control plane.
- **Flyte 2** (Apache-2.0, v2.0.51): Kubernetes-native ("on your k8s") [G]. Fails NN9's
  Docker-only local install.
- **Dagger** (Apache-2.0, v0.21.10): "Automation engine to build, test and ship any
  codebase", containerised pipelines with SDKs in 8 languages [G]. No durable queue,
  scheduler, concurrency caps or control plane. At most a harness-step building block
  (for example running a harness in a Dagger container), not a candidate for this layer.

## Verdict under the open questions

| Open question | Effect on this group |
|---|---|
| **4. License bar: OSI only** | Eliminates Restate (BUSL-1.1), Inngest (SSPL), Windmill CE image; Windmill AGPL source build survives but needs EE for caps. Remaining: Hatchet, Temporal, Kestra (OSS core), Prefect, Conductor, Trigger.dev. |
| **4. Source-available OK** | Restate re-enters (light, keyed 3-level limits, experimental). Inngest still weak (2-limit ceiling, SSPL recipe risk). Windmill still needs EE for concurrency. |
| **2. Build vs adopt: must adopt engine + scheduler** | Hatchet (caps native) > Kestra (YAML native, caps by flow/namespace) > Temporal (caps built). |
| **2. Must also adopt the control-plane UI** | None show "work items" (incoming/queued/in review). Hatchet, Temporal, Kestra show runs with cancel/retry; only Hatchet and Kestra show queue/concurrency state directly. Reprioritising a queued item is not verified for any. A thin Satat work-item view is needed regardless. |
| **2. Willing to build scheduler** | Temporal becomes more attractive (best durability, Updates for steering, 8 SDKs). |
| **5. Live steering needed** | Temporal Updates/Signals and Restate awakeables give a typed channel into a running workflow; Hatchet durable events work too. The harness adapter must still relay messages into the CLI. No engine gives a chat UI. |
| **6. Multi-tenant** | Hatchet tenants (OSS), Temporal namespaces (OSS) fit. Kestra multi-tenancy and RBAC are EE [K5]. |
| **7. Extension language** | Go: Temporal, Hatchet, Restate. Python: Temporal, Hatchet, Prefect, Restate. TypeScript: Temporal, Hatchet, Restate, Trigger.dev. Java-only plugins for Kestra (scripts in any language). |
| **1. Merge authority / 3. Tracker scope** | No effect on engine choice. Gates for merge are expressible in all; intake is Satat-built in all. |

**Overall for this layer:** under the likely answers (OSI, adopt engine, single small VM),
**Hatchet** is the first choice for queue + scheduler + engine because its chained
concurrency keys map one-to-one onto global / lane / repo caps, with **Temporal** as the
heavier, more mature alternative where Satat builds the caps. **Kestra** wins only if
"declarative YAML in git" outweighs EE-gated cloud runtimes and a 4 GiB floor. **Prefect**
deserves a follow-up because its work pools are the only OSS native runtime plug-point
across Docker, K8s, ECS, Cloud Run and ACI.

## Sources

- [G] GitHub API metadata and releases (2026-10-15): https://github.com/temporalio/temporal, https://github.com/temporalio/ui, https://github.com/restatedev/restate, https://github.com/hatchet-dev/hatchet, https://github.com/inngest/inngest, https://github.com/windmill-labs/windmill, https://github.com/kestra-io/kestra, https://github.com/dagger/dagger, https://github.com/dbos-inc/dbos-transact-py, https://github.com/triggerdotdev/trigger.dev, https://github.com/PrefectHQ/prefect, https://github.com/conductor-oss/conductor, https://github.com/flyteorg/flyte
- [H1] https://github.com/hatchet-dev/hatchet/blob/main/LICENSE
- [H2] https://docs.hatchet.run/v1/concurrency
- [H3] https://docs.hatchet.run/v1/pausing-workflows ; https://github.com/hatchet-dev/hatchet/pull/4703
- [H4] https://docs.hatchet.run/v1/bulk-retries-and-cancellations
- [H5] https://docs.hatchet.run/v1/priority
- [H6] https://docs.hatchet.run/self-hosting/hatchet-lite
- [H7] https://docs.hatchet.run/v1/webhooks ; https://docs.hatchet.run/cookbooks/webhooks-github ; https://docs.hatchet.run/v1/cron-runs
- [H8] https://docs.hatchet.run/v1/rate-limits
- [H9] https://docs.hatchet.run/v1/timeouts
- [H10] https://docs.hatchet.run/v1/durable-event-waits
- [H11] https://hatchet.run/pricing ; https://docs.hatchet.run/v1/single-sign-on ; https://docs.hatchet.run/v1/user-groups ; https://docs.hatchet.run/v1/cloud-vs-oss
- [T0] https://github.com/temporalio/temporal/blob/main/LICENSE
- [T1] https://docs.temporal.io/develop/task-queue-priority-fairness
- [T2] https://docs.temporal.io/design-patterns/fairness
- [T3] https://docs.temporal.io/encyclopedia/workflow/workflow-pause
- [T4] https://docs.temporal.io/activity-operations/pause
- [T5] https://docs.temporal.io/temporal-service/persistence ; https://docs.temporal.io/self-hosted-guide/deployment
- [T6] https://docs.temporal.io/encyclopedia/workflow-message-passing ; https://docs.temporal.io/schedule
- [T7] https://docs.temporal.io/serverless-workers
- [T8] https://docs.temporal.io/task-queue (SDK list in "Where to set Task Queues")
- [T9] https://docs.temporal.io/task-queue ; https://docs.temporal.io/develop/worker-performance
- [K1] https://kestra.io/docs/task-runners/types
- [K2] https://kestra.io/docs/workflow-components/concurrency
- [K3] https://kestra.io/docs/administrator-guide/requirements
- [K4] https://kestra.io/docs/workflow-components/triggers ; https://kestra.io/docs/how-to-guides/pause-resume ; https://kestra.io/docs/how-to-guides/replay ; https://kestra.io/docs/version-control-cicd
- [K5] https://kestra.io/docs/oss-vs-paid ; https://kestra.io/docs/enterprise/instance/maintenance-mode ; https://kestra.io/docs/enterprise/instance/kill-switch
- [K6] https://github.com/kestra-io/plugin-kubernetes (core/PodCreate.java) ; https://github.com/kestra-io/plugin-azure (batch/job/Create.java)
- [R1] https://github.com/restatedev/restate/blob/main/LICENSE
- [R2] https://docs.restate.dev/services/flow-control
- [R3] https://docs.restate.dev/admin-api/invocation/pause-an-invocation ; https://docs.restate.dev/admin-api/invocation/cancel-an-invocation
- [R4] https://docs.restate.dev/server/overview
- [R5] https://docs.restate.dev/develop/ts/durable-timers
- [I1] https://github.com/inngest/inngest/blob/main/LICENSE.md
- [I2] https://www.inngest.com/docs/guides/concurrency
- [I3] https://www.inngest.com/docs/self-hosting
- [W1] https://github.com/windmill-labs/windmill/blob/main/LICENSE
- [W2] https://www.windmill.dev/docs/script_editor/concurrency_limit
- [W3] https://www.windmill.dev/docs/advanced/git_sync
- [W4] https://www.windmill.dev/docs/advanced/docker
- [W5] https://www.windmill.dev/docs/flows/flow_approval
- [W6] https://www.windmill.dev/docs/core_concepts/worker_groups
- [W7] https://www.windmill.dev/docs/misc/plans_details (docs index, Triggers entry)
- [P1] https://docs.prefect.io/v3/concepts/work-pools
- [P2] https://docs.prefect.io/v3/concepts/global-concurrency-limits
- [P3] https://docs.prefect.io/v3/concepts/webhooks
- [D1] https://trigger.dev/docs/self-hosting/overview
- [D2] https://trigger.dev/docs/queue-concurrency
- [D3] https://github.com/triggerdotdev/trigger.dev/tree/main/hosting/docker (webapp and worker docker-compose.yml)
- [C1] https://conductor-oss.github.io/conductor/documentation/configuration/taskdef.html
- [C2] https://conductor-oss.github.io/conductor/devguide/running/deploy.html
- [B1] https://docs.dbos.dev/python/tutorials/queue-tutorial
- [B2] https://docs.dbos.dev/production/self-hosting/conductor
