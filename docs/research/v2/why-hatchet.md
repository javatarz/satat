# Research: the case for Hatchet

Status: **research, 2026-10-15. No decision.** Written for the owner, as a follow-up to
[`agent-platform-options.md`](agent-platform-options.md), Stack A.

Hatchet is the only open-source engine where Satat's three caps (global, per lane, per repo)
are configuration, not code we write. It costs no licence fee and adds one container plus
Postgres. In exchange we accept a fast-moving pre-1.0 project and a few defaults we must
override.

## What it gives Satat

- **The caps as config.** A step can carry several
  [concurrency expressions](https://docs.hatchet.run/v1/concurrency) at once (`'global'`,
  `input.lane`, `input.repo`), all enforced together. With `GROUP_ROUND_ROBIN`, over-cap work
  queues instead of being dropped. Limits can be shared across workflows and changed while
  running, which is how the Claude lane backs off.
- **The rest of the queue layer is built in:**
  - [cron](https://docs.hatchet.run/v1/cron-runs)
  - [webhooks](https://docs.hatchet.run/v1/webhooks)
  - [durable event waits](https://docs.hatchet.run/v1/durable-event-waits) for human gates
  - [timeouts and retries](https://docs.hatchet.run/v1/timeouts)
  - [pause](https://docs.hatchet.run/v1/pausing-workflows)
  - [bulk cancel and replay](https://docs.hatchet.run/v1/bulk-retries-and-cancellations) from
    a dashboard
- **A small footprint.** [Hatchet Lite](https://docs.hatchet.run/self-hosting/hatchet-lite)
  bundles the engine, API and dashboard in one image. Postgres is the only dependency;
  RabbitMQ is optional. Postgres already fits the state-store plug point on every cloud.
- **No licence ceiling.** The whole repo is
  [MIT](https://github.com/hatchet-dev/hatchet/blob/main/LICENSE). No enterprise edition gates
  the features Satat needs, and nothing stops a public recipe.
- **Any extension language.** SDKs exist in Python, TypeScript, Go and Ruby.

## What it costs

| Cost | What it means for Satat |
|---|---|
| Licence | None (MIT) |
| Hosting | One `hatchet-lite` container and Postgres on the existing VM. RAM is unmeasured: Hatchet publishes no sizing for Lite, so the spike must measure it |
| Lite's limits | Hatchet describes Lite as "designed for development and low-volume use-cases". Satat's volume is low, but a larger install moves to the [Docker Compose](https://docs.hatchet.run/self-hosting/docker-compose) or [Helm](https://docs.hatchet.run/self-hosting/kubernetes-quickstart) layout |
| Upgrades | Pre-1.0 (v0.110.5), with 39 releases between 2026-07-15 and 2026-10-15. Migrations run on engine start. Hatchet asks for a [database snapshot before every upgrade](https://docs.hatchet.run/self-hosting/upgrading-downgrading). v0.87.6 and later need the Postgres `btree_gist` extension |
| Defaults to override | Queued tasks are cancelled after a 5-minute [`schedule_timeout`](https://docs.hatchet.run/v1/timeouts). Paused workflows drop queued runs after a TTL. Runs and events are [deleted after 30 days](https://docs.hatchet.run/self-hosting/data-retention). Satat must change all three |
| Build work it does not remove | The YAML interpreter, workload drivers, work-item board and Claude-lane back-off logic. Every engine except Prefect leaves the drivers to us |
| Lock-in | Low. Workflow files are Satat's own YAML, so only the interpreter and the cap wiring are Hatchet-specific. Moving to Temporal means rewriting those two, plus semaphores for the caps |
| Vendor risk | One company maintains it (about 90 contributors; repo created 2023-12). MIT means we could fork it, but we would then own it |

## Against the alternatives

| Engine | Global / lane / repo caps | Licence | Footprint | Main gap against Hatchet |
|---|---|---|---|---|
| **Hatchet** | All three built in | MIT | Lite + Postgres | Pre-1.0, frequent releases |
| [Temporal](https://temporal.io) | Lane caps via task queues. The repo cap must be built: its [fairness keys](https://docs.temporal.io/design-patterns/fairness) do not cap concurrency | MIT | Server + UI + Postgres | More mature, with 8 SDKs, but we write the semaphore workflows. Workflow pause is pre-release |
| [Kestra](https://kestra.io) | Flow, namespace and tenant limits, not keyed by input | Apache-2.0 core; Enterprise edition proprietary | Java; needs at least [4 GiB RAM and 2 vCPU](https://kestra.io/docs/administrator-guide/requirements) | Native YAML flows, but Kubernetes and cloud [task runners are Enterprise-only](https://kestra.io/docs/task-runners/types) |
| [Prefect 3](https://www.prefect.io) | Pool, queue and named global limits | Apache-2.0 | Server + SQLite or Postgres | Launches workloads natively on Docker, Kubernetes, ECS, Cloud Run and ACI. But the agent's container must hold a Prefect API credential, webhooks are [Cloud-only](https://docs.prefect.io/v3/concepts/webhooks), and it is Python-only |
| [Restate](https://restate.dev) | Scope plus two key levels, [experimental](https://docs.restate.dev/services/flow-control) | BUSL-1.1 (not OSI) | Single binary, no database | The lightest option, but it fails an OSI licence bar and has no native cron |

## When Hatchet would be the wrong choice

- If you want workflows written in a vendor's YAML rather than Satat's own, Kestra fits
  better, though its Kubernetes and cloud runners need the Enterprise edition.
- If a long durability track record matters most, Temporal is the safer engine, and we build
  the caps ourselves.
- If the spike shows Lite cannot hold queued work for hours, or that a lane cap cannot drop
  to zero, the main reason to pick Hatchet is gone.
