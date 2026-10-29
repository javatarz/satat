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
[just](https://just.systems/), [pre-commit](https://pre-commit.com/), [gh](https://cli.github.com/),
[actionlint](https://github.com/rhysd/actionlint) and the
[1Password CLI](https://developer.1password.com/docs/cli/) (`op`).

```sh
just setup    # install the pinned tools and git hooks, create .env, then run doctor
just doctor   # check this machine can run Satat; names what is missing
just run      # bring the platform up on Docker Compose
just lint     # what CI runs: pre-commit, gofmt, go vet and the Docker socket check
just test     # Go tests (needs Docker); just test-short skips the container tests
just e2e      # bring a throwaway stack up, run it end to end, take it down
just build    # build the Go binaries; just build-images builds the images
```

The Claude lane image is built locally: `just build-images`, then put the reference it prints
in `satat.yaml`. Queue an issue with `go run ./cmd/satat-enqueue --repo owner/name --issue N` and
watch it at `http://localhost:8888`. More in [docs/design.md](./docs/design.md#run-it-locally).

### Without 1Password

`just run` reads the worker's three tokens from 1Password. To supply them yourself instead, set
each `deploy.secrets` entry in `satat.yaml` to `env` (a local change, don't commit it):

```yaml
deploy:
  secrets:
    CLAUDE_CODE_OAUTH_TOKEN: env   # from claude setup-token
    SATAT_GITHUB_TOKEN: env        # fine-grained PAT: Contents read, Issues read
    SATAT_PUSH_TOKEN: env          # fine-grained PAT: Contents and Pull requests read/write
```

`env` means the shell's environment: `satat deploy up` does not read `.env` for these. To have
mise load `.env` whenever you are in the repo, activate mise in your shell (add
`eval "$(mise activate zsh)"` to `~/.zshrc`, then open a new shell) and create a
`mise.local.toml` (gitignored):

```toml
[env]
_.file = ".env"
```

Put the three tokens in `.env` (they are then on disk in plain text, mode 0600), then:

```sh
mise trust                     # once, so mise loads mise.local.toml
go run ./cmd/satat deploy up   # creates the Hatchet token on first run, then starts the stack
```

`go run ./cmd/satat deploy status` and `deploy down` check and stop it. `just doctor` still
fails its `op` check on this path; the other checks apply.

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
