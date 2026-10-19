#!/usr/bin/env bash
# Fail if any Compose service other than docker-proxy can reach the Docker socket (ADR 0027).
# The worker reaches the engine only through docker-proxy's endpoint allowlist.
#
# usage: scripts/check-compose-socket.sh [compose-file]
set -euo pipefail

file="${1:-compose.yaml}"
command -v docker >/dev/null || { echo "error: docker is required" >&2; exit 1; }

POSTGRES_PASSWORD=unused docker compose -f "$file" config --format json | python3 -c '
import json, sys

allowed = {"docker-proxy"}
bad = []
for name, svc in json.load(sys.stdin).get("services", {}).items():
    mounts = [v.get("source", "") + ":" + v.get("target", "") for v in svc.get("volumes", [])]
    env = [f"{k}={v}" for k, v in (svc.get("environment") or {}).items()]
    touches = [m for m in mounts + env if "docker.sock" in m or "docker_host" in m.lower()]
    if svc.get("privileged"):
        touches.append("privileged: true")
    if touches and name not in allowed:
        bad.append((name, touches))
for name, touches in bad:
    print(f"service {name} can reach the Docker API: {touches}", file=sys.stderr)
sys.exit(1 if bad else 0)
'
echo "ok: only docker-proxy mounts the Docker socket"
