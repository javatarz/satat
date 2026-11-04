#!/bin/bash
# Grades the change in /app. solved: the hidden tests pass. intact: the repo's original tests,
# copied here before the agent ran, still pass against the changed code, so deleting a test
# cannot hide a break. The attempt passes only with both. No network: see task.toml.
set -uo pipefail
mkdir -p /logs/verifier
run() { (cd /tests && PYTHONPATH=/app python -m unittest -v "$@"); }

solved=0
intact=0
echo "== hidden tests"
run hidden_tests && solved=1
echo "== original tests"
run base_tests && intact=1

printf '{"reward": %d, "solved": %d, "intact": %d}\n' $((solved && intact)) "$solved" "$intact" \
  > /logs/verifier/reward.json
cat /logs/verifier/reward.json
