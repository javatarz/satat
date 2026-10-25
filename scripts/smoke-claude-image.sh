#!/usr/bin/env bash
# Smoke-test the Claude lane toolchain in the hardened run shape: non-root, read-only root, all caps
# dropped, tmpfs /tmp and /workspace (mount options as in launcher/docker/build.go), a docker volume at /cache (what the launcher mounts, #120).
# usage: scripts/smoke-claude-image.sh [image]   (default satat-claude:dev)
set -euo pipefail

image="${1:-satat-claude:dev}"
vol="satat-smoke-cache-$$"
trap 'docker volume rm -f "$vol" >/dev/null 2>&1 || true' EXIT

docker volume create "$vol" >/dev/null

# A throwaway script run inside the container; single-quoted so the host does not expand it.
# shellcheck disable=SC2016
inner='
set -eu
echo "uid=$(id -u) gid=$(id -g)"
go version
python --version
python -m unittest --help >/dev/null && echo "python -m unittest ok"
java -version 2>&1 | head -n1
mvn -v | head -n1
node -v
corepack --version
if npm ls -g --depth=0 2>/dev/null | grep -q typescript; then echo "typescript: global"; else echo "typescript: not global (repos bring their own)"; fi
# Caches are writable through the /cache volume while the root is read-only.
for d in go-mod go-build pip npm maven gradle corepack; do touch "/cache/$d/.w"; done
echo "cache volume writable"
if touch /usr/local/probe 2>/dev/null; then echo "FAIL: root is writable" >&2; exit 1; fi
echo "root read-only"
cd /workspace
printf "module smoke\n\ngo 1.21\n" > go.mod
printf "package main\nfunc main(){println(\"go build ok\")}\n" > main.go
go run .
'

# "exec" is needed: Docker mounts tmpfs noexec by default, which breaks `go run`/`go test` and Maven's
# jansi native library. The launcher's tmpfs options (launcher/docker/build.go) do not set it yet.
docker run --rm --user 10001:10001 --read-only --cap-drop ALL \
  --security-opt no-new-privileges \
  --tmpfs /tmp:rw,exec,nosuid,nodev,size=256m,mode=1777 \
  --tmpfs /workspace:rw,exec,nosuid,nodev,size=512m,mode=0755,uid=10001,gid=10001 \
  -e HOME=/workspace -v "$vol:/cache" \
  --entrypoint sh "$image" -c "$inner"
