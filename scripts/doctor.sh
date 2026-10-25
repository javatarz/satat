#!/usr/bin/env bash
# Check that this machine can run Satat. Prints one line per check and exits non-zero if any fails.
# Pure checks: it never installs, starts, pulls or writes anything, and never prints a secret.
#
# usage: scripts/doctor.sh   (or `just doctor`)
#
# To add a check, write a check_* function that calls ok, warn or fail once per thing it checks,
# and add it to CHECKS at the bottom.
set -euo pipefail
cd "$(dirname "$0")/.."

readonly TIMEOUT="${DOCTOR_TIMEOUT:-10}"
failures=0
docker_ok=0

ok()   { printf 'OK    %s\n' "$*"; }
warn() { printf 'WARN  %s\n' "$*"; }
fail() { printf 'FAIL  %s\n' "$*"; failures=$((failures + 1)); }
skip() { printf 'SKIP  %s\n' "$*"; }
has()  { command -v "$1" >/dev/null 2>&1; }

# The value of a key in .env, unquoted. Callers test it; nothing here prints it.
env_value() {
  local v
  v="$(grep -E "^$1=" .env 2>/dev/null | tail -n 1 | cut -d= -f2-)" || true
  v="${v%\"}"; v="${v#\"}"; v="${v%\'}"; v="${v#\'}"
  printf '%s' "$v"
}

# Run a command, killing it after <seconds>. macOS has no timeout(1).
with_timeout() {
  local secs="$1" pid watcher rc=0
  shift
  "$@" &
  pid=$!
  ( sleep "$secs"; kill "$pid" ) >/dev/null 2>&1 &
  watcher=$!
  wait "$pid" || rc=$?
  kill "$watcher" 2>/dev/null || true
  wait "$watcher" 2>/dev/null || true
  return "$rc"
}

# The version mise.toml pins for a tool.
mise_pin() { sed -nE "s/^$1 *= *\"([^\"]+)\".*/\1/p" mise.toml; }

check_docker() {
  if ! has docker; then
    fail "docker: not installed (install colima or Docker Desktop)"
  elif ! with_timeout "$TIMEOUT" docker info >/dev/null 2>&1; then
    fail "docker: daemon not reachable within ${TIMEOUT}s (start it: colima start, or open Docker Desktop)"
  else
    docker_ok=1
    ok "docker: daemon reachable"
  fi
}

check_compose() {
  local v
  if ! has docker; then
    skip "docker compose: needs docker"
  elif ! v="$(docker compose version --short 2>/dev/null)"; then
    fail "docker compose: plugin missing (install Docker Compose v2: brew install docker-compose)"
  elif [ "${v%%.*}" -lt 2 ] 2>/dev/null; then
    fail "docker compose: $v, need v2 or later"
  else
    ok "docker compose: $v"
  fi
}

check_go() {
  local want have
  want="$(mise_pin go)"
  if ! has go; then
    fail "go: not installed (run: mise install)"
    return
  fi
  # GOTOOLCHAIN=local: report the Go on PATH, never download another one. A mise shim whose
  # version is not installed fails here.
  have="$(GOTOOLCHAIN=local go env GOVERSION 2>/dev/null)" || have=""
  have="${have#go}"
  if [ -z "$have" ]; then
    fail "go: on PATH but does not run (run: mise install)"
  elif [ "$have" = "$want" ]; then
    ok "go: $have"
  else
    fail "go: $have, mise.toml pins $want (run: mise install, and activate mise in your shell)"
  fi
}

# FAIL is for what stops `just run`. Tools only lint or commits need are WARN.
check_just() {
  if has just; then ok "just: installed"; else fail "just: not installed (brew install just)"; fi
}

check_mise() {
  if has mise; then ok "mise: installed"; else warn "mise: not installed (brew install mise; it pins Go and Terraform)"; fi
}

check_actionlint() {
  if has actionlint; then ok "actionlint: installed"; else warn "actionlint: not installed (brew install actionlint; just lint needs it)"; fi
}

