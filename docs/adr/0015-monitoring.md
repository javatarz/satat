# ADR 0015: Monitoring stack

## Status
Accepted

## Context
Satat runs on a single AWS EC2 instance (on-demand `t4g.large`, 2 vCPU / 8 GB). Running
Prometheus and Grafana on the VM itself would consume RAM/CPU needed for the agent sandbox.
We need visibility into LLM spend, API performance, and VM health without adding
significant local overhead. We also want minimal tool sprawl.

## Decision

**Three-layer approach with two external services:**

1. **Grafana Cloud (free tier)** — LLM metrics + VM health. LiteLLM pushes
   metrics via OTLP to Grafana Cloud. A lightweight Grafana Alloy agent (~50 MB
   RAM) runs on the VM collecting node-level metrics (CPU, RAM, disk, network).
   Pre-built LiteLLM Grafana dashboards are imported directly.

2. **healthchecks.io (free tier)** — VM liveness dead man switch. A
   `healthcheck-pinger` sidecar pings healthchecks.io every 60s. If the ping
   stops (VM crashed, hung, network down), healthchecks.io alerts via its
   built-in notification channels.

3. **LiteLLM built-in alerting → ntfy** — Critical alerts (budget crossed,
   spend reports, LLM exceptions, hanging/slow requests, model outages) are
   routed to the existing ntfy instance. LiteLLM posts a **fixed JSON
   budget-alert schema** to a generic webhook (`general_settings.alerting:
   ["webhook"]` + `WEBHOOK_URL`); it is *not* natively ntfy-shaped, so a small
   relay is needed to turn those events into ntfy notifications. Events include
   `soft_budget_crossed`, `budget_crossed`, and `threshold_crossed` (85%/95% of
   budget). The non-blocking US$10/day spend warning from ADR 0002 is implemented
   via LiteLLM `max_budget: $SATAT_DAILY_BUDGET` / `budget_duration: 1d` plus the
   `deploy/budget-relay.py` sidecar (LiteLLM JSON → ntfy). See
   [ADR 0019](0019-ntfy-hosting-and-observability.md).

### What we rejected

| Alternative | Rejected because |
|---|---|
| Prometheus + Grafana on VM | Consumes ~500 MB+ RAM we would rather give to agent sandboxes |
| Langfuse | Canvas already shows conversation history for debugging; Langfuse's per-API-call traces add little value for a single-agent setup |
| Datadog | No meaningful free tier |
| Honeycomb | No native LiteLLM integration; requires OTLP bridge |
| Prometheus + Grafana only | No dead man switch for VM liveness; requires healthchecks.io anyway |

## Consequences

- Two external accounts to create: Grafana Cloud (free) and healthchecks.io (free)
- Grafana Alloy adds ~50 MB RAM overhead on the VM (acceptable on 8 GB)
- LiteLLM alerting covers budget/performance; ntfy covers agent-level notifications (existing)
- No historical conversation-level analytics beyond Canvas — acceptable for single-agent
- VM load visibility (CPU/RAM/disk) is available via Grafana Cloud without local Prometheus
