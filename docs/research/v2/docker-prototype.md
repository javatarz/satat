# Docker prototype measurements (#112)

Findings from building the Docker prototype (ADR 0026). Updated per slice. Numbers are
point-in-time `docker stats`, not load measurements.

## Slice 1: Compose platform

Run on 2026-10-19: colima (arm64) with a 5.77 GiB VM, `docker compose up -d`.

| Service | Image | Idle memory |
|---|---|---|
| `hatchet-lite` | `v0.110.5` | 137 MiB |
| `postgres` | `15.6` | 258 MiB |
| `docker-proxy` | `v0.5.0` | 18 MiB |
| **Platform total** | | **about 413 MiB** |

ADR 0024 budgeted about 2.4 GiB for k3s plus the platform (with LiteLLM and a worker). Without k3s
and LiteLLM the platform is under a fifth of that. The worker is not in the stack yet.

`hatchet-lite` was healthy about 40 s after `up` (it runs its migrations on first start and
creates a tenant). The dashboard answered on `127.0.0.1:8888`.

### Docker socket proxy (`tecnativa/docker-socket-proxy` v0.5.0)

Probed from a throwaway container on the `docker-api` network, with the flags in `compose.yaml`
(`CONTAINERS`, `NETWORKS`, `VOLUMES`, `IMAGES`, `POST`, `PING`, `VERSION` on):

| Request | Result |
|---|---|
| `GET /containers/json`, `/images/json`, `/networks`, `/volumes` | 200 |
| `POST /containers/create` | reaches the engine (400 for the empty body used) |
| `POST /containers/{id}/exec` | **reaches the engine** (400 for the empty body), not blocked |
| `POST /exec/{id}/start` | 403 |
| `POST /build`, `GET /secrets`, `GET /swarm` | 403 |
| `POST /containers/prune` | **200**: removes every stopped container on the engine, not only Satat's |

Consequences, for ADR 0027:

- Exec creation passes, but starting an exec is denied, so it is inert. Do not rely on that.
- Nothing in the proxy scopes a request to Satat's own containers. `prune` and a `create` with a
  privileged body both pass. The request builder (ADR 0027's first control) is the real gate, and
  the worker must never call `prune`. The reaper removes by label, container by container.
- The proxy works as a narrowing layer and as a record of which endpoints the launcher uses.

### Still to measure

Worker RAM, real step RAM and CPU, step start time, and the platform's RAM during a step (slices 2
to 4 and the first real run).
