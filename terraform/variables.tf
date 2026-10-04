# Satat infrastructure variables
# T1: Terraform scaffold + TFC workspace

# Domain configuration
variable "satat_domain" {
  description = "Domain name for Satat instance"
  type        = string
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
