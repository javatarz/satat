#!/usr/bin/env bash
# Frontier query for a wayfinder map (see docs/agents/issue-tracker.md).
#
# Lists the map's open sub-issues in map order, dropping any that has an open
# blocker (GitHub native issue dependencies) or an assignee. The first row is
# the next thing to pick up.
#
# usage:
#   scripts/next-issue.sh [map-number] [--all]
#
#   map-number  defaults to the newest open issue labelled wayfinder:map
#   --all       also print blocked and claimed children, marked as such
set -euo pipefail

command -v gh >/dev/null || { echo "gh is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

map=""
show_all=0
for arg in "$@"; do
  case "$arg" in
    --all) show_all=1 ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) map="$arg" ;;
  esac
done

repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)

if [ -z "$map" ]; then
  map=$(gh issue list --label wayfinder:map --state open --limit 1 --json number --jq '.[0].number // empty')
  [ -n "$map" ] || { echo "no open wayfinder:map issue found; pass a number" >&2; exit 1; }
fi

# Sub-issues come back in map order. issue_dependencies_summary.blocked_by
# counts open blockers only, which is exactly the live gate.
rows=$(gh api --paginate "repos/${repo}/issues/${map}/sub_issues?per_page=100" \
  --jq '.[] | select(.state == "open") | [
      .number,
      (.issue_dependencies_summary.blocked_by // 0),
      ((.assignees // []) | length),
      ([.labels[].name] | join(",")),
      .title
    ] | @tsv') || { echo "could not list sub-issues of #${map}" >&2; exit 1; }

printf '%s\n' "$rows" \
| awk -F'\t' -v all="$show_all" -v OFS='\t' '
  NF == 0 { next }
  {
    n=$1; blocked=$2; claimed=$3; labels=$4; title=$5
    state = (blocked > 0) ? "blocked" : (claimed > 0) ? "claimed" : "ready"
    if (state == "ready" || all) {
      printf "%-8s #%-4s %s\n", state, n, title
      if (labels != "") printf "%-8s       labels: %s\n", "", labels
    }
  }'
