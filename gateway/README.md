# Satat Gateway Configuration

This directory contains the nginx reverse proxy, LiteLLM proxy, and OpenHands Canvas configurations for Satat.

## Files

- `nginx.conf.tmpl` - Nginx configuration template with placeholders for domain and email
- `litellm-config.yaml.tmpl` - LiteLLM configuration template with placeholders for model IDs and API key
- `canvas-config.yaml.tmpl` - OpenHands Canvas configuration template with placeholders for API key

## Template Variables

The following variables are used in the configuration templates:

### Nginx Configuration
- `${SATAT_DOMAIN}` - The domain name for the Satat instance
- `${SATAT_EMAIL}` - The email address for Let's Encrypt certificate expiry notices
- `${CANVAS_API_KEY}` - The API key for OpenHands Canvas

### LiteLLM Configuration
- `${LLMGATEWAY_API_KEY}` - The LLM Gateway API key
- `${SATAT_MODEL_CHEAP}` - The model ID for the cheap tier
- `${SATAT_MODEL_STANDARD}` - The model ID for the standard tier
- `${SATAT_MODEL_EXPENSIVE}` - The model ID for the expensive tier

### Canvas Configuration
- `${CANVAS_API_KEY}` - The API key for OpenHands Canvas

## Deployment

The configurations are deployed via the GitHub Actions deploy pipeline (T9) which replaces the template variables with actual values from the TFC workspace variables.