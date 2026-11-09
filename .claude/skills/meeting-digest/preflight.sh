#!/usr/bin/env bash
# The meeting-digest skill's first step: check the run can start before any agent runs, and
# snapshot the target repo's open issues so no agent queries GitHub.
#
# On success prints key=value lines: root= (the absolute repo root) first, then
# repo-relative paths. Otherwise exits 1 with `error: ...` on stderr. Calls
# "${DIGEST_GH:-gh}", so a proof can point it at a fake gh.
#
# usage:
#   .claude/skills/meeting-digest/preflight.sh <YYYY-MM-DD>
set -euo pipefail

die() {
  echo "error: $*" >&2
  exit 1
}

origin_repo() {
  local url owner_path name
  url="$(git config --get remote.origin.url)" || die "no origin remote"
  url="${url%/}"
  url="${url%.git}"
  name="${url##*/}"
  owner_path="${url%/*}"
  echo "${owner_path##*[/:]}/$name"
}

[[ $# -eq 1 ]] || die "usage: $0 <YYYY-MM-DD>"
readonly date="$1"
[[ $date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "date must be YYYY-MM-DD, got: $date"

cd "$(dirname "${BASH_SOURCE[0]}")/../../.."

readonly record="docs/calls/$date.md"
[[ ! -e $record ]] || die "$record exists; a date holds one digest"

readonly transcript="docs/scratch/calls/$date.txt"
[[ -f $transcript ]] || die "transcript missing: $transcript"

repo="$(origin_repo)"
readonly repo
[[ $repo =~ ^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$ ]] || die "cannot read owner/name from the origin URL"

readonly issues="docs/scratch/calls/$date.issues.json"
if ! "${DIGEST_GH:-gh}" issue list --repo "$repo" --state open --limit 500 \
  --json number,title,body,labels,assignees,url >"$issues"; then
  rm -f "$issues"
  die "could not snapshot the open issues of $repo"
fi

echo "root=$(pwd -P)"
echo "date=$date"
echo "transcript=$transcript"
echo "items=docs/scratch/calls/$date.items.json"
echo "summary=docs/scratch/calls/$date.summary.md"
echo "repo=$repo"
echo "issues=$issues"
echo "record=$record"
echo "outbox=docs/scratch/calls/$date.outbox.json"
