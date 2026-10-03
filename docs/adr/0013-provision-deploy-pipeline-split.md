# ADR 0013: Provision-deploy pipeline split

## Status

Accepted (2025-10-03)

## Context

Satat has two distinct classes of change in the repo:
1. **Provision**: Changes to `terraform/` — VM creation, resize, firewall, DNS. Infrequent
   (monthly or less).
2. **Deploy**: Changes to service configs (`gateway/`, `ntfy/`, `automations/`, etc.) —
   update LiteLLM model tiers, ntfy notification rules, agent runtime configs. Frequent
   (multiple times per week).
Both need to reach the Contabo VM, but with very different execution contexts.

Provisionings need Contabo API access and a locked state; config deploys need SSH
access and a running VM.

## Decision

Split the pipeline into two systems that share one repo:

| Concern | Owner | Trigger | Mechanism |
|---------|-------|---------|-----------|
| Provision | Terraform Cloud (VCS integration) | Push to files under `terraform/` | TFC runs `plan` on PR, `apply` on merge |
| Deploy | GitHub Actions | Push to config files (`gateway/`, `ntfy/`, etc.) | SSH + rsync + restart |

The deploy workflow gates on provision when both change in the same push:

```yaml
wait-for-provision:
  if: steps.filter.outputs.terraform == 'true'
  steps:
    - run: |
        until curl -sf "..." | jq -e '.status | test("applied|errored")'; do
          sleep 10
        done

deploy:
  needs: [wait-for-provision]
```

On config-only changes the gate is skipped (zero delay). On mixed changes deploy
polls TFC run status until the provision completes, then proceeds.

## Consequences

- **Provision is rare, simple**: no CI code to write for terraform; TFC handles it
  natively via VCS integration.
- **Deploy is fast**: config-only pushes skip the provision gate, deploy runs within
  seconds.
- **Mixed commits are safe**: if someone changes a terraform file and a config file
  in one push, deploy cannot race against provision.
- **Two secrets remain**: `HCLOUD_TOKEN` (TFC workspace variable) and an SSH key
  (GitHub secret). TFC API token is read-only and used only for the polling gate.
- **Single source of truth**: the repo is the canonical state — TFC reads the infra
  branch, GH Actions reads the config branch, both from the same repo at the same
  commit.
