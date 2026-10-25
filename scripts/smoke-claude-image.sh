#!/usr/bin/env bash
# Smoke-test the Claude lane toolchain in the hardened run shape: non-root, read-only root, all caps
# dropped, noexec tmpfs on /tmp and /workspace (mount options as in launcher/docker/build.go), and a
# docker volume at /cache (what the launcher mounts, #120). Needs network (mise downloads tools).
# It runs the same checks twice on one volume: the second run must reuse /cache, not redownload.
# usage: scripts/smoke-claude-image.sh [image]   (default satat-claude:dev)
set -euo pipefail

image="${1:-satat-claude:dev}"
vol="satat-smoke-cache-$$"
trap 'docker volume rm -f "$vol" >/dev/null 2>&1 || true' EXIT

docker volume create "$vol" >/dev/null

# A throwaway script run inside the container; single-quoted so the host does not expand it.
# The pins are deliberately not the lane defaults, to prove mise honours a repo's own versions.
# shellcheck disable=SC2016
inner='
set -eu
echo "uid=$(id -u) gid=$(id -g) TMPDIR=$TMPDIR"
if touch /usr/local/probe 2>/dev/null; then echo "FAIL: root is writable" >&2; exit 1; fi
printf "#!/bin/sh\nexit 0\n" > /tmp/x.sh
chmod +x /tmp/x.sh
if /tmp/x.sh 2>/dev/null; then echo "FAIL: /tmp is executable" >&2; exit 1; fi
echo "root read-only, /tmp noexec"

# Lane default names must resolve (no install).
for t in node@lts python@3.13 go@latest java@temurin-21; do echo "default $t -> $(mise latest "$t")"; done

cd /workspace
cat > .mise.toml <<EOF
[tools]
go = "1.24"
node = "20"
python = "3.12"
java = "temurin-17"
EOF
start=$(date +%s)
mise install
echo "MISE_INSTALL_SECONDS=$(( $(date +%s) - start ))"

check() { # name, expected prefix, actual output
  case "$3" in *"$2"*) echo "ok   $1: $3" ;; *) echo "FAIL $1: wanted $2 in: $3" >&2; exit 1 ;; esac
}
check go     "go1.24"   "$(go version)"
check node   "v20."     "$(node -v)"
check python "Python 3.12" "$(python --version)"
check java   "17."      "$(java -version 2>&1 | grep -m1 version)"
check exec   "go1.24"   "$(mise exec -- go version)"

printf "module smoke\n\ngo 1.21\n" > go.mod
printf "package main\nfunc main(){println(\"go run ok\")}\n" > main.go
printf "package main\nimport \"testing\"\nfunc TestX(t *testing.T){}\n" > main_test.go
go run .
go test ./...
mkdir -p py && printf "import unittest\nclass T(unittest.TestCase):\n    def test_x(self):\n        self.assertTrue(True)\n" > py/test_x.py
(cd py && python -m unittest)
node -e "console.log(\"node ok\")"
mkdir -p j && printf "public class H { public static void main(String[] a){ System.out.println(\"javac ok\"); } }\n" > j/H.java
javac -d j j/H.java && java -cp j H
'

run() {
  docker run --rm --user 10001:10001 --read-only --cap-drop ALL \
    --security-opt no-new-privileges \
    --tmpfs /tmp:rw,nosuid,nodev,size=256m,mode=1777 \
    --tmpfs /workspace:rw,nosuid,nodev,size=512m,mode=0755,uid=10001,gid=10001 \
    -e HOME=/workspace -v "$vol:/cache" \
    --entrypoint sh "$image" -c "$inner"
}

echo "=== run 1: cold /cache volume ==="
t0=$(date +%s)
run
echo "RUN1_TOTAL_SECONDS=$(( $(date +%s) - t0 ))"

echo "=== run 2: same /cache volume (must not redownload) ==="
t0=$(date +%s)
run
echo "RUN2_TOTAL_SECONDS=$(( $(date +%s) - t0 ))"
