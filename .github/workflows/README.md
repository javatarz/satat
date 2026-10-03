# Satat GitHub Actions Workflows

This directory contains the GitHub Actions workflows for Satat.

## Files

- `deploy.yml` - Deploy configuration to VM workflow

## Workflow Details

The deploy workflow is triggered on pushes to the main branch that modify files in the gateway/, ntfy/, or automations/ directories. It:

1. Sets up a WireGuard connection to the VM
2. Renders template files with GitHub Secrets
3. Deploys the rendered configs to the VM via SCP
4. Restarts affected services
5. Cleans up the WireGuard connection
6. Notifies via ntfy on failure

## Required GitHub Secrets

The following secrets must be configured in the GitHub repository:

- `SATAT_DOMAIN` - The domain name for the Satat instance
- `SATAT_EMAIL` - The email for Let's Encrypt certificate expiry notices
- `LLMGATEWAY_API_KEY` - The LLM Gateway API key
- `SATAT_MODEL_CHEAP` - The cheap model ID for LLM Gateway
- `SATAT_MODEL_STANDARD` - The standard model ID for LLM Gateway
- `SATAT_MODEL_EXPENSIVE` - The expensive model ID for LLM Gateway
- `CANVAS_API_KEY` - The API key for OpenHands Canvas
- `OAUTH2_PROXY_CLIENT_ID` - The GitHub OAuth2 client ID
- `OAUTH2_PROXY_CLIENT_SECRET` - The GitHub OAuth2 client secret
- `OAUTH2_PROXY_COOKIE_SECRET` - The OAuth2 proxy cookie secret
- `GRAFANA_CLOUD_OTLP_ENDPOINT` - The Grafana Cloud OTLP endpoint URL
- `GRAFANA_CLOUD_API_KEY` - The Grafana Cloud API key
- `HEALTHCHECKS_PING_URL` - The Healthchecks.io ping URL
- `NTFY_BASE_URL` - The ntfy base URL
- `GITHUB_APP_ID` - The GitHub App ID
- `GITHUB_APP_PRIVATE_KEY` - The GitHub App private key
- `WEBHOOK_SECRET` - The webhook secret
- `TARGET_REPOSITORY` - The target repository for automation
- `TARGET_USER` - The target user/organization for automation
- `DEPLOY_SSH_PRIVATE_KEY` - The private key for the deploy user on VM
- `DEPLOY_SSH_KNOWN_HOSTS` - The VM host key
- `HEADSCALE_PUBLIC_KEY` - The Headscale public key