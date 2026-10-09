#!/usr/bin/env bash
# PreToolUse hook: tamper protection and dangerous-command blocking.
#
# OpenHands passes the tool call as JSON on stdin (tool_name, tool_input).
# Exit 0 allows the call; exit 2 blocks it and reports the reason. This is the
# deterministic, bottom rung of the Judgement Pyramid: the agent cannot edit
# the files that judge it, and cannot run destructive shell commands, no matter
# what its prompt says.
set -o pipefail

event=$(cat)

tool_name=$(printf '%s' "$event" | jq -r '.tool_name // empty')
if [ -z "$tool_name" ]; then
  tool_name="${OPENHANDS_TOOL_NAME:-}"
fi

deny() {
  # $1 is the reason; JSON-encode it so quotes/newlines cannot break the payload.
  jq -cn --arg reason "$1" '{decision: "deny", reason: $reason}'
  exit 2
}

# Paths the agent must never write to: the hook config that enforces these
# rules, the tests that judge the change, CI wiring, lint/format rules, and the
# pre-commit config that gates the commit.
path_is_protected() {
  local path="$1"
  [ -n "$path" ] || return 1

  case "$path" in
    *.openhands/hooks.json|*/.openhands/*|.openhands/*)
      return 0
      ;;
  esac

  case "$path" in
    */tests/*|tests/*|*/test/*|test/*)
      return 0
      ;;
    test_*.py|*/test_*.py|*_test.py|*_test.go|*_test.rb|*_spec.rb)
      return 0
      ;;
    *.test.js|*.test.jsx|*.test.ts|*.test.tsx|*.test.mjs|*.test.cjs)
      return 0
      ;;
    *.spec.js|*.spec.jsx|*.spec.ts|*.spec.tsx|*.spec.mjs|*.spec.cjs)
      return 0
      ;;
    *Test.java|*Tests.java|*_test.c|*_test.cpp|*_test.cc)
      return 0
      ;;
  esac

  case "$path" in
    */.github/workflows/*|.github/workflows/*)
      return 0
      ;;
    */.gitlab-ci.yml|.gitlab-ci.yml|*/.circleci/*|.circleci/*)
      return 0
      ;;
    *Jenkinsfile*|*.travis.yml|azure-pipelines.yml|*/.drone.yml|bitbucket-pipelines.yml)
      return 0
      ;;
  esac

  case "$path" in
    *.eslintrc*|*eslint.config.*|*.prettierrc*|*prettier.config.*)
      return 0
      ;;
    *.pylintrc|*pylintrc|*/.flake8|*.flake8|*/.ruff.toml|*ruff.toml)
      return 0
      ;;
    *.stylelintrc*|*.markdownlint*|*.shellcheckrc|*/.yamllint|*.yamllint)
      return 0
      ;;
    */.golangci.yml|*/.golangci.yaml|*detekt.yml|*.pre-commit-config.yaml)
      return 0
      ;;
  esac

  return 1
}

dangerous_command() {
  local cmd="$1"
  [ -n "$cmd" ] || return 1

  # Recursive, forced deletion of the filesystem root (either flag order).
  # The target must be `/` itself (optionally followed by `*`), so a normal
  # `rm -rf /tmp/build` is not caught.
  if [[ "$cmd" =~ rm[[:space:]]+([^[:space:]]+[[:space:]]+)*-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*[[:space:]]+/([[:space:]]|\*|$) ]]; then
    return 0
  fi
  if [[ "$cmd" =~ rm[[:space:]]+([^[:space:]]+[[:space:]]+)*-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*[[:space:]]+/([[:space:]]|\*|$) ]]; then
    return 0
  fi

  # World-writable permissions.
  if [[ "$cmd" =~ chmod[[:space:]]+(-[a-zA-Z]+[[:space:]]+)*777 ]]; then
    return 0
  fi

  # Piping a downloaded script straight into a shell.
  if [[ "$cmd" =~ (curl|wget)[^|]*\|[[:space:]]*(ba|da|z|k)?sh([[:space:]]|$|\|) ]]; then
    return 0
  fi

  return 1
}

# Best-effort detection of a shell command that writes into a protected path
# via a redirect or `tee`, which would otherwise bypass file_editor.
terminal_writes_protected() {
  local cmd="$1"
  local target

  while IFS= read -r target; do
    [ -n "$target" ] || continue
    if path_is_protected "$target"; then
      return 0
    fi
  done < <(
    printf '%s' "$cmd" |
      grep -oE '(>>?|tee)[[:space:]]+[^[:space:]|;&]+' |
      sed -E 's/^(>>?|tee)[[:space:]]+//'
  )

  return 1
}

case "$tool_name" in
  file_editor|write|edit|create)
    edit_command=$(printf '%s' "$event" | jq -r '.tool_input.command // empty')
    # file_editor "view" is read-only; older write/edit tools carry no command.
    if [ "$tool_name" = "file_editor" ] && [ "$edit_command" = "view" ]; then
      exit 0
    fi
    path=$(printf '%s' "$event" | jq -r '.tool_input.path // .tool_input.file_path // empty')
    if path_is_protected "$path"; then
      deny "Editing protected path is not allowed: $path"
    fi
    exit 0
    ;;
  terminal|shell|bash)
    cmd=$(printf '%s' "$event" | jq -r '.tool_input.command // empty')
    if dangerous_command "$cmd"; then
      deny "Dangerous shell command blocked: $cmd"
    fi
    if terminal_writes_protected "$cmd"; then
      deny "Writing to a protected path is not allowed: $cmd"
    fi
    exit 0
    ;;
esac

exit 0
