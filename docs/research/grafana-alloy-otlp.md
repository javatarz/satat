# Grafana Alloy for LiteLLM OTLP + host metrics

Researched against Alloy stable **v1.20.1** (released 2026-09-28) and Grafana Cloud docs.
Goal: one `grafana/alloy` container receives OTLP from a LiteLLM container and exports to
Grafana Cloud, and scrapes host CPU/RAM/disk and remote-writes to Grafana Cloud.

## TL;DR

- LiteLLM (OTel v2) sends **traces and GenAI metrics over OTLP**; `otelcol.receiver.otlp`
  handles both signals. LiteLLM's *operational* metrics (spend, budgets, rate limits,
  deployment health) are **not** OTLP - they live on the proxy `/metrics` endpoint and are
  scraped with `prometheus.scrape`.
- Alloy's node exporter component is **`prometheus.exporter.unix`** (embeds upstream
  `node_exporter`). There is **no `prometheus.exporter.node_exporter`** component in v1.20.1.
- Grafana Cloud OTLP gateway = `https://otlp-gateway-<zone>.grafana.net/otlp`, **Basic auth**
  (username = numeric instance ID, password = Cloud Access Policy token).
- Grafana Cloud Prometheus remote_write = `https://prometheus-prod-<zone>.grafana.net/api/prom/push`,
  **Basic auth** (username = Prometheus instance ID, password = Cloud Access Policy token).

Sources:
- https://grafana.com/docs/alloy/latest/reference/components/otelcol/otelcol.receiver.otlp/
- https://grafana.com/docs/alloy/latest/reference/components/otelcol/otelcol.exporter.otlphttp/
- https://grafana.com/docs/alloy/latest/reference/components/otelcol/otelcol.exporter.otlp/
- https://grafana.com/docs/alloy/latest/reference/components/otelcol/otelcol.auth.basic/
- https://grafana.com/docs/alloy/latest/reference/components/otelcol/otelcol.processor.batch/
- https://grafana.com/docs/alloy/latest/reference/components/otelcol/otelcol.processor.memory_limiter/
- https://grafana.com/docs/alloy/latest/reference/components/prometheus/prometheus.exporter.unix/
- https://grafana.com/docs/alloy/latest/reference/components/prometheus/prometheus.remote_write/
- https://grafana.com/docs/alloy/latest/set-up/install/docker/
- https://grafana.com/docs/grafana-cloud/send-data/otlp/send-data-otlp/
- https://grafana.com/docs/grafana-cloud/send-data/metrics/metrics-prometheus/
- https://docs.litellm.ai/docs/observability/grafana_cloud
- https://docs.litellm.ai/docs/observability/opentelemetry_v2
- https://docs.litellm.ai/docs/proxy/prometheus

---

## 1. OTLP receive (`otelcol.receiver.otlp`)

`otelcol.receiver.otlp` accepts gRPC and/or HTTP and routes each signal via the `output`
block. Defaults already bind `grpc {}` to `0.0.0.0:4317` and `http {}` to `0.0.0.0:4318`, but
set them explicitly. It supports **metrics, logs, and traces**.

Which signal types LiteLLM sends:
- With `LITELLM_OTEL_V2=true` and `OTEL_EXPORTER=otlp_http|otlp_grpc`, LiteLLM exports
  **traces** (GenAI spans) and, when `LITELLM_OTEL_INTEGRATION_ENABLE_METRICS=true`,
  **GenAI metrics** (`gen_ai.client.operation.duration`, `gen_ai.client.token.usage`,
  `gen_ai.usage.cost`, etc.) over the same OTLP exporter. It does **not** emit OTLP logs
  through that path. Source: LiteLLM OTel v2 docs.
- Operational LiteLLM metrics are on the Prometheus `/metrics` endpoint, so they arrive via
  `prometheus.scrape`, not OTLP. Both paths are shown below.

```alloy
otelcol.receiver.otlp "litellm" {
  grpc {
    endpoint = "0.0.0.0:4317"
  }
  http {
    endpoint = "0.0.0.0:4318"
  }

  output {
    metrics = [otelcol.processor.memory_limiter.default.input]
    traces  = [otelcol.processor.memory_limiter.default.input]
    logs    = [otelcol.processor.memory_limiter.default.input]
  }
}
```

## 2. Export to Grafana Cloud (`otelcol.exporter.otlphttp`)

Use `otelcol.exporter.otlphttp` (HTTP/protobuf) for Grafana Cloud. Set `client.endpoint` to
the **base** gateway URL; the exporter appends `/v1/metrics` and `/v1/traces` itself.
Grafana Cloud OTLP uses HTTP **Basic auth**: username = numeric instance ID, password = Cloud
Access Policy token with `metrics:write`/`traces:write` scopes.

