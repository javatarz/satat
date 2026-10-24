# Kubernetes adapter and launcher, k3d first

Design for deploying the Satat platform to Kubernetes, starting with k3d on a laptop, and for
running one-shot agents there as `SandboxClaim`s. Status: **approved in outline (2026-10-23)**.
It builds on the Compose adapter of [deploy-targets.md](deploy-targets.md) (PR #129) and follows
its "Later: k3d" section, [ADR 0024](../../adr/0024-kubernetes-agent-sandbox-runtime.md) and the
[agent-sandbox spike](agent-sandbox-spike.md). Calls marked _(my call)_ weren't decided by the
owner. The five trade-offs marked _(agreed)_ were put to the owner and accepted.

## Intent

`deploy.target: kubernetes` in `satat.yaml` brings the same platform up on a Kubernetes cluster
(k3d on the laptop now), and runs every agent as a one-shot `SandboxClaim` instead of a Docker
container. Nothing else in `satat.yaml` changes: the lanes, limits, secrets map, the workflow and
`satat agent run|ls|rm` all stay as they are.

## Goals

- A `deploy/kubernetes` adapter implementing `deploy.Target`: `Render`, `Up`, `Down`, `Status`.
- A Helm chart in `deploy/helm/satat/`: Postgres, hatchet-lite, the worker.
- `launcher/kubernetes`, passing `launcher/conformance` unchanged.
- `satat-entry`, a small static wrapper that makes a claim's pod behave like a Docker step.
- `scripts/k3d-up.sh`: a pinned, idempotent k3d cluster with agent-sandbox and a registry.
- The same `satat.yaml`, with only `deploy.target` changed, brings the platform up on k3d, and
  `satat agent run smoke --wait` prints `ok`.

## Non-goals

- gVisor on k3d. ADR 0024 keeps plain container isolation on the laptop.
- Real k3s on a VM, a managed cluster, Terraform. k3d only.
- Egress allowlists by hostname or an egress proxy. Lanes are `internal` (no egress) or open
  (internet only, no cluster addresses), as on Compose.
- Warm pools with replicas above zero.
- A mixed install (Compose platform with Kubernetes agents, or the reverse).
- Private-registry pull credentials (unchanged from ADR 0028).

## Anti-goals

- The worker able to create pods, `Sandbox`es, templates, warm pools, Secrets or ServiceAccounts,
  or to read Secrets. It creates, gets, watches and deletes `sandboxclaims`, and gets `pods` and
  `pods/log`, in lane namespaces only. Nothing cluster-wide.
- A claim that sets `env`, or any pod field taken from run input. Input travels in an annotation
  and is exposed as a file only.
- A secret value in git, a values file, the rendered output, a command line or the Hatchet run
  history.
- A step doing anything that matters before its lane's network policy is enforced.
- `satat deploy up` acting on whatever cluster the shell happens to point at.

## Decisions

### 1. One setting picks both seams

`deploy.target` accepts `compose` or `kubernetes`. `kubernetes` selects the Kubernetes adapter in
`satat` and the Kubernetes launcher in the worker. Config load fails, for the kubernetes target
only, if a lane has no `command` _(agreed)_: the wrapper has to know what to run, and a claim
can't read an image's entrypoint. The `claude` lane gains its command explicitly, which Compose
ignores (it already passes `Cmd` through).

### 2. Images reach the cluster through a registry _(agreed)_

The worker and `satat-entry` ship in **one platform image** built from `images/worker/Dockerfile`
(the wrapper is one more static binary in it). `satat deploy up` builds it, pushes it to the
registry named by `SATAT_REGISTRY` (or `--registry`), reads the manifest digest back, and installs
by `<registry>/satat-platform@sha256:…`. `satat.yaml` doesn't name it, so the file stays
target-neutral. `scripts/k3d-up.sh` creates a local registry and prints the value to use. Lane
images stay as they are: digest-pinned and anonymously pullable, as ADR 0028 says.

### 3. Who creates what _(agreed)_

| Object | Created by | Notes |
|---|---|---|
| Cluster, agent-sandbox, registry | `scripts/k3d-up.sh` | Pinned versions, idempotent, takes no secrets |
| Lane namespaces, `SandboxTemplate`s, `SandboxWarmPool`s, `NetworkPolicy`s, the worker's per-lane `Role` and `RoleBinding` | `deploy up`, from `Render`, via `kubectl apply` | Rendered by Go, so Go tests assert the hardening floor |
| Postgres, hatchet-lite, worker `Deployment`, worker `ServiceAccount`, `satat.yaml` `ConfigMap` | `deploy up`, via `helm upgrade --install` | Chart in `deploy/helm/satat/`, our own minimal templates, no subcharts |
| Lane `Secret`s (one per lane, its `pass_env` values) | `deploy up`, from `deploy.secrets` | Piped to `kubectl apply -f -` on stdin, never argv or disk |
| Worker `Secret` (`SATAT_PUSH_TOKEN`), Postgres password, Hatchet token | `deploy up` | Password and token created once, never rotated |

The per-lane `Role` lives with the lane objects, not the chart _(my call)_: it is per lane, like
the template it guards, and the conformance test reuses the same render to bind a test
ServiceAccount. The chart holds no `ClusterRole` at all.

### 4. Render, and what's checked in _(my call)_

`Render` returns two files, written next to `satat.yaml` under `satat.k8s/`:

- `values.yaml`: the chart values that follow from `satat.yaml` (the lane namespace list and the
  worker's settings). The raw `satat.yaml` goes into the `ConfigMap` at install time
  (`--set-file`), not into the values file.
- `lanes.yaml`: every lane object of decision 3 except Secrets.

As on Compose, they're checked in when the config targets Kubernetes, `render --check` fails on a
stale copy and `Up` refuses one. The platform image digest changes with every worker build, so
the render writes a fixed placeholder (`satat-platform`) where the `satat-entry` init container's
image goes, and `Up` substitutes the pushed reference in memory before applying. The repo's own
`satat.yaml` stays `compose`, so nothing new is checked in at the root.

### 5. A lane on Kubernetes

Per lane `L`, in namespace `satat-lane-L`:

- **Namespace** labelled `pod-security.kubernetes.io/enforce: restricted`.
- **`SandboxTemplate`** whose pod has `automountServiceAccountToken: false`, `restartPolicy:
  Never`, `envVarsInjectionPolicy: Disallowed`, `runAsNonRoot`, the lane's numeric uid and gid,
  `fsGroup` the gid, seccomp `RuntimeDefault`, and on every container `allowPrivilegeEscalation:
  false`, `capabilities.drop: [ALL]`, `readOnlyRootFilesystem: true`. Requests equal limits for
  memory and CPU. `/workspace` and `/tmp` are memory-backed `emptyDir`s sized like Compose's
  tmpfs. `runtimeClassName` is set when the lane says `runsc`. `activeDeadlineSeconds` is the
  lane timeout plus 90 s (the probe wait and a grace), as a backstop to the wrapper's own timeout. `dnsPolicy: ClusterFirst`
  is set explicitly, because the controller otherwise points pods at public resolvers.
- **Init container** `entry` (platform image) copies `/usr/local/bin/satat-entry` into an
  `emptyDir` at `/satat`. The step container runs `/satat/satat-entry <flags> -- <lane command>`.
- **Inputs**: the claim sets annotations `satat.dev/run`, `satat.dev/step` and `satat.dev/input`.
  A `downwardAPI` volume exposes them as files under `/etc/podinfo`, and the wrapper copies the
  input to `/etc/satat/input.json` on a writable `emptyDir`, so `SATAT_INPUT` is the same path as
  on Docker. Input over 200 KiB is refused by the launcher (annotations cap at 256 KiB in all).
- **Secrets**: `Secret` `satat-lane-secrets` mounted read-only at `/run/satat/secrets`, one file
  per `pass_env` name. The step container's own env has only the static `HOME`, `SATAT_INPUT`
  and `SATAT_OUTPUT`.
- **`NetworkPolicy`** through the template's managed policy: egress to cluster DNS always; for an
  open lane, also `0.0.0.0/0` except `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` and
  `169.254.0.0/16`; for an `internal` lane nothing else. No ingress.
- **`SandboxWarmPool`** at an explicit `replicas: 0` (the default is 1). A claim can only name a
  warm pool (`spec.warmPoolRef`), which names the template, all in the claim's namespace.
- **`Role`** granting the worker's ServiceAccount `create, get, watch, delete` on `sandboxclaims`
  and `get` on `pods` and `pods/log`, and a `RoleBinding`.

`ValidateTemplate` in Go is the floor for a rendered template, the way `ValidateRequest` is for a
Docker create request: it checks the finished object, not the lane, and returns every violation.

### 6. `satat-entry`

A static Go binary, `cmd/satat-entry`, the step container's PID 1:

1. **Waits out the network-policy gap, fail closed.** It dials a probe address that every lane's
   policy denies (the in-cluster Postgres service, `--probe`) until the connection is refused,
   every 200 ms for up to 30 s. If it still connects at the deadline it exits 125 with a framed
   result whose stderr says so. Blocked connections are refused (spike finding 2), so a refusal
   is the signal.
2. **Loads secrets**: for each `--pass-env NAME`, the file `/run/satat/secrets/NAME`. A missing or
   empty one fails the step, as an unset name fails a Docker launch.
3. **Sets the step env**: the container env, `SATAT_RUN` and `SATAT_STEP` from the annotations,
   and the secrets. It copies the input into place and creates `$SATAT_OUTPUT`.
4. **Runs the command** with stdout and stderr captured separately, each capped (decision 8).
   It forwards SIGTERM, and kills the child at `--timeout` with SIGTERM then SIGKILL after 5 s.
5. **Detects OOM** from the cgroup's `memory.events` `oom_kill` count. If the kernel kills the
   whole container (`memory.oom.group`), there's no frame and the launcher reads `OOMKilled` from
   the pod status instead.
6. **Scrubs** every secret value it loaded from both streams, replaced with `***`, before
   framing. The worker can't read lane secrets, so this is the only place that knows them.
7. **Writes one framed result block** to its stdout, the only thing in the pod log:

   ```text
   ---SATAT-RESULT-BEGIN v1---
   <base64 of JSON: exit_code, oom_killed, timed_out, stdout, stderr, logs_truncated, output (tar), files_truncated>
   ---SATAT-RESULT-END v1---
   ```

   It exits with the child's code (128+signal for a signal).

The frame isn't an integrity boundary: the step runs as the same uid and could forge one. That
gains it nothing, since the result is its own output either way, so the launcher takes the last
complete frame.

### 7. `launcher/kubernetes`

`New(clients, lanes, opts)` checks every lane and maps lanes to namespaces. `Launch`:

1. Refuses an unknown lane, an invalid run or step id, or an input over 200 KiB.
2. Creates the claim `satat-<run>-<step>` in the lane namespace, with the run, step and input
   annotations, `satat.dev/managed`, `run` and `step` labels, the warm pool reference, and
   lifecycle `ttlSecondsAfterFinished: 300`, `shutdownPolicy: Delete`. `AlreadyExists` adopts the
   claim if its labels match (`Adopted`), and refuses otherwise. Calls for one step share one
   execution in-process (`singleflight`), as on Docker.
3. Watches the claim until `Finished`, a terminal pod, the lane timeout plus a grace, cancel, or
   the caller's context ends. A claim that's rejected (`Ready=False`) fails the launch with the
   reason.
4. Gets the pod named in the claim's status, reads the exit code and `OOMKilled` from
   `containerStatuses`, and reads `pods/log` of the step container. It parses the last frame into
   `Stdout`, `Stderr`, `Files` and the flags. No frame: the exit code and the OOM flag come from
   the pod and the result has no logs.
5. Deletes the claim on every exit path with foreground propagation, and waits until it's gone,
   so the next launch of the same step is a fresh execution.

`Cancel` marks the step cancelled and deletes its claim; `Launch` then returns `Cancelled`.
`TimedOut` comes from the frame, or from the pod's `DeadlineExceeded` reason, or from the
launcher's own deadline. There's no reaper sweep: the worker can't list claims. Finished claims go
by TTL, every launch deletes its own claim, and `deploy down` removes the rest.

### 8. Smaller caps on Kubernetes _(agreed)_

kubelet rotates a container log at 10 MiB, and the frame is base64. So the wrapper caps stdout and
stderr at 1 MiB each and the output tar at 4 MiB, at most 64 files, and sets the truncated flags.
Docker keeps its 4 MiB and 64 MiB caps. The conformance suite stays far below either.

### 9. The worker on Kubernetes

- `cmd/satat-worker` picks the launcher by `deploy.target`. The Kubernetes one uses the
  in-cluster config.
- Readiness checks become: Hatchet's worker record (unchanged), the Kubernetes API answering
  `/version` (replacing the Docker ping), and lanes (the launcher built).
- The Deployment runs `/livez` as the liveness probe and `/readyz` as the readiness probe on
  `SATAT_HEALTH_ADDR=0.0.0.0:8001`, with `strategy: Recreate`, one replica, the same hardening as
  Compose (non-root, read-only root, drop ALL), and memory request and limit 512 MiB.
- Its env holds the Hatchet client settings (in-cluster service addresses), the token from its
  Secret, and `SATAT_PUSH_TOKEN` from the worker Secret. No lane secret reaches the worker.
- An agent run's 4 KiB tail is still scrubbed by the worker of what it holds. Lane secrets were
  already scrubbed by the wrapper.

### 10. `satat deploy up|down|status` on Kubernetes

The adapter acts on the kube context `SATAT_KUBE_CONTEXT` or `--context`, default `k3d-satat`,
and passes `--context` to every `kubectl` and `helm` call, so it never acts on whichever cluster
the shell points at _(my call)_.

`Up`, idempotent:

1. Refuse a stale render. Check the context exists and the agent-sandbox CRDs are installed.
2. Build and push the platform image, and read its digest.
3. Apply namespace `satat`, then `lanes.yaml` with the image substituted.
4. Create the Postgres password Secret if it's absent. Apply each lane's Secret and the worker
   Secret from the resolved `deploy.secrets`, on stdin.
5. `helm upgrade --install satat` with the worker at zero replicas if the Hatchet token Secret
   is absent, `--wait`. Then mint the token with `hatchet-admin` in the hatchet-lite pod, store
   it as a Secret, and copy it into `.env` beside `satat.yaml` (as Compose does, for the
   `satat agent` CLI).
6. `helm upgrade --install` with the worker at one replica, `--wait`, then confirm `/readyz`.

`Down` deletes every `SandboxClaim` in the lane namespaces, uninstalls the release and deletes the
lane namespaces (with their Secrets). It keeps namespace `satat` with its Secrets and the Postgres
volume. `--volumes` also deletes the volume claims and the Hatchet token (Secret and `.env` line),
keeping the password, as on Compose.

`Status` lists Postgres, hatchet-lite and the worker from their pods' readiness, plus the worker's
`/readyz` detail through `kubectl exec … satat-worker healthcheck`.

The hatchet-lite Service is a `NodePort` on 30077 and 30888, which k3d maps to `127.0.0.1:7077`
and `127.0.0.1:8888`, so the `satat agent` CLI's existing localhost defaults work unchanged.

### 11. `scripts/k3d-up.sh`

- Downloads k3d **v5.9.0** into `docs/scratch/bin` if it's absent (checksum-verified), and uses
  `rancher/k3s:v1.34.12-k3s1` (the spike's versions).
- Creates a registry `satat-registry` on `localhost:5111` unless `SATAT_K3D_REGISTRY_CONFIG`
  names a `registries.yaml` to pass to k3d instead. The registry mirror makes the same
  `localhost:5111/…` reference work from the host and inside the cluster.
- Creates cluster `satat` with ports `127.0.0.1:7077` and `127.0.0.1:8888` mapped, if it's absent.
- Installs agent-sandbox **v1.0.7** (`sandbox-with-extensions.yaml`, server-side apply) and waits
  for the controller.
- Prints `SATAT_REGISTRY=localhost:5111`. Running it again changes nothing.

### 12. Tests

- Unit: config (`kubernetes` target, lane command required), `ValidateTemplate` and the lane
  render (golden file, hardening, RBAC verbs), the frame codec, `satat-entry` against fake probe,
  secret and cgroup paths, the launcher against a fake dynamic client, and `Up`, `Down` and
  `Status` against a fake runner (call order, idempotence, no secret on argv).
- `launcher/kubernetes` conformance runs only with `SATAT_K8S_E2E=1`, against the
  `k3d-satat` context, and with `SATAT_K8S_PLATFORM_IMAGE` set. The test applies each test lane's
  objects as the admin, binds a test ServiceAccount through the rendered `Role`, starts a probe
  target, and runs the launcher **impersonating that ServiceAccount**, so the suite also proves
  the narrow RBAC is enough.
- An RBAC test (same flag) asserts `auth can-i` is no for the worker on pods, templates, warm
  pools, sandboxes, secrets and serviceaccounts, and yes for the four claim verbs.

## Acceptance

- `go test -p 1 ./...` passes, including Docker conformance; with `SATAT_K8S_E2E=1` the
  Kubernetes conformance and RBAC tests pass against k3d.
- From `scripts/k3d-up.sh` and a copy of `satat.yaml` with only `deploy.target: kubernetes`:
  `satat deploy render`, then `satat deploy up` ends with every service ready, and a second run
  changes nothing.
- `satat agent run smoke --wait` prints exit 0 and `ok`, and no claim is left afterwards.
- `kubectl auth can-i create pods|sandboxtemplates|secrets --as=system:serviceaccount:satat:satat-worker -n satat-lane-smoke`
  is `no`.
- No secret value in git, `values.yaml`, `lanes.yaml`, the helm release manifest, or `deploy status`.
- gofmt, vet, `satat deploy render --check` and pre-commit pass.

## ADRs

- **ADR 0031: Kubernetes adapter and launcher.** Decisions 2 to 10: the platform image and how it
  reaches the cluster, lane objects rendered in Go and the chart for the platform, `satat-entry`,
  secrets as mounted files scrubbed in the wrapper, the framed result block and its caps, the
  RBAC, no claim listing, and the kube-context guard.
- **ADR 0024**: status gains "Built for k3d by ADR 0031", and its RBAC line is met as written.
- **ADR 0028**: status notes the second adapter exists; revisit condition met.

## Not verified yet

- The agent-sandbox v1.0.7 field names were read from its source at the tag (claim → warm pool →
  template in one namespace; claim, Sandbox and pod share one name; `status.sandbox.name`;
  `Finished` with `PodSucceeded`/`PodFailed`). Not yet run on a cluster.
- Whether kubelet on k3s 1.34 sets `memory.oom.group` (decides which OOM path fires). Both are
  handled.
- That the NodePort mapping and registry mirror work on colima as described.
