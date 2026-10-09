#!/usr/bin/env bash
# Satat's OpenHands lifecycle hooks: the deterministic, bottom rung of the
# Judgement Pyramid. They run in the agent runtime, outside the prompt, so the
# agent cannot talk its way past them.
#
#   satat-hooks.sh pre-tool-use   block dangerous commands and writes to the
#                                 files that judge the change
#   satat-hooks.sh stop           tamper check + CI-round cap before finishing
#
# OpenHands passes the event as JSON on stdin. Exit 0 allows; exit 2 blocks,
# with a {decision, reason} JSON object on stdout. The dispatcher in
# satat-automations embeds this script in the conversation's hook_config, so no
# copy of it lives in the workspace for the agent to edit.
set -o pipefail

project_dir="${OPENHANDS_PROJECT_DIR:-$PWD}"

emit() {
  # $1 is allow/deny, $2 the reason; jq encodes it so quotes and newlines are safe.
  if command -v jq >/dev/null 2>&1; then
    jq -cn --arg decision "$1" --arg reason "$2" '{decision: $decision, reason: $reason}'
  else
    printf '{"decision": "%s", "reason": "Satat hook: see hook logs (jq unavailable)."}\n' "$1"
  fi
}

deny() {
  emit deny "$1"
  exit 2
}

# Which protected class a path falls in (agent-config, test, ci, lint), or
# return 1 if it is not protected. Absolute paths inside the project are
# matched relative to it.
protected_kind() {
  local path="${1#"$project_dir"/}"
  path="${path#./}"
  [ -n "$path" ] || return 1

  case "$path" in
    .openhands|.openhands/*|*/.openhands|*/.openhands/*)
      echo agent-config; return 0 ;;
  esac

  case "$path" in
    tests|test|*/tests|*/test|tests/*|test/*|*/tests/*|*/test/*) echo test; return 0 ;;
    test_*.py|*/test_*.py|*_test.py|*_test.go|*_test.rb|*_spec.rb) echo test; return 0 ;;
    *.test.js|*.test.jsx|*.test.ts|*.test.tsx|*.test.mjs|*.test.cjs) echo test; return 0 ;;
    *.spec.js|*.spec.jsx|*.spec.ts|*.spec.tsx|*.spec.mjs|*.spec.cjs) echo test; return 0 ;;
    *Test.java|*Tests.java|*_test.c|*_test.cpp|*_test.cc) echo test; return 0 ;;
  esac

  case "$path" in
    .github|*/.github|.github/workflows|.github/workflows/*|*/.github/workflows/*) echo ci; return 0 ;;
    .gitlab-ci.yml|*/.gitlab-ci.yml|.circleci|.circleci/*|*/.circleci/*) echo ci; return 0 ;;
    *Jenkinsfile*|*.travis.yml|azure-pipelines.yml|*/.drone.yml|bitbucket-pipelines.yml) echo ci; return 0 ;;
  esac

  case "$path" in
    *.eslintrc*|*eslint.config.*|*.prettierrc*|*prettier.config.*) echo lint; return 0 ;;
    *.pylintrc|*pylintrc|*.flake8|*ruff.toml) echo lint; return 0 ;;
    *.stylelintrc*|*.markdownlint*|*.shellcheckrc|*.yamllint|*.editorconfig) echo lint; return 0 ;;
    *.golangci.yml|*.golangci.yaml|*detekt.yml|*.pre-commit-config.yaml) echo lint; return 0 ;;
  esac

  return 1
}

# The commit the branch left its base at: the merge-base of HEAD with
# SATAT_BASE_REF (the dispatcher pins it to the base branch's commit, which the
# agent cannot move), else with origin's default branch. $1 is a directory in
# the repository. Fails when there is no repository or no common commit.
base_commit() {
  local ref="${SATAT_BASE_REF:-}"
  [ -n "$ref" ] || ref=$(git -C "$1" symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null)
  git -C "$1" merge-base HEAD "${ref:-origin/main}" 2>/dev/null
}

absolute() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s\n' "$project_dir/$1" ;;
  esac
}

# Whether a path did not exist at the base commit, so changing or removing it
# cannot undo anything the branch started with. Fails closed: without a
# resolvable base, nothing counts as new.
new_since_base() {
  local abs dir base
  abs=$(absolute "$1")
  dir=$(dirname "$abs")
  while [ ! -d "$dir" ]; do dir=$(dirname "$dir"); done
  base=$(base_commit "$dir") || return 1
  # `<commit>:./<path>` is relative to the directory git runs in.
  ! git -C "$dir" cat-file -e "$base:./${abs#"$dir"/}" 2>/dev/null
}

