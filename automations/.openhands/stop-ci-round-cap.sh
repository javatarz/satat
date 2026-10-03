#!/bin/bash
# Satat Stop hook
# T11: PreToolUse hooks + CI-round cap

# Enforce CI-round cap of 3

# Track CI-round count in a file
CI_COUNT_FILE="/tmp/ci-round-count"
CI_ROUND_CAP=3

# Initialize count if file doesn't exist
if [[ ! -f "$CI_COUNT_FILE" ]]; then
  echo "0" > "$CI_COUNT_FILE"
fi

# Read current count
CURRENT_COUNT=$(cat "$CI_COUNT_FILE")

# Check if tests are failing (this would be determined by the agent's test results)
# For this example, we'll assume we get this information from environment variables
if [[ "$TESTS_PASSED" == "false" ]]; then
  # Increment count
  NEW_COUNT=$((CURRENT_COUNT + 1))
  echo "$NEW_COUNT" > "$CI_COUNT_FILE"
  
  # Check if we've exceeded the cap
  if [[ "$NEW_COUNT" -ge "$CI_ROUND_CAP" ]]; then
    echo "Handing off to human — exceeded CI-round cap"
    exit 0  # Allow finish
  else
    echo "Tests failed but CI-round cap not exceeded ($NEW_COUNT/$CI_ROUND_CAP)"
    exit 1  # Deny finish
  fi
else
  # Tests passed, reset count
  echo "0" > "$CI_COUNT_FILE"
  exit 0  # Allow finish
fi