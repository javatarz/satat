#!/usr/bin/env bash
# Build the Claude lane image, push it to a registry, and print the manifest-digest reference for
# satat.yaml (ADR 0026). Lane images are registry references pinned by manifest digest.
#
#   SATAT_REGISTRY=localhost:5000 scripts/build-claude-image.sh
#   SATAT_REGISTRY=ghcr.io/javatarz scripts/build-claude-image.sh   (after `docker login ghcr.io`)
set -euo pipefail

GO_IMAGE='golang@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61'     # 1.27.2-bookworm
NODE_IMAGE='node@sha256:c3de60bf2f9dd0ac6370e6117950ff62d6e339527e7472301c9c78a017978392'     # 22-bookworm-slim
CLAUDE_CODE_VERSION='2.1.301'                                                                  # the #102 spike version

if [ -z "${SATAT_REGISTRY:-}" ]; then
  echo "set SATAT_REGISTRY (for example localhost:5000 or ghcr.io/javatarz)" >&2
  exit 1
fi

cd "$(dirname "$0")/.."
ref="$SATAT_REGISTRY/satat-claude:dev"
docker build \
  --build-arg "GO_IMAGE=$GO_IMAGE" \
  --build-arg "NODE_IMAGE=$NODE_IMAGE" \
  --build-arg "CLAUDE_CODE_VERSION=$CLAUDE_CODE_VERSION" \
  -t "$ref" \
  -f images/claude/Dockerfile .
docker push "$ref"

# After a push, RepoDigests holds the registry's manifest digest. Keep only this registry's entry:
# the image may also carry digests from other repositories.
digest="$(docker image inspect "$ref" --format '{{range .RepoDigests}}{{println .}}{{end}}' \
  | awk -v p="$SATAT_REGISTRY/satat-claude@" 'index($0, p) == 1 { print; exit }')"
if [ -z "$digest" ]; then
  echo "no $SATAT_REGISTRY/satat-claude@ digest in RepoDigests after the push" >&2
  exit 1
fi

echo
echo "image: $digest"
