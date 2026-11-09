# OpenBao server config for Compose (ADR 0032). It holds no policy and no role: those are store
# state, written by scripts/secret-store.sh, so a change to this file cannot widen who reads what.
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

# Unseals itself at start from the key `just secrets-init` writes into the bao-seal volume.
seal "static" {
  current_key_id = "satat-1"
  current_key    = "file:///openbao/seal/unseal.key"
}
