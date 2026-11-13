# The operator writes values, policies and roles, and cannot read a value back.
path "satat/data/*"        { capabilities = ["create", "update"] }
path "sys/policies/acl/*"  { capabilities = ["create", "read", "update", "delete"] }
path "auth/approle/role/*" { capabilities = ["create", "read", "update", "delete"] }
