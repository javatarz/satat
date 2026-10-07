# LiteLLM: OTLP metrics export & budget alerting

Research target: `BerriAI/litellm` running the `ghcr.io/berriai/litellm:main-stable` image.
Primary sources: docs.litellm.ai (pages last updated 2026-10-06) and the BerriAI/litellm repo.
Everything below is from those docs unless marked **UNVERIFIED**.

---

## A) OTLP metrics export

### What is exported over OTLP

LiteLLM emits **traces, metrics, and (optionally) logs/events** over OTLP. Metrics are **not**
Prometheus-only anymore, but OTLP metrics are **off by default** and must be explicitly enabled.

| Signal | Default | How to enable |
|---|---|---|
| Traces (spans) | On when an OTEL callback is configured | `callbacks: ["otel"]` (v1) or `LITELLM_OTEL_V2=true` |
| Metrics (GenAI histograms) | **Off** | `LITELLM_OTEL_INTEGRATION_ENABLE_METRICS=true` |
| Logs/events (`gen_ai.content.*`) | **Off** | `LITELLM_OTEL_INTEGRATION_ENABLE_EVENTS=true` |
| Prometheus `/metrics` | Off | `callbacks: ["prometheus"]` (separate subsystem) |

The OTLP metrics emitted (identical names/units in v1 and v2) are GenAI semconv histograms:
`gen_ai.client.operation.duration`, `gen_ai.client.token.usage`, `gen_ai.usage.cost`,
`gen_ai.server.time_to_first_token`, `gen_ai.server.time_per_output_token`,
`gen_ai.client.response.duration`. Source:
<https://docs.litellm.ai/docs/observability/opentelemetry_integration#metrics-reference>,
<https://docs.litellm.ai/docs/observability/opentelemetry_v2#metrics>

`gen_ai.usage.cost`, `gen_ai.server.time_to_first_token`, and `gen_ai.server.time_per_output_token`
were renamed in the current release (older names `gen_ai.client.token.cost` etc. no longer emitted).

### Env vars — exact, current set

The canonical/current recommendation is to use **`OTEL_EXPORTER` + `OTEL_ENDPOINT` + `OTEL_HEADERS`**;
the `OTEL_EXPORTER_OTLP_*` names are documented **aliases**. From the config reference:

| Variable | Alias | Default | Purpose |
|---|---|---|---|
| `OTEL_EXPORTER` | `OTEL_EXPORTER_OTLP_PROTOCOL` | `console` | Exporter type: `console`, `otlp_http`, `otlp_grpc`, `http/json`, `http/protobuf`, `grpc` |
| `OTEL_ENDPOINT` | `OTEL_EXPORTER_OTLP_ENDPOINT` | none | OTLP endpoint URL |
| `OTEL_HEADERS` | `OTEL_EXPORTER_OTLP_HEADERS` | none | `key=value,key2=value2` |
| `OTEL_SERVICE_NAME` | — | `litellm` | `service.name` resource attr |
| `OTEL_ENVIRONMENT_NAME` | — | `production` | `deployment.environment` resource attr |
| `OTEL_MODEL_ID` | — | `OTEL_SERVICE_NAME` | `model_id` resource attr |
| `OTEL_TRACER_NAME` | — | `litellm` | Tracer name |
| `LITELLM_METER_NAME` | — | `litellm` | Meter name (when metrics enabled) |
| `LITELLM_OTEL_INTEGRATION_ENABLE_METRICS` | — | `false` | **Enable OTLP metrics** |
| `LITELLM_OTEL_INTEGRATION_ENABLE_EVENTS` | — | `false` | Enable OTLP semantic logs |
| `LITELLM_OTEL_V2` | — | `false` | Opt into the newer OTel v2 integration |
| `SSL_CERT_FILE` | — | — | CA bundle honored by the `otlp_http` exporter |

Note: `OTEL_EXPORTER_OTLP_CERTIFICATE` is set by the OTEL SDK and wins over `SSL_CERT_FILE`/`ssl_verify`.

### Config keys

- v1 (legacy, still default): `litellm_settings.callbacks: ["otel"]` (or `success_callback`).
- v2 (opt-in): still `callbacks: ["otel"]`, plus `LITELLM_OTEL_V2=true` in the env.
- Per-callback settings: `callback_settings.otel.*` (e.g. `message_logging`, `attributes.include_list` /
  `exclude_list`). There is **no documented `litellm_settings.otel: true` boolean key** — that
  spelling does not appear in the config reference or config_settings page. **UNVERIFIED / likely not a
  real key**; the verified form is `callbacks: ["otel"]`.

The task's assertion that OTLP historically supported only traces is consistent with older docs, but
current docs expose metrics over OTLP via `LITELLM_OTEL_INTEGRATION_ENABLE_METRICS=true`.

