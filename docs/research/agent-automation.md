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
- `agent_profile_id` is only validated when set; with it `null` and `model` `null`
  the run uses the deployment's default agent settings. A profile id belongs to the
  Agent Server, not the automation DB, so it cannot be hand-authored reliably.
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

The receiver is `POST /api/automation/v1/events/{source}` — for GitHub,
`/api/automation/v1/events/github`. Signature is HMAC-SHA256 in `X-Hub-Signature-256`,
verified against **`AUTOMATION_WEBHOOK_SECRET`**; `X-GitHub-Delivery` de-duplicates
(`providers.py`). Externally that is
`https://<SATAT_DOMAIN>/api/automation/v1/events/github`, which the existing Caddy
`/api/*` route already proxies to `canvas:8000` **without oauth2-proxy**. A separate
`/webhook` route is unnecessary and would 404: the Canvas ingress only routes
`/api/automation/*`, and GitHub does not follow redirects.

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
   not values in this repo or in `automation.yaml`.
5. `WEBHOOK_SECRET` (GitHub secret) is passed to Canvas as
   `AUTOMATION_WEBHOOK_SECRET`; the webhook itself is registered by hand at
   `https://<SATAT_DOMAIN>/api/automation/v1/events/github`.

## Open items

- Whether a future story-refinement / review automation should use an event trigger
  (then the webhook secret becomes load-bearing).
- The standard-tier model profile name in Canvas; the bundle reads the deployment's
  default LLM settings, so pin the profile in Canvas rather than in `automation.yaml`.
