#!/usr/bin/env bash
# PROTOTYPE for #155: load (or rotate) claude-oauth-token. Value on stdin, never printed.
set -euo pipefail
docker compose exec -T -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=placeholder-root openbao \
  sh -c 'bao kv put -mount=satat claude-oauth-token value=- >/dev/null && bao kv metadata get -mount=satat -format=json claude-oauth-token | grep -o "\"current_version\": [0-9]*"'
