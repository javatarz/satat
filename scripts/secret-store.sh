#!/usr/bin/env bash
# The secret store's operator commands (ADR 0032). Use them through just:
#
#   just secrets-init                        once per environment: seal key, policies, credentials
#   just secrets-load < file                 NAME=value lines on stdin, from any source
#   just secrets-grant <component> <name>    let a component read one more entry
#   scripts/secret-store.sh check                 doctor's check: running, unsealed, every entry loaded
#
# load and grant log in as the operator: the password comes from SATAT_OPERATOR_PASSWORD, else a
# prompt on the terminal. The operator's tokens work only from loopback inside the OpenBao
# container, so every call goes through `docker compose exec`. Values pass on stdin only, never
# in argv, and are never printed. COMPOSE_PROJECT_NAME and COMPOSE_FILE select another stack.
# Single-quoted scripts run in the OpenBao container and expand there.
# shellcheck disable=SC2016
set -euo pipefail
cd "$(dirname "$0")/.."

# The bootstrap grants. After init the store is the authority: change them with secrets-grant.
readonly WORKER_ENTRIES=(github-read-token github-push-token claude-oauth-token)
readonly WORKER_UID=10002 # images/worker/Dockerfile
readonly BAO_UID=100:1000 # the openbao user in the OpenBao image
readonly PROJECT="${COMPOSE_PROJECT_NAME:-satat}"

die() { echo "error: $*" >&2; exit 1; }

# docker compose with a placeholder for each variable compose.yaml requires that neither the
# environment nor .env sets. Compose interpolates the whole file for any command; no command here
# creates a container that uses one.
dc() {
  local name vars=()
  while read -r name; do
    if [ -z "${!name:-}" ] && ! grep -qE "^$name=." .env 2>/dev/null; then
      vars+=("$name=unused")
    fi
  done < <(grep -oE '\$\{[A-Za-z_][A-Za-z0-9_]*:?\?' compose.yaml | sed -E 's/^\$\{//; s/:?\?$//' | sort -u)
  env ${vars[@]+"${vars[@]}"} docker compose "$@"
}

# bao in the OpenBao container, from loopback, with $token (if any) as BAO_TOKEN.
bx() { BAO_TOKEN="${token:-}" dc exec -T -e BAO_TOKEN openbao bao "$@"; }

# A shell script in the OpenBao container, with $token (if any) as BAO_TOKEN.
bsh() { BAO_TOKEN="${token:-}" dc exec -T -e BAO_TOKEN openbao sh -c "$1"; }

# put <volume> <file> <uid:gid>: write stdin to a file in a volume, mode 0400, owned by uid:gid.
# secrets-setup is the only other service that mounts the volumes.
put() {
  dc run --rm -T --quiet-pull secrets-setup \
    'umask 077 && cat > "/v/$1/$2" && chown "$3" "/v/$1/$2"' sh "$1" "$2" "$3" 2>/dev/null
}

running() { [ -n "$(dc ps -q openbao 2>/dev/null)" ]; }

# Wait for the server to answer (unsealed or not). bao status: 0 unsealed, 2 sealed, 1 error.
wait_answering() {
  local i rc
  for i in $(seq 30); do
    rc=0; bx status >/dev/null 2>&1 </dev/null || rc=$?
    if [ "$rc" -ne 1 ]; then return 0; fi
    sleep 1
  done
  die "OpenBao did not answer within 30s (see: docker compose logs openbao)"
}

wait_unsealed() {
  local i
  for i in $(seq 30); do
    if bx status >/dev/null 2>&1 </dev/null; then return 0; fi
    sleep 1
  done
  die "OpenBao is still sealed after 30s (see: docker compose logs openbao)"
}

# initialised: bao operator init -status exits 0 when initialised, 2 when not.
initialised() {
  local rc=0
  bx operator init -status >/dev/null 2>&1 </dev/null || rc=$?
  case "$rc" in
    0) return 0 ;;
    2) return 1 ;;
    *) die "cannot tell whether the store is initialised (see: docker compose logs openbao)" ;;
  esac
}

# read-only grants on named entries: the only kind of component policy.
policy_for() {
  local e
  for e in "$@"; do printf 'path "satat/data/%s" { capabilities = ["read"] }\n' "$e"; done
}

worker_addr() {
  dc config --format json | python3 -c 'import json, sys
print(json.load(sys.stdin)["services"]["worker"]["networks"]["secrets"]["ipv4_address"])'
}

