# Git Sync for automation-as-code

Automation definitions are stored as YAML files in the private `satat-automations` repo and synchronized bidirectionally with Agent Canvas via OpenHands' native Git Sync. Changes to automations in Canvas are exported to git; changes merged into the git branch are imported into Canvas. This is the native approach (no custom generator) and makes automations reviewable via PR. The custom `repos.yaml → generate automations` layer proposed in the bootstrap is dropped — it would duplicate functionality Git Sync already provides and add a maintenance burden.

## How it is configured (verified against `OpenHands/automation`, 2026-10)

On a self-hosted Agent Canvas the Automation Server runs in **local mode**
(`is_local_mode` = `agent_server_url` is set; the all-in-one container sets it to
`http://127.0.0.1:18000`), so Git Sync is configured by environment variables rather
than a committed config file:

- `AUTOMATION_GIT_SYNC_REPO_URL` — `https://github.com/javatarz/satat-automations.git`
- `AUTOMATION_GIT_SYNC_BRANCH` — `main`
- `AUTOMATION_GIT_SYNC_PATH` — `automations`
- `AUTOMATION_GIT_SYNC_TOKEN` — an HTTPS PAT with read+write **contents** (there is no
  SSH or GitHub-App field; the automation's own GitHub auth is likewise a PAT, see
  [ADR 0020](0020-automation-definition-auth-trigger.md))
- `AUTOMATION_GIT_SYNC_AUTHOR_NAME` / `_EMAIL` — fixed bot identity (`Satat` /
  `satat@karun.me`), hardcoded in compose rather than a GitHub variable since it is a
  constant, not a deployer-supplied value.

Git Sync is enabled by configuring a repo. The sync **interval** has no env var
(runtime-only, `PUT /api/automation/v1/git-sync/config`, `0` = manual); it is set once
via the Canvas UI/API. This is why the T5 `gateway/canvas-config.yaml.tmpl` from the
rebuild plan is dropped: there is no config file, only env vars. See
[research](../../docs/research/agent-canvas-git-sync.md) for the full schema.
