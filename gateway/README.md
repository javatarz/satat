# Satat Gateway Configuration

This directory contains the nginx reverse proxy, oauth2-proxy, Headscale WireGuard, and other service configurations for Satat.

## Files

- `nginx.conf.tmpl` - Nginx configuration template with placeholders for domain and email
- `oauth2-proxy.yaml.tmpl` - OAuth2 proxy configuration template with placeholders for GitHub OAuth credentials
- `headscale-config.yaml.tmpl` - Headscale configuration template with placeholders for domain and email

## Template Variables

The following variables are used in the configuration templates:

### Nginx Configuration
- `${SATAT_DOMAIN}` - The domain name for the Satat instance
- `${SATAT_EMAIL}` - The email address for Let's Encrypt certificate expiry notices

### OAuth2 Proxy Configuration
- `${OAUTH2_PROXY_CLIENT_ID}` - The GitHub OAuth2 client ID
- `${OAUTH2_PROXY_CLIENT_SECRET}` - The GitHub OAuth2 client secret
- `${OAUTH2_PROXY_COOKIE_SECRET}` - The OAuth2 proxy cookie secret

### Headscale Configuration
- `${SATAT_DOMAIN}` - The domain name for the Satat instance
- `${SATAT_EMAIL}` - The email address for Let's Encrypt certificate expiry notices

## Deployment

The configurations are deployed via the GitHub Actions deploy pipeline (T9) which replaces the template variables with actual values from the TFC workspace variables.