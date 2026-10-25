#!/usr/bin/env bash
# Smoke-test the Claude lane toolchain in the hardened run shape: non-root, read-only root, all caps
# dropped, noexec tmpfs on /tmp and /workspace (mount options as in launcher/docker/build.go), and a
# docker volume at /cache (what the launcher mounts, #120).
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
echo "TMPDIR=$TMPDIR"
go version
python --version
java -version 2>&1 | head -n1
mvn -v | head -n1
node -v
corepack --version
if npm ls -g --depth=0 2>/dev/null | grep -q typescript; then echo "typescript: global"; else echo "typescript: not global (repos bring their own)"; fi
# Caches are writable through the /cache volume while the root is read-only.
for d in go-mod go-build pip npm maven gradle corepack tmp; do touch "/cache/$d/.w"; done
echo "cache volume writable"
if touch /usr/local/probe 2>/dev/null; then echo "FAIL: root is writable" >&2; exit 1; fi
echo "root read-only"
# Prove the tmpfs mounts really are noexec, so the checks below are meaningful.
printf "#!/bin/sh\nexit 0\n" > /tmp/x.sh
chmod +x /tmp/x.sh
if /tmp/x.sh 2>/dev/null; then echo "FAIL: /tmp is executable" >&2; exit 1; fi
echo "/tmp noexec confirmed"

cd /workspace
printf "module smoke\n\ngo 1.21\n" > go.mod
printf "package main\nfunc main(){println(\"go run ok\")}\n" > main.go
printf "package main\nimport \"testing\"\nfunc TestX(t *testing.T){}\n" > main_test.go
go run .
go test ./...

mkdir -p py && printf "import unittest\nclass T(unittest.TestCase):\n    def test_x(self):\n        self.assertTrue(True)\n" > py/test_x.py
(cd py && python -m unittest -v)

node -e "console.log(\"node ok\")"

# Offline Maven: a tiny pom with no plugins to resolve (the local repo is empty), then javac.
printf "<project xmlns=\"http://maven.apache.org/POM/4.0.0\"><modelVersion>4.0.0</modelVersion><groupId>s</groupId><artifactId>s</artifactId><version>1</version><packaging>pom</packaging></project>\n" > pom.xml
mvn -o -q validate && echo "mvn -o validate ok"
mkdir -p j && printf "public class H { public static void main(String[] a){ System.out.println(\"javac ok\"); } }\n" > j/H.java
javac -d j j/H.java && java -cp j H
'

docker run --rm --user 10001:10001 --read-only --cap-drop ALL \
  --security-opt no-new-privileges \
  --tmpfs /tmp:rw,nosuid,nodev,size=256m,mode=1777 \
  --tmpfs /workspace:rw,nosuid,nodev,size=512m,mode=0755,uid=10001,gid=10001 \
  -e HOME=/workspace -v "$vol:/cache" \
  --entrypoint sh "$image" -c "$inner"
