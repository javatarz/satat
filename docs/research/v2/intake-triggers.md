# Research: intake triggers for Satat

Status: **research, 2026-10-15. No decision.** Follows
[ADR 0021](../../adr/0021-adopt-platform-plumbing.md) (adopt platform plumbing, own only
workflows) and Stack A in [`agent-platform-options.md`](agent-platform-options.md).

## The question

Work should start from GitHub and GitLab webhooks, cron schedules, production alerts and
manual dispatch. The owner wants:

- not to monitor or maintain these trigger endpoints
- not to deal with GitHub-versus-GitLab differences
- to write only the integration code and the workflows (issue to PR, alert to issue or PR)

## Answer

No self-hostable framework takes all of intake off Satat's hands. Hatchet, already in
Stack A, covers most of it, so the recommendation is to use Hatchet's built-in triggers and
not run a separate intake service.

Two things remain Satat's whatever is adopted:

- a small reconciler for missed GitHub deliveries
- per-provider mappers into one event schema

No tool found normalises GitHub, GitLab and Bitbucket payloads into one shape.

The managed Hookdeck Event Gateway would remove the most work, but it fails NN11 (no managed
control plane we cannot run ourselves).

## What Hatchet already does

Hatchet's incoming webhooks are verified in source: the
[webhook schema](https://github.com/hatchet-dev/hatchet/blob/main/api-contracts/openapi/components/schemas/v1/webhook.yaml)
and `api/v1/server/handlers/v1/webhooks/receive.go`. The
[docs](https://docs.hatchet.run/v1/webhooks) cover the rest.

| Need | Hatchet built-in |
|---|---|
| Webhook endpoint per source | Yes. Source presets `GITHUB`, `STRIPE`, `SLACK`, `LINEAR`, `SVIX`, plus `GENERIC` |
| Signature checks | GitHub preset, or generic `HMAC`, `API_KEY` or `BASIC` auth. Secrets are stored encrypted |
| Routing and filtering | CEL expressions for the event key and scope |
| Dedupe | Run idempotency, by TTL or until the run finishes. It keys on body fields: headers such as `X-GitHub-Delivery` are not visible to it |
| Cron, schedules, manual runs | Yes ([cron](https://docs.hatchet.run/v1/cron-runs)) |
| Event log and replay | Events are stored and validation failures logged. Replaying an event from the UI is UNVERIFIED |

Coverage by source:

| Source | Through Hatchet |
|---|---|
| GitHub | Native preset |
| GitLab | Generic auth with GitLab's shared token header |
| Grafana Alerting, Alertmanager | Generic HMAC or API key |
| Sentry, Gitea, Bitbucket | Generic HMAC. Header formats UNVERIFIED |
| PagerDuty | Its `v1=` signature prefix is not stripped, so use a custom-header API key or a small shim |

What Hatchet does not give:

1. **A durable buffer.** If Hatchet is down, delivery depends on the sender. GitHub never
   retries automatically, and failed deliveries can be redelivered by API for only 3 days.
   Fix: a Hatchet cron task that lists failed deliveries and redelivers them, and
   periodically reconciles label state. The estimate is UNVERIFIED: roughly 100 to 200 lines.
2. **Payload normalisation.** Each provider's payload arrives raw.
3. **Missed cron runs are not back-filled.** If the engine is down at a cron tick, that tick
   is lost. The reconciler covers the cases that matter.

## Alternatives

| Option | What it adds over Hatchet | Licence | Cost to Satat | Verdict |
|---|---|---|---|---|
| [Hookdeck Event Gateway](https://hookdeck.com) | Presets for GitHub, GitLab, Bitbucket and Grafana; retries, dedupe, replay, filters, transforms, UI | Managed SaaS | No service to run, but an external dependency | Removes the most work; **fails NN11 and NN9** |
| [Convoy](https://github.com/frain-dev/convoy) | Durable buffer, dedupe on a header (for example the delivery id), retries, replay, UI | Elastic License 2.0 | A service plus Postgres and Redis. Free edition: 1 user, 2 projects | Fails an OSI-only bar. Revisit if lost deliveries start to hurt |
| [Keep](https://github.com/keephq/keep) | ~120 alert providers, one alert schema, dedupe, correlation, GitHub/GitLab issue actions | MIT outside `ee/` | A service (API, UI, websocket) | Worth it only if alerts come from several monitoring tools. Elastic-owned since 2025, activity slowing |
| [Trigger.dev](https://trigger.dev) webhooks | ~30 provider presets (GitHub, GitLab, Sentry, PagerDuty, Linear, Jira), dedupe, replay, delivery log | Apache-2.0 | Replaces Hatchet as the engine | Closest to intake done for us, but **private beta**, and self-hosting needs a vendor flag |
| n8n, Activepieces, Windmill, Kestra | Maintained trigger nodes or routes | Mixed: n8n Sustainable Use; Windmill CE image proprietary; Activepieces MIT core; Kestra Apache-2.0 | A second workflow platform to run | Not worth a second engine. Kestra's webhook has no signature check |
| Argo Events, Knative Eventing | Event sources, including Git hosts | Apache-2.0 | Kubernetes only | Fails NN9 (no Docker-only local install) |
| Svix Ingest, Pipedream, Composio, Nango forwarding | Hosted ingest or trigger libraries | Managed, or Enterprise-gated when self-hosted | n/a | Fail NN11 |

## Removing GitHub-versus-GitLab differences

Only Go has an open-source library that handles both webhooks and API calls across forges:

- [jenkins-x/go-scm](https://github.com/jenkins-x/go-scm) (BSD-3-Clause, v1.17.1). It covers
  GitHub, GitLab, Gitea and Bitbucket, including issues, comments, labels, reviews with state,
  and GitHub App tokens.
- Gaps found in its source:
  - GitLab issue events are rejected as unknown.
  - GitLab merge-request approvals arrive as a plain update.
  - GitLab review calls return "not supported".
  - GitHub signatures are checked with SHA-1 only.
- Python and TypeScript have nothing equivalent for issue, PR and review webhooks. Pair
  per-forge clients instead:
  - Python: githubkit, plus [packit/ogr](https://github.com/packit/ogr) for the API side
  - TypeScript: `@octokit/webhooks`, plus gitbeaker

Some differences stay provider-specific whatever library is used:

- auth: GitHub App tokens versus GitLab access tokens
- signatures: HMAC versus GitLab's plain shared token
- labels: GitLab scoped labels, and label changes that arrive only as diffs
- reviews: GitHub review states versus GitLab approvals plus discussions
- identifiers: `number` versus `iid`, and how drafts are handled
- comment threading

For alerts, the Alertmanager webhook payload (v4) is the practical common format:

- Grafana's default webhook payload is a superset of it, and can be HMAC-signed.
- Datadog payloads are templated, so they can emit it.
- Sentry and PagerDuty need small translators.

[CloudEvents](https://cloudevents.io) (spec v1.0.2, graduated CNCF project) is a reasonable
envelope, but it standardises only the wrapper, not the payload.

## What Satat would write under the recommendation

- Hatchet webhook configs, one per source, kept in code through the SDK.
- A versioned Satat event schema:
  - work item labelled
  - comment added
  - PR reviewed, with state
  - PR opened, closed or merged
  - alert firing or resolved
  - cron tick
- Mappers into that schema for GitHub and GitLab (on go-scm if adapters are written in Go),
  and for Alertmanager or Grafana.
- A Hatchet cron reconciler for missed GitHub deliveries and label drift.
- A `Forge` interface for the actions workflows take: comment, open PR, read reviews.

## Revisit when

- **Sources grow past about five, or lost deliveries start to hurt.** Add Convoy, if a
  source-available licence is acceptable (open question 4).
- **Alerts come from several monitoring vendors.** Add Keep in front of Hatchet.
- **Trigger.dev webhooks reach general availability.** Re-evaluate Trigger.dev as the engine.
- **NN11 is relaxed for intake only.** Hookdeck becomes the low-maintenance option.

## Sources

- Hatchet:
  - https://docs.hatchet.run/v1/webhooks
  - https://docs.hatchet.run/v1/cron-runs
  - https://github.com/hatchet-dev/hatchet/blob/main/api-contracts/openapi/components/schemas/v1/webhook.yaml
- GitHub redelivery: https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks
- Convoy: https://github.com/frain-dev/convoy
- Hookdeck: https://hookdeck.com/docs
- Keep: https://github.com/keephq/keep
- Trigger.dev: https://trigger.dev/docs
- go-scm: https://github.com/jenkins-x/go-scm, https://github.com/drone/go-scm
- ogr: https://github.com/packit/ogr
- Alertmanager webhook: https://prometheus.io/docs/alerting/latest/configuration/#webhook_config
- Grafana webhook notifier: https://grafana.com/docs/grafana/latest/alerting/configure-notifications/manage-contact-points/integrations/webhook-notifier/
- CloudEvents: https://cloudevents.io
