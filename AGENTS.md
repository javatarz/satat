# AGENTS.md — Satat infra repo

## Agent skills

### Issue tracker

GitHub Issues on javatarz/satat. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-label vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` at root + `docs/adr/`. See `docs/agents/domain.md`.

### Design roadmap

`docs/roadmap/` holds direction that isn't an ADR or an issue yet. When grooming or designing,
read the relevant theme and flag any change that goes against it.

## Conventions

### Scratch directory

Use `docs/scratch/` for temporary working files, not `/tmp`. This directory is gitignored.

### Container image digests

Resolve image tags/digests with `scripts/image-digest.sh tags|digest <ghcr.io/owner/name> [tag]` (registry API, auth-free for GHCR). `docker manifest inspect` can block for minutes on a cold or rate-limited registry.

### Go tests

Run `go test -p 1 ./...`. Tests that start containers share one Docker daemon and reap by label, so
packages must not run in parallel. `-short` skips the Docker tests.

### Research flow

When a wayfinder research ticket is open: commit findings to `docs/research/<topic>.md`, capture the settled outcome as an ADR in `docs/adr/`, then close the ticket. Never leave research in ticket bodies only.

### Provision-deploy split

Satat v0.2 starts on Docker Compose on the owner's laptop (ADR 0026): no provisioning step yet.
Kubernetes is the later target. A conformant cluster is then assumed, provisioning will be
Terraform recipes per cloud provider, and deploying Satat onto it is a separate step (Helm chart).
The deploy step exists for a local k3d cluster (ADR 0031); Terraform provisioning does not. The EC2 + Docker Compose pipeline lives on the `v0.1` branch.

### Secrets

Never in git-tracked files, Terraform state or cloud-init. Loaded secrets live in one OpenBao
store per environment, read per component identity, with the policy held in the store (ADR
0032). Until that is built, `scripts/compose-up.sh` reads them from 1Password. CI uses GitHub
repository secrets and cloud OIDC (no long-lived keys).

### External repos

- `javatarz/satat` — this repo, public, contains docs, service configs and CI
- `javatarz/satat-automations` — private, the owner's eval set (v0.1: the Agent Canvas Git Sync target)
