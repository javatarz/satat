# Infrastructure adapter: Compose first, k3d later

Design for deploying the Satat platform through an infrastructure adapter, starting with Docker
Compose, and for spawning one-shot agents on demand. Status: **draft, for approval**. It builds on
`main` at `a358bae` (2026-10-22): the #112 prototype through slice 5 (Hatchet worker), the pusher
and the eval harness, described in [design.md](../../design.md), and ADRs 0023 to 0027. Calls
marked _(my call)_ weren't decided by the owner.

## Intent

Today the platform comes up through `scripts/hatchet-token.sh`, then `scripts/compose-up.sh`,
then `docker compose up`, and agents are spawned only by the issue workflow. The owner wants:

1. `satat deploy up` to bring the platform up on Compose. It goes through an adapter
   interface, so a k3d adapter can be added later without touching the worker, the lanes or the
   workflows.
2. The ability to create and destroy temporary agents on demand, from a lane's image config in
   `satat.yaml`. An agent does one task and exits.
3. A worker health check that includes Hatchet connectivity.

## Two seams, kept separate

| Seam | Decides | Now | Later |
|---|---|---|---|
| **Infrastructure adapter** (new, `deploy/`) | Where the long-lived **platform** runs: Postgres, hatchet-lite, socket proxy, worker | Compose | k3d with Helm |
| **Launcher** (exists, `launcher/`) | Where each one-shot **agent** runs | Docker containers | `SandboxClaim`s (ADR 0024) |

Agents are never platform services. They aren't in `compose.yaml`. The worker creates them at run
time through the launcher and removes them when they exit.

## Goals

- `satat deploy up | down | status | render` with a Compose adapter behind a Go interface.
- `satat agent run | ls | rm`, so an operator can spawn, list and destroy one-shot agents on any
  lane, through Hatchet, so caps and the ADR 0027 trust model still apply.
