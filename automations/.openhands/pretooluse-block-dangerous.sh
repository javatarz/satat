#!/bin/bash
# Satat PreToolUse hook
# T11: PreToolUse hooks + CI-round cap

# Block dangerous operations and enforce CI-round cap

# Get the tool being used
TOOL_NAME="$1"
TOOL_ARGS="$2"

# Block editing test files
if [[ "$TOOL_NAME" == "write" ]] || [[ "$TOOL_NAME" == "edit" ]]; then
  if [[ "$TOOL_ARGS" == *"_test.go" ]] || [[ "$TOOL_ARGS" == *"test_"*".py" ]] || [[ "$TOOL_ARGS" == *".test.js" ]] || [[ "$TOOL_ARGS" == *"test."*".ts" ]]; then
    echo "ERROR: Editing test files is not allowed"
    exit 2
  fi
fi

# Block editing CI config
if [[ "$TOOL_NAME" == "write" ]] || [[ "$TOOL_NAME" == "edit" ]]; then
  if [[ "$TOOL_ARGS" == *".github/workflows/"* ]] || [[ "$TOOL_ARGS" == *".gitlab-ci.yml" ]] || [[ "$TOOL_ARGS" == *"Jenkinsfile" ]] || [[ "$TOOL_ARGS" == *".circleci/"* ]]; then
    echo "ERROR: Editing CI configuration is not allowed"
    exit 2
  fi
fi

# Block editing lint rules
if [[ "$TOOL_NAME" == "write" ]] || [[ "$TOOL_NAME" == "edit" ]]; then
  if [[ "$TOOL_ARGS" == *".eslintrc"* ]] || [[ "$TOOL_ARGS" == *".prettierrc" ]] || [[ "$TOOL_ARGS" == *"pylintrc" ]] || [[ "$TOOL_ARGS" == *".stylelintrc" ]]; then
    echo "ERROR: Editing lint rules is not allowed"
    exit 2
  fi
fi

# Block dangerous shell commands
if [[ "$TOOL_NAME" == "bash" ]] || [[ "$TOOL_NAME" == "shell" ]]; then
  if [[ "$TOOL_ARGS" == *"rm -rf /"* ]] || [[ "$TOOL_ARGS" == *"chmod 777"* ]] || [[ "$TOOL_ARGS" == *"curl "*"|"*"bash"* ]] || [[ "$TOOL_ARGS" == *"wget "*"|"*"bash"* ]]; then
    echo "ERROR: Dangerous shell command detected"
    exit 2
  fi
fi

# Block editing hooks.json itself
if [[ "$TOOL_NAME" == "write" ]] || [[ "$TOOL_NAME" == "edit" ]]; then
  if [[ "$TOOL_ARGS" == *".openhands/hooks.json" ]]; then
    echo "ERROR: Editing hooks.json is not allowed"
    exit 2
  fi
fi

# Allow all other operations
exit 0