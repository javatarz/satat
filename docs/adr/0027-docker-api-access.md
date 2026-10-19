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
  a proxy that allows only the container, network, image-inspect, volume and `_ping`
  endpoints, and denies exec, swarm, secrets, build and the rest. It is documented as
  narrowing the surface, not as a boundary against a compromised worker.
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
- The hardening test in ADR 0026 is also the access-control test: the same builder is the gate.
- The rootless requirement makes `runsc` and some network features more work on a Linux host.
  That is accepted at that point.

## Not verified

- That the endpoint allowlist is enough for the launcher (volume create, network attach,
  `PutArchive`, logs, wait). The allowlist is written against the prototype and corrected.
- That the proxy works with the Hatchet Go worker's Docker client on Docker Desktop and colima.
- Rootless Docker with per-lane networks and the egress proxy on a Linux host.

Revisit this ADR if a second component needs the Docker API, or if the worker starts to act
on step output.
