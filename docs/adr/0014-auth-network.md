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
| 1 — Browser access | nginx (443) | oauth2-proxy with GitHub OAuth. Allowlist lives in `gateway/oauth2-proxy.yaml` in the repo (version-controlled, PR-reviewed). Supports `users`, `orgs`, `allowed_emails` across multiple providers. |
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

- **Low-sensitivity actions** (view PR, mute): ntfy action buttons with `view`
  type open the Canvas UI. The user is already authenticated in their browser.
- **Sensitive actions** (re-trigger agent, cancel run): same pattern — action
  button opens Canvas UI. No static API key embedded in action button URLs.
- Notification content is E2E encrypted between the self-hosted ntfy server and
  the iOS app. The ntfy.sh relay carrier sees only encrypted payloads.

## Consequences

- **Zero open ports**: only 443 (nginx) is public. SSH is unreachable from the
  internet.
- **Defense in depth**: oauth2-proxy (GitHub OAuth + allowlist) → Canvas API
  key. Either alone would stop an attacker; both together means a leaked API key
  is useless without GitHub auth.
- **CI deployment requires Headscale**: the deploy workflow must join the
  tailnet before SSHing. Ephemeral keys make this secure and zero-persistence.
- **Allowlist lives in the repo**: oauth2-proxy config is version-controlled
  and deployed automatically. Adding a new user is a PR.
- **ntfy.sh relay privacy**: E2E encryption means the relay never sees
  notification content. Only the iOS app can decrypt.
