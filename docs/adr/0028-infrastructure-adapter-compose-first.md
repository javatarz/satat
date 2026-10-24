# ADR 0028: Infrastructure adapter, Compose first

## Status

Proposed (2026-10-23). Based on the [infrastructure adapter design](../research/v2/deploy-targets.md).
Amends [ADR 0026](0026-docker-first-runtime.md): "Kubernetes later" now means a second
infrastructure adapter plus a second launcher, not a rewrite. Keeps [ADR 0027](0027-docker-api-access.md)
as written.

## Context

The platform on `main` comes up by hand: `scripts/hatchet-token.sh`, a `POSTGRES_PASSWORD` set in
`.env`, then `scripts/compose-up.sh`, which reads three secrets from 1Password and runs `docker
compose up`. `compose.yaml` is hand-written, and the worker's environment lists every lane's
`pass_env` name, so adding a lane means editing two files that have to agree.

The owner wants one command to bring the platform up on Compose now, and room to deploy it to
k3d later without changing the worker, the lanes or the workflows. A database for agent
definitions was considered and left out. `satat.yaml` stays the only source.

## Decision

- **Two seams.** The *infrastructure adapter* decides where the long-lived platform runs
  (Postgres, hatchet-lite, the socket proxy, the worker). The existing *launcher* decides where
  each one-shot agent runs. Agents are never platform services and are not in `compose.yaml`.
- **One interface.** A `deploy.Target` with `Render`, `Up`, `Down` and `Status`. The Compose
  adapter is the only one built. `deploy.target: compose` in `satat.yaml` selects both the adapter
  and the Docker launcher, because a mixed install is not supported.
- **`compose.yaml` is rendered and checked in.** An embedded template plus `satat.yaml` produces
  it. What varies is the worker's environment (every lane's `pass_env` and the worker's own
  secrets). The file stays in git so reviewers read it and the socket-exposure check keeps
  working. CI fails when the checked-in file differs from a fresh render.
- **`satat deploy up` does the manual steps in order:**
  1. refuse a stale `compose.yaml`;
  2. create `POSTGRES_PASSWORD` in `.env` if it's absent, and never rotate it;
  3. start Postgres, hatchet-lite and the proxy, and wait for them to be healthy;
  4. create the Hatchet token if it's absent;
  5. resolve the other secrets into the child process environment only, so they are never
     written to the repo or the host's working tree (dockerd still keeps the worker's container
     environment inside the Docker VM);
  6. start the worker and wait for its readiness ([ADR 0029](0029-worker-health.md)).

  It's idempotent. `down` also removes agent containers left behind, by label. `status` reports
  each service and the worker's readiness detail.
- **Secrets are an explicit map** under `deploy.secrets`, from each variable name to an `op://`
  reference or `env`. The 1Password field names don't follow from the variable names. When the
  section is present, every lane `pass_env` name and `SATAT_PUSH_TOKEN` must be mapped. `.env`
  holds only `POSTGRES_PASSWORD` and `HATCHET_CLIENT_TOKEN`.
- **Images come from a registry.** Lane images are registry references pinned by manifest digest,
  pullable anonymously. Satat doesn't run or choose the registry. Private-registry pull
  credentials are a later change, because they add a worker secret.
- **The CLI runs on the host as the operator.** `satat deploy` drives `docker compose` and, for
  `down`, removes labelled agent containers. The operator already owns the engine, so this is not
  a second holder of the Docker API inside the platform. No container besides the worker gets the
  socket proxy.

## Consequences

- Adding a lane is one edit to `satat.yaml` and a re-render. CI catches a forgotten render.
- `scripts/compose-up.sh` and `scripts/hatchet-token.sh` duplicate `deploy up` until they are
  retired. They are kept for now.
- A k3d adapter later implements the same `Target` with Helm, plus a Kubernetes launcher
  ([ADR 0024](0024-kubernetes-agent-sandbox-runtime.md)). The design records what that needs: a
  `satat-entry` wrapper, lane secrets mounted from a per-lane Secret, and a framed result block on
  the pod log.
- The rendered `compose.yaml` is generated text in git. A hand edit is lost on the next render,
  and its header says so.
- Anonymous-pull registries only, for now. A public package or a local registry.
- `deploy up` always runs the worker's `compose up`, so a changed worker image or environment
  recreates the worker, and agents running at that moment are orphaned. The new worker neither
  adopts nor stops them: `agent rm` reports the run cancelled in Hatchet while the container
  keeps running, with its tokens, until it exits, the reaper removes it (after twice the longest
  lane timeout plus an hour) or `deploy down`. `deploy up` prints a warning first when any agent
  container is running. Run it when no agents are running, or follow it with `deploy down` and
  `deploy up` to clear them.
- `deploy down --volumes` deletes Hatchet's data, so it also removes the now-dead
  `HATCHET_CLIENT_TOKEN` from `.env`; the next `up` mints a new one. `POSTGRES_PASSWORD` is kept
  and the fresh volume adopts it.

## Verified

Measured on colima with a scratch config, no `claude` lane and placeholder secrets
([docker-prototype.md](../research/v2/docker-prototype.md#deploy-adapter-2026-10-23)):

- `docker compose up --remove-orphans` leaves agent containers alone. Colima only: Docker Desktop
  was not tried.
- `deploy up` takes 3 min 32 s cold (with the worker image build) and 0.3 s warm. The 0.3 s was
  measured before `up` was changed to always run the worker's `compose up -d --build`, so a warm
  run now also includes a cached image build and takes longer. What still holds is the worker
  check taken after that change: a warm run leaves the worker container's ID and `StartedAt`
  unchanged ([docker-prototype.md](../research/v2/docker-prototype.md#deploy-adapter-2026-10-23)).

## Not verified

- The platform's idle RAM with the health server. It was not measured.
- `deploy up` with the real `claude` lane image and 1Password secrets, and with Docker Desktop.
- The build script's manifest digest against a registry. The script was written and shellchecked,
  and nobody has run it against one yet.

Revisit this ADR when a second adapter is built, or if `deploy.secrets` needs a source other than
1Password or the shell.
