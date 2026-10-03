# ADR 0014: Authentication and network access

## Status

Accepted (2025-10-03)

## Context

Everything on the Satat VM must be behind authentication. No service should be
reachable from the public internet without proper authorization. The VM exposes:
a browser-facing Canvas UI, ntfy notifications (via iOS), GitHub webhooks, and
administrative SSH access.

## Decision

### Entry point

nginx terminates TLS on port 443. All public traffic hits nginx first; no other
port is exposed to the public internet.

### Authentication layers

| Layer | Service | Mechanism |
|-------|---------|-----------|
| 1 — Browser access | nginx (443) | oauth2-proxy with GitHub OAuth. Allowlist stored as a GitHub Variable (not in the repo — prevents email doxxing in the public repo). Supports `users`, `orgs`, `allowed_emails` across multiple providers. Config template in `gateway/oauth2-proxy.yaml.tmpl` with placeholders; actual values injected at deploy time. |
| 2 — Canvas | Canvas (localhost:8000) | OpenHands API key. Inner layer — oauth2-proxy blocks unauthenticated visitors before they reach Canvas. |
| 3 — SSH | VM (WireGuard interface only) | Headscale (self-hosted, open-source Tailscale-compatible control server). SSH daemon binds to WireGuard interface only. No SSH port on public IP. |
| 4 — CI deployment | VM (WireGuard interface only) | Headscale ephemeral auth keys. GH Actions runner joins the tailnet for the duration of the deploy workflow, deploys via SSH over WireGuard, then leaves. Auth key stored as a GitHub secret. |
| 5 — ntfy notifications | ntfy (localhost:8080) | Self-hosted ntfy server behind nginx. E2E encryption between server and iOS app — ntfy.sh relay sees only encrypted blobs. Topic-as-password for topic access control. |
| 6 — LiteLLM | LiteLLM (localhost:4000) | Internal-only. Canvas communicates with LiteLLM on localhost. Not exposed through nginx. |

### Network diagram

```
Internet
   │
   ├─ HTTPS (443) ─────────────────► nginx
   │                                   ├─ /          → oauth2-proxy → Canvas (8000)
   │                                   ├─ /ntfy      → ntfy (8080)
   │                                   └─ /api/*     → [future custom endpoints]
   │
   ├─ Webhook (443) ───────────────► nginx → Canvas
   │
   └─ Headscale/WireGuard ─────────► SSH (wg interface only)
                                      CI deploy
                                      Admin access
```

### Bootstrap sequence

1. TFC creates the VM. Cloud-init installs Headscale, Docker, nginx, and base
   packages. Generates a Headscale admin key, outputs it as a Terraform output.
2. GH Actions deploy pipeline reads the Headscale auth key from TFC outputs,
   joins the tailnet ephemerally, and deploys the full service stack via SSH
   over WireGuard.
3. SSH was never open on the public IP. From first boot, access is via
   Headscale only.
4. If cloud-init fails, debug via Hetzner console/rescue mode — no SSH needed.

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

- **Zero open ports**: only 443 (nginx) is public. SSH is unreachable from the
  internet.
- **Defense in depth**: oauth2-proxy (GitHub OAuth + allowlist) → Canvas API
  key. Either alone would stop an attacker; both together means a leaked API key
  is useless without GitHub auth.
- **CI deployment requires Headscale**: the deploy workflow must join the
  tailnet before SSHing. Ephemeral keys make this secure and zero-persistence.
- **Allowlist in GitHub Variables**: oauth2-proxy config template lives in the
  repo with placeholders. Actual user/org/email values are stored as GitHub
  Variables (not committed — avoids doxxing in the public repo). Adding a user
  is done via the GitHub Actions Variables UI without a code change.
- **ntfy.sh relay privacy**: E2E encryption means the relay never sees
  notification content. Only the iOS app can decrypt.
