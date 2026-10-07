# Research: self-hosted ntfy (v2.x) for Docker Compose

Primary sources: docs.ntfy.sh (`config`, `publish`, `subscribe/phone`, `install`) and the
upstream repo `binwiederhier/ntfy` (`server/server.yml`, `server/config.go`,
`server/server.go`, `server/server_web.go`, `server/server_middleware.go`, `cmd/serve.go`,
`web/src/**`), inspected on `main` 2026-10-07. Latest tagged release per the install page is
`v2.28.0`. Facts below were read from source unless marked otherwise.

## TL;DR for the `/ntfy/*` plan

**ntfy cannot be served under a subpath.** `base-url` with a path is rejected at startup, and
the server has no prefix-handling (no `X-Forwarded-Prefix`, no server-side route prefix). The
only supported topology is ntfy at the **root of its own hostname** (dedicated subdomain). The
`web-root: /ntfy` value in `server.yml` only names the SPA's home route — it does not create a
URL prefix for the API or static assets. See [§1.3](#13-subpath-ntfy-critical--not-supported).

## 1. `server.yml` schema

The config file is `/etc/ntfy/server.yml`; every option also has an env var
(`NTFY_*`) and a CLI flag form. Keys below are from `server/server.yml` and `server/config.go`.

### 1.1 Minimal config

```yaml
# /etc/ntfy/server.yml
base-url: "https://ntfy.example.com"   # external URL; no path (see 1.3)
listen-http: ":8080"                   # container listen address (default ":80")
cache-file: "/var/cache/ntfy/cache.db" # SQLite cache; enables persistence across restarts
cache-duration: "12h"                  # how long messages are cached (default "12h"); 0 = disable
auth-file: "/var/lib/ntfy/user.db"     # required to enable auth/ACL manager
auth-default-access: "deny-all"        # fallback when no ACL entry matches (default "read-write")
behind-proxy: true                     # REQUIRED behind a reverse proxy (rate-limit IPs)
```

- `base-url` — external base URL. Required for attachments, e-mail footers, iOS push, Matrix
  gateway, and web push (`cmd/serve.go` validation).
- `listen-http` — HTTP listen address; default is `":80"`. The official Docker image exposes the
  web UI + API on **port 80**. Set `":8080"` only if you want the container on 8080.
- `cache-file` / `cache-duration` — with a cache file, messages survive restart; without one,
  cache is in-memory only (12h) and lost on restart.
- `behind-proxy` — must be `true` behind Caddy, otherwise every visitor is rate-limited as one IP
  (`server.yml` warning; docs `#behind-a-proxy-tls-etc`).

`auth-default-access` values: `read-write` (default), `read-only`, `write-only`, `deny-all`.

### 1.2 Dedicated subdomain is the supported topology

All official reverse-proxy examples (nginx/Apache2/Caddy) proxy the whole host at `location /`,
e.g. Caddy:

```
ntfy.example.com {
    reverse_proxy 127.0.0.1:2586
}
```

Sources: docs.ntfy.sh/config `#nginxapache2caddy`.

### 1.3 Subpath (`/ntfy`) — CRITICAL: NOT SUPPORTED

Verified from current source, not inferred:

- `cmd/serve.go` rejects any path in `base-url` at startup:

  ```go
  u, err := url.Parse(baseURL)
  ...
  } else if u.Path != "" {
      return fmt.Errorf("if set, base-url must not have a path (%s), as hosting ntfy on a sub-path is not supported, e.g. https://ntfy.mydomain.com", u.Path)
  }
  ```

  So `base-url: https://satat.karun.me/ntfy` **fails to start the container**.

- Server routes are hardcoded at the root (`server/server.go`): `apiHealthPath = "/v1/health"`,
  `webAppConfigPath = "/config.js"`, `webAppManifestPath = "/manifest.webmanifest"`,
  static assets at `/static/...`. There is no prefix option and no `X-Forwarded-Prefix`
  handling anywhere in the tree.
- `web-root` (default `/`) only sets the SPA's home route (`app_root` in `/config.js`, consumed
  as `routes.app` in `web/src/components/routes.js`). It does **not** add a URL prefix. All other
  SPA routes (`/login`, `/settings`, `/:topic`) and `index.html` asset URLs
  (`/static/...`, `/config.js`) stay absolute at root.
