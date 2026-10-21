#!/usr/bin/env bash
# Create a Hatchet API token for the worker and the enqueue tool, and store it in .env
# (gitignored) as HATCHET_CLIENT_TOKEN. Run it once after `docker compose up -d`; running it again
# replaces the token. The token is never printed.
set -euo pipefail
cd "$(dirname "$0")/.."

# compose.yaml requires the worker's variables even for `exec`; they are not used here.
export HATCHET_CLIENT_TOKEN="${HATCHET_CLIENT_TOKEN:-unused}" CLAUDE_CODE_OAUTH_TOKEN="${CLAUDE_CODE_OAUTH_TOKEN:-unused}"
export SATAT_GITHUB_TOKEN="${SATAT_GITHUB_TOKEN:-unused}" SATAT_PUSH_TOKEN="${SATAT_PUSH_TOKEN:-unused}"

tenant="$(docker compose exec -T postgres psql -U hatchet -d hatchet -tAc "select id from \"Tenant\" where slug = 'default'" | tr -d '[:space:]')"
[ -n "$tenant" ] || { echo "no 'default' tenant found: is the stack up and Hatchet healthy?" >&2; exit 1; }

token="$(docker compose exec -T hatchet-lite /hatchet-admin token create --config /config --tenant-id "$tenant" --name "satat-$(date +%Y%m%d%H%M%S)" | tr -d '[:space:]')"
case "$token" in
  eyJ*) ;;
  *) echo "unexpected output from hatchet-admin (not a token)" >&2; exit 1 ;;
esac

touch .env
grep -v '^HATCHET_CLIENT_TOKEN=' .env > .env.tmp || true
printf 'HATCHET_CLIENT_TOKEN=%s\n' "$token" >> .env.tmp
mv .env.tmp .env
chmod 600 .env
echo "HATCHET_CLIENT_TOKEN written to .env"
