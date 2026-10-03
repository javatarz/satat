# ADR 0015: Monitoring stack

## Status
Accepted

## Context
Satat runs on a single Contabo Core VPS 4 (4 vCPU / 8 GB). Running Prometheus
and Grafana on the VM itself would consume RAM/CPU needed for the agent sandbox.
We need visibility into LLM spend, API performance, and VM health without adding
significant local overhead. We also want minimal tool sprawl.

## Decision

**Three-layer approach with two external services:**

1. **Grafana Cloud (free tier)** — LLM metrics + VM health. LiteLLM pushes
   metrics via OTLP to Grafana Cloud. A lightweight Grafana Alloy agent (~50 MB
   RAM) runs on the VM collecting node-level metrics (CPU, RAM, disk, network).
   Pre-built LiteLLM Grafana dashboards are imported directly.

2. **healthchecks.io (free tier)** — VM liveness dead man switch. A cron job on
   the VM pings healthchecks.io periodically. If the ping stops (VM crashed,
   hung, network down), healthchecks.io alerts via its built-in notification
   channels.

3. **LiteLLM built-in alerting → ntfy** — Critical alerts (budget crossed,
   spend reports, LLM exceptions, hanging/slow requests, model outages) are
   routed to the existing ntfy instance. LiteLLM's webhook alert format is
   Slack-compatible and can be directed at ntfy.

### What we rejected

| Alternative | Rejected because |
|---|---|
| Prometheus + Grafana on VM | Consumes ~500 MB+ RAM on an already-tight 8 GB VM |
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
