# Intake and event-ingestion platforms for Satat

Research date: 2026-10-15. Scope: the **Intake** layer from `docs/research/v2/platform-requirements.md`
(webhooks, cron, production alerts, polling, manual dispatch) in front of Hatchet (Stack A in
`docs/research/v2/agent-platform-options.md`). Release dates and licenses come from the GitHub API and
LICENSE files unless noted otherwise. Claims not checked against a primary source are marked UNVERIFIED.

## The short answer

No one framework does all of this for us, and Hatchet already covers more of it than Stack A
assumed. Since v1, Hatchet has **built-in incoming webhooks**: an endpoint for each webhook,
provider signature presets (GitHub, Stripe, Slack, Svix/Standard Webhooks, Linear), generic
HMAC, API-key and basic auth, CEL routing, encrypted secrets, and a 429 when the tenant is over
its limit. Cron, scheduled runs, events with CEL filters, run idempotency and manual triggers
were already there. What Hatchet does not give us:

1. **A durable buffer.** If Hatchet is down, the sender's retry policy is all we have. GitHub
   **never retries automatically**, and failed deliveries can only be redelivered for **3 days**.
2. **Payload normalisation across providers.** No tool here normalises GitHub, GitLab and
   Bitbucket payloads into one schema. Keep does normalise alerts.
3. **A received-events log that is easy to replay.** Hatchet stores events and logs
   validation failures; whether events can be replayed from the UI is UNVERIFIED.
4. **Prefix quirks.** Generic HMAC removes only `sha1=`, `sha256=`, `sha512=` and `md5=`, so
   PagerDuty's `v1=` signatures fail verification.

Options, best fit first:
- **Hatchet only, plus a small reconciler (recommended).** A Hatchet cron task re-lists failed
  GitHub deliveries and redelivers them, and periodically reconciles label state. This adds no
  new service.
- **Add Convoy** only if a durable buffer with replay and dedupe is worth running another
  service (Postgres + Redis). It is ELv2, so it fails an OSI-only license bar.
- **Add Keep** only if production alerts arrive in volume from many tools and need
  normalisation, deduplication and correlation before they become issues. Its core is MIT,
  but Elastic has owned it since May 2025 and activity is slowing.
- **Hookdeck Event Gateway** (managed) would remove the most work: presets for GitHub,
  GitLab, Bitbucket and Grafana, plus retries, dedupe, replay, filters and transforms. It fails
  NN11.

## Summary table

Key: NN8 = adds webhook or alert triggers beyond what Hatchet has. NN9 = runs locally with Docker
alone. NN11 = we can self-host it with a license that allows a public recipe. License bar:
**OSI** = OSI-approved only; **SA** = source-available allowed.

