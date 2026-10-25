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
| [octo-sts-fit.md](octo-sts-fit.md) | Whether octo-sts can mint per-step GitHub tokens on k3s, what self-hosting involves, and the alternatives. Recommends a Satat token broker |
| [lane-started-runs.md](lane-started-runs.md) | How a lane is marked as running an agent, why lane-started runs are refused on agent lanes, and what #129 should change |
| [secret-store.md](secret-store.md) | Where loaded secrets live and who may read them: OpenBao with one identity per component, the same policies on Compose (AppRole) and Kubernetes (service accounts), a Compose spike, and the alternatives. Feeds ADR 0032 |
| [payload-limits.md](payload-limits.md) | Measured size ceilings of the two worker-to-pusher handoff routes (a Hatchet task input and an HTTP `PUT`), GitHub's limits, the Git LFS refusal, a proposed bundle ceiling, and whether a Hatchet token can be scoped to one task. Feeds #156 |
| [notes/](notes/) | Raw per-topic research with full citations, used as input to the files above. Notes may describe options that were later rejected |

The current architecture is kept in the onboarding guide
[How Satat works](https://claude.ai/code/artifact/ad820f93-494e-403a-a790-ed353561669e).
