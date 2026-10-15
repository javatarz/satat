# Research: Kubernetes everywhere + agent-sandbox as Satat's workload runtime

Status: scratch research, 2026-10-15. Question: can Satat drop its own Docker/ECS/Cloud Run/ACA
drivers and use Kubernetes as the only runtime API (k3d on a laptop, k3s on one VM, EKS/GKE/AKS
in clouds), with `kubernetes-sigs/agent-sandbox` providing one sandbox per workflow step?
Checked against `docs/research/v2/platform-requirements.md` (NN1, NN3, NN9, NN10, NN11, "cheapest
deployment must stay cheap"). Builds on `docs/research/v2/notes/platforms-runtime.md` and
`docs/research/v2/agent-platform-options.md`.

The agent-sandbox source was read at commit `807cc37` (main, 2026-10-15). Repo and release
metadata came from `gh` on 2026-10-15. Anything not confirmed in a primary source is marked
**UNVERIFIED**.

## Summary table

| Shape | Isolation runtime available | Egress enforcement | Lane identity / short-lived creds | Control-plane cost | Caveats |
|---|---|---|---|---|---|
| **Laptop: k3d** (k3s in Docker) | **runc only in practice.** gVisor works in kind on a *Linux* host when host `runsc` is mounted into the node container (documented by agent-sandbox for kind). Nobody documents it for k3d or for macOS Docker Desktop/colima: **UNVERIFIED** | k3s embedded kube-router netpol controller is on by default. IP/CIDR only, no FQDN. In-cluster egress proxy for allowlists | Namespace + ServiceAccount + k8s Secret per lane. No cloud identity. Per-task GitHub tokens need a broker or claim env | $0 | Needs Docker plus the `k3d` binary (and kubectl/helm, or a bootstrap container). k3d releases are slow (v5.8.3 Feb 2025, then v5.9.0 Jun 2026). On macOS the Docker VM adds a kernel boundary from the Mac host, but pods share the VM kernel |
| **Single VM: k3s** (current t4g.xlarge, arm64) | **gVisor (runsc, systrap platform) via RuntimeClass.** arm64 supported. You add a containerd template by hand; k3s does not auto-detect runsc. **No Kata on t4g**: EC2 nested virtualization covers Intel families only (C7i/M7i/R7i/C8i/M8i/R8i…), not Graviton | kube-router netpol (default on). FQDN via an egress proxy, or by replacing flannel with Cilium (more RAM) | SA + Secret per lane namespace. No EKS Pod Identity (EKS-only). Instance role stays out of reach because the managed NetworkPolicy blocks 169.254.0.0/16 | $0 extra (same VM) | No HA (one node, SQLite/kine). Spot interruption kills running sandboxes, and Hatchet retries them. RAM overhead about 1.5 to 2.5 GB before workloads (estimate) |
| **EKS** | Managed node groups: gVisor via launch-template user data (community pattern, not an AWS-documented feature). Kata only on nested-virt Intel instances or metal. **Auto Mode: no custom AMI, so gVisor is effectively unavailable (UNVERIFIED for 2026)**. Fargate: VM per pod by design | VPC CNI network policy (v1.21+, opt-in). Not applied to Fargate. Enforcement can be unreliable for pods without ownerReferences, but agent-sandbox pods do have them. FQDN: egress proxy or Cilium | **EKS Pod Identity** per lane ServiceAccount (not on Fargate) or IRSA. ESO / Secrets Store CSI for Secrets Manager | **$0.10/h per cluster (~$73/mo)**, $0.60/h on extended support | The most DIY of the three clouds for gVisor. Fargate: no arm64, private subnets + NAT required, no DaemonSets |
| **GKE** | **GKE Sandbox (gVisor)**: Standard needs a separate non-default COS node pool, Arm supported. **Autopilot: gVisor pre-configured.** GKE ships a managed Agent Sandbox (`--enable-agent-sandbox`, beta) | Dataplane V2 network policy (default on Autopilot, opt-in on Standard). **FQDN NetworkPolicy** is native on Dataplane V2 | **Workload Identity Federation** (always on in Autopilot). Required for gVisor pods to reach Google APIs | $0.10/h per cluster, **covered by a $74.40/mo free-tier credit for one zonal or Autopilot cluster** | Best fit. Regional clusters pay the fee. `kubectl port-forward` does not work under gVisor |
| **AKS** | **Pod Sandboxing (Kata, `kata-vm-isolation`)**: Azure Linux only, Gen2 nested-virt VM sizes, **no arm64**. The pod VM's memory is fixed at the pod limit (default 512Mi) | **Cilium** (recommended). Azure NPM on Linux is retired 2028-09-30. FQDN filtering via Cilium/ACNS (ACNS pricing **UNVERIFIED**) | **AKS Workload Identity**; Key Vault via Secrets Store CSI or ESO | **Free tier $0** (no SLA, fewer than 10 nodes recommended). Standard ~$0.10/h (third-party figures; official page did not render) | Virtual nodes (ACI) are not viable: no NetworkPolicy, no init containers, no managed identity |

**Verdict.** The plan satisfies NN3, NN11 and "cheapest stays cheap". It satisfies NN1 and NN9 only if those NNs are reworded: NN1 currently names ECS/Cloud Run/ACA, and NN9's "nothing but Docker" becomes "Docker + k3d". NN10 is met on GKE, AKS and the k3s VM (gVisor). It is weaker on the laptop (runc + restricted PSS) and needs custom node bootstrap on EKS. The code Satat still owns is a thin launcher (about 150 to 300 LOC) plus manifests (details below).

## 1. agent-sandbox: install, API, SDK, task semantics

### Install and footprint

- **What gets installed.** `sandbox-with-extensions.yaml` installs 4 CRDs (`sandboxes.agents.x-k8s.io`, and `sandboxclaims`, `sandboxtemplates`, `sandboxwarmpools` under `extensions.agents.x-k8s.io`) and a controller Deployment `agent-sandbox-controller` in `agent-sandbox-system`. Helm and OLM are also offered ([README](https://github.com/kubernetes-sigs/agent-sandbox#installation)). The Sandbox Router (an HTTP reverse proxy to sandbox pods) is optional ([sandbox-router/](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/sandbox-router)).
- **Controller footprint.** No default requests or limits. The Helm `values.yaml` suggests requests of 10m/64Mi and limits of 500m/128Mi (commented out) ([helm/values.yaml](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/helm/values.yaml)). Real RSS on a small cluster is **UNVERIFIED** (likely about 50 to 150 MB, typical for controller-runtime).
- **arm64.** Release images are built for `linux/amd64,linux/arm64` ([dev/tools/push-images](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/dev/tools/push-images)).
- **Minimum Kubernetes version: not documented.** The README, Helm `Chart.yaml` (no `kubeVersion`) and docs give none. The project builds against `k8s.io/api v0.37.1` / controller-runtime v0.25.1, and the VAP tests use envtest 1.36.2. Current k3s lines are v1.35.9 and v1.34.12 (2026-09-30), so a supported k3s is close to what upstream builds against. **UNVERIFIED** whether k3s 1.34 works.
- **k3s/k3d testing.** Upstream CI, the quickstarts and the examples all use **kind** ([docs/testing.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/testing.md), [examples/quickstart/gvisor.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/examples/quickstart/gvisor.md)). The only k3s mention is the `urunc-sandbox` example's `--flavor k3s` overlay. No k3d mention, and no k3s or k3d issues in the tracker (searched 2026-10-15). k3s is a conformant distribution and the controller only creates Pods, Services and NetworkPolicies, so no blocker is expected. Formally it is **untested upstream**.
- **Maturity.** v1.0.0 (2026-08-28) made `v1beta1` the only served version and removed `v1alpha1`. Upgrading required a staged path through v0.5.x plus a storage migration ([api-migration-guide.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/api-migration-guide.md)). Since then there have been weekly patches up to v1.0.6 (2026-10-08). Apache-2.0, SIG Apps.

### Who owns the pod spec

- `SandboxTemplate.spec` embeds `SandboxBlueprint.podTemplate`, a full `PodTemplateSpec`. Image, command, args, env, `envFrom`/`secretKeyRef`, volumes, `runtimeClassName`, `serviceAccountName`, securityContext and resources all come from the **template** ([sandboxtemplate_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/api/v1beta1/sandboxtemplate_types.go), [sandbox_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/api/v1beta1/sandbox_types.go)). With templates, `automountServiceAccountToken` defaults to `false` unless set.
- A **`SandboxClaim`** can set only these: `warmPoolRef` (required; claims target a `SandboxWarmPool`, which references a template), `lifecycle` (`shutdownTime`, `ttlSecondsAfterFinished`, `shutdownPolicy` Delete/DeleteForeground/Retain, default **Retain**), `additionalPodMetadata` (labels limited to an allowlisted domain, default `sandbox.users.io`; annotations allowed except in system domains), `env` (name/value **literals only**, no `valueFrom`), and `volumeClaimTemplates` ([sandboxclaim_types.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/api/v1beta1/sandboxclaim_types.go)). A claim **cannot** change the image, command, ServiceAccount, secrets or runtimeClass, so lanes are fixed by the template.
- **`envVarsInjectionPolicy`** (default **Disallowed**):
  - Disallowed rejects any claim that sets `spec.env`.
  - `Allowed` lets a claim add names the template does not define but never change a template-defined value.
  - `Overrides` permits both.
  - Any claim that sets env forces a cold start; it cannot adopt a warm pod.
- **A warm pool with `replicas: 0` is fine.** The claim falls back to "Cold path: no sandbox found, creating from template" (`sandboxclaim_controller.go` ~L668). Several examples use `replicas: 0`.

### Getting per-step inputs in when env injection is Disallowed

1. **Annotations + Downward API (works with Disallowed).** The worker sets `additionalPodMetadata.annotations` (e.g. `satat.dev/task: <json>`), and the template mounts a `downwardAPI` volume exposing that annotation as a file. Source confirms that claim metadata is merged into the pod template on both the cold and warm-adoption paths (`mergePodMetadata`, L1383/L1395/L1831). Annotations are capped at 256 KiB in total (a Kubernetes limit) and are readable by anyone who can `get pods`, so use them for non-secret inputs only (repo, branch, task id, prompt ref). The Downward API pattern for this exact purpose is **UNVERIFIED** in agent-sandbox docs; it is inferred from source.
2. **`envVarsInjectionPolicy: Allowed` + a ValidatingAdmissionPolicy.** The claim adds `SATAT_REPO`, `SATAT_TASK_ID` and similar. The risk: a claim could add a credential-shaped name the template did not define, e.g. `ANTHROPIC_API_KEY` on the Claude lane. Two mitigations:
   - The template pre-defines the dangerous names (empty), and under `Allowed` they cannot be overridden.
   - A VAP on `sandboxclaims` restricts env names to `SATAT_*`. agent-sandbox already ships VAP examples ([examples/policy/vap](https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/policy/vap)).
   Values are plaintext in the claim object, which lives in etcd.
3. **Pull model.** The entrypoint calls a Satat endpoint with a projected, audience-scoped SA token to fetch its task and a per-task GitHub token. This is the cleanest home for *per-task secrets*, at the cost of a small token-broker service in Satat. `octo-sts/app` (Apache-2.0, v0.12.0, 2026-10-09) is an existing OIDC-to-GitHub-token exchanger. Whether it fits a laptop k3d issuer is **UNVERIFIED**.

Recommendation: use option 1 for inputs. For the per-task GitHub token, use option 3 (or option 2 if a 1-hour, repo-scoped token in the claim object is acceptable).

### One-shot task semantics: supported

- The core Sandbox controller sets a **`Finished`** condition (`PodSucceeded`/`PodFailed`) once the pod reaches a terminal phase. The claim controller mirrors it onto the `SandboxClaim` (`syncFinishedCondition`), and `ttlSecondsAfterFinished` keys off it ([sandbox_types.go L124-131](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/api/v1beta1/sandbox_types.go)).
- Issue [#1577](https://github.com/kubernetes-sigs/agent-sandbox/issues/1577) (closed 2026-09-10, docs PR #1587) documents "Ephemeral batch / one-shot → `restartPolicy: Never` → TTL fires on pod exit". With the default `Always`, the pod never finishes and TTL never fires.
- Exit code: the claim has no exit-code field. Read it from the pod's `containerStatuses[].state.terminated.exitCode`. New sandboxes name their pod after the Sandbox, and the claim's `status.sandbox.name` gives that name.
- Logs: plain `pods/log` via the Kubernetes API. The SDKs do not wrap logs.
- Cancel: delete the claim (`DeleteForeground` lets the caller watch teardown).

### SDKs

- **Python** `k8s-agent-sandbox`: `SandboxClient.create_sandbox(warmpool, namespace, sandbox_ready_timeout=180, labels, claim_name, shutdown_after_seconds, pod_labels, pod_annotations, env, …)` → handle with `.commands.run()` returning stdout/stderr/exit_code, file read/write, `delete_sandbox` ([python_sdk_reference.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/python_sdk_reference.md)). `commands.run` assumes an **in-sandbox runtime server** (sandboxd or the Python runtime), reached through the Sandbox Router, a gateway or a port-forward tunnel. `kubectl port-forward` does **not** work under gVisor ([openclaw-gvisor README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/examples/openclaw-gvisor-sandbox/README.md)), so exec under gVisor needs the router.
- **Go** (`sigs.k8s.io/agent-sandbox/clients/go/sandbox`) and **TypeScript** (`clients/typescript`) exist with similar scope.
- **For Satat**, the one-shot model is simpler than exec: the harness runs as the container command. A Hatchet worker can create the claim, watch `Finished`, read the exit code and stream `pods/log` with the plain Kubernetes client (`CustomObjectsApi` + `CoreV1Api`). That needs no router and no in-pod server, and the SDK's ready-wait (which expects a long-lived pod) becomes optional.

### Managed NetworkPolicy defaults (relevant to lanes)

- `networkPolicyManagement: Managed` (default) creates one shared NetworkPolicy per template.
- If `networkPolicy` is omitted, the "secure default" applies: ingress only from the Sandbox Router; egress to the public internet only, **blocking 10/8, 172.16/12, 192.168/16 and 169.254/16** (`sandboxtemplate_controller.go` ~L236).
- That **also blocks cluster DNS, an in-cluster LiteLLM and node-local DNS** ([dns_poc_walkthrough.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/examples/policy/network-policy-management/dns_poc_walkthrough.md)). On k3s the pod CIDR (10.42/16) and service CIDR (10.43/16) both sit inside 10/8.
- So every Satat template must give explicit egress rules: DNS, LiteLLM (open lane) or an egress proxy, and nothing else.
- `Unmanaged` hands networking to Cilium etc. The controller sets a controller ownerReference on pods (`sandbox_controller.go` L1398), which matters for EKS VPC CNI enforcement (see §3).

## 2. Isolation runtimes per platform

| Platform | gVisor | Kata | Notes |
|---|---|---|---|
| k3s (VM) | Yes. Install `runsc` + `containerd-shim-runsc-v1`, add a runsc runtime in k3s's containerd template, create a RuntimeClass `gvisor` (handler `runsc`). k3s auto-detects crun/nvidia/wasm runtimes but **not runsc or kata** ([k3s advanced](https://docs.k3s.io/advanced)). Template filename (`config.toml.tmpl` vs `config-v3.toml.tmpl` for containerd 2.x) is from third-party guides: **UNVERIFIED** | Needs KVM. t4g (Graviton) has no nested virt. Nested virt exists on Intel C8i/M8i/R8i (Feb 2026) and C7i/M7i/R7i/… (Jun 2026) ([AWS Feb](https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual), [AWS Jun](https://aws.amazon.com/about-aws/whats-new/2026/06/nested-virtualization-intel-us-gov-cloud/)) | gVisor supports x86_64 and ARM64 (Linux 5.6+) ([install](https://gvisor.dev/docs/user_guide/install/)). The default systrap platform needs no virtualization and is "a better choice when running inside a VM" ([platforms](https://gvisor.dev/docs/architecture_guide/platforms/)) |
| k3d / kind | kind on a Linux host: documented by agent-sandbox (containerd patch + host `runsc` mounted into the node). k3d: same idea via a k3s containerd template + volume mount. **No documentation; UNVERIFIED**. macOS: runsc would have to run inside the Docker Desktop/colima Linux VM. **UNVERIFIED / treat as unsupported** | No (needs KVM inside a container) | Laptop default should be runc |
| EKS managed node groups | Community pattern: launch-template user data or a custom AMI installs runsc ([gVisor issue #8047](https://github.com/google/gvisor/issues/8047), [devopscube](https://devopscube.com/set-up-gvisor-on-aws-eks/)). Not an AWS-documented feature | Nested-virt Intel instances or metal. **UNVERIFIED** on EKS-optimized AMIs | Graviton nodes: gVisor arm64 OK |
| EKS Auto Mode | No custom AMI (AWS-managed Bottlerocket), so no runsc. Source is a [re:Post answer](https://repost.aws/questions/QUsX411EWrQwyZocI6CFTqxA/eks-auto-mode-custom-amis); 2026 status **UNVERIFIED** | No | |
| EKS Fargate | n/a. Each pod gets its own VM and kernel ([EKS Fargate](https://docs.aws.amazon.com/eks/latest/userguide/fargate.html)). RuntimeClass behaviour on Fargate: **UNVERIFIED** (no doc found) | n/a | No arm64, private subnets + NAT only, no DaemonSets, no IMDS (IRSA only) |
| GKE Standard | GKE Sandbox node pool (COS only, not the default pool). Arm supported ([sandbox-pods](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods)) | GKE also offers a `microvm` sandbox type on nested-virt nodes | |
| GKE Autopilot | Pre-configured ([feature comparison](https://docs.cloud.google.com/kubernetes-engine/docs/resources/autopilot-standard-feature-comparison)). Managed Agent Sandbox enforces via VAP: `runtimeClassName: gvisor`, `automountServiceAccountToken: false`, `runAsNonRoot`, drop ALL ([GKE Agent Sandbox](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/agent-sandbox), [enable](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/how-install-agent-sandbox)) | — | |
| AKS | Not offered as a managed option | `kata-vm-isolation`. Azure Linux only, Gen2 nested-virt sizes (e.g. D4s_v3), k8s ≥1.27, **no arm64**, no FIPS/Trusted Launch, no host network. The doc carries no preview label as of the 2026-10-02 revision ([use-pod-sandboxing](https://learn.microsoft.com/en-us/azure/aks/use-pod-sandboxing)) | Needs a dedicated node pool + nodeSelector/taint |

**Without a sandbox runtime** (laptop, EKS Auto Mode), what remains is runc plus:

- Pod Security Standards `restricted` on lane namespaces: non-root, drop ALL caps, no privilege escalation, seccomp RuntimeDefault, no hostPath/hostNetwork/hostPID.
- No runtime socket mounted (the template controls volumes, and the claim cannot add any).
- `automountServiceAccountToken: false`.
- Default-deny egress, resource limits, read-only root FS where the harness allows it.

This is the "container is not a security boundary" tier (EKS docs say so explicitly). A kernel exploit escapes to the node. Acceptable for a laptop running one's own tasks. Weak for a shared or cloud host.

## 3. Egress restriction

| Platform | NetworkPolicy out of the box | FQDN allowlist |
|---|---|---|
| k3s / k3d | **Yes.** "K3s includes an embedded network policy controller… kube-router's netpol controller library", disabled only by `--disable-network-policy` ([k3s networking-services](https://docs.k3s.io/networking/networking-services)). Open k3d issue [#1513](https://github.com/k3d-io/k3d/issues/1513): kube-router netpol misbehaves after cluster restart (2024, k3d 5.7.3) | No. Use an in-cluster egress proxy (Squid/Envoy/smokescreen; harness uses `HTTPS_PROXY`; NetworkPolicy allows only proxy + DNS), or replace flannel with Cilium (`toFQDNs`, more RAM; **UNVERIFIED** figure) |
| EKS | VPC CNI network policy (≥ v1.21, must be enabled). EC2 Linux nodes only, **not Fargate**. Enforcement "might not work reliably" for pods without `ownerReferences` (agent-sandbox pods have them). 24 port/protocol combos per CIDR. Admin `ClusterNetworkPolicy` tier exists ([cni-network-policy](https://docs.aws.amazon.com/eks/latest/userguide/cni-network-policy.html)) | No native FQDN in VPC CNI. Proxy or Cilium |
| GKE | Dataplane V2: default on Autopilot, opt-in on Standard | **FQDNNetworkPolicy** on Dataplane V2 (≥1.26.4/1.27.1). Standard and Autopilot (Autopilot: enable on update). Max 50 IPs per policy, kube-dns/Cloud DNS only ([fqdn-network-policies](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/fqdn-network-policies)). Compatibility with gVisor not stated: **UNVERIFIED** |
| AKS | Must choose an engine: Cilium (recommended), NPM (Linux retired 2028-09-30), Calico ([use-network-policies](https://learn.microsoft.com/en-us/azure/aks/use-network-policies)) | Cilium FQDN filtering via Advanced Container Networking Services (paid add-on; price **UNVERIFIED**) |

Portable answer: the **same egress-proxy pattern on every shape**, with plain NetworkPolicy allowing only DNS + proxy (+ LiteLLM on the open lane). FQDN NetworkPolicy on GKE or Cilium is an optimisation, not a requirement. Per-lane allowlists live in proxy config, keyed on the source namespace or ServiceAccount.

## 4. Per-lane credentials

- **Same-namespace boundary.** "Anyone who is authorized to create a Pod in a namespace can use that access to read any Secret in that namespace" ([Secrets](https://kubernetes.io/docs/concepts/configuration/secret/)). The lane boundary is therefore **namespace + who may create pods there**. Use one namespace per lane (`satat-lane-claude`, `satat-lane-gateway`), each with its own SandboxTemplate, ServiceAccount and Secrets. Pods can mount Secrets only from their own namespace (standard Kubernetes; the exact doc sentence was not re-fetched).
- **Default RBAC.** "Default RBAC policies… grant *no permissions* to service accounts outside the `kube-system` namespace (beyond… API discovery roles)" ([RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)). Together with `automountServiceAccountToken: false`, a sandbox pod cannot read any Secret through the API, its own or another lane's.
- **Worker RBAC is the real enforcement point.** The Hatchet worker gets `create/get/watch/delete` on `sandboxclaims`, and `get` on `pods` and `pods/log`, in lane namespaces. It gets **nothing** on `pods` create, `sandboxes`, `sandboxtemplates`, `secrets` or `serviceaccounts`. A creator of bare `Sandbox` objects or templates could pick any ServiceAccount or Secret in the namespace (the agent-sandbox [threat model](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/security/threat_model.md) says to restrict these via RBAC). Templates are applied by GitOps/Terraform only.
- **Cloud secret stores.** External Secrets Operator (Apache-2.0, chart 2.12.0, 2026-10-06) or Secrets Store CSI Driver (Apache-2.0, v1.5.7, 2026-09-15) sync Secrets Manager / Secret Manager / Key Vault into each lane namespace using the *ESO controller's* cloud identity. The lane pods never get cloud credentials. On laptop/k3s, plain k8s Secrets are created from GitHub repo secrets by the deploy workflow (ADR 0011 still holds: not in git, not in Terraform state).
- **Short-lived cloud identity** (only if a lane needs cloud APIs):
  - EKS Pod Identity: one IAM role per ServiceAccount; agent on 169.254.170.23; not on Fargate; EKS-only, so not on k3s-on-EC2 ([pod-identities](https://docs.aws.amazon.com/eks/latest/userguide/pod-identities.html)). Its link-local address is blocked by agent-sandbox's secure default, so allow `169.254.170.23/32:80` explicitly if used.
  - GKE Workload Identity Federation (always on in Autopilot; required for gVisor pods to reach Google APIs).
  - AKS Workload Identity.
  Today's lanes (Claude OAuth token, LiteLLM virtual key, GitHub App token) are not cloud credentials, so this mostly matters for ESO, not for sandboxes.
- **Claude OAuth token** is a long-lived Secret only in the Claude lane namespace. The **LiteLLM key** lives only in the gateway lane. **GitHub tokens** are per task and short-lived via a broker (§1 option 3).

## 5. Costs and footprint

- **Control plane (2026):**
  - EKS: $0.10/h standard, $0.60/h extended support; Auto Mode adds a per-instance fee ([EKS pricing](https://aws.amazon.com/eks/pricing/)).
  - GKE: $0.10/h per cluster; **$74.40/mo free-tier credit per billing account = one zonal Standard or Autopilot cluster**; regional clusters pay in full ([GKE pricing](https://cloud.google.com/kubernetes-engine/pricing). The page did not render, so figures are from search snippets quoting it).
  - AKS: Free tier = free cluster management, no SLA, "fewer than 10 nodes" recommended ([AKS tiers](https://learn.microsoft.com/en-us/azure/aks/free-standard-pricing-tiers)). Standard ~$0.10/h and Premium ~$0.60/h per third-party sources (the official price page did not render).
- **Minimum practical node footprint:**
  - EKS: 1 to 2 small EC2 nodes (gVisor needs custom user data).
  - GKE Autopilot: pay per pod request, no idle nodes.
  - GKE Standard: needs ≥2 pools (default + sandbox pool), so ≥2 nodes.
  - AKS Kata: Gen2 nested-virt x86 nodes (D4s_v3-class) plus a system pool.
  - Fargate: also needs a NAT gateway (hourly + data charges).
- **k3s requirements:** server minimum 2 cores / 2 GB; agent 1 core / 512 MB; arm64 supported ([requirements](https://docs.k3s.io/installation/requirements)). Measured server-with-workload RSS is ~1.6 GB, but that includes Prometheus + Grafana + nginx (v1.26.5, c6id.xlarge and Pi 4) ([resource profiling](https://docs.k3s.io/reference/resource-profiling)), so it is an upper bound for k3s plus a monitoring stack.
- **t4g.xlarge (4 vCPU / 16 GB, arm64) budget (estimates, UNVERIFIED):**

  | Component | RAM |
  |---|---|
  | k3s server (no monitoring) | 0.7 to 1.2 GB |
  | agent-sandbox controller | ~0.1 GB |
  | Hatchet engine + API | 0.3 to 0.8 GB |
  | Postgres | 0.3 to 1 GB |
  | LiteLLM | 0.4 to 1 GB |
  | Egress proxy | <0.1 GB |
  | **Platform total** | **~2 to 4 GB** |

  That leaves ~12 GB for sandboxes: about 4 to 6 concurrent harness pods at 2 GB each, plus small per-sandbox gVisor sentry overhead. Every component has arm64 builds (k3s, gVisor, agent-sandbox, Hatchet **UNVERIFIED arm64 image**, Postgres).

## 6. Does "Docker-only local install" hold with k3d?

- k3d needs Docker ≥20.10.5 plus the `k3d` binary (brew, script, release binary). kubectl is needed to interact ([k3d.io](https://k3d.io/stable/)). Strictly, that is "Docker + one static binary". A Satat `bootstrap` container image bundling k3d/kubectl/helm and talking to the Docker socket would make it literally Docker-only. The bootstrap gets the host socket, but no sandbox does.
- k3d: MIT, v5.9.0 (2026-06-02), active pushes. Release cadence is slow (16 months between v5.8.3 and v5.9.0). Pin the `rancher/k3s` image explicitly.
- macOS gotchas:
  - Docker Desktop and colima VM memory caps bound sandbox concurrency.
  - Open issue [#1624](https://github.com/k3d-io/k3d/issues/1624): multiple k3d clusters on colima fail.
  - Podman on macOS has open failures (#1447, #1388).
  - kube-router netpol after restart (#1513).
  - No gVisor on macOS (§2).
- Alternative: kind is what agent-sandbox upstream tests on. Swapping k3d for kind on the laptop costs nothing architecturally and aligns with upstream CI. k3d's advantage is that it is the same distribution (k3s) as the VM shape.

## 7. Serverless-ish Kubernetes

| Option | Can agent-sandbox run? | RuntimeClass | NetworkPolicy | Fit |
|---|---|---|---|---|
| EKS Fargate | Controller is a plain Deployment, so yes. Sandbox pods land on Fargate if a profile matches | VM per pod already; RuntimeClass handling **UNVERIFIED** | **No** (VPC CNI policy not applied to Fargate). Security groups for pods only (IP-based) | Poor: no egress policy, no Pod Identity, no arm64, NAT required |
| GKE Autopilot | **Yes, first-class.** GKE ships managed Agent Sandbox (`--enable-agent-sandbox`, beta) | gVisor pre-configured | Dataplane V2 default + FQDN policy | **Best serverless fit**, and the cluster fee is covered by the free tier |
| AKS virtual nodes (ACI) | Controller runs on a real node; pods could target virtual nodes | No Kata | **No** NetworkPolicy, no init containers, no managed identity ([virtual-nodes](https://learn.microsoft.com/en-us/azure/aks/virtual-nodes)). Virtual Nodes v2 is preview | Not viable |

## 8. Risks and mitigations

1. **agent-sandbox API churn.** v1alpha1 to v1beta1 needed a staged migration within the last two months, and the API is still beta. *Mitigation:* Satat touches only `SandboxClaim` create/watch/delete and pod status/logs. Templates are plain YAML. Pin the release and upgrade deliberately.
2. **Untested on k3s/k3d upstream; no documented minimum Kubernetes version.** *Mitigation:* run Satat's own CI on k3d (or kind) + the k3s version used on the VM. Or use kind on the laptop to match upstream.
3. **Managed NetworkPolicy secure default breaks DNS and in-cluster LiteLLM.** *Mitigation:* explicit egress rules per template (§1).
4. **Lane bypass through RBAC.** Anyone who can create pods, Sandboxes or templates in a lane namespace can read its Secrets. *Mitigation:* the worker gets claim-only RBAC, and templates are GitOps-only.
5. **Single-node k3s.** No HA, embedded SQLite, Spot interruption. *Mitigation:* the cluster holds no durable state (Hatchet's Postgres does; back it up or put it on a persistent EBS volume). Sandboxes are retried by Hatchet. Reprovision with Terraform + cloud-init.
6. **Upgrade burden.** k3s minors every ~4 months; EKS/GKE/AKS force upgrades (EKS extended support costs 6x). *Mitigation:* GKE/AKS auto-upgrade channels; on the VM, k3s system-upgrade-controller or a reprovision.
7. **NN10 gap on laptop and EKS.** *Mitigation:* document that the laptop is runc + restricted PSS. On EKS, use a gVisor node group via user data, or accept Fargate/Auto Mode trade-offs.
8. **k3d maintenance cadence.** *Mitigation:* kind is a drop-in alternative.
9. **Exec model and gVisor.** If live steering (open question 5) later needs exec into running sandboxes, it needs the Sandbox Router, because port-forward fails under gVisor.

## Verdict against the non-negotiables

| NN | Result | Notes |
|---|---|---|
| NN1 pluggable isolated per-task workloads | **Pass with rewording** | One pod per step, no shared FS, capacity via nodes. "Pluggable" moves from Satat drivers to cluster flavour + RuntimeClass. ECS/Cloud Run/ACA are dropped as targets and replaced by EKS/GKE/AKS (+ Autopilot as the serverless option). The NN text must change |
| NN3 lanes enforced by secrets | **Pass** | Template per lane fixes ServiceAccount, Secrets and image. Claims cannot touch them. Worker RBAC is claim-only. Env injection Disallowed; inputs via annotations |
| NN9 local install with nothing but Docker | **Pass with caveat** | Docker + k3d binary (or a bootstrap container). The "every dependency is a plugin" spirit holds: the runtime plugin becomes the cluster flavour |
| NN10 untrusted code | **Pass on k3s VM (gVisor), GKE (gVisor), AKS (Kata). Partial on laptop (runc + PSS) and EKS (gVisor needs custom node bootstrap; Fargate lacks NetworkPolicy)** | No host access, no socket (template controls volumes), default-deny egress + proxy, no SA token. Short-lived creds via a broker or workload identity |
| NN11 self-hosted, license | **Pass** | agent-sandbox, k3s, gVisor, Kata, ESO, CSI driver, Cilium, octo-sts: Apache-2.0. k3d: MIT. k3s means no managed control plane is required |
| Cheapest stays cheap | **Pass** | Same t4g.xlarge, no new spend, ~2 to 4 GB RAM platform overhead (estimate). GKE zonal/Autopilot and AKS Free add $0 control-plane cost; EKS adds ~$73/mo |

## What Satat still owns

- **Launcher (~150 to 300 LOC, Python in the Hatchet worker):**
  - Create a `SandboxClaim` (lane → warm pool, annotations for inputs, `lifecycle.shutdownTime` as a hard timeout, `shutdownPolicy: Delete`).
  - Watch `Ready`/`Finished`, read the exit code from pod status, stream `pods/log` to the control plane, delete the claim on cancel.
  - Plain Kubernetes client; the agent-sandbox SDK is optional.
- **Harness entrypoint in each image:** reads `/etc/satat/task` (Downward API), runs the harness, exits non-zero on failure. Satat already owns this; it is the harness adapter.
- **Manifests (no driver code):**
  - Lane namespaces with PSS `restricted`.
  - Per lane: ServiceAccount, Secret or ExternalSecret, SandboxTemplate (`restartPolicy: Never`, runtimeClassName, explicit egress), and SandboxWarmPool (`replicas: 0`).
  - Worker Role/RoleBinding (claims + pods/log only).
  - VAP on claims.
  - RuntimeClass.
  - Egress proxy + per-lane allowlists.
  - Per-shape bootstrap: k3d config; k3s cloud-init/Terraform with the runsc containerd template; EKS/GKE/AKS Terraform.
- **Optional token broker** for per-task GitHub tokens (or adopt octo-sts).

## Sources

agent-sandbox (source read at commit 807cc37, 2026-10-15)
- README / install: https://github.com/kubernetes-sigs/agent-sandbox
- SandboxTemplate types: https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/api/v1beta1/sandboxtemplate_types.go
- SandboxClaim types: https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/api/v1beta1/sandboxclaim_types.go
- Sandbox types (Finished condition): https://github.com/kubernetes-sigs/agent-sandbox/blob/main/api/v1beta1/sandbox_types.go
- Claim controller: https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/controllers/sandboxclaim_controller.go
- Template controller (secure default CIDRs): https://github.com/kubernetes-sigs/agent-sandbox/blob/main/extensions/controllers/sandboxtemplate_controller.go
- Python SDK reference: https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/python_sdk_reference.md
- API migration guide: https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/api-migration-guide.md
- Threat model: https://github.com/kubernetes-sigs/agent-sandbox/blob/main/docs/security/threat_model.md
- gVisor on kind quickstart: https://github.com/kubernetes-sigs/agent-sandbox/blob/main/examples/quickstart/gvisor.md
- openclaw-gvisor example (port-forward under gVisor): https://github.com/kubernetes-sigs/agent-sandbox/blob/main/examples/openclaw-gvisor-sandbox/README.md
- DNS / network policy walkthrough: https://github.com/kubernetes-sigs/agent-sandbox/blob/main/examples/policy/network-policy-management/dns_poc_walkthrough.md
- VAP examples: https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/policy/vap
- Helm values: https://github.com/kubernetes-sigs/agent-sandbox/blob/main/helm/values.yaml
- Issue #1577 (restartPolicy × lifecycle): https://github.com/kubernetes-sigs/agent-sandbox/issues/1577
- Releases: https://github.com/kubernetes-sigs/agent-sandbox/releases

k3s / k3d / gVisor / Kata
- k3s requirements: https://docs.k3s.io/installation/requirements
- k3s resource profiling: https://docs.k3s.io/reference/resource-profiling
- k3s advanced (alternative runtimes, containerd templates): https://docs.k3s.io/advanced
- k3s networking services (embedded netpol): https://docs.k3s.io/networking/networking-services
- k3s basic network options: https://docs.k3s.io/networking/basic-network-options
- k3d: https://k3d.io/stable/ ; issues #1513, #1624, #1447, #1388 at https://github.com/k3d-io/k3d/issues
- gVisor install: https://gvisor.dev/docs/user_guide/install/
- gVisor platforms: https://gvisor.dev/docs/architecture_guide/platforms/
- gVisor containerd quick start: https://gvisor.dev/docs/user_guide/containerd/quick_start/
- gVisor on EKS (community): https://github.com/google/gvisor/issues/8047 , https://devopscube.com/set-up-gvisor-on-aws-eks/
- k3s + gVisor templates (third-party, unverified): https://oneuptime.com/blog/post/2026-03-20-k3s-containerd/view
- EC2 nested virtualization: https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual , https://aws.amazon.com/about-aws/whats-new/2026/06/nested-virtualization-intel-us-gov-cloud/

AWS
- EKS pricing: https://aws.amazon.com/eks/pricing/
- EKS Fargate: https://docs.aws.amazon.com/eks/latest/userguide/fargate.html
- VPC CNI network policy: https://docs.aws.amazon.com/eks/latest/userguide/cni-network-policy.html
- EKS Pod Identity: https://docs.aws.amazon.com/eks/latest/userguide/pod-identities.html
- Auto Mode custom AMIs (re:Post): https://repost.aws/questions/QUsX411EWrQwyZocI6CFTqxA/eks-auto-mode-custom-amis

Google Cloud
- GKE pricing: https://cloud.google.com/kubernetes-engine/pricing
- GKE Sandbox: https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods
- Autopilot vs Standard features: https://docs.cloud.google.com/kubernetes-engine/docs/resources/autopilot-standard-feature-comparison
- Autopilot security: https://docs.cloud.google.com/kubernetes-engine/docs/concepts/autopilot-security
- FQDN network policies: https://docs.cloud.google.com/kubernetes-engine/docs/how-to/fqdn-network-policies
- GKE Agent Sandbox: https://docs.cloud.google.com/kubernetes-engine/docs/how-to/agent-sandbox , https://docs.cloud.google.com/kubernetes-engine/docs/how-to/how-install-agent-sandbox

Azure
- AKS pricing tiers: https://learn.microsoft.com/en-us/azure/aks/free-standard-pricing-tiers
- AKS pricing (third-party figures for Standard/Premium): https://cloudchipr.com/blog/aks-pricing
- AKS Pod Sandboxing: https://learn.microsoft.com/en-us/azure/aks/use-pod-sandboxing
- AKS network policies: https://learn.microsoft.com/en-us/azure/aks/use-network-policies
- AKS virtual nodes: https://learn.microsoft.com/en-us/azure/aks/virtual-nodes

Kubernetes
- RBAC (default SA permissions): https://kubernetes.io/docs/reference/access-authn-authz/rbac/
- Secrets (pod-create implies secret read): https://kubernetes.io/docs/concepts/configuration/secret/
- Secrets good practices: https://kubernetes.io/docs/concepts/security/secrets-good-practices/

Other projects (gh metadata 2026-10-15)
- External Secrets Operator (Apache-2.0, chart 2.12.0): https://github.com/external-secrets/external-secrets
- Secrets Store CSI Driver (Apache-2.0, v1.5.7): https://github.com/kubernetes-sigs/secrets-store-csi-driver
- octo-sts (Apache-2.0, v0.12.0): https://github.com/octo-sts/app
- Kata Containers (Apache-2.0, 4.2.0): https://github.com/kata-containers/kata-containers
- Cilium (Apache-2.0, v1.20.3): https://github.com/cilium/cilium
