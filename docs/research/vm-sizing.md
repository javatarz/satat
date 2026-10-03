# VM sizing for Satat

Research for Satat ticket #2 ("Right-size the VM"). Investigated against primary sources: the
OpenHands docs site (`docs.openhands.dev`), LiteLLM docs (`docs.litellm.ai`), ntfy docs
(`docs.ntfy.sh`), Hetzner docs/site (`docs.hetzner.com`, `hetzner.com`), Contabo site, and the
`OpenHands/OpenHands` GitHub issue tracker. Every claim carries a source URL; pricing figures are
marked **approximate** where the provider page loads them via JavaScript and should be confirmed on
the linked page.

## TL;DR

The dominant cost is **Docker sandboxes (one per concurrent agent)**, not the control plane. OpenHands'
official planning unit is **0.5 vCPU + 4 GiB RAM per sandbox**. A fixed base of ~2.5–4 vCPU / ~6–9 GiB
covers Agent Canvas + Agent Server + Automation Server + LiteLLM + ntfy + nginx.

**Decision (ADR 0012): Contabo Core VPS 4 (4 vCPU / 8 GB / 100 GB SSD, ~€4.40/mo)** for a fixed pool
of 1 agent. This hits the minimum spec (4 vCPU / 8 GB for base + 1 sandbox). Scale to Core VPS 6
(6 vCPU / 12 GB, ~€6.00/mo) if headroom is needed.

| Concurrent agents | Min (vCPU / RAM / disk) | Contabo plan | Est. 24mo |
|---|---|---|---|
| 1 | 4 / 8 GB / 40 GB | Core VPS 4 | €4.40/mo |
| 1 (recommended) | 8 / 16 GB / 80 GB | Core VPS 8 | €11.20/mo |
| 2-3 (recommended) | 8-16 / 16-32 GB | Core VPS 8-12 | €11-20/mo |

Trade-offs vs. Hetzner: Contabo Core VPS uses older CPUs and SSD (not NVMe), has lower port speeds,
and no S3-compatible Object Storage (Terraform state goes to Terraform Cloud). The 4x cost savings
at the entry tier outweigh these for a single-agent deployment.

---

## 1. OpenHands Agent Canvas + Agent Server

