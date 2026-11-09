#!/usr/bin/env bash
# Fail if a Compose service outside the allow list can reach the secret store (ADR 0032): joins the
# secrets network, shares the network namespace of a service on it, or mounts one of the store's
# volumes (cred-*, bao-seal, bao-data). A service on the list must have exactly its listed mounts.
# Every profile is checked, so secrets-setup is too.
#
# usage: scripts/check-compose-secrets.sh [compose-file]
set -euo pipefail

file="${1:-compose.yaml}"
command -v docker >/dev/null || { echo "error: docker is required" >&2; exit 1; }

[[ -f "$file" ]] || { echo "error: $file not found" >&2; exit 1; }

# Dummy values for every variable compose.yaml requires, as in scripts/check-compose-socket.sh.
placeholders=()
while IFS= read -r name; do
  placeholders+=("$name=unused")
done < <(grep -oE '\$\{[A-Za-z_][A-Za-z0-9_]*:?\?' "$file" | sed -E 's/^\$\{//; s/:?\?$//' | sort -u)

env ${placeholders[@]+"${placeholders[@]}"} docker compose -f "$file" --profile '*' config --format json | python3 -c '
import json, re, sys

# Service -> (joins secrets, {store volume: read-only}). Change it only with ADR 0032 in mind.
allowed = {
    "openbao": (True, {"bao-data": False, "bao-seal": True}),
    "worker": (True, {"cred-worker": True}),
    "secrets-setup": (False, {"bao-seal": False, "cred-worker": False}),
}
store_volume = re.compile(r"(^|/|_)(cred-[^/]*|bao-seal|bao-data)(/|$)")
store_network = re.compile(r"^([^/]*_)?secrets$")

cfg = json.load(sys.stdin)
services = cfg.get("services", {})
net_names = {k: v.get("name", k) for k, v in (cfg.get("networks") or {}).items()}
vol_names = {k: v.get("name", k) for k, v in (cfg.get("volumes") or {}).items()}

def joins(svc):
    return any(store_network.match(k) or store_network.match(net_names.get(k, k)) for k in (svc.get("networks") or {}))

bad = []
for name, svc in services.items():
    on_secrets = joins(svc)
    mode = svc.get("network_mode", "")
    kind, _, other = mode.partition(":")
    if kind in ("service", "container") and other in services and joins(services[other]):
        on_secrets = True
    mounts = {}
    for v in svc.get("volumes", []):
        src = v.get("source", "")
        if store_volume.search(src) or store_volume.search(vol_names.get(src, "")):
            mounts[src] = bool(v.get("read_only"))
    want = allowed.get(name, (False, {}))
    if on_secrets != want[0]:
        verb = "joins" if on_secrets else "does not join"
        bad.append(f"service {name} {verb} the secrets network")
    if mounts != want[1]:
        bad.append(f"service {name} mounts store volumes {mounts}, allowed {want[1]}")
for b in bad:
    print(b, file=sys.stderr)
sys.exit(1 if bad else 0)
'
echo "ok: only openbao and the worker join secrets; only openbao, the worker and secrets-setup mount store volumes"
