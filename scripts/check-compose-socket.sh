#!/usr/bin/env bash
# Fail if any Compose service other than docker-proxy can reach the Docker socket (ADR 0027).
# The worker (and only the worker) reaches the engine, through docker-proxy's endpoint allowlist:
# it may set DOCKER_HOST, and only to the proxy.
#
# usage: scripts/check-compose-socket.sh [compose-file]
set -euo pipefail

file="${1:-compose.yaml}"
command -v docker >/dev/null || { echo "error: docker is required" >&2; exit 1; }

[[ -f "$file" ]] || { echo "error: $file not found" >&2; exit 1; }

# Dummy values for every variable compose.yaml requires (${NAME:?...} or ${NAME?...}, the pattern
# satat deploy uses for its placeholders), so a new lane pass_env name needs no change here; only
# the shape of the config is checked.
placeholders=()
while IFS= read -r name; do
  placeholders+=("$name=unused")
done < <(grep -oE '\$\{[A-Za-z_][A-Za-z0-9_]*:?\?' "$file" | sed -E 's/^\$\{//; s/:?\?$//' | sort -u)

env ${placeholders[@]+"${placeholders[@]}"} docker compose -f "$file" config --format json | python3 -c '
import json, sys

allowed = {"docker-proxy"}
holders = {"worker"}
proxy = "tcp://docker-proxy:2375"
bad = []
for name, svc in json.load(sys.stdin).get("services", {}).items():
    mounts = [v.get("source", "") + ":" + v.get("target", "") for v in svc.get("volumes", [])]
    env = [f"{k}={v}" for k, v in (svc.get("environment") or {}).items()]
    if name in holders:
        env = [e for e in env if e != f"DOCKER_HOST={proxy}"]
    touches = [m for m in mounts + env if "docker.sock" in m or "docker_host" in m.lower()]
    if svc.get("privileged"):
        touches.append("privileged: true")
    if touches and name not in allowed:
        bad.append((name, touches))
for name, touches in bad:
    print(f"service {name} can reach the Docker API: {touches}", file=sys.stderr)
sys.exit(1 if bad else 0)
'
echo "ok: only docker-proxy mounts the Docker socket; only the worker reaches it, through the proxy"
