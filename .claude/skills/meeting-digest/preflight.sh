#!/usr/bin/env bash
# The meeting-digest skill's first step: check the run can start before any agent runs.
#
# On success prints key=value lines: root= (the absolute repo root) first, then
# repo-relative paths. Otherwise exits 1 with `error: ...` on stderr.
#
# usage:
#   .claude/skills/meeting-digest/preflight.sh <YYYY-MM-DD>
set -euo pipefail

die() {
  echo "error: $*" >&2
  exit 1
}

[[ $# -eq 1 ]] || die "usage: $0 <YYYY-MM-DD>"
readonly date="$1"
[[ $date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "date must be YYYY-MM-DD, got: $date"

cd "$(dirname "${BASH_SOURCE[0]}")/../../.."

readonly transcript="docs/scratch/calls/$date.txt"
[[ -f $transcript ]] || die "transcript missing: $transcript"

echo "root=$(pwd -P)"
echo "date=$date"
echo "transcript=$transcript"
echo "items=docs/scratch/calls/$date.items.json"
echo "summary=docs/scratch/calls/$date.summary.md"
