# Satat's entry point. `just` lists the recipes.

set shell := ["bash", "-euo", "pipefail", "-c"]

# compose.yaml requires the worker's variables to parse, even for commands that don't start it.
compose_placeholders := "HATCHET_CLIENT_TOKEN=unused CLAUDE_CODE_OAUTH_TOKEN=unused SATAT_GITHUB_TOKEN=unused SATAT_PUSH_TOKEN=unused"

# List the recipes
[private]
default:
    @just --list

# Install the pinned tools and the git hooks, create .env if missing, then run doctor
setup:
    mise install
    pre-commit install --hook-type pre-commit --hook-type commit-msg
    @[ -f .env ] || { pw="$(openssl rand -hex 24 2>/dev/null)" || pw=""; [ -n "$pw" ] || { echo "error: openssl is needed to generate POSTGRES_PASSWORD; .env not created" >&2; exit 1; }; umask 077; sed "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$pw/" .env.example > .env; echo "created .env with a random POSTGRES_PASSWORD"; }
    @just doctor

# Check this machine can run Satat (changes nothing)
doctor:
    @scripts/doctor.sh

# Bring the platform up: Hatchet, the worker's token, then the worker
run: doctor
    #!/usr/bin/env bash
    set -euo pipefail
    if ! grep -q '^HATCHET_CLIENT_TOKEN=.' .env; then
      {{compose_placeholders}} docker compose up -d --wait hatchet-lite
      scripts/hatchet-token.sh
    fi
    scripts/compose-up.sh

# Start or update the stack with satat deploy up (creates the Hatchet token on first run)
up *args:
    go run ./cmd/satat deploy up {{args}}

# Show each service and the worker's readiness
status *args:
    go run ./cmd/satat deploy status {{args}}

# Stop the stack and its agent containers; `just down --volumes` also deletes the platform's data
down *args:
    go run ./cmd/satat deploy down {{args}}

# Follow a service's logs (postgres, hatchet-lite, docker-proxy or worker)
logs service="worker":
    {{compose_placeholders}} docker compose logs -f {{service}}

# Run a one-shot agent on the smoke lane and wait for its result
smoke *args:
    go run ./cmd/satat agent run smoke --wait {{args}}

# List queued and running agent runs
agents *args:
    go run ./cmd/satat agent ls {{args}}

# Queue an issue for the worker (it must be labelled ready-for-agent), e.g. `just enqueue owner/name 42`
enqueue repo issue:
    go run ./cmd/satat-enqueue --repo {{repo}} --issue {{issue}}

# Run what CI runs: pre-commit, gofmt, go vet, the Docker socket check and the compose render check
lint:
    pre-commit run --all-files
    @unformatted="$(git ls-files -z '*.go' | xargs -0 gofmt -l)"; [ -z "$unformatted" ] || { echo "$unformatted"; echo "run: gofmt -w on these"; exit 1; }
    go vet ./...
    scripts/check-compose-socket.sh
    go run ./cmd/satat deploy render --check

# Run all Go tests (needs Docker)
test:
    go test -p 1 -count=1 ./...

# Run the Go tests that don't start containers
test-short:
    go test -short -p 1 ./...

# Check the Harbor eval tasks: the reference solution must pass each one and doing nothing must fail
eval-check:
    harbor run -p evals/tasks -a oracle -n 4 -o docs/scratch/harbor-jobs --job-name "oracle-$(date +%Y%m%d-%H%M%S)"
    harbor run -p evals/tasks -a nop -n 4 -o docs/scratch/harbor-jobs --job-name "nop-$(date +%Y%m%d-%H%M%S)"

# Run the evals with Satat as the agent, 3 tries per task, 4 trials at a time: they mostly wait on
# the API (e.g. `just eval-satat -i greet -k 1`, or `-n 1` for one at a time)
eval-satat *args:
    PYTHONPATH=evals/agents harbor run -p evals/tasks -a satat_agent:SatatAgent -k 3 -n 4 \
      -o docs/scratch/harbor-jobs --job-name "satat-$(date +%Y%m%d-%H%M%S)" {{args}}

# Run the evals with Claude Code alone, at the Claude Code version Satat's image pins (the baseline),
# 3 tries per task, 4 trials at a time.
# CLAUDE_FORCE_OAUTH keeps it on the subscription token, as Satat's runner is.
eval-baseline *args:
    CLAUDE_FORCE_OAUTH=1 harbor run -p evals/tasks -a claude-code --ak "version=$(sed -n "s/^CLAUDE_CODE_VERSION='\([^']*\)'.*/\1/p" scripts/build-claude-image.sh)" \
      -k 3 -n 4 -o docs/scratch/harbor-jobs --job-name "baseline-$(date +%Y%m%d-%H%M%S)" {{args}}

# Summarise a Harbor job and save it in evals/results, compared with the agent's previous run
eval-summary job:
    python3 evals/summarize.py {{job}} --save

# Build the Go binaries
build:
    go build ./...

# Build the Claude lane image and the worker image
build-images:
    scripts/build-claude-image.sh
    {{compose_placeholders}} docker compose build worker
