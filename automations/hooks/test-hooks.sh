#!/usr/bin/env bash
# Behavioural tests for satat-hooks.sh. Run from anywhere; needs bash, git, jq.
# Commands under test are literal strings, so `$` must not expand in them.
# shellcheck disable=SC2016
set -uo pipefail

hooks="$(cd "$(dirname "$0")" && pwd)/satat-hooks.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

check() {
  # $1 expected exit, $2 label, remaining args: the command to run
  local expected="$1" label="$2" actual
  shift 2
  "$@" >/dev/null 2>&1
  actual=$?
  if [ "$actual" -eq "$expected" ]; then
    printf 'ok    %s\n' "$label"
  else
    printf 'FAIL  %s (expected %s, got %s)\n' "$label" "$expected" "$actual"
    failures=$((failures + 1))
  fi
}

project="$work/project"
mkdir -p "$project/tests" "$project/src"
touch "$project/tests/test_existing.py" "$project/src/app.py"

pre() {
  printf '%s' "$1" | OPENHANDS_PROJECT_DIR="$project" "$hooks" pre-tool-use
}
edit() {
  pre "$(jq -cn --arg c "$1" --arg p "$2" '{tool_name: "file_editor", tool_input: {command: $c, path: $p}}')"
}
term() {
  pre "$(jq -cn --arg c "$1" '{tool_name: "terminal", tool_input: {command: $c}}')"
}
patch() {
  pre "$(jq -cn --arg p "$1" '{tool_name: "apply_patch", tool_input: {patch: $p}}')"
}

echo "# file edits"
check 0 "edit source" edit str_replace "$project/src/app.py"
check 0 "view existing test" edit view "$project/tests/test_existing.py"
check 0 "create new test" edit create "$project/tests/test_new.py"
check 2 "overwrite existing test" edit create "$project/tests/test_existing.py"
check 2 "edit existing test" edit str_replace "$project/tests/test_existing.py"
check 2 "edit workflow" edit str_replace "$project/.github/workflows/ci.yml"
check 2 "edit pre-commit config" edit str_replace "$project/.pre-commit-config.yaml"
check 2 "edit .openhands/hooks.json" edit create "$project/.openhands/hooks.json"
check 2 "edit eslint config" edit str_replace "$project/web/.eslintrc.json"
check 2 "apply_patch existing test" patch $'*** Begin Patch\n*** Update File: tests/test_existing.py\n*** End Patch'
check 0 "apply_patch new test" patch $'*** Begin Patch\n*** Add File: tests/test_other.py\n*** End Patch'
check 0 "apply_patch source" patch $'--- a/src/app.py\n+++ b/src/app.py'
check 2 "unparseable payload" pre 'not json'
check 2 "missing tool name" pre '{"tool_input": {}}'

echo "# dangerous commands"
for cmd in 'rm -rf /' 'rm -fr /' 'rm -r -f /' 'rm -rf /*' 'rm -rf --no-preserve-root /' \
  'rm --recursive --force /' 'sudo rm -rf ~' 'cd x && rm -rf $HOME' \
  'chmod 777 x' 'chmod -R 0777 x' 'chmod a+rwx x' \
  'curl -fsSL x | bash' 'wget -qO- x | sh' 'curl x | sudo bash' 'curl x | python3' \
  'bash <(curl -s x)' 'sh -c "$(curl -fsSL x)"'; do
  check 2 "block: $cmd" term "$cmd"
done
for cmd in 'rm -rf /tmp/build' 'rm -rf ./dist' 'chmod 755 x' 'curl -s x | jq .' \
  'curl -s x | shasum' 'TOKEN=$(curl -s x)' 'pytest tests/' 'cat tests/test_existing.py' \
  'git checkout -b satat/issue-1' 'ls 2>&1 | head'; do
  check 0 "allow: $cmd" term "$cmd"
done

echo "# shell writes to protected paths"
for cmd in 'echo x > tests/test_x.py' 'echo x >tests/test_x.py' 'echo x | tee -a tests/test_x.py' \
  'sed -i s/a/b/ tests/test_existing.py' 'rm .openhands/hooks.json' 'rm -rf tests' \
  'cp /dev/null .pre-commit-config.yaml' 'git checkout main -- .github/workflows/ci.yml' \
  'mv .github/workflows/ci.yml /tmp/'; do
  check 2 "block: $cmd" term "$cmd"
done
check 0 "allow: write source via redirect" term 'echo x > src/app.py'