| Candidate | What it handles for intake | Hand-off to Hatchet | License (SPDX / actual) | Latest release | NN9 | NN11 | OSI bar | SA bar | Net maintenance |
|---|---|---|---|---|---|---|---|---|---|
| **Hatchet built-in** (baseline) | Webhook endpoints; GitHub, Stripe, Slack, Svix, Linear presets; generic HMAC, API key or basic auth; CEL event key, scope and filters; cron; scheduled; manual; idempotency (TTL or status) | Native | MIT | v0.110.5 (2026-10-06) | Pass | Pass | Pass | Pass | **Lowest.** Already running; we write the reconciler and normalisers |
| **Convoy** (incoming projects) | HTTP ingest with HMAC, basic or API key verification, plus a custom GitHub verifier; idempotency key from header, body or query; retries with backoff; replay and batch replay; UI. Community edition: 1 user, 1 org, 2 projects; payload filters, JS transforms and event search are paid | Convoy endpoint is a Hatchet webhook (HMAC) | `LicenseRef-Elastic-2.0` (ELv2; GitHub reports NOASSERTION) | v26.9.1 (2026-10-15) | Pass (Docker; Postgres + Redis) | Pass | **Fail** | Pass | Adds a service plus Postgres and Redis; removes buffering, dedupe and replay code |
| **Svix** (OSS server) | Outbound sender only. Bridge receives Svix-signed webhooks only. **Svix Ingest** is the hosted service | n/a | MIT (OSS); Ingest is managed | v2.8.0 (2026-10-08) | n/a | Ingest **fails** | — | — | Not an intake solution when self-hosted |
| **Hookdeck Event Gateway** | Source types with preset auth for GitHub, GitLab, Bitbucket and Grafana; generic HMAC; retries; dedupe; bookmarks and replay; filters; JS transforms; UI | HTTP push to a Hatchet webhook | Proprietary SaaS | n/a | **Fail** | **Fail** | Fail | Fail | Removes the most work, but managed |
| Hookdeck Outpost | Outbound only | n/a | Apache-2.0 | v1.6.0 (2026-10-02) | — | — | — | — | Not intake |
| **n8n** | Trigger nodes such as GitHub Trigger (registers the repo webhook through the API and verifies the signature), GitLab, Schedule, Webhook; execution log and retry | HTTP Request node to Hatchet's push API or a webhook | Sustainable Use License (+ `.ee.` files) | n8n@2.43.6 (2026-10-15) | Pass | Pass with caveat | **Fail** | Pass with caveat (internal-use only) | Adds a full automation platform to run and upgrade |
| **Activepieces** | Many "pieces" with polling or instant triggers (GitHub: issue, PR, label, push and more); schedules; run log | HTTP piece to Hatchet | MIT outside `packages/ee` (GitHub reports NOASSERTION) | 0.92.3 (2026-10-12) | Pass (Postgres + Redis) | Pass | Pass | Pass | Adds a platform; per-piece behaviour (polling vs webhook, signature checks) UNVERIFIED |
| **Windmill** triggers | HTTP routes with signature presets (GitHub, Stripe, generic HMAC); schedules; Kafka, NATS, SQS, Postgres CDC, MQTT, email and more; preprocessors | Script calls the Hatchet SDK or HTTP | AGPL-3.0 + Apache-2.0 + proprietary EE; the official CE image contains proprietary code | v1.835.0 (2026-10-14) | Pass | Pass | Pass only if built from source without EE | Pass | Heavy: a second workflow engine |
| **Kestra** triggers | Schedule, Webhook (secret key in the URL, **no HMAC**), Pebble `when` filter, polling plugin triggers | HTTP task to Hatchet | Apache-2.0 (Enterprise is proprietary) | v2.0.6 (2026-10-13) | Pass | Pass | Pass | Pass | Heavy (JVM); weak webhook auth |
| **Trigger.dev** webhooks | New in 2026: endpoints, about 30 provider presets (GitHub, GitLab, Sentry, PagerDuty, Linear, Jira, Standard Webhooks), dedupe, filters, replay API, delivery log. **Private beta**; self-hosted needs a flag from the vendor | Its own task engine (would replace Hatchet) | Apache-2.0 | v4.7.4 (2026-10-14) | Pass | Webhooks gated by the vendor | Pass (code) | Pass | Best feature fit, but it means switching engines and it is beta |
| **Pipedream** | Huge trigger library | HTTP | Pipedream Source Available License (components); platform is SaaS | n/a | **Fail** | **Fail** | Fail | Fail | Managed |
| **Nango** | Webhook forwarding with provider verification and connection attribution | HTTP push | `LicenseRef-Elastic-2.0` | v0.71.13 (2026-10-14) | Pass | **Fail**: free self-hosting is Auth and Proxy only; forwarding needs Enterprise | Fail | Fail | — |
| **Composio** triggers | Agent-oriented triggers (GitHub and others) with signature checks | Webhook to us | MIT SDK; triggers run on Composio's cloud ingress | @composio/cli@0.4.4 (2026-10-15) | **Fail** | **Fail** | Fail | Fail | Managed |
| **Keep** (alerts) | Webhook and pull providers for Grafana, Prometheus, Datadog, Sentry, PagerDuty, Opsgenie and 100+ more; normalised alert schema; dedupe; correlation; YAML workflows (alert, incident, interval, manual triggers); GitHub and GitLab issue actions | Workflow `http`/`webhook` action to a Hatchet webhook | MIT outside `ee/` (GitHub reports NOASSERTION) | v0.54.3 (2026-09-09) | Pass (Compose: API, UI, websocket) | Pass | Pass | Pass | Adds a service; removes alert-normalisation work. Elastic-owned and slowing |
| Tracecat | SOAR: webhooks, schedules, YAML/UI workflows | HTTP | AGPL-3.0 | 1.1.1 (2026-10-13) | Pass but heavy (Temporal, Postgres, MinIO, Redis, LiteLLM…) | Pass | Pass | Pass | Too heavy for this role |
| Robusta | Kubernetes alert enrichment and playbooks | HTTP sink | MIT | 0.53.0 (2026-10-15) | **Fail** (Kubernetes only) | Pass | Pass | Pass | — |
| **Argo Events** | Event sources for GitHub, GitLab, Bitbucket (Cloud and Server), webhook, calendar (cron), Kafka, SQS, NATS and more; registers webhooks automatically | HTTP trigger | Apache-2.0 | v1.9.11 (2026-07-13) | **Fail** (Kubernetes CRDs) | Pass | Pass | Pass | Kubernetes plugin only |
| Knative Eventing (+ eventing-github, eventing-gitlab) | CloudEvents sources and brokers | HTTP sink | Apache-2.0 | knative-v1.23.1 (2026-10-08) | **Fail** (Kubernetes) | Pass | Pass | Pass | Kubernetes plugin only |
| Redpanda Connect | `http_server` input; Bloblang can compute HMAC; buffers; any output | `http_client` output | Apache-2.0 core + RCL for enterprise connectors | v4.113.1 (2026-10-14) | Pass (single binary) | Pass | Pass (Apache parts) | Pass | DIY gateway in YAML; little gained over Hatchet |
| Vector | `http_server` source, transforms, sinks | HTTP sink | MPL-2.0 | vdev-v0.3.30 (2026-10-09) | Pass | Pass | Pass | Pass | Observability pipeline; no webhook verification |
| WebhookX | OSS gateway: inbound auth plugins (HMAC, key, basic), JS `function` plugin for verification, retries, declarative config | HTTP | Apache-2.0; some features need a license | v1.2.0 (2026-08-18) | Pass | Pass | Pass (core) | Pass | Small project (about 300 stars) |
| Inngest | Engine with webhooks, transforms, cron, idempotency | Its own engine | SSPL-1.0 (converts to Apache later) | v1.46.0 (2026-10-06) | Pass | Pass | **Fail** | Pass | Engine competitor |

