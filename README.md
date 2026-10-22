# Satat

सतत — "continuous / constant"

A self-hosted, always-on AI coding agent that watches your GitHub backlog, picks up
labeled issues, writes code, runs tests, and opens draft pull requests for human review.
It never merges anything itself.

> **Status: v0.2, pre-1.0, a prototype.** v0.2 is a platform (Hatchet queue, one container
> per step, a launcher interface) that runs on Docker Compose on a laptop and targets
> Kubernetes with agent-sandbox later. The issue-to-draft-PR path works end to end when you
> queue an issue by hand; webhook intake is not built. It is not a deployable product.
>
> The earlier EC2 + Docker Compose stack (OpenHands Agent Canvas, Caddy, Terraform) is **v0.1**.
> It is kept working on the [`v0.1` branch](https://github.com/javatarz/satat/tree/v0.1)
> (tag `v0.1.0-final`).

See [docs/design.md](./docs/design.md) for how it works today, and [CONTEXT.md](./CONTEXT.md)
for versions and the domain glossary.

## Docs

- [docs/design.md](./docs/design.md) — how Satat works today: flow, launcher, pusher, caps, evals
- [CONTEXT.md](./CONTEXT.md) — domain glossary and versions
- [docs/adr/](./docs/adr/) — architecture decisions (0026 is the runtime, 0024 the later Kubernetes target)
- [docs/roadmap/](./docs/roadmap/) — design roadmap: where Satat is heading, not yet decided
- [docs/research/v2/](./docs/research/v2/) — v0.2 research and spikes
- [CONTRIBUTING.md](./CONTRIBUTING.md) — how the team works, PRs and commit messages
- [AGENTS.md](./AGENTS.md) — conventions for agents and contributors

## Repo structure

```
satat/
├── cmd/            # satat-worker, satat-runner, satat-enqueue, satat-step, satat-eval
├── workflows/      # the Hatchet workflow `satat-issue` and its concurrency caps
├── pipeline/       # issue to draft PR: launch a step, then publish
├── launcher/       # launcher interface, Docker backend, hardening floor, conformance suite
├── runner/         # runs inside the Claude lane container: clone, Claude Code, commit
├── pusher/         # pushes the branch and opens the draft PR (holds the write token)
├── config/         # satat.yaml loader (limits and lanes)
├── eval/           # eval harness and cases
├── images/         # Dockerfiles for the worker and the Claude lane
├── compose.yaml    # the platform: Postgres, Hatchet Lite, Docker socket proxy, worker
├── satat.yaml      # concurrency limits and lanes
├── gateway/        # LiteLLM config template (reused from v0.1, not in the v0.2 stack), Alloy config (reference)
├── ntfy/           # ntfy server config (reused)
├── observability/  # Grafana dashboard (VM panels are v0.1-only)
├── deploy/         # budget-relay.py: v0.1 reference, to be rewritten
├── scripts/
└── docs/
```
