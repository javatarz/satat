# Satat infrastructure variables
# T1: Terraform scaffold + TFC workspace

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

# LLM Gateway configuration
variable "llmgateway_api_key" {
  description = "LLM Gateway API key (format: llmgtwy_XXXX)"
  type        = string
  sensitive   = true
}

variable "satat_model_cheap" {
  description = "Cheap model ID for LLM Gateway"
  type        = string
}

variable "satat_model_standard" {
  description = "Standard model ID for LLM Gateway"
  type        = string
}

variable "satat_model_expensive" {
  description = "Expensive model ID for LLM Gateway"
  type        = string
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