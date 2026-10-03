# Satat infrastructure variables
# T1: Terraform scaffold + TFC workspace

# Contabo API credentials
variable "contabo_client_id" {
  description = "Contabo API Client ID"
  type        = string
  sensitive   = true
}

variable "contabo_client_secret" {
  description = "Contabo API Client Secret"
  type        = string
  sensitive   = true
}

variable "contabo_api_user" {
  description = "Contabo API User"
  type        = string
  sensitive   = true
}

variable "contabo_api_password" {
  description = "Contabo API Password"
  type        = string
  sensitive   = true
}