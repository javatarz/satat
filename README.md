# Satat

सतत — "continuous / constant"

A self-hosted, always-on AI coding agent that watches your GitHub backlog, picks up
labeled issues, writes code, runs tests, and opens draft pull requests for human review.
It never merges anything itself.

> **This repo is the recipe.** It contains everything needed to deploy your own Satat:
> Terraform infrastructure, service configs, CI pipeline, and architecture docs.
> The per-repo automation config (which repos to watch, under which labels) lives in
> a separate private repo for security.

## How it works

```
GitHub Issue labeled ──webhook──► Automation Server dispatches agent
                                         │
                              Agent Server (Docker sandbox)
                                ├─ clones repo, reads AGENTS.md
                                ├─ implements fix
                                ├─ runs pre-commit, lint, tests (up to 3 attempts)
                                ├─ commits, pushes branch satat/issue-N
                                └─ opens DRAFT PR "Closes #N"
                                         │
                              ntfy notification ──► iOS push
                                         │
                              Human reviews PR, merges or requests changes
```

## Stack

| Component | Purpose | Port |
|-----------|---------|------|
| OpenHands Agent Canvas | Control plane UI + agent execution + automations | 8000 |
| LiteLLM proxy | Model routing + spend tracking + budget cap | 4000 |
| ntfy server | Self-hosted push notifications | 8080 |
| nginx + Let's Encrypt | TLS termination + reverse proxy | 443 |
| Docker | Agent sandbox runtime | — |

LLM access: [LLM Gateway](https://llmgateway.io) (DevPass subscription) with three
cost tiers — cheap, standard, expensive — routed through LiteLLM with a hard
monthly budget cap.

## Architecture decisions

Every significant choice is recorded as an ADR in [docs/adr/](./docs/adr/). See also
[CONTEXT.md](./CONTEXT.md) for the glossary of domain terms.

## Deploy your own

1. Fork this repo (rename it to `satat`).
2. Create a private `satat-automations` repo for Git Sync.
3. Set up required secrets as GitHub Actions repository secrets (see [.env.example](./.env.example)).
4. Push — CI provisions the VM via Terraform and injects secrets.
5. Point your domain at the VM, configure DNS.
6. Install the Satat GitHub App on your target repo.
7. Label an issue with the trigger label — Satat picks it up within seconds.

Detailed onboarding: [docs/runbook.md](./docs/runbook.md) (coming soon).

## Cost model

Infrastructure cost depends on the hosting provider (see ADRs for the current
choice). LLM spend varies by issue complexity and model tier; LiteLLM enforces a
hard monthly cap so it cannot exceed budget.

## Repo structure

```
satat/
├── README.md
├── CONTEXT.md
├── .env.example
├── terraform/          # VM, firewall, DNS, cloud-init
├── gateway/            # LiteLLM model routing config
├── ntfy/               # Notification server config
├── github-app/         # Satat GitHub App manifest
├── scripts/            # CI helpers (secret injection)
├── docs/
│   ├── adr/            # Architecture Decision Records
│   └── runbook.md
└── .github/
    └── workflows/      # CI pipeline
```
