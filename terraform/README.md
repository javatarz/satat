# Satat Terraform Infrastructure

This directory contains the Terraform configuration for Satat infrastructure.

## Setup

1. Create a Terraform Cloud account at https://app.terraform.io
2. Create a workspace linked to this repository
3. Configure the workspace for VCS-driven runs
4. Add the Contabo credentials as sensitive **environment variables** to
   the workspace (the Contabo provider reads them from the environment):
   - `CONTABO_CLIENT_ID`
   - `CONTABO_CLIENT_SECRET`
   - `CONTABO_API_USER`
   - `CONTABO_API_PASSWORD`

## Usage

The infrastructure is managed through Terraform Cloud. Changes are planned on PR and applied on merge to main.

Local development:
```bash
# Initialize (requires TFC token in ~/.terraformrc)
terraform init

# Plan (uses TFC remote backend)
terraform plan
```

