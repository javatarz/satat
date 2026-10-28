#!/usr/bin/env bash
# PROTOTYPE for #155: configure the dev OpenBao like ADR 0032 (satat/ KV v2, one policy and AppRole per component).
set -euo pipefail
bao() { docker compose exec -T -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=placeholder-root openbao bao "$@"; }
until bao status >/dev/null 2>&1; do sleep 1; done
bao secrets enable -path=satat kv-v2 >/dev/null
printf 'path "satat/data/claude-oauth-token" {\n  capabilities = ["read"]\n}\n' | bao policy write litellm - >/dev/null
bao auth enable approle >/dev/null
bao write auth/approle/role/litellm token_policies=litellm token_ttl=1h token_max_ttl=24h \
  secret_id_bound_cidrs=172.31.155.10/32 token_bound_cidrs=172.31.155.10/32 >/dev/null
role_id=$(bao read -field=role_id auth/approle/role/litellm/role-id)
# The secret_id never reaches the terminal: piped from OpenBao straight into the volume.
docker run --rm -v satat-155_litellm-approle:/a busybox sh -c "printf %s '$role_id' > /a/role_id"
bao write -f -field=secret_id auth/approle/role/litellm/secret-id | tr -d '\n' | docker run --rm -i -v satat-155_litellm-approle:/a busybox sh -c 'cat > /a/secret_id'
echo "approle ready"
