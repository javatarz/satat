#!/usr/bin/env bash
# Publish a meeting digest as a draft PR. The owner runs it in a terminal outside Claude Code.
#
# In order: checks the target is origin, refuses any working-tree change outside the exact
# allowlist (docs/calls/<date>.md, and edits to tracked docs/roadmap/<name>.md themes other
# than README.md), runs scripts/check-digest.sh, then runs gitleaks over the files it will
# commit and the PR body it will send. Only then does it commit those paths on digest/<date>,
# push that branch fast-forward only and open a draft PR, or reuse its open PR.
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
done < <(git status --porcelain=v1 -z --untracked-files=all)
[[ $is_refused -eq 0 ]] || exit 1
[[ -f $outbox ]] || die "$outbox missing"

if ! check_output="$(scripts/check-digest.sh "$date")"; then
  grep '^fail: ' <<<"$check_output" >&2 || true
  die "check-digest failed"
fi

readonly gitleaks="${DIGEST_GITLEAKS:-gitleaks}"
command -v "$gitleaks" >/dev/null || die "gitleaks not found"
rm -rf "$outgoing" && mkdir -p "$outgoing"
for path in ${commit_paths[@]+"${commit_paths[@]}"}; do
  mkdir -p "$outgoing/$(dirname "$path")"
  cp -- "$path" "$outgoing/$path"
done
jq -r -L "$skill_dir" --arg date "$date" 'include "bodies"; pr_body($date)' "$outbox" >"$outgoing/pr-body.md"
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
    --title "$title" --body-file "$outgoing/pr-body.md")" || die "gh pr create failed"
fi
echo "$pr_url"
