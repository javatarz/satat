# ADR 0014: Authentication and network access

## Status

Accepted (2025-10-03). The ingress and remote-access portions are superseded by
[ADR 0017](0017-ingress-runtime-and-access.md) (Caddy ingress, plain WireGuard, Docker
Compose runtime). The authentication layers and ntfy model below remain in force.

## Context

Everything on the Satat VM must be behind authentication. No service should be
reachable from the public internet without proper authorization. The VM exposes:
a browser-facing Canvas UI, ntfy notifications (via iOS), GitHub webhooks, and
administrative SSH access.

## Decision

### Entry point

Caddy terminates TLS on port 443. All public traffic hits Caddy first; no other
port is exposed to the public internet. Caddy obtains and renews certificates
automatically (ACME).

### Authentication layers

| Layer | Service | Mechanism |
|-------|---------|-----------|
| 1 — Browser access | Caddy (443) | oauth2-proxy with GitHub OAuth via Caddy `forward_auth`. Allowlist stored as a GitHub Variable (not in the repo — prevents username/email doxxing in the public repo) and passed as `OAUTH2_PROXY_GITHUB_USERS`, a pure username allowlist (see [research](../research/oauth2-proxy-github.md)). Configured entirely by `OAUTH2_PROXY_*` environment variables written to `/opt/satat/.env` at deploy time — no committed config template. |
| 2 — Canvas | Canvas (8000) | OpenHands API key. Inner layer — oauth2-proxy blocks unauthenticated visitors before they reach Canvas. |
| 3 — SSH | VM (WireGuard interface only) | Plain WireGuard. SSH daemon binds to the WireGuard interface only. No SSH port on the public IP. |
| 4 — CI deployment | VM (WireGuard interface only) | Static WireGuard peer (`ci`) with a keypair; the deploy runner brings the tunnel up for the workflow, deploys via SSH, then tears it down. Key stored as a GitHub secret. |
| 5 — ntfy notifications | ntfy (8080) | Self-hosted ntfy on **`notify.${SATAT_DOMAIN}`** (its own hostname — ntfy cannot run under a subpath, [ADR 0019](0019-ntfy-hosting-and-observability.md)). Uses **ntfy's own auth** (topic-as-password), *not* oauth2-proxy, because the iOS app cannot complete a browser OAuth redirect. E2E encryption between server and iOS app — the ntfy.sh relay sees only encrypted blobs. |
| 6 — LiteLLM | LiteLLM (4000) | Reachable at `/v1/*` through Caddy for the agent, and internally on the Compose network. |

### Network diagram

```
Internet
   │
   ├─ HTTPS (443) ─────────────────► Caddy
   │                                   ├─ satat.karun.me
   │                                   │    ├─ /oauth2/*  → oauth2-proxy (4180)
   │                                   │    ├─ /canvas/*  → oauth2-proxy → Canvas (8000)
   │                                   │    ├─ /api/*     → Canvas (8000)
   │                                   │    ├─ /sockets/* → Canvas (8000)
   │                                   │    └─ /v1/*      → LiteLLM (4000)
   │                                   └─ notify.satat.karun.me → ntfy (8080)  (native ntfy auth)
   │
   └─ WireGuard (UDP 51820) ───────► SSH (wg0 interface only)
                                       CI deploy (peer ci)
                                       Admin access (peer laptop)
```

### Bootstrap sequence

1. GitHub Actions creates the EC2 instance. Cloud-init installs Docker, WireGuard, and base
   packages, generates the WireGuard server keypair, and brings up `wg0` with the
   static `ci` and `laptop` peers. `sshd` binds `wg0` only.
2. The GitHub Actions deploy pipeline brings up its WireGuard tunnel, deploys the
   Compose stack via SSH over the tunnel, then tears the tunnel down.
3. SSH was never open on the public IP. From first boot, access is via WireGuard only.
4. If cloud-init fails, debug via the cloud provider console (no SSH needed).

### ntfy interaction model

- Action buttons use `view` type with URL — no API keys in notification payloads.
- **View PR**: opens the GitHub PR in the GitHub app (for mobile code review).
- **View Canvas**: opens Canvas UI (for debugging, retry, cancel).
- Notification content is E2E encrypted between the self-hosted ntfy server and
  the iOS app. The ntfy.sh relay carrier sees only encrypted payloads.

### Notification events

| Event | Priority | Title | Actions |
|---|---|---|---|
| Agent started | 1 | `{repo}#{issue} — Agent started` | — |
| PR opened | 3 | `{repo}#{issue} — PR #{num} open` | View PR, View Canvas |
| PR updated | 4 | `{repo}#{issue} — PR #{num} updated` | View PR, View Canvas |
| Agent timeout | 5 | `{repo}#{issue} — Running >{min}min` | View Canvas |
| Agent failed | 5 | `{repo}#{issue} — Agent failed` | View Canvas |
| Budget warning | 5 | `Satat budget warning` (spend vs `$SATAT_DAILY_BUDGET`) | View Canvas |

Agent started will be dropped once Satat is stable. Click action on all notifications opens Canvas. Tags: `satat` for filtering.

## Consequences

- **Zero open ports** except 443 (Caddy) and 51820/udp (WireGuard). SSH is unreachable
  from the internet.
- **Defense in depth**: oauth2-proxy (GitHub OAuth + allowlist) guards the browser
  surface (`/canvas/*`); ntfy (`notify.${SATAT_DOMAIN}`) uses its own topic auth
  (ADR 0019); the Canvas API key guards the programmatic
  surfaces (`/api/*`, `/sockets/*`), which bypass oauth2-proxy so GitHub webhooks
  (event automations; the shipped issue-to-PR automation is cron-polled per
  [ADR 0020](0020-automation-definition-auth-trigger.md)) can reach the Automation
  Server. A leaked API key is useless without also
  reaching a gated path; a valid OAuth session alone does not unlock `/api/*`.
- **CI deployment requires WireGuard**: the deploy workflow brings the tunnel up
  before SSHing. A static peer key keeps this simple and stateless.
- **Allowlist in GitHub Variables**: oauth2-proxy is configured by `OAUTH2_PROXY_*`
  environment variables (no committed config template). The username allowlist
  (`OAUTH2_ALLOWLIST`) is stored as a GitHub Variable, not committed — avoids doxxing
  in the public repo. Adding a user is done via the GitHub Actions Variables UI without
  a code change.
- **ntfy.sh relay privacy**: E2E encryption means the relay never sees
  notification content. Only the iOS app can decrypt.
