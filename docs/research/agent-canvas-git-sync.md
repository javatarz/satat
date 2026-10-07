# Research: Agent Canvas image + Git Sync (T5, issue #24)

Primary sources: docs.openhands.dev (Agent Canvas docs, fetched 2026-10-07) and the
upstream source repos `OpenHands/automation` and `OpenHands/OpenHands` (default branch
`main`, inspected 2026-10-07 via GitHub code search / contents API). Anything not
directly observed in source is marked UNVERIFIED.

## Image and runtime

- Official all-in-one image: **`ghcr.io/openhands/agent-canvas:<version>`**. Pin
  `1.25.0` (tag verified to exist; `docker manifest inspect`). Latest npm package is
  `@openhands/agent-canvas` v1.25.0. The image packages Canvas client, Agent Server,
  Automation Server and an ingress in one container.
- The container runs its ingress on `$PORT` (default **8000**). Persistent state lives
  under `/home/openhands/.openhands`; project workspaces are mounted at `/projects`.
- Public mode (`--public`) requires `LOCAL_BACKEND_API_KEY` and is intended for
  deployments reachable beyond localhost. In the container the flag is already implied
  by setting the key; there is no separate `--public` for `docker run`.

### Ingress routes (from `OpenHands/OpenHands` `docker/entrypoint.sh`)

The entrypoint runs a proxy on `$PORT`:

| External path | Upstream |
|---|---|
| `/api/automation/*` | Automation Server |
| `/api/*` | Agent Server |
| `/canvas` (+ `/canvas/*`) | static Canvas frontend |

- `AGENT_CANVAS_BASE_PATH` (default `/canvas`) is the frontend mount. `AGENT_SERVER_PORT`
  defaults to `18000`; `AUTOMATION_AGENT_SERVER_URL` defaults to
  `http://127.0.0.1:18000`.
- Reserved paths rejected as base-path collisions: `/api`, `/sockets`, `/server_info`,
  `/alive`, `/health`, `/ready`, `/docs`, `/redoc`, `/openapi.json`, and the canvas mount.
- **Consequence for Caddy:** proxy `/canvas/*`, `/api/*`, and `/sockets/*`
  (websockets) to `canvas:8000` *without* stripping the prefix (`handle`, not
  `handle_path`). The old `handle_path /canvas/*` would strip `/canvas` and 404. A bare
  `/canvas` is redirected to `/canvas/` because `handle /canvas/*` does not match it.

### Container environment variables

| Variable | Purpose |
|---|---|
| `LOCAL_BACKEND_API_KEY` | Server API key / session auth. Required in public mode; the user-facing key. |
| `OH_SECRET_KEY` | Encrypts stored settings and secrets. Auto-generated + persisted if unset (we set it for reproducibility). |
| `PORT` | Ingress port (default 8000). |
| `AGENT_SERVER_PORT` | Internal agent-server port (default 18000). |
| `AGENT_CANVAS_BASE_PATH` | Frontend mount (default `/canvas`). |
| `AGENT_CANVAS_DISABLE_TELEMETRY` | `1`/`true` disables product telemetry. |

## Local mode and Git Sync

- `is_local_mode` = `bool(agent_server_url)` (`automation/config.py`). The all-in-one
  image sets `AUTOMATION_AGENT_SERVER_URL` to `http://127.0.0.1:18000`, so **the
  container is always in local mode** — env-level Git Sync config is honored.
- `app.py`: if `git_sync_repo_url` is set *and not local mode*, the env config is
  **ignored** with a warning (each org configures its own repo in the UI). In local
  mode the env config is used. Git Sync defaults to **enabled**; configuring a repo is
  what turns it on.

### Git Sync environment variables (all `AUTOMATION_` prefixed)

