# Research: Agent Canvas automations (T8, issue #27)

Primary sources (fetched 2026-10-08): the open-source `OpenHands/automation` service
(models, trigger matcher, git-sync serializer and import loop) and
`OpenHands/OpenHands` Agent Canvas (manifest/`bundle` packing), plus the
`OpenHands/extensions` catalog that ships the pre-built issue-to-PR automation.
Anything not directly observed in those sources is marked UNVERIFIED.

## What an automation actually is

An automation is **a code tarball plus metadata**, not a declarative YAML document.
The DB model (`openhands/automation/models.py`, `Automation`) is: `name`, `trigger`,
`tarball_path`, `entrypoint`, `setup_script_path`, `timeout`, `keep_alive`, `model`,
`agent_profile_id`, `prompt`, `preset_metadata`, `state`, `observability_associations`.

Git Sync stores each automation as a directory under the configured sync path
(`automations/` here):

```
automations/
└── <slug>/
    ├── automation.yaml     # metadata; generated on export, hand-editable
    └── tarball/**          # the code bundle, expanded for reviewable diffs
```

`automation.yaml` fields, from `git_sync/serializer.py::_automation_yaml_fields`:
`name`, `model`, `agent_profile_id`, `trigger`, `observability_associations`,
`setup_script_path`, `entrypoint`, `timeout`, `keep_alive`, `enabled`, `state`,
`prompt`, `preset_metadata`, `tarball_source`, and (only when something is
executable) `tarball_executables`.

### Import rules (`git_sync/loop.py`)

- `name` and `entrypoint` are **required**; every other field is optional.
- An internal tarball is rebuilt from `tarball/**` and stored as a fresh upload.
  `tarball_source` matters only when `tarball/` is absent (external URL only).
- Importing a **new** directory needs an owner. In local mode that is the
  deterministic local identity, but only once the org's Git Sync config has been
  saved (`AUTOMATION_GIT_SYNC_*`), otherwise the directory is skipped with a warning.
- `agent_profile_id` is only validated when set; with it `null` the API layer accepts
  the record, but **a dispatcher-based bundle cannot run** — the bundle's entrypoint
  reads `AUTOMATION_AGENT_PROFILE_ID` (`agent_conversation.py`), and the dispatcher
  injects that env var **only when `automation.agent_profile_id` is truthy**
  (`OpenHands/automation` `dispatcher.py`). A profile id belongs to the Agent Server,
  not the automation DB, so it cannot be hand-authored reliably — it must be selected
  in Canvas (which Git Sync then exports back).
- A malformed `automation.yaml` (bad YAML, missing `name`/`entrypoint`, invalid
  trigger) skips **only that directory**; the cycle continues.

## Triggers

Discriminated union (`schemas.py`):

- **cron**: `{"type": "cron", "schedule": "*/15 * * * *", "timezone": "UTC"}`
- **event**: `{"type": "event", "source": "github", "on": "issues.labeled",
  "filter": "<JMESPath>"}`

Event keys are `"{event_type}.{action}"` (`issues.labeled`, `pull_request.opened`,
`push`), wildcards allowed (`pull_request.*`). `filter` is a JMESPath expression over
the raw webhook payload with custom functions `contains`, `glob`, `icontains`,
`regex`, `starts_with`, `ends_with`, `lower`, `upper` (`trigger_matcher.py`).

## Inbound GitHub events

The receiver is `POST /api/automation/v1/events/{org_id}/{source}`
(`event_router.py`). Two paths reach it:

- **Built-in `github`** (`{source}` = `github`) expects the body the OpenHands server
  forwards, a normalized `{"payload": <github payload>}` wrapper, not a raw GitHub
  delivery (`event_router.py`: "Missing payload in builtin source request"). Its
  signature is HMAC-SHA256 in `X-Hub-Signature-256`, verified against the shared
  **`AUTOMATION_WEBHOOK_SECRET`** (`providers.py`); `X-GitHub-Delivery` de-duplicates.
  On self-hosted Canvas nothing forwards that wrapper, so this path is for OpenHands
  Cloud/enterprise.
- **A custom webhook** registered via `POST /api/automation/v1/webhooks` accepts the
  **raw** payload with a per-webhook `webhook_secret` and configurable
  `signature_header` (GitHub: `X-Hub-Signature-256`). This is the viable self-hosted
  path for raw GitHub webhooks.

Either way the route is under `/api/*`, which the existing Caddy config proxies to
`canvas:8000` **without oauth2-proxy**; the `{org_id}` segment means an
automation server's org UUID must be in the URL. A separate `/webhook` route is
unnecessary and would 404 (the Canvas ingress only routes `/api/automation/*`, and
GitHub does not follow redirects).

## GitHub identity

