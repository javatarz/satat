#!/usr/bin/env bash
# Publish a meeting digest as a draft PR. The owner runs it in a terminal outside Claude Code.
#
# In order: checks the target is origin, refuses any working-tree change outside the exact
# allowlist (docs/calls/<date>.md, and edits to tracked docs/roadmap/<name>.md themes other
# than README.md), refuses git attributes or binary content on those paths (both can hide a
# diff), runs scripts/check-digest.sh, then runs gitleaks over the files it will commit and the
# PR body it will send. Only then does it commit those paths on digest/<date>, push that branch
# fast-forward only and open a draft PR, or reuse its open PR.
#
# --dry-run stops after gitleaks and prints the files to commit and the PR draft.
#
# Exits 0 when done, 1 with `refused: <reason>` on stderr, 2 on bad usage. Calls
# "${DIGEST_GH:-gh}" and "${DIGEST_GITLEAKS:-gitleaks}".
#
# usage:
#   scripts/publish-digest.sh [--dry-run] digest <owner/repo> <YYYY-MM-DD>
set -euo pipefail

readonly skill_dir=".claude/skills/meeting-digest"

die() {
  echo "refused: $*" >&2
  exit 1
}

usage() {
  echo "usage: $0 [--dry-run] digest <owner/repo> <YYYY-MM-DD>" >&2
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
[[ $mode == digest ]] || die "agenda mode is not built yet"

for tool in git jq; do
  command -v "$tool" >/dev/null || die "$tool is required"
done
cd "$(dirname "${BASH_SOURCE[0]}")/.."

readonly record="docs/calls/$date.md"
readonly outbox="docs/scratch/calls/$date.outbox.json"
readonly outgoing="docs/scratch/calls/$date.outgoing"
readonly scan_header="docs/scratch/calls/$date.scan-header"
readonly pr_body="docs/scratch/calls/$date.pr-body.md"
readonly branch="digest/$date"
readonly title="Digest call $date"

origin="$(origin_repo)"
readonly origin
[[ $repo == "$origin" ]] || die "$repo is not origin ($origin)"

is_allowed() {
  local status="$1" path="$2"
  if [[ $path == "$record" ]]; then
    [[ $status == '??' || $status =~ ^[\ AM][\ AM]$ ]]
  elif [[ $path =~ ^docs/roadmap/[^/]+\.md$ && $path != docs/roadmap/README.md ]]; then
    [[ $status =~ ^[\ M][\ M]$ ]] && git cat-file -e "HEAD:$path" 2>/dev/null
  else
    return 1
  fi
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
    commit_paths+=("$path")
  else
    echo "refused: path not allowed: $status $path" >&2
    is_refused=1
  fi
done <"$status_file"
[[ $is_refused -eq 0 ]] || exit 1
[[ -f $outbox ]] || die "$outbox missing"

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

if ! check_output="$(scripts/check-digest.sh "$date")"; then
  grep '^fail: ' <<<"$check_output" >&2 || true
  die "check-digest failed"
fi

readonly gitleaks="${DIGEST_GITLEAKS:-gitleaks}"
command -v "$gitleaks" >/dev/null || die "gitleaks not found"
rm -rf "$outgoing" && mkdir -p "$outgoing"
# gitleaks type-sniffs the first 100,000 bytes of a file, each signature at its own offset (%PDF-
# at 0, tar's ustar at 257, ISO's CD001 at 32769), and skips the whole file on a match, which
# would hide a secret. Each scan copy starts with 131072 bytes of fixed header text, so all the
# real content lands past that first chunk.
{ head -c 131072 < <(yes 'gitleaks scan copy: padding line'); echo; } >"$scan_header"
for path in ${commit_paths[@]+"${commit_paths[@]}"}; do
  mkdir -p "$outgoing/$(dirname "$path")"
  cat -- "$scan_header" "$path" >"$outgoing/$path"
done
jq -r -L "$skill_dir" --arg date "$date" 'include "bodies"; pr_body($date)' "$outbox" >"$pr_body"
cat -- "$scan_header" "$pr_body" >"$outgoing/pr-body.md"
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
  jq -r -L "$skill_dir" --arg date "$date" 'include "bodies"; pr_preview($date), ""' "$outbox"
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

if [[ ${#commit_paths[@]} -gt 0 ]]; then
  git add -- "${commit_paths[@]}"
fi
if ! git diff --cached --quiet; then
  git commit -q -m "$title" || die "git commit failed"
fi

git push origin "$branch" || die "git push failed"

gh_cli="${DIGEST_GH:-gh}"
pr_url="$("$gh_cli" pr list --repo "$repo" --head "$branch" --state open --json number,url,isCrossRepository |
  jq -r '[.[] | select(.isCrossRepository == false)][0].url // empty')" ||
  die "gh pr list failed"
if [[ -z $pr_url ]]; then
  pr_url="$("$gh_cli" pr create --repo "$repo" --draft --base "$default_branch" --head "$branch" \
    --title "$title" --body-file "$pr_body")" || die "gh pr create failed"
fi
echo "$pr_url"
