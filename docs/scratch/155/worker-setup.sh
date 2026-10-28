#!/usr/bin/env bash
# PROTOTYPE for #155: operator steps for the minter. A LiteLLM internal_user whose key goes into OpenBao,
# readable only by the worker's AppRole (bound to the worker's address on the secrets network).
set -euo pipefail
M='Authorization: Bearer sk-placeholder-master'
curl -s localhost:4000/user/new -H "$M" -H 'content-type: application/json' \
  -d '{"user_id":"satat-minter","user_role":"internal_user","max_budget":2,"models":["claude-haiku-4-5-20251001","claude-sonnet-5-5","claude-haiku-5-5"]}' >/dev/null
bao() { docker compose exec -T -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=placeholder-root openbao bao "$@"; }
# The minter key goes from LiteLLM straight into OpenBao, never printed.
curl -s localhost:4000/key/generate -H "$M" -H 'content-type: application/json' -d '{"user_id":"satat-minter","key_alias":"minter"}' \
  | python3 -c 'import sys,json;sys.stdout.write(json.load(sys.stdin)["key"])' \
  | docker compose exec -T -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=placeholder-root openbao bao kv put -mount=satat litellm-minter-key value=- >/dev/null
printf 'path "satat/data/litellm-minter-key" {\n  capabilities = ["read"]\n}\n' | bao policy write worker - >/dev/null
bao write auth/approle/role/worker token_policies=worker token_ttl=1h secret_id_bound_cidrs=172.31.155.20/32 token_bound_cidrs=172.31.155.20/32 >/dev/null
role_id=$(bao read -field=role_id auth/approle/role/worker/role-id)
docker volume create satat-155_worker-approle >/dev/null
docker run --rm -v satat-155_worker-approle:/a busybox sh -c "printf %s '$role_id' > /a/role_id"
bao write -f -field=secret_id auth/approle/role/worker/secret-id | tr -d '\n' | docker run --rm -i -v satat-155_worker-approle:/a busybox sh -c 'cat > /a/secret_id'
echo "worker approle ready"
