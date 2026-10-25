#!/usr/bin/env bash
# Print whether a repo is ready for Satat (the check the worker runs before an issue). Read-only.
# Usage: scripts/check-repo.sh [--base BRANCH] [--issue N] owner/name
# The token-reach check treats only SATAT_INTAKE_REPOS and poller.repos (satat.yaml) as listed, so
# a sandbox repo must be listed there, as must every other repo the machine user can reach.
# SATAT_PUSH_TOKEN comes from the environment, or from 1Password like scripts/compose-up.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -z "${SATAT_PUSH_TOKEN:-}" ]; then
  item="${SATAT_OP_ITEM:-Satat Harness}"
  SATAT_PUSH_TOKEN="$(op item get "$item" --fields satat-sandbox-write --reveal)"
  export SATAT_PUSH_TOKEN
fi
exec go run ./cmd/satat-check-repo "$@"