# The pre-commit hooks run terraform fmt and validate.
check_terraform() {
  local want have
  want="$(mise_pin terraform)"
  if ! has terraform; then
    warn "terraform: not installed (run: mise install; just lint needs it)"
    return
  fi
  have="$(terraform version 2>/dev/null | sed -nE '1s/^Terraform v//p')" || have=""
  if [ "$have" = "$want" ]; then
    ok "terraform: $have"
  else
    warn "terraform: ${have:-does not run}, mise.toml pins $want (run: mise install)"
  fi
}

check_pre_commit() {
  local hook name
  if ! has pre-commit; then
    warn "pre-commit: not installed (brew install pre-commit, then: just setup; just lint needs it)"
    return
  fi
  for name in commit-msg pre-push; do
    hook="$(git rev-parse --git-path "hooks/$name" 2>/dev/null || true)"
    if [ -z "$hook" ] || [ ! -f "$hook" ] || ! grep -q pre-commit "$hook"; then
      hook=""
      break
    fi
  done
  if [ -n "$hook" ]; then
    ok "pre-commit: installed, hooks installed"
  else
    # Not needed to run Satat, only to commit.
    warn "pre-commit: installed, hooks not installed (run: just setup)"
  fi
}

# Signing is optional. Not needed by `just run`.
check_signing() {
  local reason
  if reason="$(scripts/check-signing-config.sh --brief 2>&1)"; then
    ok "git: commit signing configured"
  else
    warn "git: $reason (see Signed commits in CONTRIBUTING.md)"
  fi
}

# Not needed by `just run`. `gh auth token` reads the local config; `gh auth status` calls GitHub.
check_gh() {
  if ! has gh; then
    warn "gh: not installed (brew install gh)"
  elif ! gh auth token >/dev/null 2>&1; then
    warn "gh: not signed in (run: gh auth login)"
  else
    ok "gh: installed, signed in"
  fi
}

# scripts/compose-up.sh reads the worker's tokens from 1Password.
# op can wait on the 1Password app indefinitely, so every call has a timeout.
check_op() {
  local accounts rc=0
  if ! has op; then
    fail "op: not installed (brew install 1password-cli; scripts/compose-up.sh reads tokens with it)"
    return
  fi
  if with_timeout "$TIMEOUT" op whoami >/dev/null 2>&1; then
    ok "op: installed, signed in"
    return
  fi
  accounts="$(with_timeout "$TIMEOUT" op account list 2>/dev/null)" || rc=$?
  if [ -n "$accounts" ]; then
    # With the desktop app integration there is no session until a command asks; compose-up.sh will.
    warn "op: account set up, no active session (1Password will ask to unlock when the stack starts)"
  elif [ "$rc" -ge 128 ]; then
    warn "op: no answer within ${TIMEOUT}s (check the 1Password app; compose-up.sh needs it)"
  else
    fail "op: no 1Password account (run: op account add, or turn on the desktop app integration)"
  fi
}

check_env() {
  local pw
  if [ ! -f .env ]; then
    fail ".env: missing (run: just setup, or copy .env.example and set POSTGRES_PASSWORD)"
    return
  fi
  pw="$(env_value POSTGRES_PASSWORD)"
  if [ -z "$pw" ] || [ "$pw" = "change-me" ]; then
    fail ".env: POSTGRES_PASSWORD unset or still the example value (set it in .env)"
  else
    ok ".env: POSTGRES_PASSWORD set"
  fi
  if [ -n "$(env_value HATCHET_CLIENT_TOKEN)" ]; then
    ok ".env: HATCHET_CLIENT_TOKEN set"
  else
    warn ".env: HATCHET_CLIENT_TOKEN unset (just run creates it with scripts/hatchet-token.sh)"
  fi
}

