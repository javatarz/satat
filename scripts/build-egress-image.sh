#!/usr/bin/env bash
# Build the lane egress proxy image locally and print the digest-pinned reference for satat.yaml
# (egress.image). Local build now, GHCR publish later (ADR 0026).
set -euo pipefail

GO_IMAGE='golang@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61'     # 1.27.2-bookworm

cd "$(dirname "$0")/.."
docker build \
  --build-arg "GO_IMAGE=$GO_IMAGE" \
  -t satat-egress:dev \
  -f images/egress/Dockerfile .

id="$(docker image inspect satat-egress:dev --format '{{.Id}}')"
echo
echo "image: satat-egress@${id}"
