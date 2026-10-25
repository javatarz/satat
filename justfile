# Satat's entry point. `just` lists the recipes.

set shell := ["bash", "-euo", "pipefail", "-c"]

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

# Print whether a repo is ready for Satat, as the worker checks it before an issue (reads only; opts: --base B, --issue N)
check-repo *args:
    @scripts/check-repo.sh {{args}}

# Bring the platform up: Hatchet, the worker's token, then the worker
run: doctor
    #!/usr/bin/env bash
    set -euo pipefail
    if ! grep -q '^HATCHET_CLIENT_TOKEN=.' .env; then
      # compose.yaml requires the worker's variables to parse, even to start only Hatchet.
      HATCHET_CLIENT_TOKEN=unused CLAUDE_CODE_OAUTH_TOKEN=unused SATAT_GITHUB_TOKEN=unused SATAT_PUSH_TOKEN=unused \
        docker compose up -d --wait hatchet-lite
      scripts/hatchet-token.sh
    fi
    scripts/compose-up.sh

# Run what CI runs: pre-commit, gofmt, go vet and the Docker socket check
lint:
    pre-commit run --all-files
    @unformatted="$(git ls-files -z '*.go' | xargs -0 gofmt -l)"; [ -z "$unformatted" ] || { echo "$unformatted"; echo "run: gofmt -w on these"; exit 1; }
    go vet ./...
    scripts/check-compose-socket.sh

# Run all Go tests (needs Docker)
test:
    go test -p 1 -count=1 ./...

# Run the Go tests that don't start containers
test-short:
    go test -short -p 1 ./...

# Build the Go binaries
build:
    go build ./...

# Build the Claude lane image and the worker image
build-images:
    scripts/build-claude-image.sh
    HATCHET_CLIENT_TOKEN=unused CLAUDE_CODE_OAUTH_TOKEN=unused SATAT_GITHUB_TOKEN=unused SATAT_PUSH_TOKEN=unused \
      docker compose build worker