init() {
  local addr out recovery root password role secret
  addr="$(worker_addr)" || die "no fixed worker address on the secrets network in compose.yaml"
  if running && initialised; then
    die "the store is already initialised; secrets-init runs once per environment"
  fi

  # The seal key first: OpenBao reads it at start. Kept if a failed init left one.
  dc run --rm -T --quiet-pull secrets-setup \
    'test -s /v/bao-seal/unseal.key || { umask 077 && head -c 32 /dev/urandom > /v/bao-seal/unseal.key && chown '"$BAO_UID"' /v/bao-seal/unseal.key; }' \
    2>/dev/null </dev/null
  dc up -d --no-deps --quiet-pull openbao 2>/dev/null </dev/null
  wait_answering
  if initialised; then
    die "the store is already initialised; secrets-init runs once per environment"
  fi

  out="$(bx operator init -recovery-shares=1 -recovery-threshold=1 </dev/null)"
  recovery="$(sed -nE 's/^Recovery Key 1: //p' <<<"$out")"
  root="$(sed -nE 's/^Initial Root Token: //p' <<<"$out")"
  [ -n "$recovery" ] && [ -n "$root" ] || die "unexpected output from bao operator init"
  trap 'echo "error: init stopped part way. To start again: just down, docker volume rm ${PROJECT}_bao-data ${PROJECT}_bao-seal ${PROJECT}_cred-worker, just secrets-init" >&2' ERR
  token="$root"
  wait_unsealed

  bx secrets enable -path=satat -version=2 kv >/dev/null </dev/null
  bx auth enable approle >/dev/null </dev/null
  bx auth enable userpass >/dev/null </dev/null

  policy_for "${WORKER_ENTRIES[@]}" | bx policy write worker - >/dev/null
  # The operator writes values, policies and roles. It cannot read a value back.
  bx policy write operator - >/dev/null <<'EOF'
path "satat/data/*"        { capabilities = ["create", "update"] }
path "satat/metadata"      { capabilities = ["list"] }
path "sys/policies/acl"    { capabilities = ["list"] }
path "sys/policies/acl/*"  { capabilities = ["create", "read", "update", "delete"] }
path "auth/approle/role/*" { capabilities = ["create", "read", "update"] }
EOF
  # doctor lists entry names and policies, never values.
  bx policy write doctor - >/dev/null <<'EOF'
path "satat/metadata"     { capabilities = ["list"] }
path "sys/policies/acl"   { capabilities = ["list"] }
path "sys/policies/acl/*" { capabilities = ["read"] }
EOF

  # The worker's credential works only from its fixed address on the secrets network.
  bx write auth/approle/role/worker token_policies=worker token_ttl=1h \
    secret_id_bound_cidrs="$addr/32" token_bound_cidrs="$addr/32" >/dev/null </dev/null
  role="$(bx read -field=role_id auth/approle/role/worker/role-id </dev/null)"
  secret="$(bx write -f -field=secret_id auth/approle/role/worker/secret-id </dev/null)"
  printf '%s' "$role" | put cred-worker role_id "$WORKER_UID:$WORKER_UID"
  printf '%s' "$secret" | put cred-worker secret_id "$WORKER_UID:$WORKER_UID"

  # doctor's credential sits beside the seal key, readable only inside the OpenBao container,
  # where whoever can read it can already read the seal key.
  bx write auth/approle/role/doctor token_policies=doctor token_ttl=1m \
    secret_id_bound_cidrs=127.0.0.1/32 token_bound_cidrs=127.0.0.1/32 >/dev/null </dev/null
  bx read -field=role_id auth/approle/role/doctor/role-id </dev/null | put bao-seal doctor-role-id "$BAO_UID"
  bx write -f -field=secret_id auth/approle/role/doctor/secret-id </dev/null | put bao-seal doctor-secret-id "$BAO_UID"

  password="$(openssl rand -hex 16)"
  printf '%s' "$password" | bx write auth/userpass/users/operator password=- \
    token_policies=operator token_bound_cidrs=127.0.0.1/32 >/dev/null

  bx token revoke -self >/dev/null </dev/null
  if bx token lookup >/dev/null 2>&1 </dev/null; then
    die "the root token still works after revoke"
  fi
  trap - ERR
  token=""

  cat <<EOF
The store is initialised and the root token is revoked.
Save these two in your password manager now. They are shown only this once:

  recovery key:       $recovery
  operator password:  $password

Next: just secrets-load < your-secrets-file (names in secrets.example.env)
EOF
}

# Log in as the operator; sets token.
login() {
  local password="${SATAT_OPERATOR_PASSWORD:-}"
  if [ -z "$password" ]; then
    [ -r /dev/tty ] || die "set SATAT_OPERATOR_PASSWORD or run from a terminal"
    read -rsp "operator password: " password </dev/tty
    echo >&2
  fi
  running || die "OpenBao is not running (start the stack: just up)"
  token="$(printf '%s' "$password" | bx write -field=token auth/userpass/login/operator password=- 2>/dev/null)" ||
    die "operator login failed (wrong password, or the store is not initialised: just secrets-init)"
}

logout() { bx token revoke -self >/dev/null 2>&1 </dev/null || true; token=""; }

# The entry names the policies grant, one per line.
required_names() {
  bsh 'for p in $(bao policy list); do bao policy read "$p" 2>/dev/null || true; done' </dev/null |
    sed -nE 's/^path "satat\/data\/([A-Za-z0-9_-]+)".*/\1/p' | sort -u
}

# The entry names in the store, one per line (metadata only).
present_names() {
  { bx list -format=json satat/metadata 2>/dev/null </dev/null || true; } | { grep -oE '"[^"]+"' || true; } | tr -d '"' | sort -u
}

