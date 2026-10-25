#!/usr/bin/env bash
# Build the Claude lane image locally and print the digest-pinned reference for satat.yaml.
# Local build now, GHCR publish later (ADR 0026).
set -euo pipefail

GO_IMAGE='golang@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61'     # 1.27.2-bookworm
NODE_IMAGE='node@sha256:c3de60bf2f9dd0ac6370e6117950ff62d6e339527e7472301c9c78a017978392'     # 22-bookworm-slim
# Resolved with: scripts/image-digest.sh digest ghcr.io/jdx/mise latest   (binary reports 2026.10.19)
MISE_IMAGE='ghcr.io/jdx/mise@sha256:6bd2515bf27389f537921fc8c3e5a6579f15cf889a63a8432dc542670174e50a'  # latest = 2026.10.19
CLAUDE_CODE_VERSION='2.1.301'                                                                  # the #102 spike version

cd "$(dirname "$0")/.."
docker build \
  --build-arg "GO_IMAGE=$GO_IMAGE" \
  --build-arg "NODE_IMAGE=$NODE_IMAGE" \
  --build-arg "MISE_IMAGE=$MISE_IMAGE" \
  --build-arg "CLAUDE_CODE_VERSION=$CLAUDE_CODE_VERSION" \
  -t satat-claude:dev \
  -f images/claude/Dockerfile .

id="$(docker image inspect satat-claude:dev --format '{{.Id}}')"
echo
echo "image: satat-claude@${id}"