## Hatchet built-in: the baseline in detail

Sources: the webhooks doc, the `receive.go` handler, and the events and idempotency docs.

- **Endpoint:** `/api/v1/stable/tenants/{tenant}/webhooks/{name}`, one per webhook.
  Webhooks can be created in the dashboard or through the SDK webhooks feature client (Python, TS,
  Go, Ruby), so they can be managed as code.
- **Sources (enum in code):** `GITHUB`, `STRIPE`, `SLACK` (handles the URL-verification
  challenge and the timestamp window), `SVIX` (also accepts `webhook-*` Standard Webhooks headers),
  `LINEAR`, and `GENERIC`. GitHub and Linear use the generic path with presets.
- **Generic auth:** basic auth, API key (header name plus value, compared exactly), or HMAC
  with MD5, SHA1, SHA256 or SHA512 and hex, base64 or base64url encoding. Comparison is
  constant-time. Prefixes `sha1=`, `sha256=`, `sha512=` and `md5=` are removed before comparing.
  Secrets are encrypted at rest.
- **Routing:** a CEL *event key expression* over `input` (the body) and `headers`, such as
  `'github:' + headers['x-github-event'] + ':' + input.action`. An optional CEL scope
  expression feeds event filters. An optional static payload is merged into the body. Workflows
  subscribe with `on_events`. Event filters are CEL expressions per scope.
- **Failures:** signature failures and CEL errors are recorded through
  `IngestWebhookValidationFailure` and `IngestCELEvaluationFailure`. An exhausted tenant
  resource limit returns 429.
- **Dedupe:** workflow-level **idempotency**, either TTL-based (sliding window) or status-based,
  keyed by a CEL expression over input and additional metadata. Webhook **headers are not
  added to the payload**. Only the event key and scope expressions can read them. So GitHub's
  `X-GitHub-Delivery` cannot be the idempotency key directly. Use body fields instead (for
  example issue id + action + label), or carry the delivery id in the scope (UNVERIFIED whether
  scope is visible to the idempotency expression).
