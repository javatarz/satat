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

# Domain configuration
variable "satat_domain" {
  description = "Domain name for Satat instance"
  type        = string
}

# Let's Encrypt configuration
variable "satat_email" {
  description = "Email for Let's Encrypt certificate expiry notices"
  type        = string
}

# OAuth2 proxy configuration
variable "oauth2_proxy_client_id" {
  description = "GitHub OAuth2 client ID"
  type        = string
  sensitive   = true
}

variable "oauth2_proxy_client_secret" {
  description = "GitHub OAuth2 client secret"
  type        = string
  sensitive   = true
}

variable "oauth2_proxy_cookie_secret" {
  description = "OAuth2 proxy cookie secret"
  type        = string
  sensitive   = true
}

# Canvas configuration
variable "canvas_api_key" {
  description = "API key for OpenHands Canvas"
  type        = string
  sensitive   = true
}

# Cloudflare DNS configuration (example - uncomment and configure as needed)
# variable "cloudflare_zone_id" {
#   description = "Cloudflare zone ID"
#   type        = string
#   sensitive   = true
# }

# AWS Route53 DNS configuration (example - uncomment and configure as needed)
# variable "route53_zone_id" {
#   description = "Route53 zone ID"
#   type        = string
# }

# AWS Route53 DNS configuration (example - uncomment and configure as needed)
# variable "route53_zone_id" {
#   description = "Route53 zone ID"
#   type        = string
# }