# A test file may be created, and changed while it is still new to the branch
# (the agent should add tests for its change and be able to fix them), but a
# test the branch started with may not be changed. Every other protected class
# is write-only.
check_write() {
  # $1 is the path, $2 is "create" when the write only adds a new file.
  local kind
  kind=$(protected_kind "$1") || return 0
  if [ "$kind" = test ]; then
    if { [ "${2:-}" = create ] && [ ! -e "$(absolute "$1")" ]; } || new_since_base "$1"; then
      return 0
    fi
    deny "Changing an existing test is not allowed: $1. Fix the code, not the test; new tests may be created with file_editor create."
  fi
  deny "Editing protected $kind path is not allowed: $1"
}

# Words of one shell command segment, quotes dropped and leading wrappers
# (sudo, env, VAR=value, ...) skipped, in the global `words` array.
words=()
segment_words() {
  local segment="${1//[\"\'\`]/}"
  read -ra words <<<"$segment"
  while [ "${#words[@]}" -gt 0 ]; do
    case "${words[0]}" in
      sudo|env|command|nohup|time|exec|nice|-*|*=*) words=("${words[@]:1}") ;;
      *) break ;;
    esac
  done
}

segments() {
  printf '%s\n' "${1//[;&|()]/$'\n'}"
}

re_pipe_shell='(curl|wget)[^;&]*\|[[:space:]]*((sudo|env)[[:space:]]+(-[^[:space:]]+[[:space:]]+|[A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*[[:space:]]+)*)?([^[:space:]|;&]*/)?((ba|da|z|k|fi)?sh|python[0-9.]*|perl|ruby|node)([[:space:]]|$)'
re_procsub_shell='(ba|da|z|k|fi)?sh[[:space:]]+(-[^[:space:]]+[[:space:]]+)*<\([[:space:]]*(curl|wget)'
re_cmdsub_shell='(sh[[:space:]]+-c|eval)[[:space:]]+["'\'']?\$\([[:space:]]*(curl|wget)'

dangerous_command() {
  local cmd="$1" segment w recursive root

  if [[ "$cmd" =~ $re_pipe_shell || "$cmd" =~ $re_procsub_shell || "$cmd" =~ $re_cmdsub_shell ]]; then
    echo "running a downloaded script"; return 0
  fi

  while IFS= read -r segment; do
    segment_words "$segment"
    [ "${#words[@]}" -gt 0 ] || continue
    case "${words[0]##*/}" in
      rm)
        recursive=0 root=0
        for w in "${words[@]:1}"; do
          # Literal `~` and `$HOME` as typed: the command has not been expanded.
          # shellcheck disable=SC2088,SC2016
          case "$w" in
            --recursive) recursive=1 ;;
            --no-preserve-root) root=1 ;;
            --*) ;;
            -*[rR]*) recursive=1 ;;
            /|/.|'/*'|'~'|'~/'|'~/*'|'$HOME'|'$HOME/'|'${HOME}'|'${HOME}/') root=1 ;;
          esac
        done
        if [ "$recursive" = 1 ] && [ "$root" = 1 ]; then
          echo "recursive delete of / or \$HOME"; return 0
        fi
        ;;
      chmod)
        for w in "${words[@]:1}"; do
          case "$w" in
            777|[0-7]777|a+rwx|a=rwx|ugo+rwx|ugo=rwx)
              echo "world-writable permissions"; return 0 ;;
          esac
        done
        ;;
    esac
  done < <(segments "$cmd")

  return 1
}

# Whether the command segment in `words` changes files.
segment_mutates() {
  local w
  case "${words[0]##*/}" in
    rm|rmdir|mv|cp|ln|truncate|unlink|install|dd|rsync|chmod|chown|touch|patch|shred|tee)
      return 0 ;;
    sed|perl)
      for w in "${words[@]:1}"; do
        case "$w" in
          --in-place*|-i*|-[a-zA-Z]*i*) return 0 ;;
        esac
      done
      ;;
    git)
      case "${words[1]:-}" in
        rm|mv|checkout|restore|reset|apply|am|stash|clean|switch) return 0 ;;
      esac
      ;;
  esac
  return 1
}

# Best effort: shell writes into a protected path (redirects, tee, rm, mv, cp,
# sed -i, git checkout, ...). The Stop hook's diff check is the backstop for
# anything this misses.
check_terminal_writes() {
  local cmd="$1" segment target w
  while IFS= read -r target; do
    if protected_kind "$target" >/dev/null; then
      deny "Writing to a protected path from the shell is not allowed: $target. Create new tests with file_editor create."
    fi
  done < <(printf '%s\n' "$cmd" | grep -oE '[0-9]*>>?\|?[[:space:]]*[^[:space:];|&<>()]+' | sed -E 's/^[0-9]*>>?\|?[[:space:]]*//')

  while IFS= read -r segment; do
    segment_words "$segment"
    [ "${#words[@]}" -gt 1 ] || continue
    segment_mutates || continue
    restores_to_base && continue
    for w in "${words[@]:1}"; do
      w="${w#*=}"
      if protected_kind "$w" >/dev/null; then
        deny "Changing a protected path from the shell is not allowed: $w. Read it with cat; create or edit new tests with file_editor. To undo a change, restore it from the base commit with \`git checkout <base> -- <path>\`."
      fi
    done
  done < <(segments "$cmd")
}

