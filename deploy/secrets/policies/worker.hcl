# The worker's policy (ADR 0032): read on named entries, nothing else. deploy/secrets/tofu applies
# it on just run and just up, and refuses any other kind of line.
path "satat/data/github-read-token" { capabilities = ["read"] }
# Until the pusher is its own service (#240).
path "satat/data/github-push-token" { capabilities = ["read"] }
# Until the LLM gateway holds it (#238).
path "satat/data/claude-oauth-token" { capabilities = ["read"] }
