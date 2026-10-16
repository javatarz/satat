# Research: Satat v2 platform

Research for the next version of Satat: several harnesses, horizontal scale, and a backlog
control plane. Decisions that come out of it are recorded as ADRs in [`docs/adr`](../../adr),
starting with [ADR 0021](../../adr/0021-adopt-platform-plumbing.md).

| File | What it holds |
|---|---|
| [platform-requirements.md](platform-requirements.md) | Confirmed requirements: the non-negotiables, the layer model, the scoring rubric, and the open questions |
| [agent-platform-options.md](agent-platform-options.md) | Comparison of candidates by layer, three candidate stacks, and the recommendation. Stack A here predates ADR 0021: its Satat-built drivers and intake service are superseded |
| [why-hatchet.md](why-hatchet.md) | The case for Hatchet as the queue and workflow engine, its costs, and how it compares with the alternatives |
| [platform-ram-spike.md](platform-ram-spike.md) | Measured RAM and CPU of k3s, Hatchet, Postgres and LiteLLM and how many step pods fit, across three VM sizes |
| [intake-triggers.md](intake-triggers.md) | Intake for webhooks, cron and alerts: Hatchet built-ins versus gateways and integration platforms |
| [notes/](notes/) | Raw per-topic research with full citations, used as input to the files above. Notes may describe options that were later rejected |

The current architecture is kept in the onboarding guide
[How Satat works](https://claude.ai/code/artifact/ad820f93-494e-403a-a790-ed353561669e).
