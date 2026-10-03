# Terraform Cloud backend configuration
# T1: Terraform scaffold + TFC workspace

terraform {
  backend "remote" {
    # Configuration will be provided by Terraform Cloud
    # hostname     = "app.terraform.io"
    # organization = "your-organization"
    # workspaces {
    #   name = "satat-infra"
    # }
  }
}