### Snippet (OTLP HTTP collector, traces + metrics)

```yaml
# config.yaml
litellm_settings:
  callbacks: ["otel"]

callback_settings:
  otel:
    message_logging: false        # optional: keep prompts/responses off OTEL
    attributes:
      exclude_list:               # optional: bound metric cardinality
        - hidden_params
        - metadata.requester_metadata
```

```shell
OTEL_EXPORTER="otlp_http"
OTEL_ENDPOINT="http://otel-collector:4318"
OTEL_HEADERS="api-key=..."
LITELLM_OTEL_INTEGRATION_ENABLE_METRICS=true
# for the newer integration instead of v1:
# LITELLM_OTEL_V2=true
```

Exporter/protocol aliases accepted: `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL`,
`OTEL_EXPORTER_OTLP_HEADERS`. OTLP gRPC requires `grpcio` (install `litellm[grpc]`).

### Prometheus (separate, documented path)

Prometheus metrics are a **different callback**, not OTLP. They are exposed at `GET /metrics`.

```yaml
litellm_settings:
  callbacks: ["prometheus"]
```

```shell
curl http://localhost:4000/metrics -H "Authorization: Bearer sk-..."
```

Key facts:
- `prometheus_client==0.20.0` is **pre-installed on the litellm Docker image** (needed for CLI runs).
- Multiple workers need `PROMETHEUS_MULTIPROC_DIR=/prometheus_multiproc`.
- Dedicated metrics port (v1.101.0+): `--prometheus_metrics_port` / `PROMETHEUS_METRICS_PORT` (Helm
  `metricsServer.enabled`, Terraform `gateway_metrics_port`). That listener does **not** use virtual-key
  auth; keep it private.
- Budget gauges exist here: `litellm_team_max_budget_metric`, `litellm_remaining_team_budget_metric`,
  `litellm_api_key_max_budget_metric`, `litellm_remaining_api_key_budget_metric`, plus
  `..._budget_remaining_hours_metric`, user/org/customer variants, and
  `prometheus_initialize_budget_metrics: true` to emit them without traffic.

Source: <https://docs.litellm.ai/docs/proxy/prometheus>

---

## B) Budget alerting / `$10/day` warning

### Budget config keys (verified)

| Key | Where | Notes |
|---|---|---|
| `max_budget` | `litellm_settings` (global), or per key/team/user/customer | float USD; `0`/unset = no cap (global doc says `0` disables) |
| `budget_duration` | same scopes | string: `"30s"`, `"30m"`, `"30h"`, `"30d"` |
| `budget_limits` | key API | list of `{budget_duration, max_budget}` — multiple concurrent windows (e.g. `$10/day` **and** `$100/month`) |
| `soft_budget` | key/team | soft threshold that alerts without blocking |
| `budget_reset_time` | `litellm_settings` | `"HH:MM"`, newest release after v1.94.0 |
| `timezone` | `litellm_settings` | IANA tz for resets (default UTC) |
| `budget_exceeded_status_code` | `litellm_settings` | e.g. `429` instead of default `422` |
| `max_internal_user_budget` / `internal_user_budget_duration` | `litellm_settings` | default per-user budget + reset |

**Is a daily budget `1d` valid?** Yes. `"1d"` is valid and resets at midnight in the configured
timezone (default UTC). Weekly `7d` resets Monday midnight; monthly `30d` resets the 1st. Sub-day
durations roll forward by interval. Source:
<https://docs.litellm.ai/docs/proxy/budget_reset_and_tz>,
<https://docs.litellm.ai/docs/proxy/team_budgets>

A `$10/day` cap is set on the **key/team/user**, not as a special "daily warning" setting:

```shell
curl 'http://0.0.0.0:4000/key/generate' \
  -H "Authorization: Bearer $LITELLM_MASTER_KEY" \
  -H 'Content-Type: application/json' \
  --data-raw '{
    "budget_limits": [
      {"budget_duration": "24h", "max_budget": 10},
      {"budget_duration": "30d", "max_budget": 100}
    ]
  }'
```

Global form (all traffic on the proxy):

```yaml
litellm_settings:
  max_budget: 10
  budget_duration: 1d
  timezone: "UTC"
```

### Alerting config keys (verified)

```yaml
general_settings:
  alerting: ["slack"]            # destinations: slack, email, webhook, ms_teams
  alerting_threshold: 300        # SECONDS — slow/hanging request threshold, NOT a budget %
  alert_types:                   # opt-in subset; unset = all defaults
    - "budget_alerts"
    - "spend_reports"
  alerting_args:
    budget_alert_ttl: 86400      # cache TTL to prevent repeat budget alerts (24h)
    daily_report_frequency: 43200
    report_check_interval: 300
```

