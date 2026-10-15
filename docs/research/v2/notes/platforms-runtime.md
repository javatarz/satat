# Platforms research: workload runtime / sandbox layer

Status: scratch research, 2026-10-15. Scope: the "workload runtime driver" layer from
`docs/research/v2/platform-requirements.md` (start, watch, tear down an isolated workload per
task step across Docker, Kubernetes, ECS/Fargate, Cloud Run jobs, Azure Container Apps jobs).
Non-negotiables (NN) checked here: 1 (pluggable runtime), 3 (credential lanes by
infrastructure), 9 (every dependency a plugin, Docker-only local install), 10 (security
posture), 11 (self-hosted, license allows public recipe).

Repo metadata (license SPDX, latest release, last push) was pulled with `gh api` on
2026-10-15 unless noted. Anything not checked against a primary source is marked
**UNVERIFIED**.

## Summary table

| Candidate | Backends spanned | Isolation | License (SPDX) | Latest release | NN1 | NN3 | NN9 | NN10 | NN11 | Verdict |
|---|---|---|---|---|---|---|---|---|---|---|
| **Own thin driver** (Docker API + k8s Job/Pod + ECS RunTask + Cloud Run jobs + ACA jobs) | All five, by construction | runc / gVisor (`runsc`) / Kata on Docker+k8s; Firecracker-class VM on Fargate; microVM on Cloud Run jobs | n/a (ours) | n/a | Pass | Pass (per-lane identities) | Pass | Pass (with egress proxy) | Pass | **Recommended** |
| kubernetes-sigs/agent-sandbox | Kubernetes only | Delegates to RuntimeClass (gVisor, Kata) | Apache-2.0 | v1.0.6, 2026-10-08 | Partial (k8s backend only) | Pass | Fails alone (no Docker-only path) | Pass | Pass | **Adopt inside the k8s driver** (optional) |
| OpenSandbox (opensandbox-group, Alibaba origin) | Docker, Kubernetes (own controller or agent-sandbox), Firecracker "Fast Sandbox" on k8s | runc / gVisor / Kata (Docker+k8s), Firecracker (k8s only) | Apache-2.0 | release-1.1.1, 2026-10-09 (1.2.0-rc.1 2026-10-15) | Partial (no ECS/Cloud Run/ACA) | Pass (+ credential vault) | Partial (Docker + k8s plugins only) | Pass (strongest egress story) | Pass | **Strongest adoptable project; candidate backend behind our interface** |
| E2B runtime (formerly e2b-dev/infra) incl. E2B Embed | Its own Firecracker nodes; Embed installs via Compose, Terraform GCP/AWS/Azure VM, or one k8s node | Firecracker microVM | Apache-2.0 | 2026.30, 2026-09-10 | Fail (one runtime: KVM hosts; not ECS/Cloud Run/ACA) | Pass | Fail (needs KVM; 12 GiB RAM recommended) | Pass | Pass (multi-node needs Enterprise) | Reject as the driver; too heavy for cheap tier |
| Daytona (OSS) | Docker-based runners | Container (details UNVERIFIED) | AGPL-3.0 (frozen); Helm charts Elastic-2.0 | v0.190.0, 2026-06-23, **repo archived** | Fail | Pass | Fail | ? | Fail on maintenance; Helm fails OSI | **Reject** |
| Modal | Modal's cloud only | gVisor or VM | Client Apache-2.0; server proprietary | n/a | Fail | n/a | Fail | n/a | **Fail** (managed only) | **Reject** |
| Cloudflare Sandbox SDK | Cloudflare Containers only | Cloudflare container VM | Apache-2.0 (LICENSE text; GitHub reports NOASSERTION) | @cloudflare/sandbox 1.0.1, 2026-10-13 | Fail | n/a | Fail | n/a | **Fail** (managed only) | **Reject** |
| OpenKruise Agents | Kubernetes only | RuntimeClass | Apache-2.0 | v0.6.0, 2026-10-09 | Partial | Pass | Fail alone | Pass | Pass | Watch; pre-1.0 alternative to agent-sandbox |
| microsandbox | Single host (libkrun microVMs), embeddable | microVM (KVM) | Apache-2.0 | v0.7.8, 2026-10-09 | Fail (single host) | Pass (secrets never enter VM) | Partial (needs KVM) | Pass | Pass | Watch; beta |
| Firecracker / firecracker-containerd / Flintlock / Kata | Building blocks (single host or k8s RuntimeClass) | microVM | Apache-2.0 / Apache-2.0 / MPL-2.0 / Apache-2.0 | v1.17.0 2026-09-10 / no releases (last commit 2026-07-16) / v0.16.0 2026-10-03 / 4.2.0 2026-09-15 | n/a (component) | n/a | n/a | Raises isolation | Pass | Use Kata (or gVisor) as an isolation *option* in Docker/k8s drivers, not as the driver |
| OpenHands workspace abstraction | Docker, agent-sandbox (k8s), Apptainer, OpenHands Cloud, remote API | Container | MIT | software-agent-sdk v1.55.0, 2026-10-15 | Partial (no ECS/Cloud Run/ACA) | Weak (assumes in-workload agent-server) | Pass | Partial | Pass | Not a fit; assumes its agent-server lives in the workload |
| Kestra task runners | Docker (OSS); k8s, AWS Batch, Cloud Run, Azure Batch (Enterprise) | Container | Apache-2.0 (EE runners proprietary) | v2.0.6, 2026-10-13 | Fail in OSS | n/a | Fail in OSS | n/a | Fail for cloud runners | Reject for this layer |
| Nomad | Docker/exec/etc. on Nomad clients | Container (or Firecracker driver, community) | BUSL-1.1 (GitHub: NOASSERTION) | v2.0.7, 2026-09-18 | Partial | Pass | Pass | Pass | Fails OSI bar; passes source-available bar | Reject (another cluster to run) |
| Dagger, SkyPilot, Virtual Kubelet, Coder, Arrakis | See brief notes | — | Apache-2.0 / Apache-2.0 / Apache-2.0 / AGPL-3.0 / AGPL-3.0 | various | — | — | — | — | — | Not fits (see below) |