**Agent Canvas has no self-hosted GitHub App support.** `GITHUB_APP_ID` /
`GITHUB_APP_PRIVATE_KEY` appear only in OpenHands Cloud / enterprise charts; the open
source automation service and its bundles authenticate with a **saved Agent Server
secret** (`GITHUB_PERSONAL_ACCESS_TOKEN` by convention, read via
`GET /api/settings/secrets/<name>`). This is the same reversal the Git Sync research
already recorded for `AUTOMATION_GIT_SYNC_TOKEN`. See ADR 0020.

## The pre-built `github-issue-to-pr` automation

`OpenHands/extensions/automations/catalog/github-issue-to-pr/manifest.json` declares a
`bundle` (code, not prompt) automation, version `1.2.2`:

- `bundle.files`: `worker.py`, `main.py`, `github_client.py`, `agent_conversation.py`.
- `bundle.entrypoint`: `python3 worker.py`; `bundle.timeout`: 300.
- `bundle.setupScript`: none — the tarball runs in an environment that already has the
  OpenHands SDK (the Canvas automation runtime).
- The host packs those files plus a rendered `config.json` and uploads the tarball
  (`OpenHands/OpenHands` `src/manifests/manifest-bundle.ts`).

`worker.py` (the entrypoint) is an idempotent **scanner**: it lists open issues
carrying the trigger label, skips any with an open `satat/issue-N` PR, and delegates
each ready issue to an agent conversation via `AgentConversationDispatcher`. The agent
clones, branches, implements, tests, pushes, and opens the draft PR **itself** — the
scanner does not poll for completion, so it works under either trigger. The catalog
form only offers a **cron** schedule (default `*/15 * * * *`).

The dispatcher starts each conversation from the automation's **agent profile**: the
profile supplies the model, tools, and secrets the conversation receives
(`agent_conversation.py` calls `workspace.get_secrets(agent_profile_id=…)`). The
`GITHUB_PERSONAL_ACCESS_TOKEN` secret must therefore be attached to that profile — the
bundle's own `AGENT_SECRET_NAMES` allow-list belongs to the older `main.py` flow and is
not what the shipped entrypoint uses.

`config.json` keys read by the bundle (`main.py` `_CONFIG_TYPES`,
`github_client.py::run_repositories`): `repos` (list of `owner/repo`),
`trigger_label`, `branch_prefix`, `pull_request_mode` (`draft`/`ready`),
`max_new_per_run`, `agent_secret_names`, `openhands_url`, `github_token_secret`,
plus optional `repository`, `base_branch`, `review_label`.

## Decisions this forces (deviations from the issue)

1. The old `automations/satat-automation.yaml.tmpl` shape (`trigger`/`filter`/`agent`/
   `github`/`notifications`/`webhook`) was never a real schema; it is dropped. The
   automation lives in the private `javatarz/satat-automations` Git Sync repo as
   `satat-issue-to-pr/automation.yaml` + `satat-issue-to-pr/tarball/`.
2. Auth is a fine-grained **PAT** saved as `GITHUB_PERSONAL_ACCESS_TOKEN` in Canvas
   Settings → Secrets, not a GitHub App (supersedes ADR 0006).
3. The shipped automation is **cron-polled** (the catalog default), not
   webhook-triggered (supersedes ADR 0010 for this automation). The webhook endpoint
   and `AUTOMATION_WEBHOOK_SECRET` are still wired so future event automations work;
   today no automation consumes the secret.
4. The LiteLLM endpoint/model, Docker sandbox, and the standard-tier profile are
   **Agent Server settings** configured in Canvas (and the profile's allowed secrets),
   not values in this repo or in `automation.yaml`. The automation is committed
   `state: INACTIVE, agent_profile_id: null`; the operator selects the profile and
   enables it in Canvas, and Git Sync exports the UUID back.
5. `WEBHOOK_SECRET` (GitHub secret) is passed to Canvas as `AUTOMATION_WEBHOOK_SECRET`.
   It is the built-in-`github` shared secret; a self-hosted raw GitHub webhook needs a
   **custom webhook** registered at
   `https://<SATAT_DOMAIN>/api/automation/v1/events/{org_id}/{source}`. No event
   automation exists yet, so the secret is currently inert (see ADR 0020).
6. Two issue #27 acceptance bullets are **not** met by the vendored bundle and are
   reassigned rather than silently dropped: the init-message additions
   (story-refinement/oracle question, CI-round cap 3) belong with the story-refinement
   and hooks work, and ntfy notifications on agent start/finish/PR/stuck are a
   follow-up (the bundle emits none).

## Open items

- Selecting the agent profile is a mandatory manual Canvas step; Git Sync then exports
  `agent_profile_id` and `state: ACTIVE` back to `satat-automations`.
- Whether a future story-refinement / review automation uses an event trigger (then the
  webhook secret becomes load-bearing) or a custom webhook source.
- ntfy notifications on automation lifecycle events are not emitted by the vendored
  bundle.
