variable "region" {
  description = "AWS region for the state bucket and OIDC role"
  type        = string
  default     = "ap-south-1"
}

variable "github_repository" {
  description = "GitHub repository allowed to assume the Terraform role (owner/name)"
  type        = string
  default     = "javatarz/satat"
}

variable "github_repository_immutable" {
  description = "Immutable owner@owner_id/name@repo_id form GitHub now emits in OIDC `sub` claims"
  type        = string
  default     = "javatarz@911203/satat@1403176068"
}
