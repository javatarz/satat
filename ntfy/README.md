# Satat ntfy Configuration

This directory contains the ntfy server configuration for Satat.

## Files

- `server.yml.tmpl` - ntfy server configuration template with placeholders for base URL

## Template Variables

The following variables are used in the configuration templates:

- `${NTFY_BASE_URL}` - The base URL for the ntfy server (e.g. https://ntfy.sh)

## Deployment

The configuration is deployed via the GitHub Actions deploy pipeline (T9) which replaces the template variables with actual values from the TFC workspace variables.