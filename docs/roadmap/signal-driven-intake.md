# Signal-driven intake

**Status:** Idea · **Related:** [ADR 0023](../adr/0023-hatchet-queue-and-workflow-engine.md) ·
**Source:** call on 2026-10-16

- Beyond labelled issues: a scheduled job watches signals (alerts, data or model drift),
  diagnoses, and turns a good diagnosis into a draft issue.
- The draft lands in `needs-triage`. It reaches the agent only after a human accepts it and
  labels it `ready-for-agent`, and then it runs as any other issue does.
