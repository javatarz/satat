# Full Agent Canvas behind a reverse proxy

The VM runs the full Agent Canvas (`agent-canvas --public`) as the UI and backend, served
behind a reverse proxy at the configured domain with automatic TLS termination. The proxy
is the single public entry point, routing HTTPS to Canvas (port 8000), ntfy (port 8080),
and LiteLLM (port 4000). This avoids running any OpenHands UI locally on the user's laptop.
The alternative of backend-only mode (Canvas UI on laptop, Agent Server on VM) requires a
running laptop to manage automations and view conversations, defeating the always-on goal.

> Tech note: the reverse proxy is **Caddy** (automatic HTTPS), not nginx + certbot. See
> [ADR 0017](0017-ingress-runtime-and-access.md). This ADR records the durable decision —
> "serve the full Canvas behind a reverse proxy" — independent of the proxy implementation.

## What the proxy must route (verified against the image, 2026-10)

The all-in-one `ghcr.io/openhands/agent-canvas` image serves its ingress on port 8000 and
maps `/api/automation/*` to the Automation Server, `/api/*` to the Agent Server, and
`/canvas` to the static frontend. Paths must be forwarded **unchanged** — the proxy must
not strip a prefix (`handle`, not `handle_path`). Caddy therefore proxies both
`/canvas/*` and `/api/*` to `canvas:8000`; the inbound GitHub event webhook lives at
`/api/automation/v1/events/github` and is covered by the `/api/*` route, so no separate
`/webhook` route is needed.
