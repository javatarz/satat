# ADR 0031: Kubernetes adapter and launcher, k3d first

## Status

Proposed (2026-10-24). Based on the [Kubernetes adapter design](../research/v2/k8s-adapter.md).
Builds [ADR 0024](0024-kubernetes-agent-sandbox-runtime.md) for k3d on the laptop. Amends
[ADR 0028](0028-infrastructure-adapter-compose-first.md): this is the second infrastructure
adapter and the second launcher, behind the same `deploy.Target` and `launcher.Launcher`.

## Context

ADR 0028 split the platform into two seams, the infrastructure adapter and the launcher, and
built only Compose and Docker. ADR 0024 chose Kubernetes with agent-sandbox as the runtime and
recorded what the spike found, but nothing ran on it. The owner wants the same `satat.yaml` to
bring the platform up on k3d with one changed line, and every agent to run as a one-shot
`SandboxClaim` there. The lanes, limits, secrets map, workflow and `satat agent` commands must not
change.

## Decision

- **One setting picks both seams.** `deploy.target: kubernetes` selects the Kubernetes adapter in
  `satat` and the Kubernetes launcher in the worker. A mixed install is not supported.
- **A lane `command` replaces the image entrypoint, on both backends.** On the kubernetes target
  config load fails if a lane has no `command`: the wrapper has to know what to run, and a claim
  can't read an image's entrypoint. The Docker launcher now maps the command to `Entrypoint` with
  `Cmd` empty (it used to pass it as `Cmd`), so the `claude` lane's explicit
  `/usr/local/bin/satat-runner` doesn't run as `satat-runner satat-runner`. A lane with no command
  keeps the image's defaults on Docker.
- **One platform image, through a registry.** The worker and `satat-entry` ship in one image built
  from `images/worker/Dockerfile`. `satat deploy up` builds it, pushes it to `SATAT_REGISTRY` (or
  `--registry`) as `satat-platform:dev`, and reads the digest from the `docker push` output line
  `dev: digest: sha256:… size: …`. It installs by `<registry>/satat-platform@sha256:…`.
  `satat.yaml` doesn't name the image, so the file stays target-neutral. Lane images stay
  digest-pinned and anonymously pullable (ADR 0028).
- **Render, checked in.** `satat deploy render` writes `satat.k8s/values.yaml` and
  `satat.k8s/lanes.yaml` next to `satat.yaml` when it targets Kubernetes. As on Compose,
  `render --check` fails on a stale copy and `up` refuses one. `values.yaml` holds only
  `worker.secretNames`. `lanes.yaml` holds every lane object except Secrets. The platform image
  digest changes with every build, so the render writes the placeholder `satat-platform` as the
  image of each lane's `entry` init container, and `up` replaces only that line with the pushed
  reference, in memory, before applying. The repo's own `satat.yaml` stays `compose`.
- **Who creates what.**

  | Object | Created by |
  |---|---|
  | Cluster `satat`, registry `satat-registry` on `127.0.0.1:5111`, agent-sandbox v1.0.7 | `scripts/k3d-up.sh` (pinned, idempotent, no secrets) |
  | Lane namespaces, `SandboxTemplate`s, `SandboxWarmPool`s, the worker's per-lane `Role` and `RoleBinding` | `deploy up`: `lanes.yaml` through `kubectl apply` |
  | Postgres, hatchet-lite, worker `Deployment`, `ServiceAccount` and the `satat.yaml` `ConfigMap` | `deploy up`: the chart in `deploy/helm/satat/` through `helm upgrade --install` |
  | Lane Secrets (`satat-lane-secrets`, one per lane with `pass_env`) and the worker Secret | `deploy up`: server-side `kubectl apply` of a manifest on stdin |
  | Postgres password Secret | `deploy up`: `kubectl create` on stdin, once, never replaced |
  | Hatchet token Secret | `deploy up`: minted once with `hatchet-admin` in the hatchet-lite pod |

  Secret values reach `kubectl` on stdin only, never argv or disk. Server-side apply leaves no
  last-applied annotation holding the values. The token is also written to the `.env` beside
  `satat.yaml`, for the `satat agent` CLI. If that file has no token but the Secret exists, `up`
  restores it from the Secret. The network policies are not separate objects: agent-sandbox
  creates them from each template. The chart has no `ClusterRole` and grants the worker nothing.