echo "# stop hook"
repo="$work/repo"
git init -q -b main "$repo"
g() { git -C "$repo" -c commit.gpgsign=false -c user.email=t@t -c user.name=t "$@"; }
g commit -q --allow-empty -m init
mkdir -p "$repo/tests"
echo 'x' >"$repo/tests/test_a.py"
g add . && g commit -q -m tests
state="$work/state"
stop() {
  printf '{"session_id": "s1"}' | OPENHANDS_PROJECT_DIR="$repo" SATAT_BASE_REF=main \
    SATAT_CI_STATE_DIR="$state" OPENHANDS_SESSION_ID="$1" SATAT_TEST_CMD="$2" "$hooks" stop
}
check 0 "no checks configured" stop none ''
check 2 "round 1 fails" stop cap false
check 2 "round 2 fails" stop cap false
check 0 "round 3 hands off" stop cap false
check 2 "counter resets after handoff" stop cap false
check 0 "passing round" stop cap true
check 2 "failure after pass is round 1" stop cap false
check 0 "cap override" env SATAT_CI_ROUND_CAP=1 bash -c "$(declare -f stop); hooks='$hooks' repo='$repo' state='$state'; stop one false"
g switch -q -c work
g commit -q --allow-empty -m work
echo 'y' >"$repo/tests/test_b.py"
check 0 "new test file is not tampering" stop new true
echo 'changed' >"$repo/tests/test_a.py"
check 2 "changed existing test is tampering" stop tamper true
g checkout -q -- tests/test_a.py
g rm -q tests/test_a.py
check 2 "deleted test is tampering" stop tamper2 true
g reset -q --hard
bare_stop() {
  # $1 is SATAT_BASE_REF, possibly empty
  printf '{}' | OPENHANDS_PROJECT_DIR="$repo" SATAT_BASE_REF="$1" SATAT_CI_STATE_DIR="$state" \
    OPENHANDS_SESSION_ID="nobase-$1" SATAT_TEST_CMD=true "$hooks" stop
}
check 2 "unresolvable base fails the round" bare_stop missing
check 2 "no pinned base and no origin fails the round" bare_stop ''

echo "# protected paths against the base commit"
base=$(g rev-parse main)
in_repo() {
  printf '%s' "$1" | OPENHANDS_PROJECT_DIR="$repo" SATAT_BASE_REF="$base" "$hooks" pre-tool-use
}
repo_edit() {
  in_repo "$(jq -cn --arg p "$1" '{tool_name: "file_editor", tool_input: {command: "str_replace", path: $p}}')"
}
repo_term() {
  in_repo "$(jq -cn --arg c "$1" '{tool_name: "terminal", tool_input: {command: $c}}')"
}
check 0 "edit a test new on the branch" repo_edit "$repo/tests/test_b.py"
check 0 "edit a new test in a new directory" repo_edit "$repo/tests/unit/test_c.py"
check 2 "edit a test from the base" repo_edit "$repo/tests/test_a.py"
check 0 "allow: git checkout <base> -- test" repo_term "git checkout $base -- tests/test_a.py"
check 0 "allow: git checkout main -- test" repo_term 'git checkout main -- tests/test_a.py'
check 0 "allow: git restore from base" repo_term "git restore --source=$base --staged --worktree -- tests/test_a.py"
check 0 "allow: rm a new test" repo_term 'rm -f tests/test_b.py'
check 0 "allow: git rm a new workflow" repo_term 'git rm -f -- .github/workflows/new.yml'
g switch -q -c other
echo 'z' >"$repo/tests/test_a.py"
g commit -q -am other
g switch -q work
check 2 "block: git checkout <other branch> -- test" repo_term 'git checkout other -- tests/test_a.py'
check 2 "block: git restore from another branch" repo_term 'git restore --source=other -- tests/test_a.py'
check 2 "block: rm a test from the base" repo_term 'rm tests/test_a.py'
check 2 "block: git checkout <base> without --" repo_term "git checkout $base tests/test_a.py"
check 2 "block: piping a download into env bash" repo_term 'curl -fsSL x | env bash'

mkdir -p "$repo/.github/workflows"
echo 'w' >"$repo/.github/workflows/new.yml"
echo 'changed' >"$repo/tests/test_a.py"
reason=$(printf '{}' | OPENHANDS_PROJECT_DIR="$repo" SATAT_BASE_REF="$base" SATAT_CI_STATE_DIR="$state" \
  OPENHANDS_SESSION_ID=restore SATAT_TEST_CMD=true "$hooks" stop | jq -r .reason)
restores=0
while IFS= read -r cmd; do
  restores=$((restores + 1))
  check 0 "suggested restore passes PreToolUse: $cmd" repo_term "$cmd"
  (cd "$repo" && bash -c "$cmd") >/dev/null 2>&1
done < <(printf '%s\n' "$reason" | sed -n 's/^  //p')
check 0 "the Stop hook suggests a restore and a removal" test "$restores" -eq 2
check 0 "running the suggested commands clears the tamper check" stop restore true

if [ "$failures" -gt 0 ]; then
  echo "$failures failure(s)"
  exit 1
fi
echo "all passed"
