#!/usr/bin/env bash
# Publish a meeting digest as a draft PR. The owner runs it in a terminal outside Claude Code.
#
# In order: checks the target is origin, refuses any working-tree change outside the exact
# allowlist (docs/calls/<date>.md, and edits to tracked docs/roadmap/<name>.md themes other
# than README.md), refuses git attributes or binary content on those paths (both can hide a
# diff), runs scripts/check-digest.sh, then renders the PR body and the issue drafts (title and
# body per drafted action, from the outbox) and runs gitleaks over the files it will commit and
# everything it will send. Only then does it commit those paths on digest/<date>, push that
# branch fast-forward only, file the drafted issues with `gh issue create` (skipping an issue
# whose marker already exists) and fill each action's `- issue:` slot in the record, and open a
# draft PR, or reuse its open PR.
#
# --dry-run stops after gitleaks and prints the files to commit, the PR draft and the issue
# drafts. It makes no gh call.
#
# Agenda mode publishes docs/calls/agenda-<date>.md the same way, on agenda/<date>: its
# allowlist is docs/calls/agenda-*.md only, it commits only that date's agenda, runs no
# check-digest, files no issue and labels nothing, and gitleaks scans the agenda and the PR body.
#
# The speaker map is DIGEST_SPEAKERS, else .claude/skills/meeting-digest/speakers.json. An
# override is accepted only for a file git tracks with no change. PUBLISH_DIGEST_STOP_AFTER=<n>
# (tests only) exits 1 after filing n issues.
#
# Exits 0 when done, 1 with `refused: <reason>` on stderr, 2 on bad usage. Calls
# "${DIGEST_GH:-gh}" and "${DIGEST_GITLEAKS:-gitleaks}".
#
# usage:
#   scripts/publish-digest.sh [--dry-run] digest <owner/repo> <YYYY-MM-DD>
#   scripts/publish-digest.sh [--dry-run] agenda <owner/repo> <YYYY-MM-DD>
set -euo pipefail

readonly skill_dir=".claude/skills/meeting-digest"

die() {
  echo "refused: $*" >&2
  exit 1
}

