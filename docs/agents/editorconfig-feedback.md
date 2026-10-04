# EditorConfig Compliance Feedback for Agents

## Overview

This document provides guidance on how to ensure all files created by agents are EditorConfig compliant and how to get feedback when they're not.

## Pre-commit Hooks

The repository uses pre-commit hooks to automatically check for EditorConfig compliance:

1. **editorconfig-checker** - Checks that all files follow the rules defined in `.editorconfig`

## Common Issues and Fixes

### 1. Missing Final Newline
**Issue**: Files don't end with a newline character
**Fix**: Add a newline at the end of the file

### 2. Trailing Whitespace
**Issue**: Lines end with spaces or tabs
**Fix**: Remove trailing whitespace from lines

### 3. Wrong Indentation
**Issue**: Indentation doesn't follow the configured rules (2 spaces)
**Fix**: Use multiples of 2 spaces for indentation

## Automated Checking

Agents can run the following command to check EditorConfig compliance:

```bash
./scripts/check-editorconfig.sh
```

This script will:
1. Run the editorconfig-checker on all files
2. Provide clear feedback on any issues found
3. Suggest ways to fix the issues

## Integration with Agent Workflow

When creating new files, agents should:
1. Always ensure files end with a newline
2. Remove trailing whitespace
3. Follow the indentation rules (2 spaces)
4. Run the editorconfig check before committing

## Pre-commit Hook for Development

The repository includes a pre-commit configuration that automatically checks for EditorConfig compliance. To install and use it:

```bash
# Install pre-commit (if not already installed)
pip install pre-commit

# Install the git hooks
pre-commit install

# Run on all files to test
pre-commit run --all-files
```

After installation, the pre-commit hook will automatically run on every commit and prevent commits that have EditorConfig violations.

## Manual Fix Commands

For common issues, these commands can help:

```bash
# Add newlines to files missing them
find . -type f -name "*.tf" -exec sh -c 'tail -c1 {} | read -r _ || echo >> {}' \;

# Remove trailing whitespace
sed -i 's/[[:space:]]*$//' filename

# Fix indentation (be careful with this one)
# Use an editorconfig-aware formatter instead
```

## EditorConfig Rules

The current `.editorconfig` file specifies:
- 2 space indentation for all files
- LF line endings
- UTF-8 charset
- Trim trailing whitespace (except for Markdown files)
- Insert final newline
- Tab indentation for Makefiles

## Testing Your Changes

Before submitting a PR, always run:

```bash
# Check editorconfig compliance
pre-commit run editorconfig-checker --files path/to/your/files

# Or check all files
./scripts/check-editorconfig.sh
```

This will ensure your changes meet the repository's formatting standards.

## For Repository Maintainers

To automatically fix some EditorConfig issues:

```bash
# Install editorconfig tools
npm install -g editorconfig

# Auto-fix files (use with caution)
editorconfig-format path/to/file
```

Note: Auto-formatting should be used carefully as it may change more than intended.