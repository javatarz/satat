# Satat Gateway Configuration

This directory contains the nginx reverse proxy and monitoring service configurations for Satat.

## Files

- `nginx.conf.tmpl` - Nginx configuration template with placeholders for domain and email
- `alloy-config.alloy.tmpl` - Grafana Alloy configuration template with placeholders for Grafana Cloud credentials

## Template Variables

The following variables are used in the configuration templates:

### Nginx Configuration
- `${SATAT_DOMAIN}` - The domain name for the Satat instance
- `${SATAT_EMAIL}` - The email address for Let's Encrypt certificate expiry notices

### Grafana Alloy Configuration
- `${GRAFANA_CLOUD_OTLP_ENDPOINT}` - The Grafana Cloud OTLP endpoint URL
- `${GRAFANA_CLOUD_API_KEY}` - The Grafana Cloud API key

## Deployment

The configurations are deployed via the GitHub Actions deploy pipeline (T9) which replaces the template variables with actual values from the TFC workspace variables.