#!/usr/bin/env bash
# Proof harness: run the meeting-digest skill on the committed fixture transcript, N times.
#
# Each run starts from the committed tree with the fixture copied to
# docs/scratch/calls/2026-10-28.txt, runs `claude -p "/meeting-digest 2026-10-28"`, and keeps
# its evidence in docs/scratch/calls/runs/<i>/: run.jsonl, stderr.txt, exit.txt, status.txt,
# diff.patch, call-record.md (when docs/calls/2026-10-28.md exists) and one <suffix> file per
# docs/scratch/calls/2026-10-28.<suffix> other than the transcript. The tree is then reset,
# except after the last run with --keep-last. When scripts/testdata/fake-env.sh exists it is
# sourced first, so the runs see the fake gh and git.
#
# Refuses a dirty tree: commit first, since each run discards tracked changes.
#
# usage:
#   scripts/digest-fixture-run.sh [--keep-last] <runs>
set -euo pipefail

readonly date="2026-10-28"
readonly fixture=".claude/skills/meeting-digest/fixtures/$date.txt"
readonly calls_dir="docs/scratch/calls"
readonly runs_dir="$calls_dir/runs"
readonly call_record="docs/calls/$date.md"
readonly fake_env="scripts/testdata/fake-env.sh"

die() {
  echo "error: $*" >&2
  exit 1
}

usage() {
  die "usage: $0 [--keep-last] <runs>"
}

is_keep_last=0
if [[ ${1:-} == --keep-last ]]; then
  is_keep_last=1
  shift
fi
[[ $# -eq 1 && $1 =~ ^[1-9][0-9]*$ ]] || usage
readonly runs="$1"

command -v claude >/dev/null || die "claude is required"
command -v jq >/dev/null || die "jq is required"
cd "$(dirname "${BASH_SOURCE[0]}")/.."
[[ -z "$(git status --porcelain --untracked-files=all)" ]] || die "the tree is dirty; commit first"

if [[ -f $fake_env ]]; then
  # shellcheck source=/dev/null
  source "$fake_env"
fi

rm -rf "$runs_dir"
for ((run = 1; run <= runs; run++)); do
  run_dir="$runs_dir/$run"
  mkdir -p "$run_dir"
  rm -f "$calls_dir/$date".*
  cp "$fixture" "$calls_dir/$date.txt"

  exit_code=0
  claude -p "/meeting-digest $date" --permission-mode acceptEdits --output-format stream-json --verbose \
    </dev/null >"$run_dir/run.jsonl" 2>"$run_dir/stderr.txt" || exit_code=$?
  echo "$exit_code" >"$run_dir/exit.txt"

  git status --porcelain=v1 --untracked-files=all >"$run_dir/status.txt"
  git add -AN
  git diff >"$run_dir/diff.patch"
  git reset -q

  if [[ -f $call_record ]]; then
    cp "$call_record" "$run_dir/call-record.md"
  fi
  for output in "$calls_dir/$date".*; do
    if [[ -f $output && $output != "$calls_dir/$date.txt" ]]; then
      cp "$output" "$run_dir/${output#"$calls_dir/$date."}"
    fi
  done

  if [[ $is_keep_last -eq 0 || $run -lt $runs ]]; then
    git reset -q --hard
    git clean -fdq
  fi

  item_count="$(jq '.items | length' "$run_dir/items.json" 2>/dev/null || echo 0)"
  echo "run $run: exit=$exit_code items=$item_count"
done