- **A lane on Kubernetes.** Per lane `L`, in namespace `satat-lane-L`:
  - the namespace enforces Pod Security `restricted`;
  - the `SandboxTemplate` sets `envVarsInjectionPolicy: Disallowed`, `automountServiceAccountToken:
    false`, `restartPolicy: Never`, `dnsPolicy: ClusterFirst`, `runAsNonRoot` with the lane's
    numeric uid and gid, `fsGroup` the gid, seccomp `RuntimeDefault`, and on both containers
    `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false` and `capabilities.drop: [ALL]`.
    Memory and CPU requests equal limits. `/workspace`, `/tmp`, `/etc/satat` and `/satat` are
    memory-backed `emptyDir`s with size limits. `activeDeadlineSeconds` is the lane timeout plus
    90 s. `runtimeClassName` is set only when the lane says `runsc`;
  - its managed network policy allows no ingress, egress to cluster DNS in `kube-system`, and, for
    an open lane only, `0.0.0.0/0` except `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` and
    `169.254.0.0/16`;
  - a `SandboxWarmPool` at `replicas: 0`, which every claim names;
  - an init container `entry` (platform image) copies `satat-entry` into `/satat`, and the step
    container runs `/satat/satat-entry --probe … --timeout … [--pass-env NAME]… -- <command>`;
  - the claim's run, step and input travel as pod annotations, exposed by a `downwardAPI` volume at
    `/etc/podinfo`. The step container's own env has only `HOME`, `SATAT_INPUT` and
    `SATAT_OUTPUT`;
  - the lane Secret is mounted read-only at `/run/satat/secrets`, one file per `pass_env` name.

  `ValidateTemplate` (`launcher/kubernetes/objects.go`) is the floor for a rendered template, the
  way `ValidateRequest` is for a Docker create request. It checks the finished object, returns
  every violation, and runs on every render.
- **`satat-entry`** (`cmd/satat-entry`) is the step container's PID 1:
  1. it dials the in-cluster Postgres Service, which every lane's policy denies, every 200 ms
     until the dial is refused or times out. If it still connects after 30 s it fails closed with
     exit 125 and a frame that says so;
  2. it reads each `--pass-env` secret from its mounted file. A missing one fails the step;
  3. it copies the input to `/etc/satat/input.json`, creates `$SATAT_OUTPUT`, and sets `SATAT_RUN`
     and `SATAT_STEP` from the annotations;
  4. it runs the command with stdout and stderr captured separately, forwards SIGTERM, and at
     `--timeout` sends SIGTERM, then SIGKILL 5 s later;
  5. it reads OOM from the cgroup's `memory.events` `oom_kill` count. If the kernel kills the whole
     container there is no frame, and the launcher reads `OOMKilled` from the pod status;
  6. it replaces every loaded secret value with `***` in both streams before cutting them to the
     cap, and drops a cut tail that is the start of a secret;
  7. it writes one result frame to stdout, the only thing in the pod log, and exits with the
     command's code (128+signal for a signal, 125 when it could not run it).

  The frame is three lines: `---SATAT-RESULT-BEGIN v1---`, the JSON result on one line,
  `---SATAT-RESULT-END v1---`. JSON escapes newlines and carries stdout, stderr and the output tar
  as base64 fields, so nothing a step prints can look like a boundary. The launcher takes the last
  complete frame. The frame is not an integrity boundary: the step runs as the same uid and could
  forge one, which gains it nothing, since the result is its own output either way.
- **Smaller caps on Kubernetes.** 1 MiB each for stdout and stderr, and 4 MiB in at most 64
  files for the output tar. kubelet rotates a container log at 10 MiB, and the frame has to fit
  in one log file. The JSON is not base64-encoded again, because that took a frame at the caps to
  about 10.7 MiB. Step input over 200 KiB is refused, since annotations cap at 256 KiB in all.
  Docker keeps its larger caps.
- **The launcher** (`launcher/kubernetes`) creates `satat-<run>-<step>` in the lane namespace with
  `ttlSecondsAfterFinished: 300` and `shutdownPolicy: Delete`. `AlreadyExists` adopts the claim if
  its labels match, and refuses otherwise. Calls for one step share one execution in-process. It
  watches the one claim and polls its pod until the pod is terminal, the step is cancelled or the
  launcher's deadline passes (lane timeout + 90 s + 30 s). It reads the exit code and `OOMKilled`
  from the pod and the frame from `pods/log`, and on every exit path deletes the claim with
  foreground propagation and waits until it's gone. `Cancel` marks the step and deletes its claim.
- **Worker RBAC, exactly.** In each lane namespace the worker's ServiceAccount
  `satat:satat-worker` may `create`, `get`, `watch` and `delete` `sandboxclaims`, and `get` `pods`
  and `pods/log`. Nothing else, nothing cluster-wide, and no `list`. So there is no reaper sweep
  on Kubernetes: finished claims go by their 300 s TTL, every launch deletes its own claim on every
  exit path, and `deploy down` deletes the rest. The worker uses the in-cluster config, and its
  readiness checks the API's `/version` in place of the Docker ping.
- **Kube-context guard.** The adapter acts on `--context`, else `SATAT_KUBE_CONTEXT`, else
  `k3d-satat`. It passes the context explicitly to every `kubectl` (`--context`) and `helm`
  (`--kube-context`) call, and `up` refuses a context kubectl doesn't know, so it never acts on
  whichever cluster the shell points at.
- **Host ports.** hatchet-lite's Service is a `NodePort` on 30077 and 30888, which k3d maps to
  `127.0.0.1:7077` and `127.0.0.1:8888`. The `satat agent` CLI's localhost defaults work unchanged.
  The API server (6550) and the registry (5111) also bind to `127.0.0.1` only.
- **`deploy down`** deletes every claim in the lane namespaces, uninstalls the release and deletes
  the lane namespaces with their Secrets. It keeps namespace `satat` with the Postgres password,
  the worker and token Secrets and the volumes. `--volumes` also deletes the volume claims and the
  token (Secret and `.env` line), keeping the password, as on Compose.

