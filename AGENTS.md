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

### Container image digests

Resolve image tags/digests with `scripts/image-digest.sh tags|digest <ghcr.io/owner/name> [tag]` (registry API, auth-free for GHCR). `docker manifest inspect` can block for minutes on a cold or rate-limited registry.

### Research flow

When a wayfinder research ticket is open: commit findings to `docs/research/<topic>.md`, capture the settled outcome as an ADR in `docs/adr/`, then close the ticket. Never leave research in ticket bodies only.

### Provision-deploy split

Satat v0.2 assumes a conformant Kubernetes cluster is available. Provisioning (a cluster) will be
Terraform recipes per cloud provider; deploying Satat onto it is a separate step (Helm chart).
Neither exists yet. The EC2 + Docker Compose pipeline lives on the `v0.1` branch.

### Secrets

Never in git-tracked files, Terraform state or cloud-init. Where secrets live on Kubernetes is
undecided (planned ADR); until then, GitHub repository secrets and cloud OIDC (no long-lived keys).

### External repos

- `javatarz/satat` — this repo, public, contains docs, service configs and CI
- `javatarz/satat-automations` — private, Git Sync target for automation YAML files
