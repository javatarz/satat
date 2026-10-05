# ADR 0013: Provision-deploy pipeline

## Status

Accepted (2025-10-03). Revised 2026-10-05: provisioning moved off Terraform Cloud to
GitHub Actions.

## Context

Satat has two distinct classes of change in the repo:

1. **Provision**: changes under `terraform/` — EC2 instance, security group, Elastic IP.
   Infrequent (monthly or less).
2. **Deploy**: changes to service configs (`gateway/`, `ntfy/`, `automations/`, …) —
   update model tiers, notification rules, agent runtime configs. Frequent (multiple times
   per week).

Both need to reach the AWS VM. The earlier design split them across two systems (Terraform
Cloud for provisioning, GitHub Actions for deploy), which required a Terraform Cloud API
token and a workflow that polled TFC run status before deploying.

## Decision

Run **both** provisioning and deploy in **GitHub Actions**, sharing one repo:

| Concern | Trigger | Mechanism |
|---------|---------|-----------|
| Provision | Push to files under `terraform/` | `terraform plan` on PR (posted as a PR comment); `apply` on merge to `main`, gated by a GitHub Environment |
| Deploy | Push to config files (`gateway/`, `ntfy/`, …) | SSH over WireGuard + `docker compose up -d` |

Supporting choices:

- **Auth to AWS via GitHub OIDC**: workflow runs assume a scoped IAM role
  (`sts:AssumeRoleWithWebIdentity`); no long-lived AWS keys are stored.
- **State in S3** with the native S3 lockfile (`use_lockfile`, Terraform ≥ 1.10) — no
  DynamoDB.
- **One-time local bootstrap** (`bootstrap/`) creates the state bucket, the GitHub OIDC
  identity provider, and the Terraform IAM role.

Because provisioning and deploy now share one system, ordering is a plain `needs:` — no
external run polling.

## Consequences

- **One CI system, one auth mechanism** (GitHub OIDC), and one place to reason about
  provision-then-deploy ordering.
- **No Terraform Cloud** account, API token, or polling gate.
- **We forgo TFC's plan UI, cost estimation, and Sentinel/OPA policy**; plans are posted as
  PR comments and applies are gated by a GitHub Environment with required reviewers.
- **A one-time bootstrap is required** (chicken-and-egg: Terraform cannot create the
  credentials it needs to run). Run it locally with admin credentials.
- **Single source of truth**: the repo is canonical; GitHub Actions reads both the infra and
  the config from the same commit.