# Whether `ref` names the base commit.
is_base() {
  local base
  base=$(base_commit "$project_dir") || return 1
  [ "$(git -C "$project_dir" rev-parse -q --verify "$1^{commit}" 2>/dev/null)" = "$base" ]
}

# Whether the segment in `words` only puts protected paths back the way the
# base commit had them, which is what the Stop hook asks the agent to do:
#   git checkout <base> -- <paths>
#   git restore --source=<base> [--staged] [--worktree] -- <paths>
#   git rm / rm of paths that did not exist at the base commit
restores_to_base() {
  local w seen_dashdash=0
  case "${words[0]##*/}" in
    git)
      case "${words[1]:-}" in
        checkout)
          [ "${words[3]:-}" = -- ] && is_base "${words[2]:-}"
          return
          ;;
        restore)
          local source=""
          for w in "${words[@]:2}"; do
            if [ "$seen_dashdash" = 1 ]; then continue; fi
            case "$w" in
              --) seen_dashdash=1 ;;
              --source=*) source="${w#--source=}" ;;
              --staged|--worktree|-S|-W|-SW|-WS) ;;
              *) return 1 ;;
            esac
          done
          [ "$seen_dashdash" = 1 ] && [ -n "$source" ] && is_base "$source"
          return
          ;;
        rm) set -- "${words[@]:2}" ;;
        *) return 1 ;;
      esac
      ;;
    rm) set -- "${words[@]:1}" ;;
    *) return 1 ;;
  esac
  # rm / git rm: every path must be new since the base commit.
  for w in "$@"; do
    case "$w" in
      --) seen_dashdash=1; continue ;;
      -*) [ "$seen_dashdash" = 1 ] || continue ;;
    esac
    new_since_base "$w" || return 1
  done
}

pre_tool_use() {
  command -v jq >/dev/null 2>&1 || deny "jq is required by the Satat PreToolUse hook."

  local event tool_name edit_command path reason line
  event=$(cat)
  printf '%s' "$event" | jq -e 'type == "object"' >/dev/null 2>&1 ||
    deny "Unreadable tool-call payload; blocked to be safe."

  tool_name=$(printf '%s' "$event" | jq -r '.tool_name // empty')
  tool_name="${tool_name:-${OPENHANDS_TOOL_NAME:-}}"
  [ -n "$tool_name" ] || deny "Tool call has no tool name; blocked to be safe."

  case "$tool_name" in
    file_editor|str_replace_editor|planning_file_editor|write|edit|create)
      edit_command=$(printf '%s' "$event" | jq -r '.tool_input.command // empty')
      [ "$edit_command" = view ] && exit 0
      path=$(printf '%s' "$event" | jq -r '.tool_input.path // .tool_input.file_path // empty')
      if [ "$edit_command" = create ] || [ "$tool_name" = create ]; then
        check_write "$path" create
      else
        check_write "$path"
      fi
      ;;
    apply_patch)
      # Codex-style patches name files on "*** Add/Update/Delete File:" lines;
      # unified diffs on "---"/"+++" lines.
      while IFS= read -r line; do
        case "$line" in
          '*** Add File: '*) check_write "${line#'*** Add File: '}" create ;;
          '*** Update File: '*) check_write "${line#'*** Update File: '}" ;;
          '*** Delete File: '*) check_write "${line#'*** Delete File: '}" ;;
          '*** Move to: '*) check_write "${line#'*** Move to: '}" ;;
          '+++ '*|'--- '*)
            path="${line:4}"
            path="${path%%$'\t'*}"
            path="${path#a/}"
            path="${path#b/}"
            [ "$path" = /dev/null ] || check_write "$path"
            ;;
        esac
      done < <(printf '%s' "$event" | jq -r '[.tool_input | .. | strings] | join("\n")')
      ;;
    terminal|shell|bash|execute_bash)
      local cmd
      cmd=$(printf '%s' "$event" | jq -r '.tool_input.command // empty')
      if reason=$(dangerous_command "$cmd"); then
        deny "Dangerous shell command blocked ($reason): $cmd"
      fi
      check_terminal_writes "$cmd"
      ;;
  esac
  exit 0
}