- `/livez` and `/readyz` on the worker, with Hatchet in readiness, wired into Compose.
- Lane images pulled from a registry by manifest digest (owner's decision).

## Non-goals

- **k3d now.** No k3d adapter, Kubernetes launcher, Helm chart or `satat-entry` in this work.
  The interfaces are shaped so they can be added (see "Later: k3d").
- **A database of agent definitions.** `satat.yaml` stays the only source (owner's decision).
- **Agents as Hatchet workers.** Agents hold no Hatchet token and have no Hatchet health check.
  The worker is the only Hatchet worker (owner's decision, ADR 0027).
- **Long-lived or steerable agents.** An agent runs one task and exits (owner's decision, ADR 0022
  Q5).
- Running the registry, LiteLLM, the egress proxy, the token broker, webhook intake (#112
  slice 6).
- Removing `scripts/compose-up.sh` and `scripts/hatchet-token.sh`. They keep working. Retiring
  them can be suggested once `deploy up` has been used for a while.

## Anti-goals

- An agent container carrying `com.docker.compose.project`. If it did, `docker compose up
  --remove-orphans` or `satat deploy up` would delete running agents as orphans.
- Any container flag of an agent set from `agent run` input, issue text or agent output. Input
  is data placed at `/etc/satat/input.json` only (ADR 0027).
- A second holder of the Docker API inside the platform. The CLI runs on the host as the operator,
  who already owns the engine. No container besides the worker gets the socket proxy.
- A worker health check that restarts the worker on every short Hatchet blip, or that stays green
  while the worker is cut off from Hatchet.
- Secrets written to `compose.yaml`, git, or rendered files. `.env` stays gitignored (mode 600)
  and holds only the two platform-local secrets it holds today, `POSTGRES_PASSWORD` and
  `HATCHET_CLIENT_TOKEN`. Agent and pusher secrets are never written to disk.

## Decisions

### 1. The `satat` command _(my call)_

A new `cmd/satat` binary with `deploy` and `agent` subcommands. The existing `satat-enqueue`,
`satat-step`, `satat-eval`, `satat-worker` and `satat-runner` stay as they are.

### 2. `satat.yaml` gains a `deploy` section _(my call)_

```yaml
deploy:
  target: compose            # the only adapter for now; picks the launcher backend too (docker)
  # Where each secret the platform needs comes from: an op:// reference (read with `op read`),
  # or "env" to take it from the shell. Names, never values.
  secrets:
    CLAUDE_CODE_OAUTH_TOKEN: op://Satat Harness/claude-code-oauth-token
    SATAT_GITHUB_TOKEN: op://Satat Harness/satat-sandbox-read
    SATAT_PUSH_TOKEN: op://Satat Harness/satat-sandbox-write
limits: { ... unchanged ... }
lanes:  { ... unchanged fields; image is a registry reference (decision 6) ... }
```

There's one setting for the target, not separate ones for the adapter and the launcher, because a
Compose platform with a Kubernetes launcher isn't a supported mix. Unknown keys stay errors.
Config load fails if a lane's `pass_env` name, or a worker-only secret (`SATAT_PUSH_TOKEN`), has
no entry under `deploy.secrets`. The map is explicit because the 1Password field names don't
follow from the variable names.

### 3. The infrastructure adapter

```go
// Package deploy brings the Satat platform up and down on one target.
type Target interface {
    Render(cfg config.Config) (map[string][]byte, error) // files the target needs, by path
    Up(ctx context.Context, cfg config.Config, secrets Secrets) error
    Down(ctx context.Context, removeData bool) error
    Status(ctx context.Context) (Status, error) // per service: running, healthy, detail
}
```

**Compose adapter (`deploy/compose`):**

- **Render.** `compose.yaml` comes from an embedded template plus `satat.yaml`. What varies is the
  worker's environment, which needs every lane's `pass_env` name, so a new lane no longer means
  a hand edit there. Everything else is copied from today's file: pinned images, the socket
  proxy flags, ports on `127.0.0.1`, the worker's hardening and retention settings. The rendered
  file stays **checked in**, so reviewers read it and `scripts/check-compose-socket.sh` keeps
  working. `satat deploy render --check` fails CI when the checked-in file differs from the render.
- **Up** does in order what's done by hand today:
  1. render and refuse if it differs from the checked-in file;
  2. if `.env` has no `POSTGRES_PASSWORD`, generate a random one into it (mode 600). It's never
     rotated once present, because a new password would lock out the existing Postgres volume;
  3. `docker compose up -d postgres hatchet-lite docker-proxy` and wait for healthy;
  4. if `.env` has no `HATCHET_CLIENT_TOKEN`, create one (the `hatchet-token.sh` logic);
  5. resolve every entry in `deploy.secrets` (`op read` or the shell) into the child process
     environment only, never to disk;
  6. `docker compose up -d --build worker` and wait until the worker's healthcheck passes
     (decision 5), with a timeout that names the failing readiness check.
  It's idempotent: running it again on a healthy stack changes nothing.
- **Down** runs `docker compose down`, plus `-v` only with `--volumes`. It then removes any agent
  containers and volumes left behind, by their `satat.*` labels (the launcher's reaper logic, run
  from the host).
- **Status** combines `docker compose ps` and the worker's `/readyz` JSON.

### 4. On-demand agents _(my call on the mechanism)_

An agent is one `launcher.Step` on a lane: created from the lane config, run once, removed. That
is what `launcher/docker` already does. What's new is how an operator reaches it:

- **One workflow, two trigger events** _(my call)_. Hatchet concurrency groups are per workflow,
  and ADR 0023 verified caps only within one. A second workflow would let an ad-hoc `claude` agent
  and an issue run share the subscription uncapped. So the existing workflow gains a second
  event, `satat:agent`, next to `satat:issue`, with the same caps and timeouts. `Input` gains
  `kind: issue | agent` and `step_input` (the lane's input JSON). The repo key is a fixed
  `adhoc` value for agent runs. The handler branches on `kind`: `agent` calls `Launch` and
  returns the result, and never pushes. `issue` is unchanged. The `Spec` checks stay the same.
- **What an agent run returns** _(my call)_: the exit code, status flags, output file names
  and sizes, plus the **last 4 KiB of stdout and stderr**. Before that leaves the worker, every
  value of every lane's `pass_env` and of the worker's own secrets is scrubbed from it (the
  runner's `scrub` approach). That keeps the Hatchet payload small and keeps secret values out of
  the 720 h run history. File contents aren't returned. An operator who needs them uses
  `satat-step` by hand, as today.
- **`satat agent run <lane> [--input file.json] [--wait]`** pushes `satat:agent` through
  Hatchet, as `satat-enqueue` does, and prints the run ID. `--wait` follows the run to the end
  and prints the returned result (stdout tail first).
- **`satat agent ls`** lists running and queued runs of the workflow from the Hatchet API: run ID,
  kind, lane, state, age.
- **`satat agent rm <run>`** cancels the run in Hatchet. The worker's task context is cancelled,
  and `Launch` stops and removes the container (the existing `Cancel` path, covered by
  conformance).
- Because all three go through Hatchet, they work unchanged when the launcher behind the worker
  becomes Kubernetes. The CLI never talks to Docker for agents, except `deploy down`'s cleanup.
- **Labels.** A unit test asserts that the launcher's create request has `satat.*` labels and no
  `com.docker.compose.*` label.
- Agents get **no** health check. The launcher already watches them through the exit code,
  timeout, OOM kill and cancel.

### 5. Worker health _(my call)_

The worker serves on port `8001`, bound inside its container:

- `/livez`: 200 while the process loop responds. Doesn't check Hatchet.
- `/readyz`: 200 only when **all** of these hold, otherwise 503 with JSON naming the failing check:
  1. the worker is registered with Hatchet and its action listener is connected;
  2. the last successful Hatchet contact is within `3 ×` the heartbeat interval;
  3. the Docker API answers `_ping` through the socket proxy;
  4. every lane loaded and passed the floor (checked once at start).
- `satat-worker healthcheck` calls `/readyz` and exits 0 or 1, so the worker image needs no
  `curl` added. Compose: `healthcheck: ["CMD", "/usr/local/bin/satat-worker", "healthcheck"]`.
- **Exit rule:** the worker exits non-zero only if it never connects to Hatchet within a start-up
  grace period, and `restart: unless-stopped` retries. After a first successful connect, an outage
  of any length makes it unready but never makes it exit, so in-flight agents finish.
- Docker doesn't restart unhealthy containers outside Swarm, so in Compose the healthcheck is what
  `deploy up` and `deploy status` read. On k3d later it maps onto liveness and readiness probes
  with no change to the worker.
- The pinned Hatchet Go SDK (v0.110.5) may already expose connection state or a health server
  (only Python and TypeScript document one). If it does, use it for checks 1 and 2 instead of
  tracking state ourselves.

### 6. Images come from a registry (owner's decision)

Every lane image is in a registry the target can pull from, local or remote. Satat doesn't run or
choose it, and for now the registry must allow **anonymous pulls** (a public package or a local
registry). Pull credentials for a private registry are a later change, since they'd add a worker
secret. A local plain-HTTP registry on `localhost` needs no daemon change on Docker. Elsewhere
it's the operator's setup. Lanes in `satat.yaml` are `<registry>/<name>@sha256:<manifest digest>`. The floor
already requires a digest, and the launcher's `Setup` inspects locally and pulls if missing, so
Compose needs no change. `scripts/build-claude-image.sh` gains a `SATAT_REGISTRY` input, pushes
(the operator is already logged in to push), and prints the manifest-digest reference, instead
of today's local image ID. `scripts/image-digest.sh` handles only anonymous GHCR today, so the
build script reads the digest from `docker buildx imagetools inspect` instead, which works for
any registry. The worker image stays built by Compose (`build:`), since it isn't a lane.

## Build order

Each step is a PR-sized slice on `main`, in a worktree, merged in order. Steps 1 and 2 are
independent.

0. **Registry references.** The `SATAT_REGISTRY` push in the build script, and lanes re-pinned.
   The existing Compose flow still runs the `smoke` and `claude` lanes.
1. **Worker health.** `/livez`, `/readyz`, the `healthcheck` subcommand, the exit rule, and a
   Compose healthcheck in today's hand-written `compose.yaml`. Tests stop hatchet-lite and expect
   `/readyz` to fail within 30 s and recover after, with no restart.
2. **`deploy` interface + Compose `Render`.** The `deploy` config section, the template, a
   golden test that the render equals the checked-in `compose.yaml`, and `render --check` in CI.
3. **`satat deploy up | down | status`.** The orchestration in decision 3, and `deploy.secrets`
   as an explicit map resolving `op://` references and `env`.
4. **`satat:agent` event + `satat agent run | ls | rm`.** The second event and `kind` branch on
   the existing workflow, the scrubbed result, the `sleep` lane, CLI commands, and the label test.
5. **Docs and measurements.** Update [design.md](../../design.md) "Run it locally" and "What is
   not built", record start-up time and idle RAM in `docker-prototype.md`, and update the ADR
   "Not verified" lists.

## Acceptance

- From a clean Docker VM with secrets in 1Password: `satat deploy up` ends with every service
  healthy, including the worker. A second run changes nothing.
- `satat deploy render --check` passes on `main` and fails when a lane's `pass_env` changes without
  a re-render.
- `satat agent run smoke --wait` prints exit 0 and `ok`. The container and volume are gone afterwards.
- `satat agent run sleep`, then `satat agent rm <run>`: the run shows cancelled, and no `satat.*`
  container remains. `sleep` is a test lane added to `satat.yaml` next to `smoke` (busybox,
  `sleep 600`, tiny limits) _(my call)_. It goes through `render --check` like any lane.
- An agent run on `claude` and an issue run on `claude` at the same time: the second waits on
  the lane and account caps.
- Three `agent run`s with `limits.global: 2` run two, then one (`satat agent ls` shows one queued).
- Stopping hatchet-lite makes `deploy status` show the worker unready within 30 s, and it isn't
  restarted. Starting hatchet-lite makes it ready again with no manual step.
- `docker compose up --remove-orphans` while an agent runs doesn't remove the agent.
- `satat deploy down` leaves no `satat.*` container or volume. `--volumes` also removes Postgres
  data.
- No secret value in `compose.yaml`, rendered output, logs or `deploy status`.

## ADRs to write

- **ADR 0028: Infrastructure adapter, Compose first.** Decisions 1, 2, 3 and 6: the `Target`
  interface, rendered-and-checked-in `compose.yaml`, `deploy up` orchestration, the single
  `deploy.target`, images from a registry by digest, and the CLI on the host as the operator (not
  a second holder of the Docker API inside the platform). Amends ADR 0026's "Kubernetes later"
  to name the adapter plus a second launcher as the path.
- **ADR 0029: Worker health.** Decision 5: liveness vs readiness, what counts as connected to
  Hatchet, the start-up-only exit rule, and how it maps to Compose now and Kubernetes probes later.
- **ADR 0030: On-demand one-shot agents.** Decision 4: a second trigger event on the one
  workflow, so caps are shared; run, ls and rm through Hatchet (not Docker); the scrubbed 4 KiB
  result; no compose labels; and no health check on agents.

## Later: k3d

Not built now. Recorded so the interfaces above don't shut it out (from the earlier k3d draft and
its review):

- **k3d adapter:** `Render` produces a Helm chart's values (Postgres, hatchet-lite, worker with
  probes from decision 5, `satat.yaml` as a ConfigMap) plus per-lane namespaces,
  `SandboxTemplate`s and network policy. `Up` runs `helm upgrade --install`. Images come from a
  registry, as in decision 6.
- **`launcher/kubernetes`** must pass `launcher/conformance` unchanged. That needs a small
  `satat-entry` wrapper injected by an init container. It waits out the 2–4 s network-policy gap,
  loads `pass_env` from a mounted lane Secret (claims can't set env), captures stdout and stderr
  separately, and writes one framed result block to the pod log, because the worker can only read
  `pods/log`.
- Lane `pass_env` values then live in a per-lane Secret, not the worker's environment, and
  `satat render` labels templates with a lanes hash so drift shows on claims.

## Left to the builder

- Package layout inside `deploy/` and `cmd/satat`, and the template mechanism (`text/template`
  is fine).
- How `deploy up` waits for health (polling `docker compose ps` or the Docker API), within the
  acceptance bounds.
- Heartbeat-staleness and start-up grace values, within the 30 s bound.
- Output format of `agent ls` and `deploy status`.

## Not verified yet

- Whether the Hatchet Go SDK v0.110.5 exposes listener connection state, a health server, run
  listing and run cancellation as decision 4 and 5 assume. If cancel or list is missing, `rm` and
  `ls` fall back to the Hatchet REST API with the same token.
- That cancelling a run in Hatchet cancels the Go task context promptly enough for `Launch` to stop
  the container (test in step 4).
- That `docker compose up --remove-orphans` ignores containers without the compose project label
  on both colima and Docker Desktop.
- That `scripts/image-digest.sh` works against a plain HTTP local registry.
