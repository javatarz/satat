#!/usr/bin/env bash
# Build the Claude lane image locally and print the digest-pinned reference for satat.yaml.
# Local build now, GHCR publish later (ADR 0026).
set -euo pipefail

GO_IMAGE='golang@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61'     # 1.27.2-bookworm
NODE_IMAGE='node@sha256:c3de60bf2f9dd0ac6370e6117950ff62d6e339527e7472301c9c78a017978392'     # 22-bookworm-slim
# scripts/image-digest.sh is GHCR-only, so these Docker Hub index digests came from the Docker Hub
# registry API (the same manifest HEAD it does, with the index Accept header). The method reproduces
# the GO_IMAGE digest above.
JDK_IMAGE='eclipse-temurin@sha256:742daacb9316be4b6fc60258f6e4ac2a11882f6c597373c92290a57bfad0d2e0'  # 21-jdk-jammy
MAVEN_IMAGE='maven@sha256:6fdc855a6ed81d288ca7ca37ac6ff5e9308b612485c0801d70b25a858c83d237'          # 3.9.11-eclipse-temurin-21
CLAUDE_CODE_VERSION='2.1.301'                                                                  # the #102 spike version

cd "$(dirname "$0")/.."
docker build \
  --build-arg "GO_IMAGE=$GO_IMAGE" \
  --build-arg "NODE_IMAGE=$NODE_IMAGE" \
  --build-arg "JDK_IMAGE=$JDK_IMAGE" \
  --build-arg "MAVEN_IMAGE=$MAVEN_IMAGE" \
  --build-arg "CLAUDE_CODE_VERSION=$CLAUDE_CODE_VERSION" \
  -t satat-claude:dev \
  -f images/claude/Dockerfile .

id="$(docker image inspect satat-claude:dev --format '{{.Id}}')"
echo
echo "image: satat-claude@${id}"
