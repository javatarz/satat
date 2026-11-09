#!/usr/bin/env bash
# Render the local, unpublished summary of a call's items, grouped by type.
#
# The quote fields are dropped before anything else reads the items, so the summary (and the
# session that shows it) never carries call wording. Prints the summary path.
#
# usage:
#   .claude/skills/meeting-digest/render-summary.sh <YYYY-MM-DD>
set -euo pipefail

die() {
  echo "error: $*" >&2
  exit 1
}

[[ $# -eq 1 ]] || die "usage: $0 <YYYY-MM-DD>"
readonly date="$1"
[[ $date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "date must be YYYY-MM-DD, got: $date"
command -v jq >/dev/null || die "jq is required"

cd "$(dirname "${BASH_SOURCE[0]}")/../../.."

readonly items="docs/scratch/calls/$date.items.json"
readonly summary="docs/scratch/calls/$date.summary.md"
[[ -f $items ]] || die "items missing: $items"

render="$(cat <<'JQ'
def one_line: gsub("\\s+"; " ");
def item_line:
  "- \(.summary | one_line) — \(.speaker), \(.ts), confidence \(.confidence)";
def detail_line:
  if .type == "action" then "  - owner: \(.owner // "none") · due: \(.due // "none")"
  elif .type == "deferral" then "  - trigger: \(.trigger // "none" | one_line)"
  elif .type == "decision" then "  - agreed by: \(.agreement.speaker)"
  elif .type == "contradiction" then "  - contradicts: \(.contradicts | one_line)"
  else empty end;
def section($heading; $type):
  "", "## \($heading)", "",
  ([.items[] | select(.type == $type)] as $group
    | if ($group | length) == 0 then "- none" else ($group[] | item_line, detail_line) end);

"# Call \($date) — local summary (not published)",
section("Decisions"; "decision"),
section("Directions"; "direction"),
section("Actions"; "action"),
section("Open questions"; "question"),
section("Deferrals"; "deferral"),
section("Contradictions"; "contradiction"),
"", "## Topics with no outcome", "",
([.topics[] | select((.items | length) == 0)] as $silent
  | if ($silent | length) == 0 then "- none" else ($silent[] | "- \(.title | one_line) — \(.ts)") end)
JQ
)"
readonly render

markdown="$(jq 'del(.items[].quote, .items[].agreement.quote)' "$items" | jq -r --arg date "$date" "$render")"
printf '%s\n' "$markdown" >"$summary"
echo "$summary"