# True if the registry serves the manifest of <name>@<digest>. Anonymous pull tokens only.
registry_has() {
  local ref="$1" name digest host repo scheme=https token_url token auth=()
  name="${ref%@*}"; digest="${ref#*@}"
  # Drop a tag, which can only be in the last path segment (a registry port is in the first).
  case "${name##*/}" in *:*) name="${name%:*}" ;; esac
  host=registry-1.docker.io; repo="$name"
  case "$name" in
    */*)
      case "${name%%/*}" in
        *.*|*:*|localhost) host="${name%%/*}"; repo="${name#*/}" ;;
      esac ;;
  esac
  case "$host" in docker.io|index.docker.io) host=registry-1.docker.io ;; esac
  if [ "$host" = registry-1.docker.io ] && [ "${repo#*/}" = "$repo" ]; then repo="library/$repo"; fi
  case "$host" in localhost|localhost:*|127.0.0.1|127.0.0.1:*) scheme=http ;; esac
  case "$host" in
    registry-1.docker.io) token_url="https://auth.docker.io/token?service=registry.docker.io&scope=repository:${repo}:pull" ;;
    ghcr.io) token_url="https://ghcr.io/token?scope=repository:${repo}:pull" ;;
    *) token_url="" ;;
  esac
  if [ -n "$token_url" ]; then
    token="$(curl -fsS --max-time "$TIMEOUT" "$token_url" 2>/dev/null | sed -nE 's/.*"token" *: *"([^"]+)".*/\1/p')" || return 1
    auth=(-H "Authorization: Bearer $token")
  fi
  curl -fsI --max-time "$TIMEOUT" ${auth[@]+"${auth[@]}"} \
    -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json' \
    "${scheme}://${host}/v2/${repo}/manifests/${digest}" >/dev/null 2>&1
}

check_lane_images() {
  local lane image hint
  if [ "$docker_ok" -ne 1 ]; then skip "lane images: need docker"; return; fi
  while read -r lane image; do
    if [ "${image#*@sha256:}" = "$image" ]; then
      fail "lane $lane: $image is not pinned by digest (pin it as name@sha256:...; see scripts/image-digest.sh)"
    elif docker image inspect "$image" >/dev/null 2>&1; then
      ok "lane $lane: image present locally"
    elif has curl && registry_has "$image"; then
      ok "lane $lane: image pullable"
    else
      hint="check the image in satat.yaml"
      [ "$lane" = claude ] && hint="build it with scripts/build-claude-image.sh and put the printed reference in satat.yaml"
      fail "lane $lane: $image not local and not pullable ($hint)"
    fi
  done < <(awk '/^lanes:/ { in_lanes = 1; next }
                /^[^ #]/ { in_lanes = 0 }
                in_lanes && /^  [A-Za-z0-9_-]+:/ { lane = $1; sub(/:$/, "", lane) }
                in_lanes && $1 == "image:" { image = $2; gsub(/^["\047]|["\047]$/, "", image); print lane, image }' satat.yaml)
}

check_ports() {
  local port
  while read -r port; do
    if ! (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
      ok "port $port: free"
    elif [ "$docker_ok" -eq 1 ] && docker ps --filter label=com.docker.compose.project=satat --format '{{.Ports}}' \
        | grep -q "127\.0\.0\.1:$port->"; then
      ok "port $port: held by Satat's own containers"
    elif [ "$docker_ok" -ne 1 ]; then
      fail "port $port: in use, and Docker is not reachable to tell whether Satat holds it"
    else
      fail "port $port: in use by another process (find it: lsof -nP -iTCP:$port -sTCP:LISTEN)"
    fi
  done < <(grep -oE '"127\.0\.0\.1:[0-9]+:' compose.yaml | cut -d: -f2)
}

CHECKS=(
  check_docker
  check_compose
  check_go
  check_just
  check_mise
  check_pre_commit
  check_signing
  check_actionlint
  check_terraform
  check_gh
  check_op
  check_env
  check_lane_images
  check_ports
)

for check in "${CHECKS[@]}"; do "$check"; done

if [ "$failures" -gt 0 ]; then
  echo "doctor: $failures check(s) failed"
  exit 1
fi
echo "doctor: ready"
