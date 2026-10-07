#!/usr/bin/env bash
# Resolve container image tags and digests via the registry HTTP API.
#
# Why not `docker manifest inspect`: it is slow and can hang for minutes on a
# cold or rate-limited registry (and `latest` can block until the tool
# timeout). The registry API answers in well under a second for public images;
# GHCR hands out an anonymous pull token, so no `gh`/package scope is needed.
#
# usage:
#   scripts/image-digest.sh tags   <image>
#   scripts/image-digest.sh digest <image> <tag>
#
# examples:
#   scripts/image-digest.sh tags   ghcr.io/openhands/agent-canvas
#   scripts/image-digest.sh digest ghcr.io/openhands/agent-canvas 1.25.0
set -euo pipefail

readonly TIMEOUT="${IMAGE_DIGEST_TIMEOUT:-15}"
readonly ACCEPT='application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'

die() {
  echo "error: $*" >&2
  exit 1
}

usage() {
  die "usage: $0 {tags <image> | digest <image> <tag>}"
}

command -v curl >/dev/null || die "curl is required"
command -v python3 >/dev/null || die "python3 is required"

cmd="${1:-}"
image="${2:-}"
tag="${3:-}"

if [ -z "$cmd" ] || [ -z "$image" ]; then
  usage
fi

readonly registry="${image%%/*}"
readonly repo="${image#*/}"

if [ "$registry" = "$image" ]; then
  die "image must include a registry, e.g. ghcr.io/owner/name"
fi
if [ "$registry" != "ghcr.io" ]; then
  die "only ghcr.io is supported (got '$registry')"
fi

token="$(curl -fsS --max-time "$TIMEOUT" \
  "https://ghcr.io/token?scope=repository:${repo}:pull" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')" \
  || die "failed to fetch a registry token for ${image}"

case "$cmd" in
  tags)
    curl -fsS --max-time "$TIMEOUT" -H "Authorization: Bearer ${token}" \
      "https://ghcr.io/v2/${repo}/tags/list" \
      | python3 -c 'import json,sys; print("\n".join(sorted(json.load(sys.stdin).get("tags") or [])))'
    ;;
  digest)
    if [ -z "$tag" ]; then
      usage
    fi
    if ! digest="$(curl -fsI --max-time "$TIMEOUT" -H "Authorization: Bearer ${token}" \
      -H "Accept: ${ACCEPT}" "https://ghcr.io/v2/${repo}/manifests/${tag}" \
      | tr -d '\r' | awk 'tolower($1) == "docker-content-digest:" {print $2}')"; then
      die "failed to fetch manifest for ${image}:${tag} (does the tag exist?)"
    fi
    [ -n "$digest" ] || die "no digest returned for ${image}:${tag} (does the tag exist?)"
    printf '%s\n' "$digest"
    ;;
  *)
    usage
    ;;
esac
