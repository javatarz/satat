# ADR 0019: ntfy hosting and Grafana Cloud observability

## Status

Accepted (2026-10-07).

## Context

T7 adds push notifications (ntfy) and metrics (Grafana Alloy → Grafana Cloud). Two
constraints surfaced during build that conflict with the original plan:

1. The plan/T3 placed ntfy behind Caddy at `/ntfy/*` on the main host. **ntfy cannot be
   served under a subpath** — it rejects a `base-url` with a path at startup, hardcodes its
   routes at the root, has no `X-Forwarded-Prefix`, and the maintainer has declined subpath
   support (upstream issue #1009). Verified in source; see
   [research](../research/ntfy-self-hosted.md).
2. The plan put ntfy behind oauth2-proxy. But the intended client is the **iOS ntfy app**,
   which authenticates with ntfy's own credentials and **cannot complete a browser OAuth
   redirect** through `forward_auth`.

## Decision

### ntfy on a dedicated hostname with native auth

ntfy runs on **`notify.${SATAT_DOMAIN}`** (e.g. `notify.satat.karun.me`), served at `/` by
its own Caddy site block (automatic TLS), reverse-proxied to `ntfy:8080`. It is **not**
gated by oauth2-proxy. Access control is ntfy's own **topic-as-password** model: a long
random topic name is the credential, stored in GitHub as the `NTFY_TOPIC` secret and used
by both publishers (VM, deploy workflow, LiteLLM) and the iOS subscriber. `behind-proxy:
true` and `upstream-base-url: https://ntfy.sh` (message-ID-only relay for iOS instant push,
E2E-encrypted content) are set.

A subdomain requires a manual DNS A record (DNS stays manual).

### Observability via Grafana Alloy → Grafana Cloud

A single `alloy` container:
- receives **OTLP** (gRPC 4317 / HTTP 4318) from LiteLLM (traces + GenAI metrics) and
  exports via `otelcol.exporter.otlphttp` to the Grafana Cloud OTLP gateway (Basic auth:
  instance ID + Cloud Access Policy token);
- scrapes **host metrics** with `prometheus.exporter.unix` (CPU/RAM/disk) and LiteLLM's
  `/metrics`, remote-writing to Grafana Cloud.

LiteLLM enables OTLP metrics (`LITELLM_OTEL_INTEGRATION_ENABLE_METRICS=true`) plus the
`otel` and `prometheus` callbacks. The `$10/day` budget is set globally
(`max_budget`/`budget_duration: 1d`); budget alerts POST to `WEBHOOK_URL` (an ntfy topic).
Alloy is configured by a static `gateway/alloy-config.alloy` that reads endpoints/credentials
from the container environment (`sys.env`), so no templating is needed.

A `healthcheck-pinger` sidecar curls the healthchecks.io `HEALTHCHECKS_PING_URL` every 60s
(dead-man switch for the VM). A final `if: failure()` deploy step publishes a deploy-failure
notification to the ntfy topic.

## Consequences

- **Two hostnames**: `SATAT_DOMAIN` and `notify.SATAT_DOMAIN`; both need DNS + a cert
  (Caddy issues both automatically).
- **ntfy is only as private as its topic name** over HTTPS. Stronger ntfy user/ACL auth is
  possible later (add `auth-file` + tokens) without changing the topology.
- **No browser SSO on ntfy** — by design, so the iOS app works.
- **Observability is outbound-only**: the VM pushes to Grafana Cloud; nothing scrapes inbound.
- **Budget webhook → ntfy** relies on LiteLLM's JSON payload being accepted by ntfy
  (UNVERIFIED); a tiny relay can sit between `WEBHOOK_URL` and the topic if needed.
