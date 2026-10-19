# Satat

सतत — "continuous / constant"

A self-hosted, always-on AI coding agent that watches your GitHub backlog, picks up
labeled issues, writes code, runs tests, and opens draft pull requests for human review.
It never merges anything itself.

> **Status: v0.2, pre-1.0, in design.** v0.2 is a platform (Hatchet queue, one container
> per step, LiteLLM gateway) that starts on Docker Compose on a laptop and targets Kubernetes
> with agent-sandbox later. Nothing is deployable from `main` yet.
>
> The earlier EC2 + Docker Compose stack (OpenHands Agent Canvas, Caddy, Terraform) is **v0.1**.
> It is kept working on the [`v0.1` branch](https://github.com/javatarz/satat/tree/v0.1)
> (tag `v0.1.0-final`).

See [CONTEXT.md](./CONTEXT.md) for versions and the domain glossary.

## Docs

- [CONTEXT.md](./CONTEXT.md) — domain glossary and versions
- [docs/adr/](./docs/adr/) — architecture decisions (0024 is the runtime)
- [docs/roadmap/](./docs/roadmap/) — design roadmap: where Satat is heading, not yet decided
- [docs/research/v2/](./docs/research/v2/) — v0.2 research and spikes
- [CONTRIBUTING.md](./CONTRIBUTING.md) — how the team works, PRs and commit messages
- [AGENTS.md](./AGENTS.md) — conventions for agents and contributors

## Repo structure

```
satat/
├── gateway/        # LiteLLM config template (reused from v0.1), Alloy config (reference)
├── ntfy/           # ntfy server config (reused)
├── observability/  # Grafana dashboard (VM panels are v0.1-only)
├── deploy/         # budget-relay.py: v0.1 reference, to be rewritten
├── scripts/
└── docs/
```
