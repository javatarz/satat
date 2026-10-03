# AGENTS.md — Satat infra repo

## Agent skills

### Issue tracker

GitHub Issues on javatarz/satat. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-label vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` at root + `docs/adr/`. See `docs/agents/domain.md`.

## Conventions

### Scratch directory

Use `docs/scratch/` for temporary working files, not `/tmp`. This directory is gitignored.

### Research flow

When a wayfinder research ticket is open: commit findings to `docs/research/<topic>.md`, capture the settled outcome as an ADR in `docs/adr/`, then close the ticket. Never leave research in ticket bodies only.

### Provision-deploy split

Terraform Cloud provisions infrastructure (changes under `terraform/`). GitHub Actions deploys configuration (gateway templates, ntfy config). When both change in one push, deploy gates on TFC run status. Config-only pushes deploy instantly.

### Secrets

Never in Terraform state, cloud-init, or git-tracked files. Secrets live in TFC workspace variables and GitHub repository secrets.

### External repos

- `javatarz/satat` — this repo, public, contains Terraform, docs, CI, service configs
- `javatarz/satat-automations` — private, Git Sync target for automation YAML files
