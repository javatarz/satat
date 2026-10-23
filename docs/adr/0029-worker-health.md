# ADR 0029: Worker health includes Hatchet; agents have none

## Status

Proposed (2026-10-23). Based on the [infrastructure adapter design](../research/v2/deploy-targets.md).
Used by [ADR 0028](0028-infrastructure-adapter-compose-first.md) (`deploy up` and `deploy status`).

## Context

The worker on `main` has no health check. Compose reports it running even when it can't reach
Hatchet and so takes no work. The owner asked for a health check that includes Hatchet
connectivity. Agents don't connect to Hatchet, so they don't get one.

The pinned Hatchet Go SDK (v0.110.5) has no worker health server and doesn't expose its listener
state publicly (the Python and TypeScript SDKs have `/livez` and `/readyz`). It does expose
`Workers().List`, with each worker's `status` and `lastHeartbeatAt`, and it sends a heartbeat
every 4 seconds.

## Decision

- **Two endpoints on the worker**, on `SATAT_HEALTH_ADDR` (default `127.0.0.1:8001`):
  - `/livez` is 200 while the process responds. It doesn't look at Hatchet, so a Hatchet restart
    never makes the worker restart and kill in-flight agents. (A recreate by `deploy up` still
    orphans them; see [ADR 0028](0028-infrastructure-adapter-compose-first.md).)
  - `/readyz` is 200 only when all of these hold. Otherwise it's 503 with JSON naming each failing
    check:
    1. Hatchet lists this process's own worker as `ACTIVE`, with `lastHeartbeatAt` within 12 s
       (3 × the SDK heartbeat);
    2. the Docker API answers `_ping` through the socket proxy;
    3. every lane loaded and passed the floor (`lanes`, checked once at start).

  The health server starts before lane setup, so a lane that fails (an image the daemon can't
  pull, say) shows as the failing `lanes` check for the start grace, after which the worker exits
  non-zero as before.
- **Its own record, by name.** The worker registers as `satat-worker-<hostname>`, so readiness never
  passes on a stale record left by an earlier container. It reads Hatchet's view of the worker,
  not local state, which also covers a worker that's connected but no longer heartbeating.
- **Checks run every 5 s.** Readiness turns false within 30 s of losing Hatchet.
- **Exit rule.** The worker exits non-zero only if it's never ready within a 2-minute start-up
  grace, and the restart policy retries. After the first ready, a transport outage (Hatchet
  stopped, restarted or unreachable) makes it unready and never makes it exit, so agents it is
  running finish. A listener stop error is different: when Hatchet rejects the worker, such as
  `Unauthenticated` after a Hatchet reset (`deploy down --volumes`, say) invalidated its token,
  the SDK's listener stops and the worker exits with it.
- **`satat-worker healthcheck`** calls `/readyz`, prints the JSON and exits 0 or 1, so the image needs
  no `curl`. Compose uses it as the service `healthcheck`. On Kubernetes later, `/livez` and
  `/readyz` become the liveness and readiness probes, with no change to the worker.
- **Agents have no health check.** The launcher already watches each agent run through its exit
  code, timeout, OOM kill and cancel, and the conformance suite tests that.

## Consequences

- The worker needs the Hatchet REST address as well as gRPC. On Compose that's
  `HATCHET_CLIENT_SERVER_URL=http://hatchet-lite:8888`, because the token's server URL is the
  browser's `localhost:8888`.
- Docker doesn't restart unhealthy containers outside Swarm. On Compose, health is what `deploy up`
  waits on and `deploy status` shows, not a restart trigger.
- Each check is one REST call every 5 s, a negligible load on hatchet-lite.
- A worker paused in Hatchet (`PAUSED`) reports unready, which is the truth: it takes no work.

## Verified

On the Compose stack on colima (scratch config, placeholder secrets,
[docker-prototype.md](../research/v2/docker-prototype.md#deploy-adapter-2026-10-23)):

- After stopping hatchet-lite the worker turned unready in about 8 s, inside the 30 s bound.
- After starting it again the worker was ready in about 15 s with a restart count of 0.

## Not verified

- That the worker keeps its heartbeat while an agent run uses most of the Docker VM's memory (the
  #104 stall), and so doesn't flap unready under load.

Revisit this ADR if the Go SDK gains a health server or public connection state. Use it then
instead of the REST check.
