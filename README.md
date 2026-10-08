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
GitHub Issue labeled ──poll (15 min)──► Automation Server dispatches agent
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
| Caddy | TLS termination + reverse proxy (automatic HTTPS) | 443 |
| OpenHands Agent Canvas | Control plane UI + agent execution + automations | 8000 |
| LiteLLM proxy | Model routing + spend tracking + budget cap | 4000 |
| Postgres | LiteLLM datastore (keys, budgets) | 5432 |
| ntfy server | Self-hosted push notifications | 8080 |
| Docker | Agent sandbox runtime | — |

## Docs

- [CONTEXT.md](./CONTEXT.md) — domain glossary
- [docs/adr/](./docs/adr/) — architecture decisions
- [AGENTS.md](./AGENTS.md) — conventions for agents and contributors

## Deploy your own

1. Fork this repo.
2. Create a private `satat-automations` repo for Git Sync.
3. Run the one-time bootstrap in [`bootstrap/`](./bootstrap/) (locally, with admin credentials) to create the S3 state bucket and the GitHub OIDC role, then set the GitHub repository variables it prints — plus the other variables/secrets from [`terraform/README.md`](./terraform/README.md).
4. GitHub Actions provisions the VM (plan on PR, apply on merge to `main`) and deploys the service config.
5. Point `SATAT_DOMAIN` at the instance's Elastic IP once provisioned.
6. Save a fine-grained GitHub PAT (Contents/Pull requests/Issues: RW, Metadata: RO) as
   the `GITHUB_PERSONAL_ACCESS_TOKEN` secret in Agent Canvas (Settings → Secrets), and
   merge the `satat-issue-to-pr` automation in your private `satat-automations` repo.
7. Label an issue with `ready-for-agent` — Satat picks it up on its next poll.

## Repo structure

```
satat/
├── AGENTS.md               # Conventions for agents working in this repo
├── CONTEXT.md              # Domain glossary
├── terraform/              # GitHub Actions-provisioned infra (EC2, security group, Elastic IP, cloud-init)
├── gateway/                # Caddy + LiteLLM templates (oauth2-proxy is env-configured)
├── ntfy/                   # Notification server config
├── docs/
│   ├── adr/                # Architecture Decision Records
│   ├── research/           # Wayfinder research findings
│   └── agents/             # Skill scaffolding (issue tracker, labels, domain)
└── .github/workflows/      # Config deployment pipeline
```
