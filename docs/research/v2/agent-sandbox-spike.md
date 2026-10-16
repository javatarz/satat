# agent-sandbox spike (#103)

Part of #99. Checks whether `kubernetes-sigs/agent-sandbox` gives a one-shot `SandboxClaim` per step that a launcher can drive. Builds on `notes/k8s-runtime.md`, which was read from source; this spike runs it.

## Setup

- k3d v5.9.0 on colima (aarch64 Linux VM, 4 CPU, 6 GiB), cluster on `rancher/k3s:v1.34.12-k3s1` (containerd 2.2.7), one server node.
- agent-sandbox v1.0.7 (`sandbox-with-extensions.yaml`, server-side apply). Controller image runs on arm64 unmodified.
- Lane namespace `satat-lane-a` with Pod Security `restricted` enforced. One `SandboxTemplate` per test, each with a `SandboxWarmPool` at `replicas: 0`. Backend pods `allowed` and `denied` in a `backends` namespace.
- gVisor release-20261005.0 (aarch64), installed by hand into the k3d node.
- Run on 2026-10-16. The harness was `docs/scratch/103/` (gitignored, not committed). The cluster was deleted afterwards.

## Results

| # | Check | Result |
|---|-------|--------|
| 1 | Controller installs on k3s 1.34, arm64 | **Pass.** Rolled out first time. Controller used 17 MiB RAM and 4m CPU idle. |
| 2 | One-shot claim (`restartPolicy: Never`, warm pool `replicas: 0`) | **Pass.** Cold path creates the pod. Pod runs to `Completed`, claim and sandbox get a `Finished` condition (`PodSucceeded`). |
| 3 | Failing step | **Pass.** `Finished` with `PodFailed`. Exit code 7 is only on the pod (`containerStatuses[0].state.terminated.exitCode`), not on the claim. `pods/log` still works after exit. |
| 4 | Inputs via claim annotation + Downward API file | **Pass.** `additionalPodMetadata.annotations["satat.dev/task"]` appeared as `/etc/podinfo/task`. The controller records the key in `agents.x-k8s.io/propagated-annotations`. Works with `envVarsInjectionPolicy: Disallowed`. |
| 5 | Claim sets `env` when policy is `Disallowed` | **Rejected.** Claim `Ready=False`, reason `EnvVarsInjectionRejected`. |
| 6 | TTL cleanup (`ttlSecondsAfterFinished`, `shutdownPolicy: Delete`) | **Pass.** Finished claims, sandboxes and pods were gone within the TTL (20 to 60 s). A rejected claim never finishes, so it is not cleaned up and stays until deleted. |
| 7 | Cancel by deleting the claim | **Pass.** Pod went `Terminating` and then gone. |
| 8 | Per-lane network policy (managed, explicit egress: DNS + one backend) | **Pass, with a start-up gap.** Allowed backend reachable, other backend and internet refused. See finding 2. |
| 9 | Pod Security `restricted`, no SA token | **Pass.** Non-root, drop ALL, seccomp RuntimeDefault admitted. `/var/run/secrets/kubernetes.io` absent. |
| 10 | gVisor (runsc, systrap) on k3d on colima arm64 | **Pass** after manual setup. Pod reported kernel `4.19.0-gvisor` and "Starting gVisor". Network policy behaves the same as under runc. |

## Findings

1. **The one-shot model works as the research note inferred.** Worker flow: create claim, watch `Finished`, read the pod's exit code, read `pods/log`, delete the claim. No router or in-pod server is needed. Annotation inputs are confirmed, not just inferred from source.
2. **Network policy is enforced late for a new pod.** kube-router (k3s's embedded controller) took 2 to 4 seconds after pod start to apply the rules. In that window the pod reached everything: the denied backend and the internet. A first-test pod that finished in under 10 seconds saw no enforcement at all in two runs. Short steps can therefore leak. The lane entrypoint must wait for the policy before doing anything that matters, for example by polling a known-denied address until it is refused, and fail closed on timeout. Blocked connections are refused (REJECT), not dropped.
3. **The managed policy needs explicit egress.** Templates that omit `networkPolicy` get the secure default (the `fail` template got a policy without declaring one). The explicit rules (DNS in kube-system plus named backends) worked as written.
4. **gVisor works on k3d, contradicting the earlier "treat as unsupported" note**, but not out of the box:
   - Copy `runsc`, `containerd-shim-runsc-v1` **and the new `gvisor-bin/` directory** into the node. Without `gvisor-bin/`, sandbox creation fails with "sidecar gvisor_sentry not usable".
   - k3s 1.34 uses containerd 2, so the template is `config-v3.toml.tmpl` with `[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc]` and `runtime_type = "io.containerd.runsc.v1"`. This also settles the template filename that `notes/k8s-runtime.md` marked UNVERIFIED.
   - Release tarballs (`gvisor.tar.bz2`, `.sha512` checked) replace per-binary downloads in the 20261005 release. The old per-binary URLs return 404.
   - Not tested: gVisor on the actual VM (k3s on t4g, Graviton). The node here is a container in a Linux VM, so it is a good proxy, not the same thing.
5. **Restarting the k3d node deleted ordinary pods.** The `backends` pods were gone after `docker restart` of the node, so any laptop restart needs workloads re-applied. Claims for finished steps are not affected because they are short-lived. This is a k3d-in-Docker behaviour; I did not check which objects survive.
6. **Rejected and finished claims need a reaper.** A claim that never starts (env rejected, bad template) has no TTL. The worker should delete claims it created, on every exit path.
7. **Footprint is small.** Controller 17 MiB. The ~0.1 GB estimate in the research note was high.

## Not covered

- Warm pools with `replicas > 0` (not needed for one-shot).
- Annotation size limit, and whether annotations leak to other users of the pod API (non-secret inputs only, as noted).
- Real k3s on the VM, and Cilium or egress-proxy patterns. The proxy approach in `notes/k8s-runtime.md` §3 is still untested here.
- Whether the lane image under gVisor can run the harness. Only a busybox-class image was run.

## Feeds ADR #106

The runtime holds: Kubernetes with agent-sandbox, one-shot claims, annotation inputs through the Downward API, one namespace and template per lane. Conditions to write into the ADR:

- Entrypoint waits for network-policy enforcement before starting the harness.
- Worker deletes its own claims.
- gVisor on k3s needs the three-part install above (and `config-v3.toml.tmpl`), to be repeated on the VM.
