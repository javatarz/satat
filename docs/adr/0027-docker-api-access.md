# ADR 0027: Docker API access: the worker only, built from lane config

## Status

Accepted (2026-10-18). Follows from [ADR 0026](0026-docker-first-runtime.md). Replaces the
role that the claim-only RBAC in [ADR 0024](0024-kubernetes-agent-sandbox-runtime.md) played
for the Docker runtime.

## Context

Anything that can reach the Docker API can start a privileged container with the host
filesystem mounted. On a Linux host that is root. On Docker Desktop or colima it is root in the
Linux VM, which holds the Satat secrets and every step's workspace. The agent runs untrusted
code from issue text, so it must never reach the API, and the one process that does hold it
must not be steerable by what the agent writes.

A common mitigation is a socket proxy such as `tecnativa/docker-socket-proxy`. It filters by
API path category, not by request body. If it allows container creation, it cannot refuse a
privileged container or a host bind mount. It narrows the surface but does not make the
holder safe.

## Decision

- **One holder: the Satat worker.** The Hatchet worker process that runs the launcher is the
  only container with the Docker API. Hatchet, Postgres, LiteLLM, the token broker and the
  egress proxy get no socket. Step containers never get a socket or a path to the API. The
  pusher runs inside the worker and gets nothing extra.
- **The worker builds every create request from lane config only.** Image (by digest), user,
  capabilities, limits, network, labels and mounts come from `satat.yaml` and are checked
  against the hardening floor in ADR 0026 before the call. Issue text, agent output, logs and
  workflow inputs never influence a container flag. A unit test asserts that a create request
  with a bind mount, `privileged`, a host network or the socket path is refused by the builder.
- **A socket proxy sits in front, as defence in depth.** The worker reaches the engine through
  a proxy that allows only the container, network, image-inspect, volume, `_ping` and
  `/system` endpoints, and denies exec, swarm, secrets, build and the rest. It is documented as
  narrowing the surface, not as a boundary against a compromised worker.
- **`SYSTEM: 1` is enabled on the proxy, for `GET /system/df` only.** The cache reaper
  (ADR 0033) needs volume sizes and use counts, and the daemon reports
  them only there. The proxy cannot keep this read-only itself: it filters by path prefix (its
  `/system` regex is not anchored) plus the global `POST` flag, which is on. The protection is
  the daemon's route table: Docker 29.5.2 registers only `GET /system/df` under `/system`
  (moby `daemon/server/router/system/system.go`), and `POST`, `PUT` and `DELETE` on
  `/system/df`, `/system/prune`, `/system/info`, `/system/events`, `/system/` and `/system` all
  return 404. A tripwire integration test (`launcher/docker/system_routes_test.go`) asserts
  this against the real daemon, so a daemon upgrade that adds a route under `/system` fails
  CI and sends us back to this decision.
- **The worker's trust is stated, not assumed away.** A compromise of the worker is a compromise
  of the Docker VM. The worker is small Go code that parses structured data from Hatchet and
  step results, and does not execute step output.
- **Where it moves to a Linux host,** use rootless Docker, so API access is user access and not
  root. This is a precondition for ADR 0026's "revisit when the laptop is no longer the host".

## Consequences

- The Compose file mounts the socket (or the proxy's endpoint) into exactly one service.
  Reviewers can check that with a grep, and a CI check does.
- A body-inspecting proxy (one that rejects create requests outside the lane shape) is a
  stronger option. It is not built now. Add it if the worker's input surface grows.
- The `/system` allowance rests on the daemon's route table, not on the proxy. The tripwire test
  is the control and must stay green. If it fails, disable `SYSTEM` (or replace the proxy with
  a body- and method-aware one) and give the reaper another way to read volume sizes.
- The hardening test in ADR 0026 is also the access-control test: the same builder is the gate.
- The rootless requirement makes `runsc` and some network features more work on a Linux host.
  That is accepted at that point.

## Not verified

- ~~That the endpoint allowlist is enough for the launcher.~~ Verified in slice 2 (2026-10-19): the
  launcher conformance suite passes through the proxy with the `compose.yaml` flags, including
  `PutArchive`, volume and network create, image pull, logs, wait and stop.
- ~~That the proxy works with the Hatchet Go worker's Docker client on Docker Desktop and colima.~~ Verified in
  slice 5 (2026-10-21) on colima: the worker container uses `DOCKER_HOST=tcp://docker-proxy:2375`.
- That the tripwire covers every daemon the worker may meet. It runs against the daemon the
  tests find through `DOCKER_HOST` (Docker 29.5.2 on colima when written). It does not run
  through the proxy, and it skips without Docker or under `-short`. A different engine or a
  newer Docker is checked only when the test runs against it.
- Rootless Docker with per-lane networks and the egress proxy on a Linux host.

Revisit this ADR if a second component needs the Docker API, or if the worker starts to act
on step output.
