#!/bin/bash
# Grades the agent's tests. solved: /app/test_money.py passes against the real split_amount and
# fails against every mutant in mutants/, each a plausible bug the issue asked to cover. intact:
# money.py is unchanged and the repo's original tests still pass. No network: see task.toml.
set -uo pipefail
mkdir -p /logs/verifier

# run_against IMPL runs the agent's test_money.py with IMPL as money.py, in a scratch directory so
# /app is never touched.
run_against() {
  local dir
  dir="$(mktemp -d)"
  cp /app/test_money.py "$dir/" && cp "$1" "$dir/money.py" \
    && (cd "$dir" && PYTHONPATH=/app python -m unittest -v test_money)
}

solved=0
if [ ! -f /app/test_money.py ]; then
  echo "== no test_money.py"
else
  echo "== against the real split_amount"
  if run_against /tests/money.py; then
    solved=1
    for m in /tests/mutants/*.py; do
      echo "== against mutant $(basename "$m" .py), must fail"
      if run_against "$m"; then
        echo "-- mutant $(basename "$m" .py) survived"
        solved=0
      fi
    done
  fi
fi

intact=0
echo "== money.py unchanged, original tests"
cmp -s /app/money.py /tests/money.py \
  && (cd /tests && PYTHONPATH=/app python -m unittest -v base_tests) && intact=1

printf '{"reward": %d, "solved": %d, "intact": %d}\n' $((solved && intact)) "$solved" "$intact" \
  > /logs/verifier/reward.json
cat /logs/verifier/reward.json
