#!/usr/bin/env bash
# The secret store's operator commands (ADR 0032). Use them through just:
#
#   scripts/secret-store.sh ensure    just run and just up, before the stack starts
#   just secrets < file               NAME=value lines on stdin, from any source
#   scripts/secret-store.sh check     doctor's check: changes nothing
#
# ensure is idempotent. Before the first start it writes the files OpenBao's self-init reads
# (deploy/compose/openbao.hcl), and prints the operator password once. On every run the
# secrets-apply service (OpenTofu, deploy/secrets/tofu) plans as doctor: do the store's component
# policies and AppRole roles match the policy files (deploy/secrets/policies/<component>.hcl)?
# Only when they differ does ensure log in as the operator and apply. The state holds no secret_id:
# ensure issues each component's credential itself (credentials). Then it asks for any granted
# entry that has no value. The operator password comes from SATAT_OPERATOR_PASSWORD, else a prompt
# on the terminal. The operator's tokens work only from loopback inside the OpenBao container's
# network namespace, so every call goes through `docker compose exec` or secrets-apply. Values pass
# on stdin only, never in argv, and are never printed. COMPOSE_PROJECT_NAME and COMPOSE_FILE select
# another stack, SATAT_POLICIES_DIR other policy files. Single-quoted scripts run in a container.
# shellcheck disable=SC2016
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C

readonly POLICIES="${SATAT_POLICIES_DIR:-deploy/secrets/policies}"
readonly PROJECT="${COMPOSE_PROJECT_NAME:-satat}"
password="${SATAT_OPERATOR_PASSWORD:-}"
token=""

die() { echo "error: $*" >&2; exit 1; }

# A component's uid, so its credential files are readable by it alone.
component_uid() {
  case "$1" in
    worker) echo 10002 ;; # images/worker/Dockerfile
    *) die "no uid for $1 in scripts/secret-store.sh component_uid" ;;
  esac
}

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
bx() { BAO_TOKEN="$token" dc exec -T -e BAO_TOKEN openbao bao "$@"; }

# put <volume> <file> <uid:gid>: write stdin to a file in a volume, mode 0400, owned by uid:gid.
# secrets-setup is the only other service that mounts the volumes.
put() {
  dc run --rm -T --quiet-pull secrets-setup \
    'umask 077 && cat > "/v/$1/$2" && chown "$3" "/v/$1/$2"' sh "$1" "$2" "$3" 2>/dev/null
}

running() { [ -n "$(dc ps -q openbao 2>/dev/null)" ]; }

# A terminal to prompt on.
terminal() { { : </dev/tty; } 2>/dev/null; }

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

