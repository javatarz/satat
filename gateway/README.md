# Satat Gateway Configuration

This directory contains the nginx reverse proxy configuration for Satat.

## Files

- `nginx.conf.tmpl` - Nginx configuration template with placeholders for domain and email

## Template Variables

The following variables are used in the nginx configuration template:

- `${SATAT_DOMAIN}` - The domain name for the Satat instance
- `${SATAT_EMAIL}` - The email address for Let's Encrypt certificate expiry notices

## Deployment

The nginx configuration is deployed via the GitHub Actions deploy pipeline (T9) which replaces the template variables with actual values from the TFC workspace variables.