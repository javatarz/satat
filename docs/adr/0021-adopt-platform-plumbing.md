# ADR 0021: Adopt platform plumbing; own only workflows

## Status

Accepted (2026-10-15). Scopes the build-vs-adopt question (open question 2) in
[platform requirements](../research/v2/platform-requirements.md) for the platform redesign.

## Context

The platform redesign needs several layers that no single project provides; see
[agent platform options](../research/v2/agent-platform-options.md). The recommended Stack A had
Satat build its own workload drivers for Docker, Kubernetes, ECS, Cloud Run and Azure
Container Apps jobs. It also had Satat build its own intake service for GitHub webhooks,
cron and other triggers.

That code is plumbing. It needs monitoring, upgrades and provider-specific fixes, such as
the differences between GitHub and GitLab webhooks or the quirks of each cloud's job API.
None of it is what makes Satat useful. The value is in the workflows: issue to PR, review
feedback to updated PR, production alert to issue or PR.

## Decision

Satat adopts existing frameworks for infrastructure plumbing and owns only its workflows and
the thin integration code that connects them.

- **Adopt, not build:**
  - the workload runtime: starting, isolating, watching and tearing down per-step workloads
    across local, single-VM, Kubernetes and cloud deployments
  - intake: receiving, verifying, retrying and scheduling trigger events from forges, cron
    and monitoring systems
- **Own:**
  - workflow definitions and the code that runs them
  - the in-workload runner that drives a harness
  - integration glue: configuration, templates, manifests, and calls into adopted frameworks
- **Avoid:** provider-specific code (for example GitHub versus GitLab payloads) where an
  adopted library or service can absorb it.

When no adoptable framework meets the non-negotiables for a layer, that gap comes back to
the owner as a decision. Satat does not build a framework by default.

## Consequences

- Stack A changes. Satat no longer writes per-backend workload drivers. The runtime
  candidate under evaluation is Kubernetes as the single runtime API everywhere (k3d
  locally, k3s on a single VM, EKS, GKE or AKS in the clouds), with
  [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) providing per-step
  sandboxes. Adopting it is a separate ADR, written once verification is done.
- Intake moves from a Satat-built service to an adopted event or trigger framework, also
  decided separately.
- Satat takes on the operational weight of the adopted frameworks: upgrades, configuration,
  and possibly a cluster even in the cheapest deployment. That cost is weighed against the
  non-negotiables, including "the cheapest deployment must stay cheap", in each follow-up
  ADR.
- Workflow code, runner code and integration glue remain Satat's to write, review and
  maintain.