# Protected files changed since the base commit $1: modified, deleted or
# renamed, or added outside the test class. Prints one "STATUS path" per line.
tampered_paths() {
  local status path kind
  {
    git diff --name-status --no-renames "$1" 2>/dev/null
    git ls-files --others --exclude-standard 2>/dev/null | awk '{ print "A\t" $0 }'
  } | while IFS=$'\t' read -r status path; do
    kind=$(protected_kind "$path") || continue
    [ "$kind" = test ] && [ "$status" = A ] && continue
    printf '%s %s\n' "$status" "$path"
  done
}

# The tamper report for the agent, with the commands that undo each change
# (the PreToolUse hook allows exactly these). Empty when nothing was tampered.
# Without a resolvable base nothing can be checked, so that fails the round.
tamper_report() {
  local base tampered restore remove
  if ! base=$(base_commit .); then
    printf '%s\n' "Cannot find the base commit (${SATAT_BASE_REF:-origin/HEAD}) to check protected files (tests, CI, lint config, .openhands) against. Keep the origin remote and the branch's history from the base branch; if you cannot, hand off instead."
    return
  fi
  tampered=$(tampered_paths "$base")
  [ -n "$tampered" ] || return 0
  restore=$(printf '%s\n' "$tampered" | awk '$1 != "A" { print $2 }' | tr '\n' ' ')
  remove=$(printf '%s\n' "$tampered" | awk '$1 == "A" { print $2 }' | tr '\n' ' ')
  printf '%s\n' "Protected files (tests, CI, lint config, .openhands) changed on this branch. Undo these changes; if a human made them, hand off instead:"
  printf '%s\n' "$tampered"
  [ -z "$restore" ] || printf '  git checkout %s -- %s\n' "$base" "${restore% }"
  [ -z "$remove" ] || printf '  git rm -rf --ignore-unmatch -- %s; rm -rf -- %s\n' "${remove% }" "${remove% }"
}

stop() {
  local cap="${SATAT_CI_ROUND_CAP:-3}"
  local event="" session state_dir state_file current next output status tampered entered
  [ -t 0 ] || event=$(cat)

  session="${OPENHANDS_SESSION_ID:-}"
  if [ -z "$session" ] && command -v jq >/dev/null 2>&1; then
    session=$(printf '%s' "$event" | jq -r '.session_id // empty' 2>/dev/null)
  fi
  # Round state survives across Stop attempts but must not be committed, so it
  # lives outside the work tree, keyed by session.
  state_dir="${SATAT_CI_STATE_DIR:-${TMPDIR:-/tmp}}"
  state_file="$state_dir/satat-ci-rounds-${session:-default}"
  mkdir -p "$state_dir" 2>/dev/null
  current=0
  [ -f "$state_file" ] && current=$(cat "$state_file" 2>/dev/null)
  case "$current" in
    ''|*[!0-9]*) current=0 ;;
  esac

  if ! command -v timeout >/dev/null 2>&1; then
    timeout() { shift; "$@"; }
  fi

  # Checks that cannot run count as a failed round, not a silent pass.
  status=0
  output=""
  tampered=""
  entered=0
  cd "$project_dir" 2>/dev/null && entered=1
  if [ "$entered" = 0 ]; then
    output="Cannot enter the project directory $project_dir to run the checks."
    status=1
  elif [ -n "${SATAT_TEST_CMD:-}" ]; then
    output=$(timeout 540 bash -c "$SATAT_TEST_CMD" 2>&1)
    status=$?
  elif [ -f .pre-commit-config.yaml ]; then
    if command -v pre-commit >/dev/null 2>&1; then
      output=$(timeout 540 pre-commit run --all-files 2>&1)
      status=$?
    elif command -v uvx >/dev/null 2>&1; then
      output=$(timeout 540 uvx pre-commit run --all-files 2>&1)
      status=$?
    else
      output="pre-commit is configured but not installed. Install it (pip install pre-commit) and finish again."
      status=1
    fi
  fi
  output=$(printf '%s' "$output" | tail -n 150)

  [ "$entered" = 1 ] && tampered=$(tamper_report)
  if [ -n "$tampered" ]; then
    status=1
    output="$tampered

$output"
  fi

  if [ "$status" -eq 0 ]; then
    printf '0\n' >"$state_file"
    exit 0
  fi

  next=$((current + 1))
  if [ "$next" -ge "$cap" ]; then
    # Reset so a later revision in the same conversation gets a fresh cap.
    printf '0\n' >"$state_file"
    emit allow "handing off to human — exceeded CI-round cap ($next/$cap); checks are still failing. Leave the remaining failures for a human in the PR description. Last output was:
$output"
    exit 0
  fi

  printf '%s\n' "$next" >"$state_file"
  deny "CI round $next/$cap failed. Fix the failing checks before finishing:
$output"
}

case "${1:-}" in
  pre-tool-use) pre_tool_use ;;
  stop) stop ;;
  *)
    echo "usage: $0 {pre-tool-use|stop}" >&2
    exit 1
    ;;
esac