OpenHands splits into three backend responsibilities (Canvas client, Agent Server, Automation Server)
plus the sandbox that actually runs tools ([Agent Canvas Architecture](https://docs.openhands.dev/openhands/usage/agent-canvas/architecture.md)).

- **Official single-user baseline: 2 vCPU + 4 GB RAM.** "Ubuntu 24.04 LTS with 2 vCPU and 4 GB RAM is
  enough for a single user" ([VM / Self-Hosted Installation](https://docs.openhands.dev/openhands/usage/agent-canvas/backend-setup/vm.md)).
- The **application server** (UI + API + orchestration) ships with a Kubernetes default of
  `requests: memory 1200Mi / limits: memory 3Gi`, and a "recommended production" of
  `requests 2560Mi / limits 4Gi`, with `cpu: 100m` request ([Resource Limits](https://docs.openhands.dev/enterprise/k8s-install/resource-limits.md)).
- **Caveat: OSS is single-user by design.** "OpenHands is meant to be run by a single user on their
  local workstation. It is not appropriate for multi-tenant deployments… no built-in authentication,
  isolation, or scalability" ([FAQs](https://docs.openhands.dev/overview/faqs.md)). Satat runs one
  operator with N *concurrent* agents, which is the multi-*agent* (not multi-*tenant*) case the
  per-sandbox math below covers.

Budget: **1 vCPU / 2 GiB min**, **2 vCPU / 4 GiB recommended** for the control plane (matches the
official "2 vCPU / 4 GB single user" figure).

## 2. Docker sandbox overhead per agent

This is the figure everything else hangs off. OpenHands sizes deployments on **peak concurrent
sandboxes** and publishes a per-sandbox planning unit:

| Resource | Per sandbox | Source |
|---|---|---|
| CPU | 0.5 vCPU | [Sizing Guide](https://docs.openhands.dev/enterprise/sizing-guide.md) |
| Memory | 4 GiB | [Sizing Guide](https://docs.openhands.dev/enterprise/sizing-guide.md) |
| Node disk | 10 GiB | [Sizing Guide](https://docs.openhands.dev/enterprise/sizing-guide.md) |
| Volume storage | 10 GiB | [Sizing Guide](https://docs.openhands.dev/enterprise/sizing-guide.md) |

The underlying sandbox defaults confirm this: `MEMORY_REQUEST`/`MEMORY_LIMIT` = **3072 Mi**,
`CPU_REQUEST` = **500m**, `EPHEMERAL_STORAGE_SIZE` = **10 Gi** ([Resource Limits](https://docs.openhands.dev/enterprise/k8s-install/resource-limits.md)).

Each conversation runs its own agent-server container. In Docker sandbox mode the container "starts
lazily" and is "removed when its workspace closes" ([Isolate Tool Execution with Docker](https://docs.openhands.dev/openhands/usage/agent-canvas/backend-setup/docker-execution.md),
[Docker Sandbox](https://docs.openhands.dev/sdk/guides/agent-server/docker-sandbox.md)).

The enterprise **single-VM** reference table scales this into full machines, and is a useful sanity
check even though Satat runs the OSS stack (not the enterprise k0s/Kubernetes bundle):

| Peak sandboxes | VM | Source |
|---|---|---|
| 5 | 8 vCPU / 32 GiB / 500 GiB SSD | [Sizing Guide](https://docs.openhands.dev/enterprise/sizing-guide.md) |
| 15 | 16 vCPU / 64 GiB / 1 TiB SSD | [Sizing Guide](https://docs.openhands.dev/enterprise/sizing-guide.md) |

Note those enterprise rows include a full single-node Kubernetes cluster (k0s) and platform pods, so
they over-size the OSS agent-canvas deployment; they are an upper bound, not the baseline.

**Per concurrent agent: 0.5 vCPU + 4 GiB RAM + 10 GiB workspace.**

## 3. LiteLLM proxy memory footprint

LiteLLM's official production guidance:

- "Give each pod **1 vCPU and 4 GiB of memory**, as both requests and limits… **4 GiB is a floor
  rather than a target**" ([Production Best Practices](https://docs.litellm.ai/docs/proxy/prod)).
- On a single VM (nothing autoscaling for you), "set `NUM_WORKERS` to the machine's vCPU count"
  ([Server Tuning](https://docs.litellm.ai/docs/proxy/server_tuning)).

That 4 GiB floor is driven by the Prisma/Postgres query engine when spend-logging is enabled. **Satat
runs LiteLLM in config-file mode (model routing + budget cap) without a Postgres database**, so its
steady-state footprint is materially lower than 4 GiB; the figure is kept as a conservative planning
number rather than an observed one. (LiteLLM's `deploy` docs also note PostgreSQL is "required for the
proxy's auth and tracking features" — Satat's config-only mode skips it
([Production Deployment](https://docs.litellm.ai/docs/proxy/deploy)).)

Budget: **0.5 vCPU / 0.5 GiB min** (config-only), **1 vCPU / 4 GiB recommended** (official floor).

## 4. ntfy server resource usage

ntfy is a single static Go binary. The official docs ship Kubernetes examples with explicit limits:

- `resources: limits: memory: "128Mi", cpu: "500m"` ([Installation — Kubernetes deployment](https://docs.ntfy.sh/install/))
- Kustomize example: `limits: memory 300Mi / cpu 200m`, `requests: 150Mi / 150m` ([Installation — Kustomize](https://docs.ntfy.sh/install/))

Budget: **0.2 vCPU / 0.3 GiB** (generous vs. the 128 MiB example).

## 5. nginx + certbot baseline

nginx is an event-driven reverse proxy with a low, fixed footprint; for a low-traffic deployment
terminating TLS to three backend services the resident set is a few tens of MB and sub-0.1 vCPU
([nginx.org documentation](https://nginx.org/en/docs/), and corroborated by the OpenHands VM guide,
which installs `nginx certbot python3-certbot-nginx` and still calls a 2 vCPU / 4 GB box "enough for a
single user" — i.e. nginx+certbot fit inside that same budget)
([VM / Self-Hosted Installation](https://docs.openhands.dev/openhands/usage/agent-canvas/backend-setup/vm.md)). Certbot runs transiently
at renewal and contributes nothing to steady-state RAM.

Budget: **0.1 vCPU / 0.1 GiB**.

## 6. Real-world experiences

- **Canvas degrades beyond a few concurrent conversations on a single box.** A user on an i7-8700 with
  32 GB RAM and Docker capped at 20 GiB reported Agent Canvas "extremely slow and unusable" with 9
  chats / >3 conversations, with proxy `ECONNREFUSED` and multi-minute chat loads
  ([OpenHands issue #16690](https://github.com/OpenHands/OpenHands/issues/16690)). This is the strongest
  signal that per-sandbox isolation and headroom matter as much as raw totals.
- **Per-sandbox memory limits are a long-standing ask**, indicating users hit memory pressure in
  practice ([OpenHands issue #4450 "Optional MEMORY LIMIT for sandbox"](https://github.com/OpenHands/OpenHands/issues/4450),
  [OpenHands issue #17090 "Support resource limits (CPU/Memory) for local Docker sandboxes"](https://github.com/OpenHands/OpenHands/issues/17090)).
- **Hetzner** publishes no AI-agent sizing guide, but its own FAQ quantifies the network ceiling Satat
  will live under: 10 Gbps shared host link, "expect about 300–500 Mbits" per instance, no bandwidth
  guarantee ([Regular Performance FAQ](https://www.hetzner.com/cloud/regular-performance/)). Its community board is at
  [forum.hetzner.com](https://forum.hetzner.com/).
- **Contabo** likewise has no agent-specific sizing guidance; community discussion is on its
  help/tutorial channels rather than a formal sizing guide ([Contabo tutorials](https://contabo.com/blog/category/tutorials/)).

No primary source documents a per-session *storage* size for recordings; see the companion note
[docs/research/session-recording.md](./session-recording.md) — the conversation event stream (JSON) is
small, browser rrweb recordings are opt-in and can be large, and Satat's code-fix agents generate the
former, not the latter.

---

## 7. Sizing table

Fixed base (one control plane + supporting services), summed from §1–§5:

| Component | Min (vCPU / RAM) | Recommended (vCPU / RAM) |
|---|---|---|
| Agent Canvas + Agent Server + Automation Server | 1 / 2 GiB | 2 / 4 GiB |
| LiteLLM proxy | 0.5 / 0.5 GiB | 1 / 4 GiB |
| ntfy | 0.2 / 0.3 GiB | 0.2 / 0.3 GiB |
| nginx + certbot | 0.1 / 0.1 GiB | 0.1 / 0.1 GiB |
| OS + Docker daemon + images | 0.5 / 1 GiB | 1 / 2 GiB |
| **Base total** | **~2.3 / ~3.9 GiB** | **~4.3 / ~10.4 GiB** |

Per concurrent agent sandbox (§2): **0.5 vCPU / 4 GiB / 10 GiB disk**.

| Concurrent agents | Min (vCPU / RAM / disk) | Recommended (vCPU / RAM / disk) |
|---|---|---|
| 1 | 4 / 8 GB / 40 GB | 8 / 16 GB / 80 GB |
| 2 | 8 / 16 GB / 80 GB | 8 / 32 GB / 160 GB |
| 4 | 16 / 32 GB / 160 GB | 16 / 64 GB / 320 GB |

Notes:

- "Min" assumes LiteLLM config-only (no Postgres), a single concurrent agent per row, small repos, and
  no browser-recording retention. "Recommended" uses the official LiteLLM 4 GiB floor and leaves
  headroom for build spikes and session recording.
- Disk = 30 GB base (OS + Docker images + OpenHands/LiteLLM images) + 10 GiB per sandbox workspace,
  rounded up. Hetzner's NVMe tiers are generous (CPX32 = 160 GB), so disk is rarely the binding
  constraint.
- Above 4 concurrent agents, the OSS Canvas single-box behavior (§6 issue #16690) and the enterprise
  guidance ("Above 100 [sandboxes] use Kubernetes"; dedicated sandbox nodes recommended)
  ([Sizing Guide](https://docs.openhands.dev/enterprise/sizing-guide.md)) argue for separating the
  control plane from sandboxes rather than buying one bigger VM.

## 8. Hosting provider recommendation (cost)

**Decision: Contabo Core VPS 4** for the initial single-agent deployment. Rationale:

1. **Lowest entry cost.** Core VPS 4 at ~€4.40/mo (24-month) is ~1/3 the cost of Hetzner CPX42
   (~€13/mo). Even the monthly plan at ~€5.50 is less than half.
2. **Meets minimum spec.** 4 vCPU / 8 GB satisfies the planning minimum of 2.3 vCPU / 7.9 GiB
   (base + 1 sandbox). Scale to Core VPS 6 (€6.00/mo) for headroom.
3. **Terraform provider available.** Contabo publishes a Terraform provider with cloud-init
   support for automated provisioning.
4. **Upgrade path.** Core VPS line scales to 18 vCPU / 96 GB without provider migration.

Trade-offs vs. Hetzner:
- **Older CPUs, slower I/O**: Contabo Core uses older CPU generations and SSD (not NVMe). Docker
  builds and git clones will be slower than on Hetzner CPX.
- **No S3 Object Storage**: Terraform state must use Terraform Cloud (free tier, already chosen
  in ADR 0004). Hetzner's S3-compatible Object Storage would allow a native `s3` backend.
- **Lower port speeds**: Core VPS 4 = 200 Mbit/s vs Hetzner ~300-500 Mbit/s. Docker image pulls
  and git clones are noticeably slower but not blocking for a single agent.
- **24-month commitment** for the lowest price. Monthly plan is ~€5.50/mo.

Hetzner remains the stronger choice for deployments needing 2+ concurrent agents or where build
speed matters, thanks to NVMe, higher port speeds, and S3 Object Storage. Contabo wins on pure
cost at the single-agent entry tier.

## 9. Terraform state storage options per provider

Per ADR 0004, Satat stores Terraform state in a **remote backend (provider TBD)**. Options per host:

### Hetzner

- **S3-compatible Object Storage → Terraform `s3` backend.** Hetzner Object Storage is "S3-compatible
  and scalable" ([Object Storage](https://www.hetzner.com/storage/object-storage/)); its docs cover
  "Generating S3 keys", "S3 compatible CLI tools", and "Creating a Bucket via MinIO Terraform
  Provider" ([Object Storage docs](https://docs.hetzner.com/storage/object-storage/)). This is the
  native, lowest-cost remote backend for a Hetzner deployment.
  - **Locking caveat:** there is no Hetzner equivalent of AWS DynamoDB for state locking, so a raw
    `s3` backend runs without a distributed lock. For Satat's single-operator CI this is acceptable;
    if locking is required, add Terraform Cloud/HCP or a self-hosted lock service.
- **Terraform Cloud / HCP Terraform (`cloud` backend)** — provider-agnostic, gives locking + encrypted
  state out of the box.
- **Generic backends** (`http`, `pg` on a self-hosted Postgres, `git`) — provider-agnostic, all
  workable on Hetzner but not as turnkey as S3.

### Contabo

- **No confirmed S3-compatible object storage.** The public site lists "Storage VPS" (block storage
  for VMs), and `/en/object-storage/` redirects to the Storage VPS product page — no S3 API is
  advertised ([Storage VPS](https://contabo.com/en/storage-vps/)). So the native `s3` backend is not a
  safe assumption on Contabo today.
- **Terraform Cloud / HCP Terraform (`cloud` backend)** is the primary provider-agnostic option
  (locking + state in one).
- **Self-hosted store on a VPS** — e.g. run Postgres for the `pg` backend, or use `http`/`git`
  backends. Contabo's cloud-init + API/CLI provision this cleanly
  ([Contabo API](https://contabo.com/en/contabo-api/), [Cloud Init](https://contabo.com/en/cloud-init/)).

**Net:** Hetzner's S3-compatible Object Storage makes the standard `s3` remote backend the
low-friction default; Contabo requires Terraform Cloud or a self-hosted state store.

---

## Sources

- OpenHands — Agent Canvas Architecture: https://docs.openhands.dev/openhands/usage/agent-canvas/architecture.md
- OpenHands — VM / Self-Hosted Installation: https://docs.openhands.dev/openhands/usage/agent-canvas/backend-setup/vm.md
- OpenHands — Enterprise Sizing Guide: https://docs.openhands.dev/enterprise/sizing-guide.md
- OpenHands — Enterprise Resource Limits: https://docs.openhands.dev/enterprise/k8s-install/resource-limits.md
- OpenHands — Isolate Tool Execution with Docker: https://docs.openhands.dev/openhands/usage/agent-canvas/backend-setup/docker-execution.md
- OpenHands — Docker Sandbox (SDK): https://docs.openhands.dev/sdk/guides/agent-server/docker-sandbox.md
- OpenHands — FAQs: https://docs.openhands.dev/overview/faqs.md
- OpenHands issue #16690: https://github.com/OpenHands/OpenHands/issues/16690
- OpenHands issue #4450: https://github.com/OpenHands/OpenHands/issues/4450
- OpenHands issue #17090: https://github.com/OpenHands/OpenHands/issues/17090
- LiteLLM — Production Best Practices: https://docs.litellm.ai/docs/proxy/prod
- LiteLLM — Server Tuning: https://docs.litellm.ai/docs/proxy/server_tuning
- LiteLLM — Production Deployment: https://docs.litellm.ai/docs/proxy/deploy
- ntfy — Installation: https://docs.ntfy.sh/install/
- nginx documentation: https://nginx.org/en/docs/
- Hetzner — Cloud: https://www.hetzner.com/cloud/
- Hetzner — Regular Performance: https://www.hetzner.com/cloud/regular-performance/
- Hetzner — Cloud server overview: https://docs.hetzner.com/cloud/servers/overview/
- Hetzner — Object Storage: https://www.hetzner.com/storage/object-storage/
- Hetzner — Object Storage docs: https://docs.hetzner.com/storage/object-storage/
- Contabo — https://contabo.com/en/
- Contabo — Pricing: https://contabo.com/en/pricing/
- Contabo — Storage VPS: https://contabo.com/en/storage-vps/
- Contabo — API: https://contabo.com/en/contabo-api/
- Contabo — Cloud Init: https://contabo.com/en/cloud-init/
- Satat — session recording research: ./session-recording.md
