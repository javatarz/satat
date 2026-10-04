# Satat VM instance
# T2: VM + firewall + DNS + cloud-init bootstrap

resource "contabo_firewall" "satat" {
  name        = "satat-firewall"
  description = "Firewall for Satat instance - allow HTTPS only"

  rules {
    # Allow HTTPS from anywhere
    direction = "ingress"
    protocol  = "tcp"
    port      = "443"
    source    = "0.0.0.0/0"
    action    = "allow"
  }

  rules {
    # Deny all other ingress
    direction = "ingress"
    protocol  = "any"
    action    = "deny"
  }

  # Allow all egress
  rules {
    direction = "egress"
    protocol  = "any"
    action    = "allow"
  }
}

resource "contabo_instance" "satat" {
  # Core VPS 4: 4 vCPU, 8 GB RAM, 100 GB SSD
  image_id    = "Ubuntu-24.04" # Ubuntu 24.04 LTS
  product_id  = "VPS-4"        # Core VPS 4
  region      = "US-central"   # US Central region
  ssh_keys    = []             # No SSH keys - will use cloud-init
  firewall_id = contabo_firewall.satat.id
  user_data = base64encode(templatefile("${path.module}/cloud-init.yml.tftpl", {
    domain = var.satat_domain
  }))
}

# Output the public IP for DNS configuration
output "instance_public_ip" {
  value = contabo_instance.satat.ip_config[0].ip
}
