# Satat infrastructure
# T1: Terraform scaffold + TFC workspace

terraform {
  required_providers {
    contabo = {
      source  = "contabo/contabo"
      version = "~> 0.1"
    }
  }
  required_version = ">= 1.0"
}

# Provider configuration will be set via TFC variables
provider "contabo" {
  # Credentials are set via environment variables:
  # CONTABO_CLIENT_ID, CONTABO_CLIENT_SECRET, CONTABO_API_USER, CONTABO_API_PASSWORD
}
