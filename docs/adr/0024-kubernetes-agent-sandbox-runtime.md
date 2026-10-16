# ADR 0024: Kubernetes with agent-sandbox as the workload runtime

## Status

Accepted (2026-10-16). Settles the runtime proposal left open in
[ADR 0021](0021-adopt-platform-plumbing.md) and
[ADR 0022](0022-v2-platform-requirement-decisions.md) (NN1). Based on the agent-sandbox spike
(#103, [findings](../research/v2/agent-sandbox-spike.md)) and the platform RAM spike (#104,
[findings](../research/v2/platform-ram-spike.md)). The options are compared in
[runtime notes](../research/v2/notes/k8s-runtime.md). It does not choose a host: where the
cluster runs is a separate decision.

## Context

ADR 0021 says Satat adopts its workload runtime instead of writing a driver per backend.
Each step needs its own isolated workload with a per-lane network policy, started, watched,
cancelled and cleaned up by the launcher. The research proposed Kubernetes as the one runtime
API, with `kubernetes-sigs/agent-sandbox` providing the per-step sandbox. Open questions were
whether agent-sandbox works one-shot on k3s, whether gVisor works, and whether the platform
leaves room for steps on one node.

## Decision

Satat runs every step as a one-shot `SandboxClaim` on Kubernetes, using agent-sandbox. The
same API runs on k3d on a laptop, k3s on a single VM, and a managed cluster later.

How Satat uses it, as verified in the spikes:

- **One-shot claims.** The launcher creates a claim, watches for the `Finished` condition,
  reads the exit code from the pod (`containerStatuses[0].state.terminated.exitCode`, not on
  the claim), reads `pods/log`, and deletes the claim. No router or in-pod server.
- **Inputs.** Non-secret inputs go in a claim annotation, exposed as a file by the Downward
  API (`/etc/podinfo/...`). The template sets `envVarsInjectionPolicy: Disallowed`; a claim
  that sets `env` is rejected.
- **One namespace and one `SandboxTemplate` per lane.** Pod Security `restricted` is
  enforced, with no service-account token. Managed network policy has explicit egress: DNS
  and the named backends only.
- **Worker RBAC.** Create, get, watch and delete on `sandboxclaims`; get on `pods` and
  `pods/log`; nothing on pod creation, sandboxes, templates, secrets or service accounts.
  Templates are applied by configuration management only.
- **Cleanup.** Finished claims are removed by `ttlSecondsAfterFinished` with
  `shutdownPolicy: Delete`. A claim that never starts has no TTL, so the launcher deletes
  every claim it creates, on every exit path.
- **Isolation.** gVisor (`runsc`, systrap) through a RuntimeClass on k3s. The laptop keeps
  plain container isolation (ADR 0022).

Conditions the build must meet:

- **Network policy start-up gap.** k3s's embedded policy controller applied rules 2 to 4
  seconds after pod start, and a short step saw no enforcement at all. The lane entrypoint
  waits until a known-denied address is refused and fails closed on timeout, before the
  harness does anything that matters.
- **gVisor on k3s is a three-part install:** copy `runsc`, `containerd-shim-runsc-v1` and the
  `gvisor-bin/` directory into the node, and register the runtime in
  `config-v3.toml.tmpl` (k3s 1.34 uses containerd 2) with
  `runtime_type = "io.containerd.runsc.v1"`. Releases ship as tarballs. This was proven on
  k3d in a Linux VM, not on a real k3s node.
- **Idempotent launcher.** Hatchet re-delivers a task if the platform stalls (#104). The
  claim name is deterministic from the run and step, `AlreadyExists` means adopt, and the
  claim is deleted on every exit path.
- **Platform pods have resource requests, and Postgres and LiteLLM have limits.** Without
  them, memory exhaustion from steps restarted Hatchet, LiteLLM and the worker (#104). The
  global cap is sized from node allocatable memory minus the platform's share, about
  2.4 GiB, and the measured harness memory per step.

## Consequences

- No per-backend drivers. Cloud Run, ACA jobs and ECS are not targets (ADR 0022). A host
  that cannot run Kubernetes needs a new ADR.
- The platform (k3s, Hatchet, Postgres, LiteLLM, worker) uses about 2.4 GiB idle. A 16 GiB
  node leaves about 13 GiB for steps. Concurrency is set by the real harness's memory and
  CPU per step, which is not measured yet (follow-up spike).
- Footprint is small: the agent-sandbox controller used 17 MiB. A pod was Running in 1 to
  2 s up to 4 concurrent steps and 3 to 5 s at 8 on a healthy node.
- A single node has no HA. The cluster holds no durable state apart from Postgres, so a
  node loss is recovered by reprovisioning and Hatchet retries. Spot or preemptible nodes
  kill running steps.
- Restarting a k3d node deleted ordinary pods in the spike, so a laptop restart needs
  workloads re-applied. Finished claims are unaffected.
- The agent-sandbox API is pre-stable and moving: pin by version and upgrade deliberately.

## Not verified

- gVisor on a real k3s node, and whether the lane image runs the harness under it. Only a
  busybox-class image ran.
- The real harness's RAM and CPU per step, and the concurrency it allows (spike to follow).
- Egress allowlists by hostname. The in-cluster egress-proxy pattern is untested, and k3s
  policy is IP and CIDR only.
- Warm pools (`replicas > 0`), annotation size limits and annotation visibility to other
  users of the pod API (inputs stay non-secret).
- Real k3s performance beyond about 10 concurrent step pods and about 30 s each.

Revisit this ADR if the harness cannot run under gVisor, if the network policy gap cannot be
closed by the entrypoint check, or if agent-sandbox's API breaks the one-shot claim flow.
