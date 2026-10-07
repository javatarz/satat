#!/usr/bin/env bash
# pre-commit commit-msg hook.
#
# Enforces the mechanical subset of the commit message rules (see "Commit
# Message Rules" in ~/.claude/CLAUDE.md): a capitalised, period-free subject of
# at most 72 characters, and a blank line before any body. Imperative mood and
# issue references stay human judgement and are deliberately not checked.
#
# pre-commit passes the path to the commit message file as the first argument.
set -euo pipefail

msg_file="${1:-}"
if [ -z "$msg_file" ]; then
  echo "commit-msg: no message file given" >&2
  exit 1
fi

# Collect non-comment lines.
lines=()
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    '#'*) continue ;;
  esac
  lines+=("$line")
done < "$msg_file"

# Drop trailing blank lines so a body check is meaningful.
last=$(( ${#lines[@]} - 1 ))
while [ "$last" -ge 0 ] && [ -z "${lines[$last]:-}" ]; do
  last=$(( last - 1 ))
done

subject="${lines[0]:-}"
status=0
fail() {
  echo "commit-msg: $*" >&2
  status=1
}

# Generated subjects are not authored, so skip them.
case "$subject" in
  "Merge "*|"Revert "*|"fixup! "*|"squash! "*|"amend! "*) exit 0 ;;
esac

if [ -z "$subject" ]; then
  fail "empty subject"
else
  len=${#subject}
  if [ "$len" -gt 72 ]; then
    fail "subject is $len chars (hard limit 72; aim for 50)"
  fi
  case "${subject:0:1}" in
    [A-Z]) ;;
    *) fail "subject must start with a capital letter" ;;
  esac
  case "$subject" in
    *.) fail "subject must not end with a period" ;;
  esac
fi

# A body, if present, must be separated from the subject by a blank line.
if [ "$last" -ge 1 ] && [ -n "${lines[1]:-}" ]; then
  fail "body must be separated from the subject by a blank line"
fi

exit "$status"
