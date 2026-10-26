# A library that launches a step on any runtime (#213)

Part of #166. Question: is there a maintained Go (preferred) or Python library that launches one Satat step on Docker, Kubernetes and the cloud job runners (ECS/Fargate, Cloud Run jobs, Azure Container Apps jobs) behind one API, so Satat puts one adapter behind `launcher.Launcher` instead of one launcher per runtime? Source-read and docs-read only; nothing was run.

Read on 2026-10-26 and 2026-10-27: GitHub repo and release metadata (`gh api`) for every candidate; Apache Libcloud `libcloud/container` at trunk `bd9cbb3`; Prefect 3.9.0 `src/prefect/workers/base.py` and `src/integrations/prefect-docker/prefect_docker/worker.py` plus the integration package listings; the Virtual Kubelet `azure-aci` provider at `382bbaa`; the `aws-fargate`, `nomad-driver-ecs` and `compose-cli` READMEs; the Nomad `LICENSE`; and the official docs pages listed under Sources. Earlier work this builds on: [agent-platform-options.md](agent-platform-options.md) (runtime layer table, Stack C) and #81 (ECS Fargate, closed not planned).

## Answer

**No candidate clears the bar. Keep Docker + Kubernetes behind `launcher.Launcher` (ADR 0026), and keep Kubernetes as the cross-cloud route (ADR 0022 NN1).** No library covers Docker, Kubernetes and all three cloud job services. The only project that comes close on runtimes, Prefect, is a scheduler with a server, not a library, and it puts the Prefect API URL and key into every step and starts a second container on a name conflict instead of adopting the first. Libcloud is the only true multi-runtime library, and its container API is too thin to run a step (no exit code, no command or limits on Kubernetes, no Fargate on ECS). A bridge under the Kubernetes launcher (EKS Fargate, AKS virtual nodes) is the better route to serverless capacity than a multi-runtime library, but it drops controls Satat relies on (network policy, gVisor, and on ACI the whole security context), so it is a later option, not a reason to change the ADRs now.

## The bar

From `launcher/launcher.go`, `launcher/conformance/conformance.go`, [ADR 0026](../../adr/0026-docker-first-runtime.md) and [ADR 0027](../../adr/0027-docker-api-access.md):

1. **Lifecycle.** Start, exit code, stdout and stderr separately, cancel, timeout, OOM reported, output files read back after exit.
2. **Identity.** Deterministic from `(Run, Name)`, so a re-delivered step adopts the running or finished workload instead of starting a second one (conformance "duplicate launches share one execution").
3. **Cleanup.** Workload removed on every exit path; labelled leftovers reaped after a worker crash.
4. **Hardening.** uid 10001, `CapEff` 0, `NoNewPrivs` 1, read-only root, memory, CPU and pids limits, image pinned by digest (conformance "step is locked down").
5. **Inputs.** `/etc/satat/input.json` and the repo tarball placed as files, no host mounts, no credential beyond what the lane passes.
6. **One API holder.** Only the worker holds the runtime API; the step cannot reach it or the orchestrator.
7. **Network.** An internal lane has no route out (conformance "internal lane has no route out").
8. **Maintained, OSI licence** (ADR 0022 Q4).

"Library" here means something the Go worker (or a Python sidecar) calls in-process against the runtime API. A scheduler or platform needs its own server and agents, and Satat already has one scheduler (Hatchet, ADR 0023).

## Candidates

