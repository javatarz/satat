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
| 1 — Browser access | Caddy (443) | oauth2-proxy with GitHub OAuth via Caddy `forward_auth`. Allowlist stored as a GitHub Variable (not in the repo — prevents email doxxing in the public repo). Config template in `gateway/oauth2-proxy.yaml.tmpl` with placeholders; actual values injected at deploy time. |
| 2 — Canvas | Canvas (8000) | OpenHands API key. Inner layer — oauth2-proxy blocks unauthenticated visitors before they reach Canvas. |
| 3 — SSH | VM (WireGuard interface only) | Plain WireGuard. SSH daemon binds to the WireGuard interface only. No SSH port on the public IP. |
| 4 — CI deployment | VM (WireGuard interface only) | Static WireGuard peer (`ci`) with a keypair; the deploy runner brings the tunnel up for the workflow, deploys via SSH, then tears it down. Key stored as a GitHub secret. |
| 5 — ntfy notifications | ntfy (8080) | Self-hosted ntfy server behind Caddy. E2E encryption between server and iOS app — the ntfy.sh relay sees only encrypted blobs. Topic-as-password for topic access control. |
| 6 — LiteLLM | LiteLLM (4000) | Reachable at `/v1/*` through Caddy for the agent, and internally on the Compose network. |

### Network diagram

```
Internet
   │
   ├─ HTTPS (443) ─────────────────► Caddy
   │                                   ├─ /canvas   → oauth2-proxy → Canvas (8000)
   │                                   ├─ /ntfy      → oauth2-proxy → ntfy (8080)
   │                                   ├─ /v1        → LiteLLM (4000)
   │                                   └─ /webhook   → Canvas (8000)
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
| Budget warning | 5 | `Satat — Monthly budget >80%` | View Canvas |

Agent started will be dropped once Satat is stable. Click action on all notifications opens Canvas. Tags: `satat` for filtering.

## Consequences

- **Zero open ports** except 443 (Caddy) and 51820/udp (WireGuard). SSH is unreachable
  from the internet.
- **Defense in depth**: oauth2-proxy (GitHub OAuth + allowlist) → Canvas API
  key. Either alone would stop an attacker; both together means a leaked API key
  is useless without GitHub auth.
- **CI deployment requires WireGuard**: the deploy workflow brings the tunnel up
  before SSHing. A static peer key keeps this simple and stateless.
- **Allowlist in GitHub Variables**: oauth2-proxy config template lives in the
  repo with placeholders. Actual user/org/email values are stored as GitHub
  Variables (not committed — avoids doxxing in the public repo). Adding a user
  is done via the GitHub Actions Variables UI without a code change.
- **ntfy.sh relay privacy**: E2E encryption means the relay never sees
  notification content. Only the iOS app can decrypt.
