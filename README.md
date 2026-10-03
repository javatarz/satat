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

## Docs

- [CONTEXT.md](./CONTEXT.md) — domain glossary
- [docs/adr/](./docs/adr/) — architecture decisions
- [AGENTS.md](./AGENTS.md) — conventions for agents and contributors

## Deploy your own

1. Fork this repo.
2. Create a private `satat-automations` repo for Git Sync.
3. Set up required secrets as TFC workspace variables and GitHub repository secrets (see [.env.example](./.env.example)).
4. Connect TFC to the fork — auto-plans on PR, auto-applies on merge to main.
5. Acquire a domain and point it at the VM once provisioned.
6. Install the Satat GitHub App on your target repo.
7. Label an issue with `ready-for-dev` — Satat picks it up within seconds.

## Repo structure

```
satat/
├── .env.example            # Required secrets (TFC workspace variables + GH Secrets)
├── AGENTS.md               # Conventions for agents working in this repo
├── CONTEXT.md              # Domain glossary
├── terraform/              # TFC-provisioned infra (VM, firewall, DNS, cloud-init)
├── gateway/                # LiteLLM config + oauth2-proxy template
├── ntfy/                   # Notification server config
├── docs/
│   ├── adr/                # Architecture Decision Records
│   ├── research/           # Wayfinder research findings
│   └── agents/             # Skill scaffolding (issue tracker, labels, domain)
└── .github/workflows/      # Config deployment pipeline
```