## Consequences

- Moving between Compose and k3d is one line in `satat.yaml`, a render, and `deploy up`.
- **Lane `command` semantics change on Docker too.** A lane command now replaces the image's
  `ENTRYPOINT` instead of being appended to it. No lane in `satat.yaml` relied on the old
  behaviour; an image that does breaks.
- No per-lane pids limit on Kubernetes. The lane's `pids` applies on Docker only: a pod spec has no
  pids field, and kubelet's pod pids limit is one setting for every pod on the node.
- A lane removed from `satat.yaml` keeps its namespace, template and Role on the cluster. `up`
  warns and names the `kubectl delete namespace` command; it doesn't delete one, because that
  would delete its running claims. `deploy down` removes every lane namespace.
- Each `deploy up` makes a new helm revision, even when nothing changed. The worker pod is not
  recreated when its spec is unchanged.
- hatchet-lite may restart once on a cold start, when it starts before Postgres is ready.
- The platform image is built and pushed on every `up`. A warm run reuses Docker's build cache.
- The digest is read from `docker push` output wording, which is brittle if Docker changes it.
- Every claim cold-starts (warm pool at zero), and the network-policy probe adds up to 30 s
  before a step starts.
- A step's output is capped lower on Kubernetes than on Docker. The conformance suite stays far
  below either.
- The launcher's deadline starts at claim creation, so image pull and cold start count against it.

## Verified

On k3d (`rancher/k3s:v1.34.12-k3s1`, k3d v5.9.0, agent-sandbox v1.0.7) in colima with 4 CPU and
6 GiB, shared with unrelated workloads (about 1.7 GiB free). Scratch config: `satat.yaml` with
`deploy.target: kubernetes`, and the three `deploy.secrets` entries switched from `op://` to
`env` with placeholder values. The `claude` lane stayed in the config and was never claimed.

- `scripts/k3d-up.sh` creates the cluster, registry and agent-sandbox with ports on `127.0.0.1`
  only; a second run changes nothing.
- `satat deploy render --check` passes for the scratch config.
- `deploy up` cold, including the platform image build and push: 4 min 00 s, every service healthy
  on the first try. Warm, right after: 4.3 s, with the same worker pod (name and start time) and
  one ReplicaSet.
- `deploy status` shows hatchet-lite, postgres and the worker running and healthy, and the worker
  detail `{"status":"ready"}`.
- `satat agent run smoke --wait`: status `ok`, exit 0, stdout `uid=10001 gid=10001 groups=10001`
  and `ok`, 8 s end to end. No `SandboxClaim` was left afterwards.
- `satat agent run sleep` then `agent rm`: `cancelled`, and the claim was gone within 2 s.
- `kubectl auth can-i` as `system:serviceaccount:satat:satat-worker` in `satat-lane-smoke`: no for
  create pods, sandboxtemplates, sandboxwarmpools, sandboxes, secrets and serviceaccounts, get
  secrets and list sandboxclaims; yes for create and delete sandboxclaims, get pods and get
  pods/log. No for create pods in namespace `satat`.
- The placeholder secret values appear nowhere in `satat.k8s/values.yaml`, `satat.k8s/lanes.yaml`,
  `helm get manifest`, `helm get values --all`, Secret annotations (no Secret has a last-applied
  annotation), the worker logs or git-tracked files.
- `deploy down`: 8 s. Lane namespaces deleted; namespace `satat` kept with the Postgres volume
  claim, the `hatchet-config` claim and the three Secrets. `deploy up` afterwards: 25 s, no new
  token minted, and the smoke run was `ok`.
- The launcher passes all 13 `launcher/conformance` subtests on k3d (`SATAT_K8S_E2E=1`), while
  impersonating a test ServiceAccount bound by the rendered `Role`, so the narrow RBAC is enough.
  The RBAC test passes too. The run took 147 s.
- The agent-sandbox v1.0.7 field names the design read from source (claim to warm pool to
  template in one namespace, `status.sandbox.name`, the `Finished` condition) work on a cluster.
- The NodePort mapping and the registry mirror work on colima.

## Not verified

- `deploy down --volumes` against a live cluster. It is unit-tested only.
- The `claude` lane on Kubernetes, including the entrypoint override on the real `claude` image.
- 1Password `op://` secrets on the kubernetes target.
- gVisor (`runsc`) on k3d or anywhere for this adapter, and real k3s on a VM.
- Network plugins that drop blocked packets or answer `EHOSTUNREACH` rather than refusing. The
  probe treats a refusal or a dial timeout as blocked and keeps waiting on any other error, so
  such a plugin would fail every step closed after 30 s.
- Whether kubelet on k3s 1.34 sets `memory.oom.group`, which decides which OOM path fires. Both
  paths are handled.
- Two calls for one step joining one execution (`singleflight`). It is not unit-tested.

Revisit this ADR when Satat moves off k3d (real k3s, a managed cluster, gVisor), if the worker
needs to list claims, or if step output needs more than the caps allow.