| Candidate | Kind | Lang | Runtimes it really supports | Latest / activity | Licence | Verdict |
|---|---|---|---|---|---|---|
| [Apache Libcloud](https://github.com/apache/libcloud) container API | **Library** | Python | Docker, Kubernetes, ECS (EC2 launch type only, see below), GKE, LXD, Rancher. No Cloud Run, no ACA, no Fargate | v3.9.1 tag 2026-04-16; last change under `libcloud/container` 2026-03-24, the one before 2025-03-02 | Apache-2.0 | **Reject.** Too thin to run a step |
| [Prefect](https://github.com/PrefectHQ/prefect) workers | **Scheduler + server** | Python | Docker, Kubernetes, ECS, Cloud Run (v1, v2), Vertex AI, Azure **Container Instances**. No ACA jobs | 3.9.0, 2026-10-21; daily commits | Apache-2.0 | **Reject.** Needs a server; puts API credentials in the step; duplicates on conflict |
| [SkyPilot](https://github.com/skypilot-org/skypilot) | **Platform** (API server) | Python | Kubernetes, Slurm, cloud VMs, existing machines. No local Docker target, no ECS, Cloud Run or ACA | v0.14.1, 2026-10-19 | Apache-2.0 | **Reject.** VM and cluster launcher, not a container job launcher |
| [Nomad](https://github.com/hashicorp/nomad) task drivers | **Scheduler** (servers + client agents) | Go | Docker, exec, Java, QEMU, raw exec; plugins Exec2, Podman, Virt. No Kubernetes. The ECS remote driver is archived and experimental | v2.0.7, 2026-09-18 | **BUSL-1.1** | **Reject.** Fails the licence bar and has no Kubernetes or cloud job driver |
| [Virtual Kubelet](https://github.com/virtual-kubelet/virtual-kubelet) providers | **Bridge** under Kubernetes | Go | `azure-aci` (ACI) maintained; `aws-fargate` inactive since 2023-12 | core v1.14.1 2026-10-06; `azure-aci` v1.6.4 2026-02-18, last push 2026-04-16 | Apache-2.0 | **Not a library.** See "Bridge under the Kubernetes launcher" |
| [testcontainers-go](https://github.com/testcontainers/testcontainers-go) | Library | Go | Docker-API-compatible engines only (Docker, Podman, Colima, Rancher Desktop) | v0.44.0, 2026-08-07 | MIT | **Reject.** Docker only, adds nothing over the Engine API Satat already calls |
| [Dagger](https://github.com/dagger/dagger) | Engine + SDKs | Go | Runs containers inside its own engine; the engine runs on Docker, Podman or a Kubernetes pod and needs `CAP_SYS_ADMIN` | v0.21.11, 2026-10-21 | Apache-2.0 | **Reject.** Steps run inside a privileged engine, not on the runtime |
| [Lithops](https://github.com/lithops-cloud/lithops) (found in this search) | Library | Python | Kubernetes, Knative, AWS Batch, Cloud Run, Azure Container Apps, Code Engine, FaaS and VMs. No Docker target listed, no ECS/Fargate | 3.8.0, 2026-09-24 | Apache-2.0 | **Reject.** Runs Python functions through object storage, not a container command |
| [Docker Compose cloud integrations](https://github.com/docker-archive/compose-cli) (found in this search) | CLI | Go | ECS and ACI from a Docker context | v1.0.35, 2023-06-21; **archived**, retired November 2023 | Apache-2.0 | **Reject.** Retired |

Already rejected in [agent-platform-options.md](agent-platform-options.md) and not reassessed: Kestra (Kubernetes and cloud runners are Enterprise), OpenSandbox (Docker and Kubernetes only, kept as a later backend), Modal and other managed-only sandboxes. A web search for a Go library spanning Docker, Kubernetes, ECS and Cloud Run found none (search only; absence is not proof).

## Row by row against the bar

V = verified from the linked source this session. U = unverified. Libcloud and Prefect are the only two worth checking cell by cell: the rest fail on runtimes, licence or model before the bar is reached.

| Bar | Libcloud container API | Prefect workers |
|---|---|---|
| Runtimes | Docker, Kubernetes, ECS, GKE, LXD, Rancher (V, [`providers.py`](https://github.com/apache/libcloud/blob/bd9cbb3e393bc42f57684481e7c62e9293007584/libcloud/container/providers.py)). ECS `RunTask` is sent with only `count` and `taskDefinition` (V, [`ecs.py` `ex_start_task`](https://github.com/apache/libcloud/blob/bd9cbb3e393bc42f57684481e7c62e9293007584/libcloud/container/drivers/ecs.py)); without `launchType` and `networkConfiguration` it cannot target Fargate (inference from the ECS API, not run) | Docker, Kubernetes, ECS, Cloud Run v1/v2, Vertex, ACI (V, [work pools](https://docs.prefect.io/v3/concepts/work-pools); integration packages `prefect-aws/ecs_worker.py`, `prefect-gcp/cloud_run_v2.py`, `prefect-azure/container_instance.py` at 3.9.0). No ACA jobs (V, absent from both) |
| Start / exit code / logs / cancel | Start, stop, destroy (V). Logs on Docker only (`ex_get_logs`). **No exit code anywhere**: `Container` holds id, name, image, state, IPs, `extra` (V, [`base.py`](https://github.com/apache/libcloud/blob/bd9cbb3e393bc42f57684481e7c62e9293007584/libcloud/container/base.py)) | Exit code reaches the worker as `BaseWorkerResult.status_code` (V, [`workers/base.py`](https://github.com/PrefectHQ/prefect/blob/3.9.0/src/prefect/workers/base.py)). Cancel is via the server: the worker polls for cancelling runs (V, same file). Separate stdout/stderr and output files back to the caller: U |
| Deterministic identity, adopt | Caller picks the name (V). Adopt on conflict: not provided, would be ours | **Fails on Docker.** On a name conflict the worker retries as `name-1`, `name-2`, ... (V, [`prefect_docker/worker.py` L1192-L1210](https://github.com/PrefectHQ/prefect/blob/3.9.0/src/integrations/prefect-docker/prefect_docker/worker.py#L1192-L1210)). The worker proposes a `Pending` state to the server before submitting (`_propose_pending_state`, V); that this is what stops a second launch is our inference. Either way de-duplication lives in the server, not the runtime |
| Cleanup after crash | `destroy_container` (V). Labels on Kubernetes: no, the pod spec has name and image only (V). Reaper would be ours | Labels `prefect.io/flow-run-id` on every workload (V). A reaper for orphans: U |
| Non-root, cap drop, read-only root, limits, digest | **Kubernetes driver sends `{"metadata":{"name"},"spec":{"containers":[{"name","image"}]}}` and nothing else**: no command, user, securityContext or limits (V, [`kubernetes.py` `deploy_container`](https://github.com/apache/libcloud/blob/bd9cbb3e393bc42f57684481e7c62e9293007584/libcloud/container/drivers/kubernetes.py)). Docker driver takes `user` and `mem_limit` but no read-only root, cap drop or pids (V, `docker.py` `deploy_container` signature) | Docker worker exposes `mem_limit`, `privileged` and a free-form `container_create_kwargs` passed to docker-py (V, `prefect_docker/worker.py`), so the floor is expressible. Kubernetes uses a templated job manifest (U for this session). Digest pinning: image is a string (V) |
| Inputs as a file, no host mounts | No file-copy API on any driver (V, method list of the three drivers) | **Host bind mount on Docker** when the work pool has no storage: the worker adds `f"{submission_dir}:/tmp/"` to the container's volumes (V, `prefect_docker/worker.py` L670-L687). Volumes also merge from per-run `job_variables` (V, same lines) |
| No credential in the step | Not applicable (nothing injected) | **Fails.** Every workload gets `get_current_settings().to_environment_variables(exclude_unset=True)`, the worker's own Prefect settings, which include `PREFECT_API_URL` and, when set, `PREFECT_API_KEY` (V, `_base_environment` in `workers/base.py`). The default command is `prefect flow-run execute`, which reports to that API from inside the step (V). Conflicts with ADR 0027 and NN10, as agent-platform-options.md flagged |
| Only the worker holds the runtime API | Yes, it is an in-process client (V by design) | The worker holds runtime credentials (V), but a second API, Prefect's, is reachable from the step (above) |
| Internal network | Docker: `network_mode`, `network_disabled` (V). Kubernetes, ECS: none (V) | Docker: `networks`, `network_mode` (V). Others: U |
| Server needed | No | **Yes**: a worker refuses to start without `PREFECT_API_URL` (V, `workers/base.py` L1244) |
| Maintained, licence | Container API mostly idle since 2023 (V, commit history); Apache-2.0 | Very active; Apache-2.0 |

What each would cost Satat (inference): Libcloud would mean writing the identity, adoption, exit-code, log, input-copy and hardening code per runtime anyway, in Python, behind a Go interface; the "one adapter" saves nothing. Prefect would mean a second scheduler beside Hatchet, a Prefect server to run and secure, and splitting the runner from the harness so the agent never sees the Prefect key.

### The others, briefly

- **SkyPilot** provisions clusters of VMs or Kubernetes pods across clouds and Slurm; it does not list ECS, Cloud Run, ACA or a local Docker daemon as targets ([overview](https://docs.skypilot.ai/en/latest/overview.html)). Every client talks to an API server, started locally in the background if none is found ([API server docs](https://docs.skypilot.ai/en/latest/reference/api-server/api-server.html)). A platform, and the wrong unit (cluster, not container step).
- **Nomad** is BUSL-1.1 for v1.7.0 and later ([LICENSE](https://github.com/hashicorp/nomad/blob/main/LICENSE)), which fails the OSI bar. Its drivers are Docker, exec, Java, QEMU and raw exec, with Exec2, Podman and Virt as plugins; none targets Kubernetes or a cloud job service ([task drivers](https://developer.hashicorp.com/nomad/docs/job-declare/task-driver)). The ECS remote driver was "experimental ... Do not run this in production" and is archived ([nomad-driver-ecs](https://github.com/hashicorp/nomad-driver-ecs)).
- **testcontainers-go** "requires a Docker-API compatible container runtime" ([docs](https://golang.testcontainers.org/system_requirements/docker/)). Docker only.
- **Dagger** runs containers inside its runner, and "the runner container currently needs root capabilities", including `CAP_SYS_ADMIN` ([custom runner](https://docs.dagger.io/reference/configuration/custom-runner)). The runner can be a Kubernetes pod (`kube-pod://`), but the step is then a container inside a privileged engine, not a workload the runtime isolates.
- **Lithops** has Kubernetes, Knative, AWS Batch, Cloud Run and Azure Container Apps backends ([compute backends](https://lithops-cloud.github.io/docs/source/compute_backends.html)), the widest serverless spread found. But the unit is a Python callable passed to `call_async` or `map`, with function, data and results kept in a storage backend ([futures API](https://lithops-cloud.github.io/docs/source/api_futures.html)). A Claude Code or opencode step is a container command, and object-storage credentials would sit with the runtime. No Docker target.
- **Docker Compose cloud integrations** (ECS, ACI) "retired in November 2023" ([README](https://github.com/docker-archive/compose-cli)).

## Bridge under the Kubernetes launcher

The alternative to a multi-runtime library is one Kubernetes launcher that schedules onto serverless capacity. Two live options, both run by the cloud provider; the Virtual Kubelet Fargate provider is not one ("currently **inactive** and is seeking maintainers", [README](https://github.com/virtual-kubelet/aws-fargate)).

| | EKS on Fargate | AKS virtual nodes (v1 add-on, on Virtual Kubelet `azure-aci`) | AKS Virtual Nodes v2 |
|---|---|---|---|
| Status | GA | GA; the docs point new work to v2 | **Preview**, "not meant for production use" |
| Isolation | One VM per pod, own kernel (V, [EKS Fargate](https://docs.aws.amazon.com/eks/latest/userguide/fargate.html)) | ACI container group (V, [virtual nodes](https://learn.microsoft.com/azure/aks/virtual-nodes)) | ACI (V, [v2](https://learn.microsoft.com/azure/aks/virtual-nodes-v2)) |
| Pod security context | Privileged, `HostPort`, `HostNetwork` refused (V). Non-root, cap drop, read-only root: U | **Dropped.** `getContainers` copies name, image, command, ports, volume mounts and env into the ACI container; `securityContext` is never read anywhere in `pkg/` (V, [`pkg/provider/aci.go` L1146](https://github.com/virtual-kubelet/azure-aci/blob/382bbaad88c324d73f9da88f1919629254e0f0bd/pkg/provider/aci.go#L1146)). Satat's uid, cap drop and read-only root would be silently ignored (inference from source) | U |
| NetworkPolicy (internal lanes) | **Not enforced**: VPC CNI policies apply "to Amazon EC2 Linux nodes only. You can't apply the policies to Fargate" (V, [network policy](https://docs.aws.amazon.com/eks/latest/userguide/cni-network-policy.html)). Per-pod security groups are the substitute (V) | **Not supported** (V, limitations list) | Cilium or NPM refused; Calico or none required (V). Whether Calico is enforced on ACI pods: U |
| gVisor / RuntimeClass | U (Fargate fixes the runtime; inference) | U | U |
| Lifecycle, identity, cleanup | Plain pods and Jobs, so the Kubernetes launcher's adopt-by-name and reaper apply unchanged (inference) | Same (inference) | Same (inference) |

So the bridge keeps one launcher and the conformance suite, which is its advantage over any library. Its cost is that the Kubernetes launcher's controls are partly replaced by the provider's: a VM per pod instead of gVisor, security groups instead of NetworkPolicy, and on AKS v1 no security context at all. The conformance tests "step is locked down" and "internal lane has no route out" would have to run against the bridged nodes before any lane used them.

## Recommendation

1. **Keep Docker + Kubernetes behind `launcher.Launcher`. No ADR change.** No library launches a container step on Docker, Kubernetes, ECS/Fargate, Cloud Run jobs and ACA jobs. Libcloud lacks exit codes and Kubernetes hardening and cannot reach Fargate. Prefect is a second scheduler that puts its API credentials into every step and does not adopt on conflict. Adopting either would mean rebuilding most of what the Docker launcher already does, plus security work that ADR 0027 rules out.
2. **When serverless capacity is wanted, prefer the bridge over a new launcher.** EKS on Fargate is the most credible: GA, a VM per pod, plain pods. It needs a lane variant that relies on per-pod security groups for egress and is rerun through the conformance suite on Fargate nodes. Avoid AKS virtual nodes v1 (drops the security context, no network policy) and v2 until it leaves preview.
3. **If a direct ECS, Cloud Run or ACA launcher is ever needed,** write it against the cloud's own SDK as a third `launcher.Launcher` implementation and gate it on the conformance suite, as agent-platform-options.md's "thin driver" row proposed. That reverses ADR 0022 NN1 and needs its own ADR; nothing found here makes it cheaper.

## Not verified

- Prefect Kubernetes, ECS, Cloud Run and ACI workers' handling of name conflicts, security context, logs and output files (only the base worker and the Docker worker were read).
- Whether Libcloud's ECS driver can reach Fargate through any `ex_` path (the read covered `deploy_container` and `ex_start_task`).
- EKS Fargate support for `runAsNonRoot`, `readOnlyRootFilesystem`, dropped capabilities and `pids` limits; gVisor on any bridge.
- AKS Virtual Nodes v2's handling of `securityContext` and Calico enforcement on ACI pods.
- That no maintained Go library exists: established by search and by the candidates above, not exhaustively.

## Sources

- Satat: `launcher/launcher.go`, `launcher/conformance/conformance.go`, ADR 0022, 0026, 0027, [agent-platform-options.md](agent-platform-options.md), `docs/roadmap/runtime-scaling.md`, issues #81 and #213.
- Apache Libcloud: https://github.com/apache/libcloud/tree/bd9cbb3e393bc42f57684481e7c62e9293007584/libcloud/container (`providers.py`, `base.py`, `drivers/docker.py`, `drivers/kubernetes.py`, `drivers/ecs.py`); commit history of `libcloud/container`.
- Prefect 3.9.0: https://github.com/PrefectHQ/prefect/blob/3.9.0/src/prefect/workers/base.py, https://github.com/PrefectHQ/prefect/blob/3.9.0/src/integrations/prefect-docker/prefect_docker/worker.py, https://github.com/PrefectHQ/prefect/tree/3.9.0/src/integrations, https://docs.prefect.io/v3/concepts/work-pools
- SkyPilot: https://docs.skypilot.ai/en/latest/overview.html, https://docs.skypilot.ai/en/latest/reference/api-server/api-server.html
- Nomad: https://github.com/hashicorp/nomad/blob/main/LICENSE, https://developer.hashicorp.com/nomad/docs/job-declare/task-driver, https://github.com/hashicorp/nomad-driver-ecs
- Virtual Kubelet: https://github.com/virtual-kubelet/virtual-kubelet, https://github.com/virtual-kubelet/azure-aci (README, `pkg/provider/aci.go` at `382bbaa`), https://github.com/virtual-kubelet/aws-fargate
- testcontainers-go: https://golang.testcontainers.org/system_requirements/docker/
- Dagger: https://docs.dagger.io/reference/configuration/custom-runner
- Lithops: https://lithops-cloud.github.io/docs/source/compute_backends.html, https://lithops-cloud.github.io/docs/source/api_futures.html
- Docker Compose cloud integrations: https://github.com/docker-archive/compose-cli
- AWS: https://docs.aws.amazon.com/eks/latest/userguide/fargate.html, https://docs.aws.amazon.com/eks/latest/userguide/cni-network-policy.html
- Azure: https://learn.microsoft.com/azure/aks/virtual-nodes, https://learn.microsoft.com/azure/aks/virtual-nodes-v2
