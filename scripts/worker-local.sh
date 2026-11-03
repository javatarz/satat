#!/usr/bin/env bash
# Run the Satat worker on this machine in local mode: each step runs as a process with your own
# claude login (no CLAUDE_CODE_OAUTH_TOKEN, no lane image), not in a container. It is not a
# sandbox: the agent runs as you with permissions bypassed, so only point it at repos you trust.
#
# Usage: scripts/worker-local.sh   (or `just run-local`, which starts Hatchet first)
#
# Hatchet must be up and HATCHET_CLIENT_TOKEN in .env (scripts/hatchet-token.sh). The GitHub tokens
# come from SATAT_GITHUB_TOKEN and SATAT_PUSH_TOKEN if set, else from your gh login, so the branch
# and PR are yours. Runs start only in repos listed under poller.repos in satat.yaml (or
# SATAT_INTAKE_REPOS). Steps go under $SATAT_LOCAL_DIR, default $TMPDIR/satat-local; each step's
# out/ (result.json, transcript, bundle) is kept there.
set -euo pipefail
cd "$(dirname "$0")/.."

HATCHET_CLIENT_TOKEN="${HATCHET_CLIENT_TOKEN:-$(sed -n 's/^HATCHET_CLIENT_TOKEN=//p' .env 2>/dev/null)}"
if [ -z "$HATCHET_CLIENT_TOKEN" ]; then
  echo "worker-local: no HATCHET_CLIENT_TOKEN in .env; run: just run-local" >&2
  exit 1
fi
if [ -z "${SATAT_GITHUB_TOKEN:-}" ] || [ -z "${SATAT_PUSH_TOKEN:-}" ]; then
  gh_token="$(gh auth token)" || { echo "worker-local: set SATAT_GITHUB_TOKEN and SATAT_PUSH_TOKEN, or log in with gh" >&2; exit 1; }
  SATAT_GITHUB_TOKEN="${SATAT_GITHUB_TOKEN:-$gh_token}"
  SATAT_PUSH_TOKEN="${SATAT_PUSH_TOKEN:-$gh_token}"
fi
export HATCHET_CLIENT_TOKEN SATAT_GITHUB_TOKEN SATAT_PUSH_TOKEN
export HATCHET_CLIENT_HOST_PORT=localhost:7077 HATCHET_CLIENT_TLS_STRATEGY=none HATCHET_CLIENT_SERVER_URL=http://localhost:8888
export SATAT_LAUNCHER=local SATAT_CONFIG="${SATAT_CONFIG:-satat.yaml}"
# The lane's default toolchains, for a repo that pins none (the image has them at /usr/share/satat).
export SATAT_MISE_DEFAULTS="${SATAT_MISE_DEFAULTS:-$PWD/images/claude/mise-config.toml}"

exec go run ./cmd/satat-worker