usage() {
  echo "usage: $0 [--dry-run] digest|agenda <owner/repo> <YYYY-MM-DD>" >&2
  exit 2
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

is_dry_run=0
if [[ ${1:-} == --dry-run ]]; then
  is_dry_run=1
  shift
fi
[[ $# -eq 3 ]] || usage
readonly mode="$1" repo="$2" date="$3"
[[ $mode == digest || $mode == agenda ]] || usage
[[ $repo =~ ^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$ ]] || usage
[[ $date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || usage

for tool in git jq; do
  command -v "$tool" >/dev/null || die "$tool is required"
done
cd "$(dirname "${BASH_SOURCE[0]}")/.."

readonly record="docs/calls/$date.md"
readonly agenda="docs/calls/agenda-$date.md"
readonly outbox="docs/scratch/calls/$date.outbox.json"
if [[ $mode == agenda ]]; then
  readonly outgoing="docs/scratch/calls/agenda-$date.outgoing"
  readonly branch="agenda/$date"
  readonly title="Agenda for call $date"
else
  readonly outgoing="docs/scratch/calls/$date.outgoing"
  readonly branch="digest/$date"
  readonly title="Digest call $date"
fi
readonly items_file="docs/scratch/calls/$date.items.json"
readonly issues_snapshot="docs/scratch/calls/$date.issues.json"
readonly transcript="docs/scratch/calls/$date.txt"
readonly speakers="${DIGEST_SPEAKERS:-$skill_dir/speakers.json}"
readonly gh_cli="${DIGEST_GH:-gh}"
readonly stop_after="${PUBLISH_DIGEST_STOP_AFTER:-}"
[[ -z $stop_after || $stop_after =~ ^[1-9][0-9]*$ ]] || die "PUBLISH_DIGEST_STOP_AFTER must be a positive integer"

origin="$(origin_repo)"
readonly origin
[[ $repo == "$origin" ]] || die "$repo is not origin ($origin)"

is_allowed() {
  local status="$1" path="$2"
  if [[ $mode == agenda ]]; then
    [[ $path =~ ^docs/calls/agenda-[^/]*\.md$ ]] && [[ $status == '??' || $status =~ ^[\ AM][\ AM]$ ]]
  elif [[ $path == "$record" ]]; then
    [[ $status == '??' || $status =~ ^[\ AM][\ AM]$ ]]
  elif [[ $path =~ ^docs/roadmap/[^/]+\.md$ && $path != docs/roadmap/README.md ]]; then
    [[ $status =~ ^[\ M][\ M]$ ]] && git cat-file -e "HEAD:$path" 2>/dev/null
  else
    return 1
  fi
}

is_commit_path() {
  [[ $mode == digest || $1 == "$agenda" ]]
}

mkdir -p docs/scratch/calls
status_file="$(mktemp docs/scratch/calls/status.XXXXXX)" || die "cannot create a status file"
readonly status_file
trap 'rm -f "$status_file"' EXIT
git status --porcelain=v1 -z --untracked-files=all >"$status_file" || die "git status failed"

commit_paths=()
is_refused=0
while IFS= read -r -d '' entry; do
  status="${entry:0:2}"
  path="${entry:3}"
  if [[ $status == *R* || $status == *C* ]]; then
    IFS= read -r -d '' source_path || true
    echo "refused: path not allowed: $status $source_path" >&2
    echo "refused: path not allowed: $status $path" >&2
    is_refused=1
  elif is_allowed "$status" "$path"; then
    if is_commit_path "$path"; then
      commit_paths+=("$path")
    fi
  else
    echo "refused: path not allowed: $status $path" >&2
    is_refused=1
  fi
done <"$status_file"
[[ $is_refused -eq 0 ]] || exit 1
if [[ $mode == agenda ]]; then
  [[ -f $agenda ]] || die "$agenda missing"
else
  [[ -f $outbox ]] || die "$outbox missing"
fi

if [[ ${#commit_paths[@]} -gt 0 ]]; then
  commit_path_attributes="$(git check-attr -a -- "${commit_paths[@]}")" || die "git check-attr failed"
  [[ -z $commit_path_attributes ]] || die "git attributes set on a commit path"
  for path in "${commit_paths[@]}"; do
    nul_count="$(LC_ALL=C tr -dc '\000' <"$path" | wc -c)" || die "cannot read $path"
    [[ $nul_count -eq 0 ]] || die "binary content in a commit path: $path"
  done
  commit_path_numstat="$(git diff --numstat HEAD -- "${commit_paths[@]}")" || die "git diff --numstat failed"
  while IFS=$'\t' read -r added deleted path; do
    if [[ $added == - && $deleted == - ]]; then
      die "binary content in a commit path: $path"
    fi
  done <<<"$commit_path_numstat"
fi

if [[ $mode == digest ]] && ! check_output="$(scripts/check-digest.sh "$date")"; then
  grep '^fail: ' <<<"$check_output" >&2 || true
  die "check-digest failed"
fi

# D57 7a. Every check that can refuse a draft runs here, before any gh call.
refuse_if() {
  local message="$1"
  [[ -z $message ]] || die "$message"
}

# Prints "<id>\t<mark kind>\t<number of issue lines>\t<text after "- issue:">" per action block.
record_action_blocks() {
  awk '
    function flush() { if (id != "") print id "\t" kind "\t" slots "\t" slot; id = "" }
    /^##/ {
      flush()
      if ($0 ~ /^### i[0-9]+ · action · /) { id = $2; kind = "none"; slots = 0; slot = "" }
      next
    }
    id == "" { next }
    /^- issue:/ { slots++; slot = substr($0, 9); next }
    /^- mark: / && kind == "none" {
      mark = substr($0, 9)
      if (mark == "new") kind = "new"
      else if (index(mark, "conflict — ") == 1) kind = "conflict"
      else if (index(mark, "update — ") == 1) kind = "update"
      else if (index(mark, "no change — ") == 1) kind = "no change"
      else kind = "unknown"
    }
    END { flush() }
  ' "$record"
}

check_speaker_map() {
  if [[ -n ${DIGEST_SPEAKERS:-} ]]; then
    if ! git ls-files --error-unmatch -- "$speakers" >/dev/null 2>&1 ||
      ! git diff --quiet -- "$speakers" || ! git diff --cached --quiet -- "$speakers"; then
      die "speaker map override is not a tracked, unchanged file: $speakers"
    fi
  fi
  jq -e 'type == "object"' "$speakers" >/dev/null 2>&1 || die "speaker map is not a JSON object: $speakers"
}

check_drafts() {
  local message drafted_items new_or_conflict_items block_id kind slot_count slot_text
  local block_lines duplicate_blocks slot_number
  local -r filled_slot=' #[0-9]+'

  [[ -f $items_file ]] || die "$items_file missing"
  [[ -f $issues_snapshot ]] || die "$issues_snapshot missing"
  [[ -f $transcript ]] || die "$transcript missing"
  [[ -f $record ]] || die "$record missing"
  jq -e '(.issues // []) | type == "array" and all(.[]; type == "object")' "$outbox" >/dev/null 2>&1 ||
    die "drafts do not match the new or conflict actions"

  message="$(jq -r --slurpfile items "$items_file" '
    def printable: tostring | gsub("[^ -~]"; "?");
    def is_action($id): any($items[0].items[]; .id == $id and .type == "action");
    first((.issues // [])[]
      | select((.item | type) != "string"
          or ((.item | test("\\Ai[0-9]+\\z")) | not)
          or (is_action(.item) | not))
      | "draft \(.item | printable): invalid item id") // empty' "$outbox")" || die "cannot read $outbox"
  refuse_if "$message"

  drafted_items="$(jq -r '(.issues // [])[].item' "$outbox" | sort)"
  new_or_conflict_items="$(record_action_blocks | awk -F '\t' '$2 == "new" || $2 == "conflict" { print $1 }' | sort -u)"
  [[ -z $(printf '%s\n' "$drafted_items" | uniq -d) && "$drafted_items" == "$new_or_conflict_items" ]] ||
    die "drafts do not match the new or conflict actions"

  message="$(jq -r '
    first((.issues // [])[]
      | select((.title | type) != "string" or (.title | test("[\\r\\n]")) or (.title | length) < 1 or (.title | length) > 80)
      | "draft \(.item): bad title") // empty' "$outbox")"
  refuse_if "$message"

  message="$(jq -r '
    def forbidden: type != "string" or contains("<!--") or contains("digest:") or contains("~~~~");
    first((.issues // [])[] | select((.title | forbidden) or (.body | forbidden))
      | "draft \(.item): forbidden text") // empty' "$outbox")"
  refuse_if "$message"

  block_lines="$(record_action_blocks)"
  duplicate_blocks="$(printf '%s\n' "$block_lines" | cut -f1 | sort | uniq -d)"
  while IFS=$'\t' read -r block_id kind slot_count slot_text; do
    [[ -n $block_id ]] || continue
    case "$kind" in
      new | conflict)
        if grep -Fxq -- "$block_id" <<<"$duplicate_blocks" || [[ $slot_count -ne 1 ]] ||
          [[ -n $slot_text && ! $slot_text =~ ^$filled_slot$ ]]; then
          die "slot $block_id: bad shape"
        fi
        ;;
      update | "no change")
        slot_number="${slot_text# \#}"
        if grep -Fxq -- "$block_id" <<<"$duplicate_blocks" || [[ $slot_count -ne 1 ]] ||
          [[ ! $slot_text =~ ^$filled_slot$ ]] ||
          ! jq -e --argjson number "$slot_number" 'any(.[]; .number == $number)' "$issues_snapshot" >/dev/null; then
          die "slot $block_id: bad shape"
        fi
        ;;
      *) die "slot $block_id: bad shape" ;;
    esac
  done <<<"$block_lines"
}

render_drafts() {
  local item
  while IFS= read -r item; do
    jq -j --arg item "$item" '.issues[] | select(.item == $item) | .title' "$outbox" >"$outgoing/issue-$item.title"
    jq -r -L "$skill_dir" --arg date "$date" --arg item "$item" --slurpfile items "$items_file" \
      'include "bodies"; .issues[] | select(.item == $item) | issue_body($date; $items)' "$outbox" >"$outgoing/issue-$item.md"
  done < <(jq -r '(.issues // [])[].item' "$outbox")
  local rendered=("$outgoing"/issue-*.md "$outgoing"/issue-*.title)
  if [[ -e ${rendered[0]} ]]; then
    if ! check_output="$(scripts/check-digest.sh --items "$items_file" --transcript "$transcript" "${rendered[@]}")"; then
      grep '^fail: ' <<<"$check_output" >&2 || true
      die "check-digest failed"
    fi
  fi
}

readonly gitleaks="${DIGEST_GITLEAKS:-gitleaks}"
command -v "$gitleaks" >/dev/null || die "gitleaks not found"
rm -rf "$outgoing" && mkdir -p "$outgoing"
# Each scan copy starts with a fixed text line: gitleaks skips a file whose first bytes look like
# a known file type (a %PDF- prefix, say), which would hide a secret in a commit path.
for path in ${commit_paths[@]+"${commit_paths[@]}"}; do
  mkdir -p "$outgoing/$(dirname "$path")"
  { echo 'gitleaks scan copy'; cat -- "$path"; } >"$outgoing/$path"
done
if [[ $mode == agenda ]]; then
  jq -n -r -L "$skill_dir" --arg agenda "$agenda" \
    'include "bodies"; "\(banner)\n\nThe agenda for the next call: `\($agenda)`."' >"$outgoing/pr-body.md"
else
  jq -r -L "$skill_dir" --arg date "$date" 'include "bodies"; pr_body($date)' "$outbox" >"$outgoing/pr-body.md"
  check_speaker_map
  check_drafts
  render_drafts
fi
if ! env -u GITLEAKS_CONFIG -u GITLEAKS_CONFIG_TOML "$gitleaks" dir --no-banner --redact \
  --ignore-gitleaks-allow --gitleaks-ignore-path "$outgoing" "$outgoing" >&2; then
  die "gitleaks found a secret"
fi

if [[ $is_dry_run -eq 1 ]]; then
  printf '## Files to commit\n\n'
  if [[ ${#commit_paths[@]} -gt 0 ]]; then
    printf '%s\n' "${commit_paths[@]}"
  fi
  printf '\n'
  if [[ $mode == agenda ]]; then
    printf '## PR draft\n\nTitle: %s\n\n~~~~markdown\n%s\n~~~~\n\n' "$title" "$(cat -- "$outgoing/pr-body.md")"
  else
    jq -r -L "$skill_dir" --arg date "$date" 'include "bodies"; pr_preview($date), ""' "$outbox"
    jq -r -L "$skill_dir" --arg date "$date" --slurpfile map "$speakers" --slurpfile items "$items_file" \
      'include "bodies"; issue_preview($date; $map; $items), ""' "$outbox"
  fi
  exit 0
fi

git fetch origin || die "git fetch origin failed"
default_ref="$(git symbolic-ref --short refs/remotes/origin/HEAD)" || die "origin/HEAD is not set"
readonly default_branch="${default_ref#origin/}"

if git show-ref --verify --quiet "refs/heads/$branch"; then
  branch_tip="refs/heads/$branch"
elif git show-ref --verify --quiet "refs/remotes/origin/$branch"; then
  branch_tip="refs/remotes/origin/$branch"
else
  [[ "$(git rev-parse HEAD)" == "$(git rev-parse "$default_ref")" ]] ||
    die "HEAD is not $default_ref; switch to it before the first publish of $date"
  branch_tip="HEAD"
fi
if git show-ref --verify --quiet "refs/remotes/origin/$branch" &&
  ! git merge-base --is-ancestor "refs/remotes/origin/$branch" "$branch_tip"; then
  die "origin/$branch has commits the local branch lacks; merge them by hand"
fi

if git show-ref --verify --quiet "refs/heads/$branch"; then
  [[ "$(git branch --show-current)" == "$branch" ]] || git switch -q "$branch" ||
    die "cannot switch to $branch"
elif git show-ref --verify --quiet "refs/remotes/origin/$branch"; then
  git switch -q --track "origin/$branch" || die "cannot switch to $branch"
else
  git switch -q -c "$branch" || die "cannot create $branch"
fi

# D57 7b, the first gh calls. Refuses before the first create when a slot or a marker conflicts.
fill_slot() {
  local item="$1" number="$2" updated
  updated="$(mktemp docs/scratch/calls/record.XXXXXX)" || die "cannot create a temp file"
  awk -v id="$item" -v number="$number" '
    index($0, "### " id " · ") == 1 { on = 1; print; next }
    /^##/ { on = 0 }
    on && /^- issue:/ { print "- issue: #" number; on = 0; next }
    { print }
  ' "$record" >"$updated" || die "cannot fill the slot of $item"
  cat -- "$updated" >"$record"
  rm -f "$updated"
}

file_issues() {
  local me listing filed_numbers item slot marker found found_count handle created number filed_count=0
  local assignee_args
  declare -A slot_of=()
  declare -A number_of=()
  local block_id kind slot_count slot_text

  while IFS=$'\t' read -r block_id kind slot_count slot_text; do
    slot_of["$block_id"]="${slot_text# \#}"
  done < <(record_action_blocks)

  me="$("$gh_cli" api user --jq .login)" || die "gh api user failed"
  listing="$("$gh_cli" api --paginate "repos/$repo/issues?state=all&per_page=100")" ||
    die "gh api issues failed"
  filed_numbers="$(jq -s -r --arg me "$me" '
    [.[][] | select((has("pull_request") | not) and .user.login == $me)
      | {number, last: ((.body // "") | rtrimstr("\n") | split("\n") | last)}]
    | .[] | "\(.number)\t\(.last)"' <<<"$listing")" || die "cannot read the issue list"

  while IFS= read -r item; do
    marker="<!-- digest:$date:$item -->"
    found="$(awk -F '\t' -v marker="$marker" '$2 == marker { print $1 }' <<<"$filed_numbers")"
    found_count="$(grep -c . <<<"$found" || true)"
    slot="${slot_of[$item]}"
    [[ $found_count -le 1 ]] || die "slot $item: conflicts with filed issue"
    [[ -z $slot || -z $found || $slot == "$found" ]] || die "slot $item: conflicts with filed issue"
    number_of["$item"]="$found"
  done < <(jq -r '(.issues // [])[].item' "$outbox")

  while IFS= read -r item; do
    if [[ -n ${number_of[$item]} ]]; then
      [[ -n ${slot_of[$item]} ]] || fill_slot "$item" "${number_of[$item]}"
      continue
    fi
    [[ -z ${slot_of[$item]} ]] || continue
    assignee_args=()
    handle="$(jq -r -L "$skill_dir" --arg item "$item" --slurpfile map "$speakers" --slurpfile items "$items_file" \
      'include "bodies"; .issues[] | select(.item == $item) | assignee($map; $items) // empty' "$outbox")"
    if [[ -n $handle ]]; then
      if "$gh_cli" api "repos/$repo/assignees/$handle" >/dev/null 2>&1; then
        assignee_args=(--assignee "$handle")
      else
        echo "warning: $handle cannot be assigned on $repo; filing $item unassigned" >&2
      fi
    fi
    created="$("$gh_cli" issue create --repo "$repo" --title "$(cat -- "$outgoing/issue-$item.title")" \
      --body-file "$outgoing/issue-$item.md" --label needs-triage ${assignee_args[@]+"${assignee_args[@]}"})" ||
      die "gh issue create failed for $item"
    number="${created##*/}"
    [[ $number =~ ^[0-9]+$ ]] || die "cannot read the issue number for $item"
    fill_slot "$item" "$number"
    filed_count=$((filed_count + 1))
    if [[ -n $stop_after && $filed_count -ge $stop_after ]]; then
      die "stopped after $filed_count issues (PUBLISH_DIGEST_STOP_AFTER)"
    fi
  done < <(jq -r '(.issues // [])[].item' "$outbox")
}

[[ $mode == agenda ]] || file_issues

if [[ ${#commit_paths[@]} -gt 0 ]]; then
  git add -- "${commit_paths[@]}"
  if ! git diff --cached --quiet -- "${commit_paths[@]}"; then
    git commit -q -m "$title" -- "${commit_paths[@]}" || die "git commit failed"
  fi
fi

git push origin "$branch" || die "git push failed"

pr_url="$("$gh_cli" pr list --repo "$repo" --head "$branch" --state open --json number,url,isCrossRepository |
  jq -r '[.[] | select(.isCrossRepository == false)][0].url // empty')" ||
  die "gh pr list failed"
if [[ -z $pr_url ]]; then
  pr_url="$("$gh_cli" pr create --repo "$repo" --draft --base "$default_branch" --head "$branch" \
    --title "$title" --body-file "$outgoing/pr-body.md")" || die "gh pr create failed"
fi
echo "$pr_url"