```alloy
otelcol.exporter.otlphttp "grafana_cloud" {
  client {
    endpoint    = "https://otlp-gateway-prod-us-west-0.grafana.net/otlp"
    auth        = otelcol.auth.basic.grafana_cloud.handler
    compression = "gzip"   // default; supported: gzip, zlib, deflate, snappy, zstd, none

    tls {
      // TLS is enabled by default for https:// endpoints. Nothing needed here.
      min_version = "TLS 1.2"
    }
  }
}

otelcol.auth.basic "grafana_cloud" {
  client_auth {
    username = sys.env("GRAFANA_OTLP_USERNAME")  // numeric instance ID
    password = sys.env("GRAFANA_OTLP_TOKEN")     // Cloud Access Policy token
  }
}
```

Notes:
- Endpoint shape is `https://otlp-gateway-<zone>.grafana.net/otlp`, where zone looks like
  `prod-us-west-0`, `prod-gb-south-0`. Take the exact host from your stack's OpenTelemetry
  tile - hosts differ by when the region was created.
- The base URL + auto-appended `/v1/<signal>` matches the Grafana Cloud gateway
  (`.../otlp/v1/metrics`). Do not append `/v1/...` yourself on `client.endpoint`.
- The old Grafana Cloud path `otlp-gateway-<zone>.grafana.net` only accepts `/otlp` prefix;
  the docs' example uses exactly this. Verified against the Alloy exporter "Grafana Cloud"
  example and the Grafana Cloud OTLP page.
- **Bearer alternative:** Alloy ships `otelcol.auth.bearer` with a `token` argument. Grafana
  Cloud's documented OTLP auth is Basic, so treat Bearer-against-Grafana-Cloud as
  **UNVERIFIED**; use it only if the endpoint documents it. Snippet for a Bearer endpoint:

```alloy
otelcol.exporter.otlphttp "bearer_endpoint" {
  client {
    endpoint = "https://otlp-gateway-<zone>.grafana.net/otlp"
    auth     = otelcol.auth.bearer.gc.handler
  }
}

otelcol.auth.bearer "gc" {
  token = sys.env("GRAFANA_CLOUD_API_TOKEN")
}
```

### Gotcha: `otlphttp` vs `otlp` (gRPC)

- `otelcol.exporter.otlphttp` speaks OTLP/HTTP (proto by default), port 4318, supports TLS and
  auth over the wire, and is what Grafana Cloud's OTLP gateway expects.
