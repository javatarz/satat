# Satat Automations

This directory contains the automation configurations for Satat.

## Files

- `satat-automation.yaml.tmpl` - Automation configuration template with placeholders for GitHub App credentials and webhook secret

## Template Variables

The following variables are used in the configuration templates:

- `${TARGET_REPOSITORY}` - The target repository for the automation
- `${TARGET_USER}` - The target user/organization for the automation
- `${GITHUB_APP_ID}` - The GitHub App ID
- `${GITHUB_APP_PRIVATE_KEY}` - The GitHub App private key
- `${WEBHOOK_SECRET}` - The webhook secret
- `${ORACLE_QUESTION}` - The oracle question for story refinement

## Deployment

The configurations are deployed via the GitHub Actions deploy pipeline (T9) which replaces the template variables with actual values from the TFC workspace variables.