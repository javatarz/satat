variable "region" {
  description = "AWS region for all resources"
  type        = string
  default     = "ap-south-1"
}

variable "instance_type" {
  description = "EC2 instance type (ARM/Graviton)"
  type        = string
  default     = "t4g.large"
}

variable "root_volume_size" {
  description = "Root EBS volume size in GiB"
  type        = number
  default     = 100
}

variable "deploy_wg_public_key" {
  description = "WireGuard public key of the `ci` peer used by the deploy pipeline (10.10.0.3)"
  type        = string
}

variable "laptop_wg_public_key" {
  description = "WireGuard public key of the `laptop` peer used by the owner (10.10.0.2)"
  type        = string
}

variable "deploy_ssh_public_key" {
  description = "SSH public key authorized for the `deploy` user (pipeline)"
  type        = string
}

variable "owner_ssh_public_key" {
  description = "SSH public key authorized for the `deploy` user (owner)"
  type        = string
}
