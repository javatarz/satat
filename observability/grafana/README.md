# Observability dashboards

Grafana dashboard definitions tracked as code. These are imported manually into
Grafana Cloud (the stack's `grafanacloud-prom` / Grafana Cloud Metrics datasource);
there is no provisioning pipeline for them yet.

## `satat-overview.json`

A single view of what Satat is doing and what it costs:

- **At a glance** — spend, requests, tokens, error rate, VM CPU %, VM memory %.
- **Spend** — spend rate by model ($/hour) and a top-models-by-spend table.
- **Models & activity** — requests, p95 latency, tokens and failures by model/model group.
- **Host (VM)** — CPU %, memory %, disk-used % per mount.

### Import

1. In Grafana, go to **Dashboards → New → Import**.
2. **Upload JSON file** and select `satat-overview.json`.
3. When prompted for a datasource, pick **`grafanacloud-prom`** (Grafana Cloud Metrics).
4. **Import**. The dashboard `uid` is `satat-overview`, so re-importing the same file
   updates the existing dashboard rather than creating a duplicate.

### Editing / re-exporting

Edit in the Grafana UI, then **Dashboard settings → JSON model → copy**, paste it back
into `satat-overview.json`, and commit. Keep the `uid` and `title` unchanged.

## Data sources (what the panels rely on)

No extra agent is needed — everything comes from the Grafana Alloy container already
running on the VM (`gateway/alloy-config.alloy`):

| Panels | Metrics | Origin |
|---|---|---|
| Spend, requests, tokens, latency, failures | `litellm_*` | LiteLLM `/metrics`, scraped by Alloy |
| CPU, memory, disk | `node_*` | `prometheus.exporter.unix`, scraped by Alloy |

The LiteLLM-maintained dashboards (v2 / all-metrics) can be imported alongside this one
from `github.com/BerriAI/litellm/tree/main/cookbook/litellm_proxy_server/grafana_dashboard`
if you want the full metric set. For a host deep-dive, import Grafana.com dashboard
**1860** ("Node Exporter Full").

## Known gap

Issue/PR-level attribution ("currently working issue #42") is not available yet — the
activity panels reflect LLM traffic. T8 tags requests with issue metadata via LiteLLM
`custom_prometheus_metadata_labels`; a "current issues" panel can be added then.