- Maintainer, issue #1009 (closed, no implementation): *"I don't have any intention on
  supporting a subpath right now. It cuts too deep into all the apps and features."* Related:
  #256 (closed), #398.

**Consequence for this deployment:** put ntfy on its own hostname (e.g.
`ntfy.satat.karun.me`) served at `/`, with Caddy terminating TLS and oauth2-proxy doing
`forward_auth`. Do **not** plan on `/ntfy/*` under `satat.karun.me`.

**UNVERIFIED workaround** (do not rely on it): an unofficial prefix-stripping proxy can make the
*API* work — Caddy `handle_path /ntfy/*` (which strips the prefix) forwarded to ntfy, plus a
`redir`/`rewrite` for `/ntfy/files/*`. Community reports (#1009, #398) confirm JSON *requests*
work but the **web UI breaks** because its assets/routes are root-absolute. It is not supported
and not guaranteed across releases.

## 2. iOS instant delivery

`server.yml` (`upstream-base-url`), docs.ntfy.sh/config `#ios-instant-notifications`:

- `upstream-base-url: "https://ntfy.sh"` is **required for timely iOS notifications** on a
  self-hosted server. On every publish, ntfy forwards a `poll_request` message (containing only
  the message ID) to `ntfy.sh`, which uses Firebase/APNS to wake the iOS app; the app then polls
  your server for the real message.
- Privacy: the upstream server **cannot read message contents** — only the message ID relays
  through ntfy.sh. Optional `upstream-access-token` only needed if upstream rate limits/auth
  apply.
- Must not end in `/` (`upstream-base-url must not end with a slash`).
- Without it, iOS delivery is delayed/unreliable. Android FCM only applies to the main `ntfy.sh`
  host and the Play flavor; self-hosted Android uses instant delivery.

## 3. Auth model

Docs.ntfy.sh/config `#access-control`.

- **Topic-as-password (no auth configured):** if `auth-file` is unset, the server is open and
  the topic name is the only secret. Pick a long random topic (`[-_A-Za-z0-9]`, ≤64 chars).
- **Minimal private setup:** set `auth-file` (enables the user/ACL manager) + a user, and either
  `auth-default-access: "deny-all"` with explicit ACLs, or use topic patterns. Declarative
  options: `auth-users` (`user:<bcrypt-hash>:<role>`), `auth-access`
  (`user:topic-pattern:<rw|ro|wo|deny>`), `auth-tokens` (`user:tk_...[:label]`).
  Render a hash with `ntfy user hash`.
- **Roles:** `user` (ACL-controlled), `admin` (read-write to all topics). Permission values:
  `read-write`/`rw`, `read-only`/`ro`, `write-only`/`wo`, `deny`/`none`.
- **iOS app credentials:** subscribe by adding the server URL + topic; supply auth either as
  username/password or an access token in the app's server settings. For an authenticated
  *proxy* in front (e.g. Cloudflare Access), the app's **Settings → Advanced → Custom headers**
  can send an `Authorization`/proxy header — but you cannot set a custom `Authorization` header
  for a server that also has an ntfy user configured (ntfy sets it itself).
- Access tokens grant full account access (no granular tokens yet). Use HTTPS always.

## 4. Publish API (scripts / CI)

Docs.ntfy.sh/publish. Method is **POST or PUT**, topic in the path, message in the body:

```
POST/PUT https://ntfy.example.com/<topic>
```

Headers (aliases in parentheses): `Title` (`X-Title`, `t`), `Priority` (`X-Priority`, `p`,
values `1`–`5` or `min|low|default|high|max|urgent`), `Tags` (`X-Tags`, comma-separated),
`Click` (`X-Click`, URL to open on tap), `Actions` (`X-Actions`), and
`Authorization: Basic <b64(user:pass)>` or `Authorization: Bearer <token>`.

**Actions header format:** multiple actions separated by `;`; short form for a view button is
`view, <label>, <url>[, clear=true]`.

**JSON publish format:** POST to the server root with `topic` in the JSON body. Action object
fields: `action` (`view` | `broadcast` | `http` | `copy`), `label`, `url`, `clear`.

Copy-pasteable curl — notification with a "View PR" action button (header form):

```sh
curl \
  -H "Title: Deploy finished" \
  -H "Priority: default" \
  -H "Tags: white_check_mark,rocket" \
  -H "Click: https://github.com/javatarz/satat/pull/123" \
  -H "Actions: view, View PR, https://github.com/javatarz/satat/pull/123, clear=true" \
  -H "Authorization: Bearer tk_AbCdEfGhIjKlMnOpQrStUvWxYz012345" \
  -d "satat: deploy of main succeeded" \
  https://ntfy.example.com/deploys
```

JSON form (same result):

```sh
curl https://ntfy.example.com \
  -H "Authorization: Bearer tk_AbCdEfGhIjKlMnOpQrStUvWxYz012345" \
  -d '{
    "topic": "deploys",
    "title": "Deploy finished",
    "message": "satat: deploy of main succeeded",
    "tags": ["white_check_mark", "rocket"],
    "priority": 3,
    "click": "https://github.com/javatarz/satat/pull/123",
    "actions": [
      { "action": "view", "label": "View PR", "url": "https://github.com/javatarz/satat/pull/123", "clear": true }
    ]
  }'
```

Notes: JSON `priority` is numeric (`1`–`5`); `tags` is an array. Up to three actions per
notification. `clear=true` dismisses the notification when the button is tapped.

## 5. Healthcheck

Endpoint: `GET /v1/health` → `{"healthy":true}`. Non-200 or `"healthy": false` = unhealthy
(docs.ntfy.sh/config `#health-checks`). Requires no auth.

```sh
curl -fsS http://localhost:8080/v1/health
# {"healthy":true}
```

Official Docker Compose healthcheck (from install docs; adapt host:port):

```yaml
healthcheck:
  test: ["CMD-SHELL", "wget -q --tries=1 http://localhost:80/v1/health -O - | grep -Eo '\"healthy\"\\s*:\\s*true' || exit 1"]
  interval: 60s
  timeout: 10s
  retries: 3
  start_period: 40s
```

(`init: true` is recommended when a healthcheck is used, to avoid zombie processes.)

## 6. Docker image and mounts

- Image: `binwiederhier/ntfy` on Docker Hub (amd64, armv6/7, arm64). Pin a tag:
  `binwiederhier/ntfy:v2.28.0` (latest release per docs install page on 2026-10-07).
- Default command must be `serve`; container listens on **port 80** by default.
- Config mount: **`/etc/ntfy`** (`server.yml` at `/etc/ntfy/server.yml`). The image does **not**
  ship a `server.yml`; create it on the host and bind-mount it.
- Cache mount: `/var/cache/ntfy` (`cache.db`, attachments). Auth DB commonly `/var/lib/ntfy`.
- If running as a non-root user, `chown` the server.yml, user.db, cache.db, and attachments dir.

Source: docs.ntfy.sh/install `#docker`.

## Sources

- https://docs.ntfy.sh/config/ (config options, cache, access control, behind-proxy, iOS, health)
- https://docs.ntfy.sh/publish/ (headers, actions, JSON publish)
- https://docs.ntfy.sh/subscribe/phone/ (iOS/Android app, instant delivery, custom headers)
- https://docs.ntfy.sh/install/ (Docker image, mounts, healthcheck)
- https://github.com/binwiederhier/ntfy/blob/main/server/server.yml
- https://github.com/binwiederhier/ntfy/blob/main/cmd/serve.go (base-url path rejection)
- https://github.com/binwiederhier/ntfy/blob/main/server/server.go (hardcoded root routes)
- https://github.com/binwiederhier/ntfy/blob/main/server/server_web.go, `web/src/components/routes.js` (web-root = SPA route only)
- https://github.com/binwiederhier/ntfy/issues/1009 (subpath: maintainer states unsupported; closed)
- https://github.com/binwiederhier/ntfy/issues/256, https://github.com/binwiederhier/ntfy/issues/398
