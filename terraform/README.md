# Satat Terraform Infrastructure

This directory contains the Terraform configuration for Satat infrastructure.

## Setup

1. Create a Terraform Cloud account at https://app.terraform.io
2. Create a workspace linked to this repository
3. Configure the workspace for VCS-driven runs
4. Add the following sensitive variables to the workspace:
  - `contabo_client_id`
  - `contabo_client_secret`
  - `contabo_api_user`
  - `contabo_api_password`

## Usage

The infrastructure is managed through Terraform Cloud. Changes are planned on PR and applied on merge to main.

Local development:
```bash
# Initialize (requires TFC token in ~/.terraformrc)
terraform init

# Plan (uses TFC remote backend)
terraform plan
```