## Requirements this layer must meet (derived)

- One workload per task step; no shared process or filesystem (NN1).
- The set of secrets a workload gets is fixed by its lane, decided outside the workload and
  outside the prompt (NN3). The Claude OAuth token reaches only Claude Code workloads; the
  gateway/LiteLLM key reaches only open-harness workloads.
- Docker-only local implementation plus at least one k8s or cloud implementation (NN9).
- No host access, no container-runtime socket in the workload, restricted egress,
  short-lived per-task credentials where possible (NN10).
- 30-120 minute runs. Startup latency matters little at that run length; a 30 s cold start
  is under 2% of a 30-minute task.
- Logs streamed to the control plane; cancel; optionally live steering (open question 5).

## Candidates in detail

### 1. kubernetes-sigs/agent-sandbox

- **What it is.** A SIG Apps project providing a `Sandbox` CRD and controller ("isolated,
  stateful, singleton workloads"), plus extensions `SandboxTemplate`, `SandboxClaim`,
  `SandboxWarmPool`. It is a "sandbox orchestrator" that "delegates low-level container
  isolation to secure 'Sandbox Runtimes' (like gVisor or Kata Containers)" via
  `RuntimeClass` [README](https://github.com/kubernetes-sigs/agent-sandbox).
- **Maturity.** v1.0.0 on 2026-08-28 moved core and extension APIs to `v1beta1` and
  removed `v1alpha1` [v1.0.0 release](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0).
  Weekly patch releases since (v1.0.6 on 2026-10-08). 4.2k stars, push on 2026-10-15.
  Apache-2.0 (`LICENSE`, GitHub SPDX).
- **Backends.** Kubernetes only. No Docker-only or cloud-job mode was found in the repo or
  roadmap. `sandboxd` is an in-pod exec/file daemon, not a non-k8s runtime
  ([examples/sandboxd-sandbox](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/sandboxd-sandbox)).
  Does not work outside Kubernetes.
- **Isolation.** Whatever RuntimeClass the template names (gVisor, Kata). The pod spec is
  ordinary, so no host socket is mounted unless you add it.
- **Egress.** `SandboxTemplate.networkPolicy` with ingress/egress rules; "If this list is
  empty, all egress traffic is blocked (Default Deny)". `Managed` mode creates one shared
  NetworkPolicy per template; `Unmanaged` defers to Cilium etc.
  ([sandboxtemplate_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/api/v1beta1/sandboxtemplate_types.go)).
  Standard NetworkPolicy is IP/CIDR-based; FQDN allowlists need Cilium (there is an
  `examples/demo-cilium-egress`).
- **Secrets / lanes.** The template owns the pod spec. `EnvVarsInjectionPolicy`
  (`Allowed`/`Overrides`/`Disallowed`) controls whether a claim may inject env vars
  (same file). Lanes map naturally to one template per lane (one ServiceAccount, one
  set of Secret refs) with `Disallowed` injection. Short-lived creds via projected SA
  tokens / workload identity are plain k8s (not agent-sandbox features).
- **Startup / warm pools.** `SandboxWarmPool` + `SandboxClaim`. v1.0.1 notes describe
  OpenHands binding "to pre-warmed Agent Sandbox pods with sub-second startup latency"
  ([v1.0.1 release](https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.1)).
- **Long runs.** `SandboxClaim.lifecycle.shutdownTime`, `ttlSecondsAfterFinished`,
  `shutdownPolicy` ([sandboxclaim_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/api/v1beta1/sandboxclaim_types.go)).
  No max-duration cap. Fits 30-120 min runs.
- **Logs / attach.** Pod logs and `kubectl exec`; `sandbox-router` reverse proxy for
  SDK/HTTP access "where direct pod port-forwarding is unavailable" (gVisor, Kata).
- **SDKs.** Go (`sigs.k8s.io/agent-sandbox/clients/go/sandbox`), Python
  (`k8s-agent-sandbox`), TypeScript (v1.0.1). OpenHands ships `AgentSandboxWorkspace`.
- **Weight.** One controller Deployment; cheap on an existing cluster, but needs a cluster.
- **NN check.** NN1 partial (k8s only). NN3 pass (per-template SA/secrets). NN9 fails on
  its own (no Docker-only path). NN10 pass with gVisor/Kata RuntimeClass + NetworkPolicy.
  NN11 pass (Apache-2.0, OSI).
- **Verdict.** Good **implementation detail of Satat's Kubernetes driver** if warm pools or
  per-template NetworkPolicy are wanted. Not the abstraction. Under any license bar it
  passes. Under multi-tenancy: per-namespace templates work.

### 2. OpenSandbox (opensandbox-group/OpenSandbox)

- **What it is.** "Run AI agents in sandboxes on your own infrastructure ... Start locally
  with Docker and deploy on Kubernetes through a unified sandbox API"
  ([README](https://github.com/opensandbox-group/OpenSandbox)). 15.9k stars, Apache-2.0,
  release-1.1.1 on 2026-10-09, 1.2.0-rc.1 on 2026-10-15. Alibaba origin (UNVERIFIED from
  primary source in this pass; repo moved to `opensandbox-group`).
- **Backends.** One `SandboxService` interface selected by `runtime.type`: "Docker for local
  and single-host deployments, Kubernetes for container workloads via the controller or
  agent-sandbox, and Fast Sandbox for template-backed microVMs"
  ([server architecture](https://github.com/opensandbox-group/OpenSandbox/blob/main/docs/architecture/control-plane/server.md)).
  No ECS, Cloud Run, or ACA backend.
- **Isolation.** `[secure_runtime] type = "" | gvisor | kata | firecracker (Kubernetes-only)`,
  with `docker_runtime` (e.g. `runsc`, `kata-runtime`) or `k8s_runtime_class`. One profile
  per server process. Docker config defaults `no_new_privileges = true`
  ([server/configuration.md](https://github.com/opensandbox-group/OpenSandbox/blob/main/server/configuration.md)).
  The server needs the Docker socket on the host; sandboxes do not get it.
- **Egress.** The strongest of any candidate. A sidecar in the sandbox network namespace:
  DNS filtering (default) or `dns+nft` packet-level default-deny, FQDN/wildcard/CIDR rules,
  operator `deny.always` floors, runtime mutation, fail-closed startup. Caveat: "gVisor's
  netstack cannot support this — Kata is the supported secure runtime". On Docker,
  `networkPolicy` requires bridge mode
  ([egress](https://github.com/opensandbox-group/OpenSandbox/blob/main/docs/architecture/network/egress.md)).
- **Secrets / lanes.** Plain env injection at create, plus **Credential Vault**: real
  credentials live in the egress sidecar and are injected into matching outbound HTTPS
  requests; the sandbox "only receives fake or empty credential values". Requires
  `dns+nft` and TLS interception
  ([credential-vault](https://github.com/opensandbox-group/OpenSandbox/blob/main/docs/guides/credential-vault.md)).
  This could keep the Claude OAuth token out of the workload entirely. Whether Claude
  Code's subscription login works with a placeholder token and header injection is
  **UNVERIFIED** and would need a spike.
- **Long runs.** Absolute TTL at create; `max_sandbox_timeout_seconds` optional; renew
  supported. Fits 30-120 min.
- **Logs / attach.** `execd` in-sandbox API for commands/files; endpoint resolution API.
  Log streaming of an entrypoint process to a control plane is via SDK command APIs
  (details UNVERIFIED).
- **Tenancy.** "Multi-tenancy is opt-in and Kubernetes-only" (server doc).
- **Weight.** Python FastAPI server (`uvx opensandbox-server`) + SQLite default + egress
  sidecar per sandbox. Light enough for a small VM (exact footprint UNVERIFIED).
- **Extension language.** Server Python; SDKs Python, Java/Kotlin, TS, C#, Go.
- **NN check.** NN1 partial (Docker + k8s, no cloud jobs). NN3 pass. NN9 partial (Docker
  local works; no cloud-job plugin). NN10 pass, with the gVisor/egress conflict noted.
  NN11 pass (Apache-2.0).
- **Verdict.** Worth considering as **one backend behind Satat's own interface** for
  Docker + k8s, mainly for its egress sidecar and credential vault. It does not remove the
  need to write ECS/Cloud Run/ACA drivers. Under "single operator" tenancy it is fine;
  under multi-tenant only on k8s. Adds a second control-plane service to run, which cuts
  against "cheapest deployment stays cheap".

### 3. E2B runtime (e2b-dev/runtime, formerly e2b-dev/infra) and E2B Embed

- **What it is.** "The complete backend that powers E2B Cloud": API, per-node orchestrator
  driving Firecracker, `envd` in each VM, edge proxy, template builder. Go, Apache-2.0
  ([README](https://github.com/e2b-dev/runtime)). Release 2026.30 on 2026-09-10; very
  active (push 2026-10-15).
- **Backends.** Its own Firecracker nodes only. **E2B Embed** packages "the whole stack ...
  on a single machine": Docker Compose on a Linux host, Terraform for one VM on GCP/AWS/
  Azure, or "one node in your Kubernetes cluster". "Embed is one machine by design; when one
  node is not enough" → E2B Enterprise ([embed/README](https://github.com/e2b-dev/runtime/blob/main/embed/README.md)).
  Not ECS/Cloud Run/ACA.
- **Isolation.** One Firecracker microVM per sandbox, own cgroup and netns. Strongest
  isolation here; no runtime socket inside.
- **Egress.** "Per-sandbox nftables egress firewall and SNI/Host-inspecting domain allow
  and deny lists" (README).
- **Secrets.** "Secrets are metadata-only on the control plane ... Workload identity gives a
  sandbox short-lived tokens" (README).
- **Startup.** Snapshot resume with lazy memory via `userfaultfd`; very fast.
- **Weight.** Linux with KVM and a 4 KiB-page kernel; nested virtualization on cloud VMs;
  "12 GiB RAM recommended and 20 GiB free disk"; Postgres, Redis, ClickHouse, object storage
  ([compose README](https://github.com/e2b-dev/runtime/blob/main/embed/compose/README.md)).
  Does not run on macOS Docker Desktop or on a small non-nested VM.
- **NN check.** NN1 fail (one runtime family; scaling beyond one node is Enterprise).
  NN3 pass. NN9 fail (Docker-only local not enough: needs KVM). NN10 pass. NN11 pass for
  Embed (Apache-2.0, "no license key needed"); multi-node is not self-hostable from the
  public recipe as documented.
- **Verdict.** Reject as Satat's runtime layer. Could be an *optional* "microVM" driver
  later for operators who have KVM hosts.

### 4. Daytona (OSS)

- **Status.** `daytonaio/daytona` is **archived**. README: "As of June 2026, Daytona's core
  development has moved to a private codebase. This repository will receive no further
  updates, fixes, or releases" ([repo](https://github.com/daytonaio/daytona)). Last release
  v0.190.0 on 2026-06-23.
- **License.** `LICENSE` at v0.190.0 is GNU AGPL v3 (AGPL-3.0). GitHub reports no SPDX
  because of the archive/COPYRIGHT layout. Helm charts are **Elastic License 2.0**
  ([daytonaio/helm-charts](https://github.com/daytonaio/helm-charts)).
- **Self-host shape.** `docker/docker-compose.yaml` with API, proxy, runner, SSH gateway,
  Postgres, Redis, Dex, registry, MinIO, MailDev, Jaeger, PgAdmin; "not safe to use in
  production"; "Sandbox resource limits are disabled ... in DinD"
  ([docker/README.md](https://github.com/daytonaio/daytona/blob/v0.190.0/docker/README.md)).
  Heavy for one small VM.
- **Verdict.** **Reject.** Fails maintenance under any license bar; Helm path fails the
  OSI bar.

### 5. Modal

- Managed only. Sandboxes "continue running on Modal's infrastructure"; isolation is gVisor
  or a KVM VM; default sandbox lifetime cap with snapshots recommended beyond 24 hours
  ([Sandboxes guide](https://modal.com/docs/guide/sandboxes),
  [networking](https://modal.com/docs/guide/sandbox-networking),
  [JS Sandbox ref](https://modal.com/docs/sdk/js/latest/Sandbox)). Client is Apache-2.0
  (`modal-labs/modal-client`); no self-hosted server is documented (absence of a
  self-host option: UNVERIFIED beyond the docs reviewed).
- **Verdict.** Fails NN11 ("No managed control plane we cannot run ourselves"). Reject.

### 6. Firecracker-family and other isolation building blocks

These are isolation options for a driver, not drivers.

- **gVisor (`runsc`)**: Apache-2.0, release-20261005.0 (2026-10-08). Docker integration is
  `runsc install` + `docker run --runtime=runsc`
  ([gVisor Docker quick start](https://gvisor.dev/docs/user_guide/quick_start/docker/)).
  Works on any Linux VM without KVM (systrap platform). Cheapest isolation upgrade for the
  single-VM tier. Conflicts with OpenSandbox's iptables/nft egress sidecar (see above);
  pairs fine with an egress *proxy* on a separate Docker network.
- **Kata Containers**: Apache-2.0, 4.2.0 (2026-09-15). k8s via RuntimeClass; supports QEMU,
  Cloud Hypervisor, Firecracker. Needs KVM. Docker use via containerd shim is possible
  (UNVERIFIED for Docker 27+).
- **Firecracker**: Apache-2.0, v1.17.0 (2026-09-10). VMM only; needs an orchestrator.
- **firecracker-containerd**: Apache-2.0, no GitHub releases, last commit 2026-07-16, warns
  multi-tenant use "at the user's own risk"
  ([README](https://github.com/firecracker-microvm/firecracker-containerd)). Low activity.
- **Flintlock**: MPL-2.0, v0.16.0 (2026-10-03). gRPC/HTTP microVM lifecycle on bare metal,
  Firecracker or Cloud Hypervisor; warm pools via "Battery"
  ([README](https://github.com/liquidmetal-dev/flintlock)). Built for k8s-node microVMs.
- **microsandbox**: Apache-2.0, v0.7.8 (2026-10-09), libkrun microVMs, embeddable, "Secrets
  That Can't Leak ... never enter the VM", but "still beta software"
  ([README](https://github.com/superradcompany/microsandbox)). Single host; needs KVM.
- **Arrakis** (abshkbh/arrakis): AGPL-3.0, last push 2025-06-02. Stale. Reject.

### 7. Cloud job APIs directly

| | ECS / Fargate `RunTask` | Cloud Run jobs | Azure Container Apps jobs |
|---|---|---|---|
| Isolation | Each Fargate task on "an isolated virtual environment"; no privileged, no `CAP_SYS_ADMIN`/`CAP_NET_ADMIN`; Fargate "prevents containers from accessing ... container runtime" ([Fargate security](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/fargate-security-considerations.html)) | Jobs "only use the second generation execution environment", a microVM ([execution environments](https://cloud.google.com/run/docs/about-execution-environments)) | Environment is "a secure boundary"; per-replica isolation details UNVERIFIED |
| Max run | No task time limit on Fargate (UNVERIFIED in this pass) | Task timeout up to 168 h ([task timeout](https://cloud.google.com/run/docs/configuring/task-timeout)) | `--replica-timeout` per job ([quickstart](https://learn.microsoft.com/en-us/azure/container-apps/jobs-get-started-cli)); max value UNVERIFIED |
| Secrets | `secrets` in task definition from Secrets Manager / SSM ([ECS secrets](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/specifying-sensitive-data-secrets.html)); task role for short-lived AWS creds | Secret Manager refs + per-job service account (UNVERIFIED page not fetched) | Job secrets + managed identity. **Warning:** anyone with `jobs/start/action` can override env in the execution template and reference any secret name of that job ([ACA jobs](https://learn.microsoft.com/en-us/azure/container-apps/jobs)) |
| Egress | Security groups / NACLs (IP-level); FQDN needs a proxy or AWS Network Firewall | VPC egress + firewall; FQDN needs a proxy (UNVERIFIED) | UDR + Azure Firewall (UNVERIFIED) |
| Logs / attach | CloudWatch / FireLens; ECS Exec for shell | Cloud Logging; exec into job tasks UNVERIFIED | Log Analytics; exec UNVERIFIED |

Lane enforcement on all three works the same way: **one job/task definition and one
identity per lane**. The Claude-lane definition references only the Claude token secret;
the gateway-lane definition references only the LiteLLM key. Because ECS `RunTask`
overrides and ACA start overrides can inject env, the driver must only ever start the
definition for the task's lane, and the controller identity should lack read on the other
lane's secret where possible. The ACA warning above means the Claude secret must live on a
job that open-harness steps never start.

### 8. Multi-backend abstractions (brief)

- **OpenHands workspace abstraction** (MIT): `openhands-workspace` ships `docker`,
  `agent_sandbox`, `apptainer`, `cloud` (OpenHands Cloud), `remote_api`
  ([source tree](https://github.com/OpenHands/software-agent-sdk/tree/main/openhands-workspace/openhands/workspace)).
  Assumes an OpenHands agent-server inside the workload; no ECS/Cloud Run/ACA. Not a fit as
  Satat's generic driver.
- **Kestra task runners**: Process and Docker are OSS; Kubernetes, AWS Batch, Google Cloud
  Run, Azure Batch "require an Enterprise Edition license"
  ([Kestra task runners](https://kestra.io/docs/task-runners/types)). Fails NN9/NN11 for
  the cloud backends.
- **Nomad**: BUSL-1.1 (`LICENSE` header; additional use grant forbids competing hosted
  offerings). v2.0.7 (2026-09-18). Fails OSI bar, passes source-available bar; adds a
  cluster to operate and still does not reach Fargate/Cloud Run/ACA. Reject.
- **Virtual Kubelet**: core Apache-2.0 v1.14.1 (2026-10-06). `azure-aci` provider last push
  2026-04-16; `aws-fargate` provider last push 2023-12-18 (dead). Would require a k8s
  control plane to reach cloud jobs. Reject.
- **Dagger** (Apache-2.0, v0.21.10): CI engine on BuildKit; runs containers on its own engine,
  not on ECS/Cloud Run/ACA. Wrong shape.
- **SkyPilot** (Apache-2.0, v0.14.0): targets k8s and cloud VMs/GPU clusters, not
  serverless container jobs (UNVERIFIED for 2026 additions). Wrong shape.
- **Coder** (AGPL-3.0, v2.37.4): Terraform-provisioned human workspaces; OSI-compliant but
  copyleft, heavy, and workspace-shaped rather than task-shaped. Reject for this layer.
- **Cloudflare Sandbox SDK**: "A sandbox is a Durable Object and the Container it starts"
  ([README](https://github.com/cloudflare/sandbox-sdk)). Runs only on Cloudflare. Fails NN11.
- **OpenKruise Agents** (Apache-2.0, v0.6.0, CNCF sub-project): k8s operator with pooling,
  hibernation, E2B-compatible API, "compatibility with Sig Agent-Sandbox"
  ([README](https://github.com/openkruise/agents)). Pre-1.0 alternative to agent-sandbox; k8s only.

## Verdicts under open questions

- **License bar (Q4).** OSI-only: agent-sandbox, OpenSandbox, E2B, gVisor, Kata, Firecracker
  pass; Nomad (BUSL) and Daytona Helm (Elastic-2.0) fail; Coder/Daytona core (AGPL) pass OSI
  but impose copyleft on modified network services. Source-available allowed: Nomad becomes
  admissible but is still rejected on weight and coverage. The recommendation does not change.
- **Tenancy (Q6).** Single operator: any of the above. Multi-tenant: our driver needs a
  tenant/namespace field and per-tenant lane identities; OpenSandbox's multi-tenancy is
  k8s-only; agent-sandbox gets it from namespaces. Recommendation unchanged; add `tenant` to
  the spec.
- **Extension language (Q7).** agent-sandbox has Go/Python/TS SDKs; OpenSandbox Python server
  with Go/Python/TS SDKs; cloud SDKs exist in all three. No candidate forces a language.
  Go gives the best k8s client story; Python matches OpenSandbox's server.
- **Live steering (Q5).** If logs + cancel + retry suffice: every backend supports it (log
  stream + stop). If live steering is required: runtime-level attach is uneven (Docker attach,
  k8s attach/exec, ECS Exec yes; Cloud Run jobs and ACA jobs UNVERIFIED). So steering should
  be a **harness-level channel** where the workload dials out to the control plane (for
  example ACP or a WebSocket) through the egress allowlist, not a runtime feature. This
  keeps every driver equal.

## Recommendation

**Define Satat's own thin workload-driver interface** and ship Docker (local and single VM),
Kubernetes, ECS/Fargate, Cloud Run jobs, and Azure Container Apps jobs implementations.
No single sandbox project spans Docker + k8s + the three cloud job services. The closest
(OpenSandbox) covers Docker + k8s only, and E2B/Daytona/Modal fail on weight, maintenance,
or self-hosting.

Adopt projects *inside* implementations where they earn it:

- Docker driver: plain Docker Engine API, optional `runsc` (gVisor) runtime, workload on an
  `--internal` network whose only route out is a per-lane egress proxy container (FQDN
  allowlist: GitHub, the lane's model endpoint, package registries). The Satat worker holds
  the Docker socket; workloads never get it. A socket proxy limiting the API surface, or
  rootless Docker, reduces the worker's blast radius.
- Kubernetes driver: plain `Pod`/`Job`, or `agent-sandbox` `SandboxClaim` when warm pools
  are wanted; RuntimeClass gVisor/Kata; NetworkPolicy default-deny egress plus egress
  proxy (or Cilium FQDN policy).
- Optional later: an OpenSandbox-backed driver (for its egress sidecar and credential
  vault) and an E2B Embed driver for KVM hosts.

### Interface sketch

```text
WorkloadSpec
  id, tenant, repo, step            # labels for GC and the control plane
  lane: "claude-subscription" | "gateway"
  image, command, args, workdir
  env: map<string,string>           # non-secret only; driver rejects secret-looking keys
  secrets: [SecretRef{name, as: env|file}]   # must be in the lane's allowlist, else fail closed
  resources: {cpu, memory, disk}
  timeout                           # hard deadline (120 min default)
  isolation: container | gvisor | microvm    # driver rejects what it cannot honour
  egress: {default: deny, allow: [fqdn|cidr], via_proxy: bool}
  callback: {control_plane_url, task_token}  # for logs/steering dial-out

Driver
  capabilities() -> {isolation[], egress: fqdn|cidr|none, attach: bool, warm_pool: bool}
  start(spec) -> Handle              # idempotent on spec.id
  watch(handle) -> stream<Event>     # Pending, Running, Succeeded(code), Failed(reason), Lost
  logs(handle, follow, since) -> stream<line>
  cancel(handle, grace)              # SIGTERM, then kill
  teardown(handle)                   # remove workload, volumes, per-task secrets
  list(labels) -> [Handle]           # reconcile orphans after controller restart
```

Lane enforcement lives in the driver, not in callers: each lane maps to a fixed identity
(ECS task definition and task role, Cloud Run job and service account, ACA job and managed
identity, k8s ServiceAccount and Secret, Docker secret set). The Claude-lane identity is the
only one that can read the Claude OAuth token. Per-task short-lived credentials: a GitHub App
installation token (about one hour) minted by the control plane per task and passed as a
secret, plus cloud workload identity for any cloud API calls.

## Sources

- Requirements: `docs/research/v2/platform-requirements.md`
- agent-sandbox: https://github.com/kubernetes-sigs/agent-sandbox ,
  https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0 ,
  https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.1 ,
  https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/api/v1beta1/sandboxtemplate_types.go ,
  https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/api/v1beta1/sandboxclaim_types.go
- OpenSandbox: https://github.com/opensandbox-group/OpenSandbox ,
  https://github.com/opensandbox-group/OpenSandbox/blob/main/docs/architecture/control-plane/server.md ,
  https://github.com/opensandbox-group/OpenSandbox/blob/main/docs/architecture/network/egress.md ,
  https://github.com/opensandbox-group/OpenSandbox/blob/main/docs/guides/credential-vault.md ,
  https://github.com/opensandbox-group/OpenSandbox/blob/main/server/configuration.md
- E2B: https://github.com/e2b-dev/runtime ,
  https://github.com/e2b-dev/runtime/blob/main/embed/README.md ,
  https://github.com/e2b-dev/runtime/blob/main/embed/compose/README.md
- Daytona: https://github.com/daytonaio/daytona ,
  https://github.com/daytonaio/daytona/blob/v0.190.0/LICENSE ,
  https://github.com/daytonaio/daytona/blob/v0.190.0/docker/README.md ,
  https://github.com/daytonaio/helm-charts
- Modal: https://modal.com/docs/guide/sandboxes , https://modal.com/docs/guide/sandbox-networking ,
  https://modal.com/docs/guide/vm-sandboxes , https://modal.com/docs/sdk/js/latest/Sandbox
- Cloudflare: https://github.com/cloudflare/sandbox-sdk
- Isolation: https://gvisor.dev/docs/user_guide/quick_start/docker/ ,
  https://github.com/google/gvisor , https://github.com/kata-containers/kata-containers ,
  https://github.com/firecracker-microvm/firecracker ,
  https://github.com/firecracker-microvm/firecracker-containerd ,
  https://github.com/liquidmetal-dev/flintlock , https://github.com/superradcompany/microsandbox ,
  https://github.com/abshkbh/arrakis
- Cloud jobs: https://docs.aws.amazon.com/AmazonECS/latest/developerguide/fargate-security-considerations.html ,
  https://docs.aws.amazon.com/AmazonECS/latest/developerguide/specifying-sensitive-data-secrets.html ,
  https://cloud.google.com/run/docs/configuring/task-timeout ,
  https://cloud.google.com/run/docs/about-execution-environments ,
  https://learn.microsoft.com/en-us/azure/container-apps/jobs ,
  https://learn.microsoft.com/en-us/azure/container-apps/jobs-get-started-cli
- Abstractions: https://github.com/OpenHands/software-agent-sdk/tree/main/openhands-workspace/openhands/workspace ,
  https://kestra.io/docs/task-runners/types , https://github.com/hashicorp/nomad/blob/main/LICENSE ,
  https://github.com/virtual-kubelet/virtual-kubelet , https://github.com/virtual-kubelet/azure-aci ,
  https://github.com/virtual-kubelet/aws-fargate , https://github.com/dagger/dagger ,
  https://github.com/skypilot-org/skypilot , https://github.com/coder/coder ,
  https://github.com/openkruise/agents