| Variable | Default | Notes |
|---|---|---|
| `AUTOMATION_GIT_SYNC_REPO_URL` | `""` | HTTPS clone URL of the private automations repo. |
| `AUTOMATION_GIT_SYNC_BRANCH` | `main` | Branch pulled from and pushed to. Created on first cycle if absent. |
| `AUTOMATION_GIT_SYNC_PATH` | `automations` | Repo-relative directory holding automations. `..` rejected. |
| `AUTOMATION_GIT_SYNC_TOKEN` | `""` | HTTPS bearer token (PAT) with read **and write** contents. No SSH/App field. |
| `AUTOMATION_GIT_SYNC_ENCRYPTION_KEY` | `""` | If set, files are committed as ciphertext (breaks diffs) — leave unset. |
| `AUTOMATION_GIT_SYNC_AUTHOR_NAME` | `OpenHands Automation` | Commit author. |
| `AUTOMATION_GIT_SYNC_AUTHOR_EMAIL` | `automation@openhands.dev` | Commit author. |
| `AUTOMATION_GIT_SYNC_LOCAL_WORKDIR` | `""` | Checkout working dir. |
| `AUTOMATION_GIT_SYNC_GIT_TIMEOUT_SECONDS` | `60` | Per-git-command timeout. |
| `AUTOMATION_GIT_SYNC_SECRET` | `""` | Wraps the per-org token/encryption key at rest. |

**The sync interval has no env var.** `git_sync_interval_seconds` is runtime-only
(default `0` = manual/`Sync now`), set via the API or the Canvas UI.

### Git Sync HTTP API

All Automation Server routes mount under `base_path` = **`/api/automation`** (derived
from `base_url` path + `/api/automation`), so externally:

- `GET  /api/automation/v1/git-sync/status`
- `PUT  /api/automation/v1/git-sync/config` — fields: `enabled`, `interval_seconds`
  (`0` = manual), `repo_url`, `branch`, `path`, `token`, `author_name`, `author_email`,
  `encryption_key`. Auth: authenticated user with manage-automations permission.
- `POST /api/automation/v1/git-sync/check`
- (plus a trigger endpoint for `Sync now`)

### Repo layout (what Git Sync pushes/pulls)

```
automations/
└── <automation-name>/
    ├── automation.yaml
    └── tarball/
        └── ...
```

`automation.yaml` holds the automation config; uploaded bundle files are expanded under
`tarball/` so Git shows meaningful diffs. Import validation skips invalid directories
and logs the error rather than applying a partial config.

## Inbound GitHub events (needed for T8)

The webhook receiver is `POST /api/automation/v1/events/{source}` (source `github`),
signature-verified via HMAC against `AUTOMATION_WEBHOOK_SECRET`. Externally this is
`https://<domain>/api/automation/v1/events/github`, i.e. it is **already covered by the
`/api/*` Caddy route** — the standalone `/webhook` route in the current Caddyfile is dead.

## Decisions this forces (deviations from the issue/spec)

1. Image is `ghcr.io/openhands/agent-canvas` (pinned `1.25.0`), not a bare "canvas" image;
   API key env is `LOCAL_BACKEND_API_KEY`, not `CANVAS_API_KEY` (we keep the GitHub
   secret name `CANVAS_API_KEY` and map it in compose).
2. Git Sync is configured by **environment variables**, not a committed
   `gateway/canvas-config.yaml.tmpl`. The `canvas-config.yaml.tmpl` file in the T5 spec
   is dropped; config lives in `/opt/satat/.env` (mode 0600).
3. Private-repo auth is an **HTTPS PAT** (`AUTOMATION_GIT_SYNC_TOKEN`), not a GitHub App
   installation token. The GitHub App is still used for *event* automations in T8.
4. Caddy routes `/canvas/*` **and** `/api/*` to `canvas:8000` with no prefix strip.
5. Also set `OH_SECRET_KEY` (settings/secrets encryption) from a GitHub secret.
6. Sync interval must be set once at runtime via the API/UI (no env var).

## Open items

- Exact ghcr image *digest* (config digest observed:
  `sha256:353c5e991cffcc4f26ffc3ea13803c71fc64df8875e449f6201a7dc17795a3ee`); pin the
  version tag for now.
- Whether `PUT /v1/git-sync/config` accepts the local `LOCAL_BACKEND_API_KEY` bearer
  directly (vs. needing an org-scoped JWT) — verify at runtime; the UI path always works.
