# ADR 0017: Ingress, runtime, and remote access

## Status

Accepted (2026-10-05). Supersedes [ADR 0008](0008-full-canvas-reverse-proxy.md),
and the nginx and Headscale portions of [ADR 0014](0014-auth-network.md).

## Context

The earlier design used nginx + certbot for TLS termination, a Headscale (Tailscale)
control plane for remote access, and an "inject secrets, restart services" deploy. That
is three moving parts where one will do, and Headscale adds a stateful control-plane
service to operate. We want the fewest components that still give automatic TLS and a
private administrative path.

## Decision

### Ingress: Caddy

Caddy is the single public entry point on port 443 only. It obtains and renews
Let's Encrypt certificates automatically (ACME, TLS-ALPN-01 on 443) — no separate
certbot cron, and no HTTP-01 listener on port 80 (port 80 is closed). Routing:

| Path | Target |
|------|--------|
| `/` | static 200 |
| `/oauth2/*` | oauth2-proxy (sign-in / callback; not gated) |
| `/canvas` | redirect to `/canvas/` |
| `/canvas/*` | Canvas (forward_auth via oauth2-proxy) |
| `/api/*` | Canvas — Automation Server + Agent Server, incl. the GitHub event receiver `/api/automation/v1/events/github` |
| `/sockets/*` | Canvas (websockets) |
| `/ntfy/*` | ntfy (forward_auth via oauth2-proxy) |
| `/v1/*` | LiteLLM |

Canvas paths are forwarded **unchanged** (no prefix stripping); the proxy must not remove
a prefix. There is no separate `/webhook` route — the GitHub receiver is under `/api/*`.

nginx and certbot are removed.

### Runtime: Docker Compose

All services run under one `docker compose` stack on the single host: `caddy`, `litellm`
(+ Postgres), `canvas`, `oauth2-proxy`, `ntfy`, `alloy`. Deploy ships
`deploy/docker-compose.yml` plus rendered configs and runs `docker compose up -d`.
LiteLLM's spend tracking and budget enforcement require a database — without one it fails
open (no cap) — so Postgres is a hard dependency of the `litellm` service, not optional.

### Remote access: plain WireGuard

No public SSH. The VM is a WireGuard server (`wg0` = `10.10.0.1/24`, UDP 51820) with two
static peers:

| Peer | Address | Used by |
|------|---------|---------|
| `laptop` | `10.10.0.2/32` | owner |
| `ci` | `10.10.0.3/32` | deploy pipeline |

`sshd` binds `wg0` only. The firewall allows only `443/tcp` and `51820/udp`. Peers
initiate (works behind NAT); the server learns endpoints by roaming. The server keypair
is generated at boot; client public keys are Terraform variables. Headscale/Tailscale is
removed.

## Consequences

- **Fewer services**: no nginx, no certbot, no Headscale — Caddy handles TLS and routing;
  plain WireGuard handles access.
- **Automatic certificates**: Caddy issues/renews without operator action.
- **Static peer management**: adding a peer means adding a `[Peer]` block and reloading
  `wg0` (no control plane, no ephemeral keys).
- **Tunnel-only SSH**: recovery when `wg0` is down requires out-of-band console access
  (cloud provider console).
- **CI auth is a WireGuard key + an SSH key**, both stored as GitHub secrets.