load() {
  local input line n=0 name value required present errs=() names=() values=() i
  # stdin first: docker compose exec -T would swallow it.
  input="$(cat)"
  login
  required="$(required_names)"
  present="$(present_names)"

  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    case "$line" in '' | '#'*) continue ;; esac
    if [[ "$line" != *=* ]]; then
      errs+=("line $n: not NAME=value")
      continue
    fi
    name="${line%%=*}"
    value="${line#*=}"
    if ! grep -qxF -- "$name" <<<"$required"; then
      errs+=("$name: unknown (no policy grants it; see secrets.example.env or just secrets-grant)")
    elif [ -z "$value" ]; then
      errs+=("$name: empty")
    elif printf '%s\n' ${names[@]+"${names[@]}"} | grep -qxF -- "$name"; then
      errs+=("$name: given twice")
    else
      names+=("$name")
      values+=("$value")
    fi
  done <<<"$input"

  while read -r name; do
    if [ -n "$name" ] && ! printf '%s\n' ${names[@]+"${names[@]}"} | grep -qxF -- "$name" &&
      ! grep -qxF -- "$name" <<<"$present"; then
      errs+=("$name: missing (not in the input and not in the store)")
    fi
  done <<<"$required"

  if [ "${#errs[@]}" -gt 0 ]; then
    logout
    printf 'error: %s\n' "${errs[@]}" >&2
    die "nothing loaded"
  fi
  if [ "${#names[@]}" -eq 0 ]; then
    logout
    die "no NAME=value lines on stdin"
  fi
  for i in "${!names[@]}"; do
    printf '%s' "${values[$i]}" | bx kv put -mount=satat "${names[$i]}" value=- >/dev/null
  done
  logout
  echo "loaded: ${names[*]}"
}

grant() {
  local component="$1" name="$2" policy
  [[ "$component" =~ ^[a-z][a-z0-9-]*$ ]] || die "component: lower case letters, digits and -"
  [[ "$name" =~ ^[a-z][a-z0-9-]*$ ]] || die "name: lower case letters, digits and -"
  case "$component" in operator | doctor | default | root) die "$component is not a component" ;; esac
  login
  if ! policy="$(bx policy read "$component" 2>/dev/null </dev/null)"; then
    logout
    die "no policy named $component"
  fi
  if grep -qF "\"satat/data/$name\"" <<<"$policy"; then
    logout
    echo "$component already reads $name"
    return
  fi
  { printf '%s\n' "$policy"; policy_for "$name"; } | bx policy write "$component" - >/dev/null
  logout
  echo "$component now reads $name. Load its value: just secrets-load"
}

# Runs inside the OpenBao container (busybox sh) as doctor's list-only identity: prints each
# granted entry name that has no entry in the store.
readonly DOCTOR_SCRIPT='set -e
BAO_TOKEN="$(bao write -field=token auth/approle/login \
  role_id=@/openbao/seal/doctor-role-id secret_id=@/openbao/seal/doctor-secret-id)"
export BAO_TOKEN
need="$(for p in $(bao policy list); do bao policy read "$p" 2>/dev/null || true; done |
  sed -nE "s/^path \"satat\/data\/([A-Za-z0-9_-]+)\".*/\1/p" | sort -u)"
have="$({ bao list -format=json satat/metadata 2>/dev/null || true; } | { grep -oE "\"[^\"]+\"" || true; } | tr -d "\"")"
for n in $need; do echo "$have" | grep -qxF "$n" || echo "$n"; done'

# For doctor. Prints one line; exits 0 ok, 3 warn, 1 fail.
check() {
  local out rc=0
  if ! running; then
    if [ -n "$(docker volume ls -q --filter "label=com.docker.compose.project=$PROJECT" \
      --filter label=com.docker.compose.volume=bao-seal)" ]; then
      echo "not running (just up starts it)"
      exit 3
    fi
    echo "no store (run: just secrets-init)"
    exit 1
  fi
  bx status >/dev/null 2>&1 </dev/null || rc=$?
  if [ "$rc" -ne 0 ]; then
    if ! initialised; then
      echo "not initialised (run: just secrets-init)"
    else
      echo "sealed (check the seal key: docker compose logs openbao)"
    fi
    exit 1
  fi
  if ! out="$(bsh "$DOCTOR_SCRIPT" </dev/null 2>&1)"; then
    echo "doctor login failed: $out"
    exit 1
  fi
  if [ -n "$out" ]; then
    echo "missing entries: $(tr '\n' ' ' <<<"$out")(run: just secrets-load)"
    exit 1
  fi
  echo "unsealed, every granted entry loaded"
}

case "${1:-}" in
  init) init ;;
  load) load ;;
  grant)
    [ "$#" -eq 3 ] || die "usage: $0 grant <component> <name>"
    grant "$2" "$3"
    ;;
  check) check ;;
  *) die "usage: $0 {init | load | grant <component> <name> | check}" ;;
esac
