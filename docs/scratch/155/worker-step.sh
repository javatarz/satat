#!/bin/sh
# PROTOTYPE for #155: what the worker does per step. Runs in a container at 172.31.155.20 on the secrets network.
# Logs in with AppRole, reads the minter key, mints a step key, prints only the step key (for the lane), or deletes one.
set -eu
export BAO_ADDR=http://openbao:8200
BAO_TOKEN=$(bao write -field=token auth/approle/login role_id="$(cat /approle/role_id)" secret_id="$(cat /approle/secret_id)")
export BAO_TOKEN
MK=$(bao kv get -mount=satat -field=value litellm-minter-key)
case "$1" in
  mint) wget -qO- --header "Authorization: Bearer $MK" --header 'content-type: application/json' \
          --post-data '{"duration":"15m","max_budget":0.5,"models":["claude-sonnet-5-5","claude-haiku-5-5","claude-haiku-4-5-20251001"],"key_type":"llm_api","key_alias":"step-from-worker"}' \
          http://litellm:4000/key/generate | sed 's/.*"key":"\([^"]*\)".*/\1/' ;;
  delete) wget -qO- --header "Authorization: Bearer $MK" --header 'content-type: application/json' \
          --post-data "{\"keys\":[\"$2\"]}" http://litellm:4000/key/delete >/dev/null && echo deleted ;;
  other) bao kv get -mount=satat -field=value claude-oauth-token >/dev/null 2>&1 && echo "READ claude-oauth-token" || echo "denied claude-oauth-token" ;;
esac