# Every entry name the policy files grant, sorted. The module refuses any other kind of line.
granted() {
  { cat "$POLICIES"/*.hcl 2>/dev/null || true; } |
    sed -nE 's|^[[:space:]]*path "satat/data/([a-z][a-z0-9-]*)".*|\1|p' | sort -u
}

# The names of the entries in the store, sorted, listed as doctor (names, never values).
readonly ENTRIES_SCRIPT='BAO_TOKEN="$(bao write -field=token auth/userpass/login/doctor password=@/openbao/seal/doctor-password)" || exit 1
export BAO_TOKEN
{ bao list -format=json satat/metadata 2>/dev/null || true; } | { grep -oE "\"[^\"]+\"" || true; } | tr -d "\"" | sort'

entries() { dc exec -T openbao sh -c "$ENTRIES_SCRIPT" </dev/null; }

# The module's inputs, as JSON: each policy file, and each service's fixed address on the secrets
# network.
inputs() {
  TF_VAR_policies="$(python3 -c 'import json, pathlib, sys
print(json.dumps({p.stem: p.read_text() for p in pathlib.Path(sys.argv[1]).glob("*.hcl")}))' "$POLICIES")"
  TF_VAR_addresses="$(dc config --format json | python3 -c 'import json, sys
a = {n: ((v.get("networks") or {}).get("secrets") or {}).get("ipv4_address")
    for n, v in json.load(sys.stdin)["services"].items()}
print(json.dumps({n: v for n, v in a.items() if v}))')"
  export TF_VAR_policies TF_VAR_addresses
}

# A plan as doctor: exits 0 when the store matches the policy files, 2 when it differs.
plan() {
  dc run --rm -T --quiet-pull -e TF_VAR_policies -e TF_VAR_addresses secrets-apply \
    'TF_VAR_password="$(cat /v/bao-seal/doctor-password)" tofu plan -detailed-exitcode -input=false -no-color -var username=doctor' \
    </dev/null 2>&1
}

apply() {
  TF_VAR_password="$password" dc run --rm -T --quiet-pull -e TF_VAR_password -e TF_VAR_policies -e TF_VAR_addresses \
    secrets-apply 'tofu apply -auto-approve -input=false -no-color -var username=operator' </dev/null 2>&1
}

# A role's role_id, read as doctor.
readonly ROLE_ID_SCRIPT='BAO_TOKEN="$(bao write -field=token auth/userpass/login/doctor password=@/openbao/seal/doctor-password)" || exit 1
export BAO_TOKEN
bao read -field=role_id "auth/approle/role/$1/role-id"'

# Each component's credential in its cred-<component> volume. A new secret_id when a file is
# missing or role_id differs from the role's: the role is new, and its old secret_ids are gone.
# secret_id goes first, so a role_id that matches means both files are written.
credentials() {
  local f c uid role secret
  for f in "$POLICIES"/*.hcl; do
    [ -f "$f" ] || continue
    c="$(basename "$f" .hcl)"
    uid="$(component_uid "$c")"
    role="$(dc exec -T openbao sh -c "$ROLE_ID_SCRIPT" sh "$c" </dev/null)" || die "cannot read the $c role_id"
    if [ "$(dc run --rm -T --quiet-pull secrets-setup 'test -s "/v/$1/secret_id" && cat "/v/$1/role_id"' sh "cred-$c" 2>/dev/null </dev/null)" = "$role" ]; then
      continue
    fi
    [ -n "$token" ] || login
    secret="$(bx write -f -field=secret_id "auth/approle/role/$c/secret-id" </dev/null)"
    printf '%s' "$secret" | put "cred-$c" secret_id "$uid:$uid"
    printf '%s' "$role" | put "cred-$c" role_id "$uid:$uid"
    echo "wrote the $c credential"
  done
}

# Log in as the operator; sets token.
login() {
  if [ -z "$password" ]; then
    terminal || die "the store needs the operator: set SATAT_OPERATOR_PASSWORD or run from a terminal"
    read -rsp "operator password: " password </dev/tty
    echo >&2
  fi
  token="$(printf '%s' "$password" | bx write -field=token auth/userpass/login/operator password=- 2>/dev/null)" ||
    die "operator login failed (wrong password?)"
}

logout() { bx token revoke -self >/dev/null 2>&1 </dev/null || true; token=""; }

# Start OpenBao, and wait until doctor can log in: self-init has finished. Before the first start,
# write the seal key and doctor's and the operator's passwords for self-init into bao-seal.
# The password file is root's, so secrets-setup can print it, and OpenBao's group can read it.
# While it exists, print it and then delete it: it is shown once and kept nowhere else. tofu/ holds
# secrets-apply's state.
start() {
  local first i
  first="$(dc run --rm -T --quiet-pull secrets-setup 'cd /v/bao-seal
if [ ! -s unseal.key ]; then
  umask 077
  head -c 32 /dev/urandom > unseal.key
  for f in doctor-password operator-password; do
    head -c 16 /dev/urandom | od -An -tx1 | tr -d " \n" > "$f"
  done
  chown 100:1000 unseal.key doctor-password
  chown 0:1000 operator-password && chmod 0440 operator-password
fi
mkdir -p -m 0700 tofu
cat operator-password 2>/dev/null || true' 2>/dev/null </dev/null)"
  dc up -d --no-deps --quiet-pull openbao 2>/dev/null </dev/null
  for i in $(seq 30); do
    if entries >/dev/null 2>&1; then break; fi
    if [ "$i" -eq 30 ]; then
      [ -z "$first" ] || die "self-init did not finish (see: docker compose logs openbao). To start again: just down, docker volume rm ${PROJECT}_bao-data ${PROJECT}_bao-seal ${PROJECT}_cred-worker, just run"
      die "OpenBao is not ready after 30s (see: docker compose logs openbao)"
    fi
    sleep 1
  done
  if [ -n "$first" ]; then
    password="$first"
    cat <<EOF
The store is set up. Save the operator password in your password manager now.
It is shown only this once, and nothing else holds it:

  operator password:  $password

EOF
    dc run --rm -T --quiet-pull secrets-setup 'rm /v/bao-seal/operator-password' 2>/dev/null </dev/null
  fi
}

ensure() {
  local out rc=0 missing n value
  start
  out="$(dc build -q secrets-apply 2>&1 </dev/null)" || die "secrets-apply build failed: $out"
  inputs
  out="$(plan)" || rc=$?
  case "$rc" in
    0) ;;
    2)
      login
      out="$(apply)" || die "secrets-apply failed: $out"
      echo "applied the policies in $POLICIES"
      ;;
    *) die "secrets-apply plan failed: $out" ;;
  esac
  credentials

  out="$(entries)" || die "doctor login failed"
  missing="$(comm -23 <(granted) <(echo "$out"))"
  if [ -n "$missing" ]; then
    if ! terminal; then
      [ -z "$token" ] || logout
      die "missing entries: $(tr '\n' ' ' <<<"$missing")(run: just secrets)"
    fi
    [ -n "$token" ] || login
    for n in $missing; do
      value=""
      while [ -z "$value" ]; do
        read -rsp "$n: " value </dev/tty
        echo >&2
      done
      printf '%s' "$value" | bx kv put -mount=satat "$n" value=- >/dev/null
    done
  fi
  [ -z "$token" ] || logout
}

# NAME=value lines on stdin. Every line is checked before anything is written.
load() {
  local input line n=0 name value granted_names errs=() names=() values=() i
  # stdin first: docker compose exec -T would swallow it.
  input="$(cat)"
  granted_names="$(granted)"

  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    case "$line" in '' | '#'*) continue ;; esac
    if [[ "$line" != *=* ]]; then
      errs+=("line $n: not NAME=value")
      continue
    fi
    name="${line%%=*}"
    value="${line#*=}"
    if ! grep -qxF -- "$name" <<<"$granted_names"; then
      errs+=("$name: unknown (no file in $POLICIES grants it)")
    elif [ -z "$value" ]; then
      errs+=("$name: empty")
    elif printf '%s\n' ${names[@]+"${names[@]}"} | grep -qxF -- "$name"; then
      errs+=("$name: given twice")
    else
      names+=("$name")
      values+=("$value")
    fi
  done <<<"$input"

  if [ "${#errs[@]}" -gt 0 ]; then
    printf 'error: %s\n' "${errs[@]}" >&2
    die "nothing loaded"
  fi
  [ "${#names[@]}" -gt 0 ] || die "no NAME=value lines on stdin"
  { running && initialised; } || die "the store is not set up (run: just run)"
  login
  for i in "${!names[@]}"; do
    printf '%s' "${values[$i]}" | bx kv put -mount=satat "${names[$i]}" value=- >/dev/null
  done
  logout
  echo "loaded: ${names[*]}"
}

# For doctor. Prints one line; exits 0 ok, 3 warn, 1 fail.
check() {
  local out rc=0 missing
  if ! running; then
    if [ -n "$(docker volume ls -q --filter "label=com.docker.compose.project=$PROJECT" \
      --filter label=com.docker.compose.volume=bao-seal)" ]; then
      echo "not running (just run starts it)"
      exit 3
    fi
    echo "not set up (just run sets it up)"
    exit 3
  fi
  bx status >/dev/null 2>&1 </dev/null || rc=$?
  if [ "$rc" -ne 0 ]; then
    if ! initialised; then
      echo "not set up (run: just run)"
    else
      echo "sealed (check the seal key: docker compose logs openbao)"
    fi
    exit 1
  fi
  inputs
  out="$(plan)" || rc=$?
  case "$rc" in
    0) ;;
    2)
      echo "grants differ from $POLICIES (run: just run)"
      exit 1
      ;;
    *)
      echo "secrets-apply plan failed: $out"
      exit 1
      ;;
  esac
  if ! out="$(entries 2>&1)"; then
    echo "doctor login failed: $out"
    exit 1
  fi
  missing="$(comm -23 <(granted) <(echo "$out"))"
  if [ -n "$missing" ]; then
    echo "missing entries: $(tr '\n' ' ' <<<"$missing")(run: just secrets)"
    exit 1
  fi
  echo "unsealed, grants applied, every granted entry loaded"
}

case "${1:-}" in
  ensure) ensure </dev/null ;;
  load) load ;;
  check) check ;;
  *) die "usage: $0 {ensure | load | check}" ;;
esac
