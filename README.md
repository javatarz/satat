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

## Getting started

You need Docker ([colima](https://github.com/abiosoft/colima) or Docker Desktop), [mise](https://mise.jdx.dev/),
[just](https://just.systems/), [pre-commit](https://pre-commit.com/), [gh](https://cli.github.com/)
and [actionlint](https://github.com/rhysd/actionlint).

```sh
just setup    # install the pinned tools and git hooks, create .env, then run doctor
just secrets-init              # once: set up the secret store (see Secret store below)
just secrets-load < secrets.env  # once: load the store's entries
just doctor   # check this machine can run Satat; names what is missing
just run      # bring the platform up on Docker Compose
just lint     # what CI runs: pre-commit, gofmt, go vet and the Docker socket check
just test     # Go tests (needs Docker); just test-short skips the container tests
just build    # build the Go binaries; just build-images builds the images
```

Once the platform runs, these wrap `satat deploy`, `satat agent` and `docker compose`:

```sh
just up                  # satat deploy up: start or update the stack (args pass through, e.g. --config)
just status              # each service and the worker's readiness
just logs [service]      # follow a service's logs (default: worker)
just smoke               # run a one-shot agent on the smoke lane and wait for it
just agents              # list queued and running agent runs
just enqueue owner/name N  # queue issue N (labelled ready-for-agent) for the worker
just down [--volumes]    # stop the stack; --volumes also deletes its data
```

The Claude lane image is built locally: `just build-images`, then put the reference it prints
in `satat.yaml`. Queue an issue with `just enqueue owner/name N` and
watch it at `http://localhost:8888`. More in [docs/design.md](./docs/design.md#run-it-locally).

### Secret store

Secrets live in an OpenBao store, one per environment ([ADR 0032](docs/adr/0032-secret-store.md)).
The worker reads its tokens from the store when it starts and keeps them in memory. No token is
in your shell, `.env` or `compose.yaml`. The worker waits for the store to be up and unsealed, so
set it up before the first `just run` or `just up`:

```sh
just secrets-init                   # once per environment
cp secrets.example.env secrets.env  # fill in the values; secrets.* is gitignored
just secrets-load < secrets.env     # then delete secrets.env
just up                             # or just run
```

`secrets-init` prints a recovery key and the operator password once. Save both in your password
manager: nothing else holds them, and `secrets-load` and `secrets-grant` ask for the password
(or read `SATAT_OPERATOR_PASSWORD`). Any `NAME=value` source can feed the load, for example
`op inject -i secrets.env.tpl | just secrets-load`. Satat never calls `op`. To change one value
later, load only that name, then restart the worker (`docker compose restart worker`): it reads
the store only at start. `just secrets-grant <component> <name>` lets a component read one more
entry.

`deploy.secrets` in `satat.yaml` names the store entry for each variable a lane or the worker
uses, for example `SATAT_GITHUB_TOKEN: github-read-token`. It holds names only. If the store is
not reachable, or it refuses an entry, the worker does not start and its log names the entry.

The store unseals itself on every start. `just down --volumes` deletes it; then run
`just secrets-init` and `just secrets-load` again.

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
├── runner/         # runs inside the Claude lane container: Claude Code, commit
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
