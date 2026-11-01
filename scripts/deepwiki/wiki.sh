#!/usr/bin/env bash
# Generate or update the DeepWiki wiki in docs/wiki/. Runs scripts/deepwiki/wiki.py
# in a throwaway DeepWiki-Open container (pinned by digest), so it needs only
# Docker, git, jq and OPENROUTER_API_KEY. The same script runs locally (`just wiki`)
# and in CI (.github/workflows/wiki.yml).
#
# usage:
#   scripts/deepwiki/wiki.sh [diff|full]
#
#   diff  (default) rewrite only the pages that cite a file changed since the
#         commit docs/wiki/wiki.json was built from; a full run when there is
#         no wiki.json yet
#   full  plan a new structure and rewrite every page
#
# Optional: WIKI_MODEL, WIKI_EMBED_MODEL (OpenRouter model IDs; unset or empty
# means the defaults below; CI passes its dispatch input, else the repository
# variable of the same name, and its input defaults mirror these) and
# DEEPWIKI_WIKI_PAGE_CONCURRENCY (pages written in parallel).
set -euo pipefail

mode="${1:-diff}"
case "$mode" in diff | full) ;; *) echo "usage: $0 [diff|full]" >&2; exit 2 ;; esac
: "${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is not set}"
export OPENROUTER_API_KEY
export WIKI_MODEL="${WIKI_MODEL:-deepseek/deepseek-v4.1-flash}"
export WIKI_EMBED_MODEL="${WIKI_EMBED_MODEL:-qwen/qwen3-embedding-8b}"
# DeepWiki-Open d92819a (its sha-<ref> tag), resolved with scripts/image-digest.sh.
image=ghcr.io/asyncfuncai/deepwiki-open@sha256:730b72494f407397c896c586af0a31af9671d509af420e6792d834ff094a9c8f

root=$(git rev-parse --show-toplevel)
out="$root/docs/wiki"
mkdir -p "$out"
WIKI_COMMIT=$(git -C "$root" rev-parse HEAD)
export WIKI_COMMIT WIKI_MODE="$mode" WIKI_CHANGED=""

if [ "$mode" = diff ]; then
  base=$(jq -r '.commit // empty' "$out/wiki.json" 2>/dev/null || true)
  if [ -z "$base" ]; then
    echo "==> no docs/wiki/wiki.json to update; running a full regeneration"
    WIKI_MODE=full
  else
    git -C "$root" cat-file -e "$base^{commit}" 2>/dev/null \
      || { echo "error: commit $base is not in this clone (fetch the full history)" >&2; exit 1; }
    # Against the working tree, so a local run also picks up uncommitted edits.
    WIKI_CHANGED=$(git -C "$root" diff --name-only "$base" --)
    if [ -z "$WIKI_CHANGED" ]; then
      echo "==> nothing changed since ${base:0:7}; the wiki is up to date"
      exit 0
    fi
  fi
fi

# As the calling user, so the files written to docs/wiki are not root-owned;
# HOME is in the container, so DeepWiki's index and caches go with it.
docker run --rm --init \
  --user "$(id -u):$(id -g)" \
  -e HOME=/tmp/home -e PYTHONUNBUFFERED=1 -e PYTHONDONTWRITEBYTECODE=1 \
  -e OPENROUTER_API_KEY -e WIKI_MODE -e WIKI_MODEL -e WIKI_EMBED_MODEL -e WIKI_COMMIT -e WIKI_CHANGED \
  -e DEEPWIKI_WIKI_PAGE_CONCURRENCY="${DEEPWIKI_WIKI_PAGE_CONCURRENCY:-2}" \
  -v "$root:/repo:ro" -v "$out:/out" \
  --entrypoint python "$image" /repo/scripts/deepwiki/wiki.py
