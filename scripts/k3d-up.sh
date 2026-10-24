#!/usr/bin/env bash
# Create the local k3d cluster `satat` with a registry and agent-sandbox. Idempotent: each step is
# skipped when already done. Touches only the cluster `satat` and the registry `satat-registry`.
# Usage: scripts/k3d-up.sh
# Env:   SATAT_K3D_REGISTRY_CONFIG  path to a registries.yaml to use instead of the generated one
# Takes no secrets.
set -euo pipefail

# Resolve the optional registries.yaml before changing directory.
registry_config_file=""
if [ -n "${SATAT_K3D_REGISTRY_CONFIG:-}" ]; then
  [ -f "$SATAT_K3D_REGISTRY_CONFIG" ] || {
    printf 'error: SATAT_K3D_REGISTRY_CONFIG file not found: %s\n' "$SATAT_K3D_REGISTRY_CONFIG" >&2
    exit 1
  }
  registry_config_file="$(cd "$(dirname "$SATAT_K3D_REGISTRY_CONFIG")" && pwd)/$(basename "$SATAT_K3D_REGISTRY_CONFIG")"
fi

cd "$(dirname "$0")/.."

K3D_VERSION=v5.9.0
K3S_IMAGE=rancher/k3s:v1.34.12-k3s1
AGENT_SANDBOX_URL=https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.7/sandbox-with-extensions.yaml
CLUSTER=satat
REGISTRY=satat-registry
REGISTRY_PORT=5111
API_PORT=6550
CONTEXT=k3d-$CLUSTER
BIN_DIR=docs/scratch/bin
K3D="$BIN_DIR/k3d"

dl_tmp=""
cleanup() { if [ -n "$dl_tmp" ]; then rm -rf "$dl_tmp"; fi; }
trap cleanup EXIT

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

install_k3d() {
  if [ -x "$K3D" ] && [ "$("$K3D" version | awk 'NR==1{print $3}')" = "$K3D_VERSION" ]; then return; fi
  local os arch asset base
  case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) die "unsupported OS $(uname -s)" ;; esac
  case "$(uname -m)" in arm64 | aarch64) arch=arm64 ;; x86_64 | amd64) arch=amd64 ;; *) die "unsupported arch $(uname -m)" ;; esac
  asset="k3d-$os-$arch"
  base="https://github.com/k3d-io/k3d/releases/download/$K3D_VERSION"
  log "installing k3d $K3D_VERSION ($asset) into $BIN_DIR"
  mkdir -p "$BIN_DIR"
  dl_tmp="$(mktemp -d)"
  curl -fsSL -o "$dl_tmp/$asset" "$base/$asset"
  curl -fsSL -o "$dl_tmp/checksums.txt" "$base/checksums.txt"
  local want got
  want="$(awk -v a="_dist/$asset" '$2 == a {print $1}' "$dl_tmp/checksums.txt")"
  [ -n "$want" ] || die "no checksum for $asset in checksums.txt"
  got="$(shasum -a 256 "$dl_tmp/$asset" | awk '{print $1}')"
  [ "$want" = "$got" ] || die "checksum mismatch for $asset"
  install -m 0755 "$dl_tmp/$asset" "$K3D"
}

check_port_free() {
  local port="$1" holders
  holders="$(docker ps --filter "publish=$port" --format '{{.Names}}')"
  [ -z "$holders" ] || die "host port $port is held by container(s): $holders"
  if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then die "host port $port is already in use"; fi
}

registry_exists() {
  local out
  out="$("$K3D" registry list --no-headers 2>/dev/null || true)"
  [[ $'\n'"$out" == *$'\n'"k3d-$REGISTRY "* ]]
}

# cluster_state prints "absent", "running" or "stopped".
cluster_state() {
  local out line servers fields
  out="$("$K3D" cluster list --no-headers 2>/dev/null || true)"
  while read -r line; do
    read -ra fields <<<"$line"
    if [ "${fields[0]:-}" = "$CLUSTER" ]; then
      servers="${fields[1]:-0/0}"
      if [ "${servers%/*}" = "${servers#*/}" ] && [ "${servers%/*}" != 0 ]; then echo running; else echo stopped; fi
      return
    fi
  done <<<"$out"
  echo absent
}

registry_config() {
  if [ -n "$registry_config_file" ]; then
    printf '%s\n' "$registry_config_file"
    return
  fi
  mkdir -p docs/scratch/k3d
  cat >docs/scratch/k3d/registries.yaml <<YAML
mirrors:
  "localhost:$REGISTRY_PORT":
    endpoint:
      - http://k3d-$REGISTRY:5000
YAML
  printf '%s\n' docs/scratch/k3d/registries.yaml
}

install_k3d
command -v kubectl >/dev/null || die "kubectl not found"
docker info >/dev/null 2>&1 || die "docker is not reachable"

# Only the objects about to be created need their host ports free.
if ! registry_exists; then check_port_free "$REGISTRY_PORT"; fi
state="$(cluster_state)"
if [ "$state" = absent ]; then
  check_port_free 7077
  check_port_free 8888
  check_port_free "$API_PORT"
fi

if registry_exists; then
  log "registry $REGISTRY exists, skipping"
else
  log "creating registry $REGISTRY on 127.0.0.1:$REGISTRY_PORT"
  "$K3D" registry create "$REGISTRY" --port "127.0.0.1:$REGISTRY_PORT"
fi

if [ "$state" = running ]; then
  log "cluster $CLUSTER exists, skipping"
elif [ "$state" = stopped ]; then
  log "starting stopped cluster $CLUSTER"
  "$K3D" cluster start "$CLUSTER" --wait
else
  log "creating cluster $CLUSTER"
  "$K3D" cluster create "$CLUSTER" \
    --image "$K3S_IMAGE" \
    --api-port "127.0.0.1:$API_PORT" \
    --kubeconfig-switch-context=false \
    -p "127.0.0.1:7077:30077@server:0" \
    -p "127.0.0.1:8888:30888@server:0" \
    --registry-use "k3d-$REGISTRY:$REGISTRY_PORT" \
    --registry-config "$(registry_config)" \
    --wait
fi

log "applying agent-sandbox v1.0.7"
kubectl --context "$CONTEXT" apply --server-side -f "$AGENT_SANDBOX_URL"
kubectl --context "$CONTEXT" -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=180s

echo "SATAT_REGISTRY=localhost:$REGISTRY_PORT"
