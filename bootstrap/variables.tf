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