- **Cron:** declared in code or created through the API. **Missed schedules are not
  back-filled** after downtime.
- **Durability gap:** Hatchet returns 204 only after the event is ingested into Postgres, so a
  receipt that is accepted is durable. A delivery that fails while Hatchet is down relies on the
  sender to retry. **GitHub does not redeliver automatically**, and you can redeliver only
  deliveries from the past 3 days.
- **Provider compatibility with generic auth.** Hatchet behaviour is verified in code; the
  sender formats come from each provider's docs.

  | Provider | Fit |
  |---|---|
  | GitHub | Preset |
  | GitLab | API key on `X-Gitlab-Token` (plain shared token) |
  | Gitea | HMAC SHA256 hex on `X-Gitea-Signature` (UNVERIFIED header name) |
  | Bitbucket Cloud | HMAC on `X-Hub-Signature` (`sha256=` prefix is removed) (UNVERIFIED) |
  | Grafana Alerting | HMAC SHA256 hex on `X-Grafana-Alerting-Signature`, **without** the optional timestamp header; or basic auth or an Authorization header |
  | Alertmanager | Basic auth or bearer through `http_config` (API-key mode compares the whole `Authorization` value) |
  | Sentry | HMAC SHA256 hex on `Sentry-Hook-Signature` (UNVERIFIED) |
  | **PagerDuty v3** | `v1=` prefix and comma-separated signatures, so **generic HMAC fails**. Needs a custom header with an API key (UNVERIFIED that PagerDuty supports custom headers) or a shim |
  | Datadog and Opsgenie | Custom header with an API key (UNVERIFIED) |

- **Ingress:** the webhook path must be reachable from the internet. The rest of Hatchet stays
  behind OAuth. This is an ingress-plugin concern (NN9).

**What we would write on Hatchet alone** (all workflow-side code, which the owner accepts):
1. Per-provider normalisers (GitHub first) in the tracker plugin, turning payloads into Satat
   work-item events. They are needed whatever the gateway, because none normalises SCM payloads.
2. A cron **reconciler** task:
   - list hook deliveries (`GET /repos/{o}/{r}/hooks/{id}/deliveries` or the app equivalent)
     and redeliver failures;
   - or simply poll for labelled issues and reconcile them against work items, which is the
     safety net anyway.

   Roughly 100–200 lines.
3. Idempotency expressions on the intake workflows.
4. One webhook definition per provider in code (SDK), plus repo or org webhook registration
   (manual, or a GitHub App with one org-level webhook).

## Candidates in brief

### Convoy (frain-dev/convoy)
- **Incoming projects:**
  - HTTP ingest with HMAC (simple and advanced signatures), basic auth, API key, and a custom
    GitHub verifier.
  - Idempotency keys built from `request.header.*`, `request.body.*` and `request.query.*`
    (SHA256), so `X-GitHub-Delivery` works.
  - Retries with a retry schedule, event replay and batch replay, and a dashboard.
- **Paid features:**
  - JS transformations, payload-based subscription filters, event search, Prometheus metrics,
    circuit breaking, RBAC.
  - The Postgres-only queue (experimental).
  - Community edition is capped at **1 user, 1 org, 2 projects**. That is fine for a single
    operator.
- **Self-hosting:** the Convoy server and agent, Postgres, and Redis. Small, but it is the
  third Postgres consumer next to Hatchet and LiteLLM.
- **License:** ELv2, which forbids offering it as a managed service. A deploy-your-own recipe
  is allowed. It fails an OSI-only bar.
- **What it removes:** the durable buffer, dedupe on delivery id, and replay. **What it adds:**
  a service plus Redis to upgrade, monitor and secure.

### Svix
The OSS `svix-webhooks` is an outbound dispatcher. Svix Bridge receives only Svix-signed
webhooks. **Svix Ingest** (GitHub, Stripe, Slack and more, with signature checks and replay)
appears only as a hosted product, so it fails NN11. Ingest code was not found in the OSS server.

