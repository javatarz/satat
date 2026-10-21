# Runtime scaling tiers

**Status:** Decided (near term) · **Related:** [ADR 0024](../adr/0024-kubernetes-agent-sandbox-runtime.md),
[ADR 0026](../adr/0026-docker-first-runtime.md) · **Source:** calls on 2026-10-16 and 2026-10-21

- Three tiers: one machine → a few machines pooled (colleagues' laptops) → cloud. A variant:
  each developer gets an always-on VM running their own Satat.
- The goal across all tiers: run 20 jobs in parallel when 20 are unblocked.
- The runtime is a swappable plugin chosen by config. Docker first (ADR 0026), Kubernetes as
  the lowest common denominator across AWS, GCP and Azure later (ADR 0024).

## Open questions

- Orgs with neither Kubernetes nor public cloud. A small population, no answer yet.
