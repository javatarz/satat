#!/usr/bin/env bash
# Stop hook: CI-round cap.
#
# Runs when the agent tries to finish. It runs the repository's checks (the
# same lint/tests the agent is judged by); if they fail it denies the finish,
# counts the failed round, and feeds the output back so the agent can fix it.
# After SATAT_CI_ROUND_CAP consecutive failed rounds it stops blocking and lets
# the agent finish with a "handing off to human" message, so a stuck agent
# cannot loop "modify -> tests fail -> modify" forever. One passing round resets
# the count.
set -o pipefail

CAP="${SATAT_CI_ROUND_CAP:-3}"
PROJECT_DIR="${OPENHANDS_PROJECT_DIR:-$PWD}"
cd "$PROJECT_DIR" || exit 0

# Round state survives across hook invocations but must not be committed, so it
# lives outside the work tree. Keyed by session so parallel conversations do not
# share a counter.
state_dir="${SATAT_CI_STATE_DIR:-${TMPDIR:-/tmp}}"
state_file="$state_dir/satat-ci-rounds-${OPENHANDS_SESSION_ID:-default}"
mkdir -p "$state_dir" 2>/dev/null
current=0
[ -f "$state_file" ] && current=$(cat "$state_file" 2>/dev/null || echo 0)
case "$current" in
  ''|*[!0-9]*) current=0 ;;
esac

# Run the checks. Tests gate the change, so they are the default; anything that
# cannot run or times out counts as a failed round rather than a silent pass.
if ! command -v timeout >/dev/null 2>&1; then
  timeout() { shift; "$@"; }
fi

if [ -n "${SATAT_TEST_CMD:-}" ]; then
  output=$(timeout 540 bash -c "$SATAT_TEST_CMD" 2>&1)
  status=$?
elif [ -f mise.toml ] && command -v mise >/dev/null 2>&1; then
  output=$(timeout 540 mise exec -- pre-commit run --all-files 2>&1)
  status=$?
elif command -v pre-commit >/dev/null 2>&1 && [ -f .pre-commit-config.yaml ]; then
  output=$(timeout 540 pre-commit run --all-files 2>&1)
  status=$?
else
  # No checks configured for this repository: nothing to gate on.
  printf '%s\n' '{"decision": "allow", "reason": "No CI checks configured; skipping CI-round cap."}'
  exit 0
fi

if [ "$status" -eq 0 ]; then
  printf '0\n' > "$state_file"
  exit 0
fi

next=$((current + 1))
printf '%s\n' "$next" > "$state_file"

if [ "$next" -ge "$CAP" ]; then
  printf '%s\n' "$next" > "$state_file"
  reason="handing off to human — exceeded CI-round cap ($next/$CAP); checks are still failing. Open the draft PR and leave the remaining failures for a human. Last output was:
$output"
  jq -cn --arg reason "$reason" '{decision: "allow", reason: $reason}'
  exit 0
fi

reason="CI round $next/$CAP failed. Fix the failing checks before finishing:
$output"
jq -cn --arg reason "$reason" '{decision: "deny", reason: $reason}'
exit 2