### Hookdeck
- **Event Gateway** (managed):
  - Source types with preset auth, including GitHub, GitLab, Bitbucket and Grafana.
  - Generic HMAC.
  - Deduplication, retries with dead-letter, bookmarks and replay, filters, JS transformations,
    and a CLI for local forwarding.
- It removes the most intake work, but it is a **managed control plane**, so it fails NN11.
- **Outpost** is Apache-2.0 but outbound only.

### Integration platforms
- **n8n:**
  - The GitHub Trigger registers the repo webhook through the API, generates a secret and
    verifies it.
  - Strong library, but the Sustainable Use License limits use to "internal business purposes".
  - It fails an OSI-only bar and adds a full platform.
- **Activepieces:**
  - MIT core with a large trigger catalogue.
  - Needs Postgres and Redis; GitHub triggers (instant vs polling) are UNVERIFIED.
- **Windmill:**
  - HTTP routes with GitHub and Stripe signature presets, and many broker triggers.
  - The official image contains proprietary CE code, and it is a second engine.
- **Kestra:** the webhook trigger has no signature verification (a key in the URL only).

All four duplicate Hatchet's engine. Using one only as a trigger front-end means running a
second workflow platform to save writing a few normalisers.

### Trigger.dev
In 2026, v4 added first-class webhooks:
- About 30 presets, including GitHub, GitLab, Sentry and PagerDuty.
- Dedupe through an idempotency field, filters on body and headers, delivery inspection, and a
  replay API.

It is the closest to "intake done for me". But it is in **private beta**, self-hosted access is
enabled by the vendor, and it would mean replacing Hatchet as the engine. Worth re-checking at GA.

### Nango and Composio
- **Nango** webhook forwarding is Enterprise-only when self-hosted. The free tier is Auth and
  Proxy only.
- **Composio** triggers go through `backend.composio.dev` ingress.

Both fail NN11.

### Keep (alerts)
- **Coverage:** about 120 providers, with webhook push or pull for Grafana, Prometheus,
  Datadog, Sentry, PagerDuty and Opsgenie.
- **Processing:** normalised alert model, deduplication and correlation rules.
- **Workflows:** YAML with `alert`, `incident`, `interval` and `manual` triggers and CEL
  filters. Actions include creating GitHub and GitLab issues, or calling HTTP (that is, Hatchet).
- **License and ownership:** MIT outside `ee/`. Elastic acquired it on 2025-05-21. The last
  release was v0.54.3 (2026-09-09), and the last commit to main was on 2026-09-28: slowing,
  but alive.
- **Fit:** good for "prod alert to issue/PR" if alerts come from several monitoring tools,
  because it removes per-tool parsing. With one Grafana stack, Grafana's HMAC webhook straight
  to Hatchet is simpler.

### Kubernetes-native (Argo Events, Knative)
- **Argo Events** has the best SCM source coverage: GitHub, GitLab, Bitbucket Cloud and
  Server, and calendar (cron). It registers webhooks automatically.
- Both projects need Kubernetes CRDs, so they **fail NN9** (Docker-only local).
- They could at most back a future Kubernetes ingress plugin. Argo Events releases have slowed:
  v1.9.11 came out on 2026-07-13.

### Stream tools
Redpanda Connect and Vector can receive HTTP and forward it with buffering, but you configure
signature checks yourself (Bloblang HMAC). Nothing is gained over Hatchet's native endpoint.

## Recommendation for the owner's question

- **Do not adopt a separate intake framework now.** Use Hatchet's native webhooks, cron and
  manual triggers.
- **Code we maintain:**
  - provider normalisers in the tracker plugin;
  - a cron reconciler that covers GitHub's lack of retries and any missed cron;
  - idempotency expressions on intake workflows.
- **Revisit when:**
  - (a) the number of sources grows past roughly 5, or delivery loss becomes painful. Add
    **Convoy** if source-available is acceptable; there is no OSI-licensed equivalent of
    similar maturity.
  - (b) prod alerts come from several monitoring vendors. Add **Keep**.
  - (c) Trigger.dev webhooks reach GA, if an engine switch is ever on the table.
- **If the owner will accept a managed service:** Hookdeck Event Gateway removes the most
  work, but violates NN11.

## Sources

