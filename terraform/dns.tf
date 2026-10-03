# Satat DNS configuration
# T2: VM + firewall + DNS + cloud-init bootstrap

# Note: DNS provider configuration will be added based on the DNS service used
# This is a placeholder for the DNS A record that points SATAT_DOMAIN to the VM's public IP

# Example for Cloudflare DNS (uncomment and configure as needed):
# resource "cloudflare_record" "satat" {
#   zone_id = var.cloudflare_zone_id
#   name    = var.satat_domain
#   type    = "A"
#   value   = contabo_instance.satat.ip_config[0].ip
#   ttl     = 1
#   proxied = false
# }

# Example for AWS Route53 (uncomment and configure as needed):
# resource "aws_route53_record" "satat" {
#   zone_id = var.route53_zone_id
#   name    = var.satat_domain
#   type    = "A"
#   ttl     = "300"
#   records = [contabo_instance.satat.ip_config[0].ip]
# }