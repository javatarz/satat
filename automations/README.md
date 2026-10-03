# Satat Automations

This directory contains the automation configurations for Satat.

## Files

- `satat-automation.yaml.tmpl` - Automation configuration template with placeholders for GitHub App credentials and webhook secret
- `.openhands/hooks.json` - OpenHands hooks configuration
- `.openhands/pretooluse-block-dangerous.sh` - PreToolUse hook script
- `.openhands/stop-ci-round-cap.sh` - Stop hook script

## Template Variables

The following variables are used in the configuration templates:

- `${TARGET_REPOSITORY}` - The target repository for the automation
- `${TARGET_USER}` - The target user/organization for the automation
- `${GITHUB_APP_ID}` - The GitHub App ID
- `${GITHUB_APP_PRIVATE_KEY}` - The GitHub App private key
- `${WEBHOOK_SECRET}` - The webhook secret
- `${ORACLE_QUESTION}` - The oracle question for story refinement

## Hooks

The OpenHands hooks enforce safety checks:

### PreToolUse Hook
- Blocks editing test files
- Blocks editing CI configuration
- Blocks editing lint rules
- Blocks dangerous shell commands
- Blocks editing the hooks.json file itself

### Stop Hook
- Enforces a CI-round cap of 3
- Tracks CI failures and allows finish only after 3 failures
- Hands off to human after exceeding the CI-round cap

## Deployment

The configurations are deployed via the GitHub Actions deploy pipeline (T9) which replaces the template variables with actual values from the TFC workspace variables.