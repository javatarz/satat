#!/usr/bin/env bash
# Bring the stack up with the worker's secrets taken from 1Password, never written to disk.
# Usage: scripts/compose-up.sh [docker compose up args]   (default: -d --build)
# Needs HATCHET_CLIENT_TOKEN in .env (scripts/hatchet-token.sh).
set -euo pipefail
cd "$(dirname "$0")/.."

item="${SATAT_OP_ITEM:-Satat Harness}"
get() { op item get "$item" --fields "$1" --reveal; }

CLAUDE_CODE_OAUTH_TOKEN="$(get claude-code-oauth-token)"
SATAT_GITHUB_TOKEN="$(get satat-sandbox-read)"
SATAT_PUSH_TOKEN="$(get satat-sandbox-write)"
export CLAUDE_CODE_OAUTH_TOKEN SATAT_GITHUB_TOKEN SATAT_PUSH_TOKEN

if [ "$#" -eq 0 ]; then set -- -d --build; fi
exec docker compose up "$@"
