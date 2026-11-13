# The store's component policies and AppRole roles (ADR 0032). One policy file in
# deploy/secrets/policies is one component. scripts/secret-store.sh runs it in the secrets-apply
# service: a plan as doctor, an apply as the operator. The state holds no secret: each role_id, but
# no secret_id. scripts/secret-store.sh issues the secret_ids. The state lives in bao-seal.
terraform {
  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "5.13.0"
    }
  }
  backend "local" {
    path = "/v/bao-seal/tofu/terraform.tfstate"
  }
}

# Each policy file's content, by component name (the file name without .hcl).
variable "policies" {
  type = map(string)
}

# Each component's fixed address on the secrets network, from compose.yaml.
variable "addresses" {
  type = map(string)
}

variable "username" {
  type = string
}

# The provider ignores password_file, so the caller reads the file into TF_VAR_password.
variable "password" {
  type      = string
  sensitive = true
}

provider "vault" {
  address          = "http://127.0.0.1:8200"
  skip_child_token = true
  auth_login_userpass {
    username = var.username
    password = var.password
  }
}

locals {
  # Each policy's lines, without blanks and # comments.
  lines = { for c, p in var.policies : c => compact([for l in split("\n", p) : trimspace(replace(l, "/#.*/", ""))]) }
  # The only line a component policy may have.
  read_line = "^path \"satat/data/[a-z][a-z0-9-]*\" \\{ capabilities = \\[\"read\"\\] \\}$"
}

resource "vault_policy" "component" {
  for_each = var.policies
  name     = each.key
  policy   = each.value
  lifecycle {
    precondition {
      condition     = can(regex("^[a-z][a-z0-9-]*$", each.key)) && !contains(["operator", "doctor", "default", "root"], each.key)
      error_message = "${each.key}.hcl: not a component name (lower case letters, digits and -)."
    }
    precondition {
      condition     = length(local.lines[each.key]) > 0 && alltrue([for l in local.lines[each.key] : can(regex(local.read_line, l))])
      error_message = "${each.key}.hcl: grants more than read on named entries."
    }
    precondition {
      condition     = contains(keys(var.addresses), each.key)
      error_message = "${each.key}: needs an address on the secrets network in compose.yaml."
    }
  }
}

resource "vault_approle_auth_backend_role" "component" {
  for_each              = var.policies
  backend               = "approle"
  role_name             = each.key
  token_policies        = [vault_policy.component[each.key].name]
  token_ttl             = 3600
  secret_id_bound_cidrs = ["${var.addresses[each.key]}/32"]
  token_bound_cidrs     = ["${var.addresses[each.key]}/32"]
}
