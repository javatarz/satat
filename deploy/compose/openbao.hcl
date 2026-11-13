# OpenBao server config for Compose (ADR 0032). It holds no component policy and no role: those are
# store state, written by the secrets-apply service from deploy/secrets/policies.
ui            = false
api_addr      = "http://openbao:8200"
cluster_addr  = "http://openbao:8201"

storage "raft" {
  path    = "/openbao/file"
  node_id = "satat-1"
}

# Plain HTTP on the internal secrets network only; nothing publishes the port.
listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}

# Unseals itself at start from the key scripts/secret-store.sh writes into the bao-seal volume.
seal "static" {
  current_key_id = "satat-1"
  current_key    = "file:///openbao/seal/unseal.key"
}

# Self-init: runs once, on the first start with empty storage, then OpenBao revokes the root
# token. A change here does nothing to a store that exists. scripts/secret-store.sh writes the
# files it reads into bao-seal before that start.
initialize "satat" {
  request "kv" {
    operation = "update"
    path      = "sys/mounts/satat"
    data      = { type = "kv", options = { version = "2" } }
  }
  request "approle" {
    operation = "update"
    path      = "sys/auth/approle"
    data      = { type = "approle" }
  }
  request "userpass" {
    operation = "update"
    path      = "sys/auth/userpass"
    data      = { type = "userpass" }
  }
  # The policies of the operator and doctor logins, from deploy/secrets/system.
  request "operator-policy" {
    operation = "update"
    path      = "sys/policies/acl/operator"
    data      = { policy = { eval_source = "file", eval_type = "string", path = "/openbao/policies/operator.hcl" } }
  }
  request "doctor-policy" {
    operation = "update"
    path      = "sys/policies/acl/doctor"
    data      = { policy = { eval_source = "file", eval_type = "string", path = "/openbao/policies/doctor.hcl" } }
  }
  # Both log in only from loopback inside the OpenBao container (docker compose exec, or
  # secrets-apply in its network namespace). doctor's password sits beside the seal key, where
  # whoever can read it can read the seal key. Short token TTLs: secrets-apply never revokes its
  # login token.
  request "operator" {
    operation = "update"
    path      = "auth/userpass/users/operator"
    data = {
      password          = { eval_source = "file", eval_type = "string", path = "/openbao/seal/operator-password" }
      token_policies    = ["operator"]
      token_ttl         = "10m"
      token_bound_cidrs = ["127.0.0.1/32"]
    }
  }
  request "doctor" {
    operation = "update"
    path      = "auth/userpass/users/doctor"
    data = {
      password          = { eval_source = "file", eval_type = "string", path = "/openbao/seal/doctor-password" }
      token_policies    = ["doctor"]
      token_ttl         = "1m"
      token_bound_cidrs = ["127.0.0.1/32"]
    }
  }
}