- Hatchet webhooks doc: https://docs.hatchet.run/v1/webhooks (source: https://github.com/hatchet-dev/hatchet/blob/main/frontend/docs/content/docs/v1/webhooks.mdx)
- Hatchet GitHub cookbook: https://docs.hatchet.run/cookbooks/webhooks-github
- Hatchet webhook handler: https://github.com/hatchet-dev/hatchet/blob/main/api/v1/server/handlers/v1/webhooks/receive.go
- Hatchet webhook repository (auth config): https://github.com/hatchet-dev/hatchet/blob/main/pkg/repository/webhooks.go
- Hatchet events and filters: https://docs.hatchet.run/v1/events ; idempotency: https://github.com/hatchet-dev/hatchet/blob/main/frontend/docs/content/docs/v1/idempotency.mdx ; cron: https://docs.hatchet.run/v1/cron-runs
- GitHub, failed deliveries: https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries ; redelivery 3 days: https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks
- Grafana webhook HMAC: https://grafana.com/docs/grafana/latest/alerting/configure-notifications/manage-contact-points/integrations/webhook-notifier/
- PagerDuty signatures: https://docs.pagerduty.com/developer/verifying-webhook-signatures
- Convoy: https://github.com/frain-dev/convoy (LICENSE) ; sources: https://getconvoy.io/docs/product-manual/sources.md ; idempotency: https://getconvoy.io/docs/product-manual/idempotency.md ; paid features: https://getconvoy.io/docs/business-and-enterprise/paid-features.md
- Svix: https://github.com/svix/svix-webhooks (README, bridge/README.md) ; Ingest: https://docs.svix.com/ingest
- Hookdeck: https://hookdeck.com/docs/sources ; https://hookdeck.com/docs/llms.txt (deduplication, retries, bookmarks, filters, transformations) ; Outpost: https://github.com/hookdeck/outpost
- n8n: https://github.com/n8n-io/n8n/blob/master/LICENSE.md ; GitHub Trigger source: https://github.com/n8n-io/n8n/blob/master/packages/nodes-base/nodes/Github/GithubTrigger.node.ts
- Activepieces: https://github.com/activepieces/activepieces/blob/main/LICENSE ; https://www.activepieces.com/docs/install/overview ; https://www.activepieces.com/pieces/github
- Windmill: https://github.com/windmill-labs/windmill/blob/main/LICENSE ; https://www.windmill.dev/docs/triggers.md ; https://www.windmill.dev/docs/triggers/http_routing.md
- Kestra webhook trigger: https://kestra.io/docs/workflow-components/triggers/webhook-trigger
- Trigger.dev webhooks: https://trigger.dev/docs/webhooks/overview.md ; https://trigger.dev/docs/webhooks/sources.md ; https://trigger.dev/docs/webhooks/deliveries.md
- Pipedream license: https://github.com/PipedreamHQ/pipedream/blob/master/LICENSE
- Nango: https://github.com/NangoHQ/nango/blob/master/LICENSE ; https://nango.dev/docs/guides/platform/webhook-forwarding.md ; https://nango.dev/docs/guides/platform/self-hosting/self-managed.md
- Composio triggers: https://docs.composio.dev/docs/setting-up-triggers/creating-triggers
- Keep: https://github.com/keephq/keep (LICENSE, docker-compose.yml) ; https://docs.keephq.dev/workflows/syntax/triggers.md ; https://docs.keephq.dev/llms.txt ; Elastic acquisition: https://www.businesswire.com/news/home/20250521686867/en/Elastic-Completes-Acquisition-of-Keep
- Tracecat: https://github.com/TracecatHQ/tracecat (docker-compose.yml) ; Robusta: https://github.com/robusta-dev/robusta
- Argo Events sources: https://github.com/argoproj/argo-events/tree/master/docs/eventsources/setup ; Knative: https://github.com/knative/eventing , https://github.com/knative-extensions/eventing-github
- Redpanda Connect licenses: https://github.com/redpanda-data/connect/tree/main/licenses ; Vector: https://github.com/vectordotdev/vector
- WebhookX: https://github.com/webhookx-io/webhookx ; Inngest: https://github.com/inngest/inngest/blob/main/LICENSE.md
