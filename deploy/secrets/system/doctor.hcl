# doctor lists entry names, policies and roles, never values.
path "satat/metadata"     { capabilities = ["list"] }
path "sys/policies/acl"   { capabilities = ["list"] }
path "sys/policies/acl/*" { capabilities = ["read"] }
path "auth/approle/role"  { capabilities = ["list"] }
# For the secrets-apply plan and scripts/secret-store.sh: each role and its role_id, never a
# secret_id.
path "auth/approle/role/+"         { capabilities = ["read"] }
path "auth/approle/role/+/role-id" { capabilities = ["read"] }
