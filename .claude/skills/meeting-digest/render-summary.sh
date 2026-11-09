#!/usr/bin/env bash
# Render the local, unpublished summary of a call's items, grouped by type, then the preview
# of what publishing would send: the roadmap edits, the call record, the PR draft and the issue
# drafts (with the assignee each would get from the speaker map). Its last
# line is the publish command for the owner to run outside Claude Code.
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
command -v jq >/dev/null || die "jq is required"

cd "$(dirname "${BASH_SOURCE[0]}")/../../.."

readonly skill_dir=".claude/skills/meeting-digest"
readonly items="docs/scratch/calls/$date.items.json"
readonly summary="docs/scratch/calls/$date.summary.md"
readonly record="docs/calls/$date.md"
readonly outbox="docs/scratch/calls/$date.outbox.json"
readonly speakers="${DIGEST_SPEAKERS:-$skill_dir/speakers.json}"
[[ -f $items ]] || die "items missing: $items"
[[ -f $record ]] || die "call record missing: $record"
jq -e 'type == "object"' "$speakers" >/dev/null 2>&1 || die "speaker map is not a JSON object: $speakers"
repo="$(origin_repo)"
readonly repo
[[ $repo =~ ^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$ ]] || die "cannot read owner/name from the origin URL"

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
roadmap_diff="$(git diff --no-color -U0 -- docs/roadmap)"

{
  printf '%s\n' "$markdown"
  printf '\n## Roadmap edits\n\n~~~~diff\n'
  if [[ -n $roadmap_diff ]]; then
    printf '%s\n' "$roadmap_diff"
  fi
  printf '~~~~\n\n## Call record\n\n~~~~markdown\n'
  cat "$record"
  printf '~~~~\n'
  if [[ -f $outbox ]]; then
    printf '\n'
    jq -r -L "$skill_dir" --arg date "$date" 'include "bodies"; pr_preview($date)' "$outbox"
    printf '\n'
    jq -r -L "$skill_dir" --arg date "$date" --slurpfile map "$speakers" --slurpfile items "$items" \
      'include "bodies"; issue_preview($date; $map; $items)' "$outbox"
  fi
  printf '\n## Publish\n\n'
  printf 'Publish (run in a terminal outside Claude Code): scripts/publish-digest.sh digest %s %s\n' "$repo" "$date"
} >"$summary"
echo "$summary"