- `alerting` is in **`general_settings`**, not `litellm_settings` (config reference default `null`;
  example shows `["slack", "email"]`).
- `alerting_threshold` default `600` seconds; it governs `llm_too_slow` / `llm_requests_hanging`, not
  budget percentages. **There is no documented `budget_alert` percentage config key.**
- `alert_types`: `budget_alerts` (default on), `spend_reports`, `daily_reports`, etc.
- Per-alert-type webhook routing: `alert_to_webhook_url` maps alert type → URL(s).
- Soft budget alerts are driven by the `soft_budget` field on a key/team, not a threshold config.

### Webhook destination (to point at ntfy)

Two distinct webhook mechanisms exist:

**1. Slack-compatible alerting** (`alerting: ["slack"]`): POSTs Slack-format JSON.
- Env: `SLACK_WEBHOOK_URL`; provider-neutral fallback `ALERTING_WEBHOOK_URL`.
- `alert_to_webhook_url` can override the URL per alert type, including `budget_alerts`.

**2. Budget webhook (BETA)** (`alerting: ["webhook"]`): POSTs a LiteLLM budget-event JSON.
- Env: **`WEBHOOK_URL`**.
- Verified payload shape:

```json
{
  "spend": 1,
  "max_budget": 0,
  "soft_budget": null,
  "token": "example-api-key-123",
  "customer_id": null,
  "user_id": "default_user_id",
  "team_id": null,
  "team_alias": null,
  "organization_id": null,
  "user_email": null,
  "key_alias": null,
  "projected_exceeded_date": null,
  "projected_spend": null,
  "event": "budget_crossed",
  "event_group": "user",
  "event_message": "User Budget: Budget Crossed"
}
```

`event` ∈ `budget_crossed`, `max_budget_alert`, `soft_budget_crossed`, `threshold_crossed`,
`projected_limit_exceeded`, `spend_tracked`, `key_created`, `key_rotated`, `internal_user_created`.
`event_group` ∈ `customer`, `internal_user`, `key`, `team`, `proxy`.

```yaml
general_settings:
  alerting: ["webhook"]
```
```shell
export WEBHOOK_URL="https://ntfy.sh/<your-topic>"
```

Test: `curl -X GET 'http://0.0.0.0:4000/health/services?service=webhook' -H "Authorization: Bearer $LITELLM_API_KEY"`.

Source: <https://docs.litellm.ai/docs/proxy/alerting>

### Threshold / percentage alerts

- `threshold_crossed` is documented as "currently sent when 85% and 95% of budget is reached."
- Whether those percentages are configurable is **UNVERIFIED** — no config key for them is documented
  on the alerting page; `alerting_args` exposes only TTLs/outage thresholds, and `budget_alert_ttl`
  dedupes repeats.
- `projected_limit_exceeded` applies to keys with `soft_budget` and includes `projected_exceeded_date`
  and `projected_spend`.
- Daily vs monthly: these are independent `budget_duration`/`budget_limits` windows. There is no
  separate "per-day warning" concept; `daily_reports`/`spend_reports` send scheduled spend summaries
  (`spend_report_frequency`, `alerting_args.daily_report_frequency`), and budget events fire per window
  as spend crosses it.

### ntfy note

LiteLLM POSTs JSON to `WEBHOOK_URL` (budget webhook) or Slack-format JSON (`alerting: ["slack"]`).
ntfy's JSON publish endpoint expects a specific body (`topic`, `message`, etc.), and the exact
acceptance of LiteLLM's payloads by ntfy is **UNVERIFIED** from primary sources. If direct POSTs are
rejected, put a tiny shim (or an OTEL/alert relay) between `WEBHOOK_URL` and the ntfy topic. The
verified env var to set remains `WEBHOOK_URL`.

---

## Source URLs

- OTLP/env/config: <https://docs.litellm.ai/docs/observability/opentelemetry_integration>
- OTel v2 + metrics: <https://docs.litellm.ai/docs/observability/opentelemetry_v2>
- Prometheus: <https://docs.litellm.ai/docs/proxy/prometheus>
- Logging (OTEL quickstart): <https://docs.litellm.ai/docs/proxy/logging>
- Alerting/webhooks: <https://docs.litellm.ai/docs/proxy/alerting>
- Budgets (keys/teams/users): <https://docs.litellm.ai/docs/proxy/users>
- Team budgets: <https://docs.litellm.ai/docs/proxy/team_budgets>
- Budget reset/timezone: <https://docs.litellm.ai/docs/proxy/budget_reset_and_tz>
- Config reference: <https://docs.litellm.ai/docs/proxy/config_settings>