- `otelcol.exporter.otlp` speaks OTLP/**gRPC** (needs a real gRPC/TLS endpoint, e.g.
  `tempo-xxx.grafana.net/tempo:443`). The docs explicitly warn gRPC **won't allow sensitive
  credentials like `auth` over insecure channels**; the Grafana Cloud OTLP gateway is HTTP,
  so use `otlphttp` for it. Use `otelcol.exporter.otlp` only when targeting a gRPC backend.

## 3. Processors: memory_limiter + batch

Grafana "strongly recommends" the batch processor on every OTel pipeline, placed **after**
`memory_limiter` and any sampling. Batching is what improves compression and reduces request
count. `otelcol.processor.batch` batching is independent per signal; defaults: `timeout`
`200ms`, `send_batch_size` `2000`, `send_batch_max_size` `3000`.

```alloy
otelcol.processor.memory_limiter "default" {
  check_interval = "1s"
  limit          = "400MiB"   // or limit_percentage + spike_limit_percentage
  spike_limit    = "80MiB"    // ~20% of limit

  output {
    metrics = [otelcol.processor.batch.default.input]
    traces  = [otelcol.processor.batch.default.input]
    logs    = [otelcol.processor.batch.default.input]
  }
}

otelcol.processor.batch "default" {
  timeout             = "5s"
  send_batch_size     = 1000
  send_batch_max_size = 2000

  output {
    metrics = [otelcol.exporter.otlphttp.grafana_cloud.input]
    traces  = [otelcol.exporter.otlphttp.grafana_cloud.input]
    logs    = [otelcol.exporter.otlphttp.grafana_cloud.input]
  }
}
```

- `memory_limiter` is **not strictly required**, but is recommended to avoid OOM when the
  exporter queue backs up. `check_interval` is required when this component is used.
- You can alternatively put batching in the exporter's `batch {}` block (exporter-level
  batching is the preferred single-location form per the docs), but don't do both.

## 4. Host metrics (`prometheus.exporter.unix` -> `prometheus.scrape` -> `prometheus.remote_write`)

Component choice: use **`prometheus.exporter.unix`**. It wraps upstream `node_exporter` and
exposes CPU (`node_cpu_seconds_total`), memory (`node_memory_*`), and disk
(`node_filesystem_*`, `node_disk_*`) collectors. There is **no `prometheus.exporter.node_exporter`**
component in v1.20.1 (confirmed against the v1.20.1 repo component tree); only
`prometheus.exporter.unix` and `prometheus.exporter.windows`.

```alloy
prometheus.exporter.unix "host" {
  // Defaults enable cpu, meminfo, filesystem, diskstats, loadavg, etc.
}

prometheus.scrape "host" {
  targets    = prometheus.exporter.unix.host.targets
  forward_to = [prometheus.remote_write.grafana_cloud.receiver]
}

prometheus.remote_write "grafana_cloud" {
  endpoint {
    url = "https://prometheus-prod-<zone>.grafana.net/api/prom/push"

    basic_auth {
      username = sys.env("GRAFANA_PROM_USERNAME")  // numeric Prometheus instance ID
      password = sys.env("GRAFANA_PROM_TOKEN")     // Cloud Access Policy token
    }
  }
}
```

Grafana Cloud remote_write details (verified):
- URL is `<Your Metrics instance remote_write endpoint>`, i.e.
  `https://prometheus-prod-<zone>.grafana.net/api/prom/push` (older stacks may use
  `https://prometheus-xxx.grafana.net/api/prom/push`).
- `basic_auth.username` = your **Metrics instance ID** (numeric), `password` = **Cloud Access
  Policy token**.

### Gotcha: running `prometheus.exporter.unix` inside Docker

The exporter reads the host's `/proc`, `/sys`, and root filesystem, so bind-mount them and
point the component at the mount points (and grant extra capabilities for some collectors):

```yaml
# docker-compose fragment
services:
  alloy:
    image: grafana/alloy:v1.20.1
    command:
      - run
      - --server.http.listen-addr=0.0.0.0:12345
      - --storage.path=/var/lib/alloy/data
      - /etc/alloy/config.alloy
    volumes:
      - ./config.alloy:/etc/alloy/config.alloy
      - alloy-data:/var/lib/alloy/data
      - /proc:/host/proc:ro
      - /sys:/host/sys:ro
      - /:/host/root:ro
    pid: host
    volumes_from: []   # illustrative; provide capabilities as needed
    cap_add:
      - SYS_TIME
    ports:
      - "4317:4317"   # OTLP gRPC from LiteLLM
      - "4318:4318"   # OTLP HTTP from LiteLLM
      - "12345:12345" # Alloy UI (optional, keep internal if you prefer)
    restart: unless-stopped
```

Then set on the exporter to read the host mounts:

```alloy
prometheus.exporter.unix "host" {
  procfs_path = "/host/proc"
  sysfs_path  = "/host/sys"
  rootfs_path = "/host/root"
}
```

Do not enable Alloy clustering with `prometheus.exporter.unix` (the default `instance` label
is the hostname; clustering's consistent hashing needs identical targets across instances).

## 5. Running Alloy in Docker (verified)

- Image: `grafana/alloy:<version>` (pin, e.g. `grafana/alloy:v1.20.1`); `grafana/alloy:latest`.
- Config path convention in the container: `/etc/alloy/config.alloy`.
- Command: `run --server.http.listen-addr=0.0.0.0:12345 --storage.path=/var/lib/alloy/data /etc/alloy/config.alloy`.
  `--server.http.listen-addr` must be `0.0.0.0` (not default localhost) or the UI is not
  reachable outside the container.
- Ports to expose: **4317** (OTLP gRPC) and **4318** (OTLP HTTP) from LiteLLM; 12345 only if
  you want the UI. In Compose, LiteLLM reaches Alloy by service name, e.g.
  `OTEL_ENDPOINT=http://alloy:4318` (so 4317/4318 need not be published to the host).
- `--storage.path` is required for `prometheus.remote_write` WAL persistence; mount a volume.

docker run equivalent from the docs:

```shell
docker run \
  -v <CONFIG_FILE_PATH>:/etc/alloy/config.alloy \
  -p 12345:12345 -p 4317:4317 -p 4318:4318 \
  grafana/alloy:v1.20.1 \
    run --server.http.listen-addr=0.0.0.0:12345 --storage.path=/var/lib/alloy/data \
    /etc/alloy/config.alloy
```

Distroless images exist from v1.20 (`grafana/alloy:<ver>-distroless`) with the same
entrypoint/config/storage paths.

## 6. Deprecations / renamed components to watch

- `otelcol.auth.basic`: top-level `username`/`password` are **deprecated**. Use the
  `client_auth` block for client auth (exporters) and the `htpasswd` block for server auth
  (receivers). The docs' current examples use `client_auth`.
- `otelcol.exporter.otlphttp` / `.otlp`: `idle_conn_timeout`, `max_idle_conns`,
  `max_idle_conns_per_host` are deprecated in favor of the `keepalive` block.
- `otelcol.exporter.*` `sending_queue`: `blocking` renamed to `block_on_overflow`.
- `prometheus.remote_write`: `wal_truncate_frequency`/`min_wal_time`/`max_wal_time` are the
  older spellings of `wal.truncate_frequency`/`min_keepalive_time`/`max_keepalive_time`.
- `prometheus.exporter.node_exporter` does not exist; the node exporter is bundled inside
  `prometheus.exporter.unix`.

## 7. Full config (combines OTLP path + host metrics path)

```alloy
// ---- OTLP from LiteLLM -> Grafana Cloud -------------------------------
otelcol.receiver.otlp "litellm" {
  grpc { endpoint = "0.0.0.0:4317" }
  http { endpoint = "0.0.0.0:4318" }

  output {
    metrics = [otelcol.processor.memory_limiter.default.input]
    traces  = [otelcol.processor.memory_limiter.default.input]
    logs    = [otelcol.processor.memory_limiter.default.input]
  }
}

otelcol.processor.memory_limiter "default" {
  check_interval = "1s"
  limit          = "400MiB"
  spike_limit    = "80MiB"

  output {
    metrics = [otelcol.processor.batch.default.input]
    traces  = [otelcol.processor.batch.default.input]
    logs    = [otelcol.processor.batch.default.input]
  }
}

otelcol.processor.batch "default" {
  timeout             = "5s"
  send_batch_size     = 1000
  send_batch_max_size = 2000

  output {
    metrics = [otelcol.exporter.otlphttp.grafana_cloud.input]
    traces  = [otelcol.exporter.otlphttp.grafana_cloud.input]
    logs    = [otelcol.exporter.otlphttp.grafana_cloud.input]
  }
}

otelcol.exporter.otlphttp "grafana_cloud" {
  client {
    endpoint    = "https://otlp-gateway-prod-us-west-0.grafana.net/otlp"
    auth        = otelcol.auth.basic.grafana_cloud.handler
    compression = "gzip"
  }
}

otelcol.auth.basic "grafana_cloud" {
  client_auth {
    username = sys.env("GRAFANA_OTLP_USERNAME")
    password = sys.env("GRAFANA_OTLP_TOKEN")
  }
}

// ---- Host metrics -> Grafana Cloud ------------------------------------
prometheus.exporter.unix "host" {
  procfs_path = "/host/proc"
  sysfs_path  = "/host/sys"
  rootfs_path = "/host/root"
}

prometheus.scrape "host" {
  targets    = prometheus.exporter.unix.host.targets
  forward_to = [prometheus.remote_write.grafana_cloud.receiver]
}

prometheus.remote_write "grafana_cloud" {
  endpoint {
    url = "https://prometheus-prod-<zone>.grafana.net/api/prom/push"
    basic_auth {
      username = sys.env("GRAFANA_PROM_USERNAME")
      password = sys.env("GRAFANA_PROM_TOKEN")
    }
  }
}

// ---- LiteLLM operational Prometheus metrics (optional) ----------------
prometheus.scrape "litellm" {
  targets      = [{ __address__ = "litellm:4000" }]
  bearer_token = sys.env("LITELLM_MASTER_KEY")
  forward_to   = [prometheus.remote_write.grafana_cloud.receiver]
}
```

## Open questions / UNVERIFIED

- **Bearer vs Basic for Grafana Cloud OTLP**: only Basic (instance ID + API token) is
  documented in both the Grafana Cloud OTLP page and the Alloy example. Bearer support at the
  OTLP gateway is UNVERIFIED.
- **Exact `<zone>` host**: must be read from your stack's OpenTelemetry tile; the
  `prod-us-west-0` / `prod-gb-south-0` values in examples are illustrative.
- **LiteLLM `OTEL_EXPORTER` value casing**: docs show `otlp_http` / `otlp_grpc`; confirm
  against the LiteLLM version you deploy.
- **Alloy version pin**: latest stable at research time is **v1.20.1** (2026-09-28); re-check
  Grafana Releases before pinning.
