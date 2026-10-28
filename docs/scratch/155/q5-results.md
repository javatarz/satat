# Q5 results (2026-10-28)

- OpenBao 2.7.2 dev server, KV v2 at `satat/`, entry `claude-oauth-token`, policy `litellm` = read on that entry only.
  AppRole `litellm` with secret_id_bound_cidrs and token_bound_cidrs = LiteLLM's fixed address on the internal `secrets` network.
  role_id and secret_id written to a volume mounted only into LiteLLM; secret_id piped from OpenBao into the volume, never printed.
- LiteLLM's built-in Hashicorp Vault secret manager is premium-only ("only available for premium users"), so not used.
- OpenBao Agent (bao binary copied into a LiteLLM-derived image) in process-supervisor mode: auto_auth approle, env_template CLAUDE_OAUTH_TOKEN,
  exec LiteLLM's entrypoint with restart_on_secret_changes=always, static_secret_render_interval 10s.
- Result: agent logs in, starts LiteLLM, real call 200. Token in docker inspect 0, image history 0, agent (pid 1) env 0, LiteLLM child env 1 (expected).
  Improvement over Q1, where the token was in the container's env and visible to `docker inspect`.
- Rotation: kv put a dummy value -> LiteLLM restarted after 6 s, serving 11 s later, call 401 (dummy reached upstream). kv put the real value -> restart after 11 s,
  serving 12 s later, call 200. No rebuild, no container restart. Gap ~20 s; in-flight requests are cut by the SIGTERM restart.
- Address binding: same role_id/secret_id from another address on the secrets network -> 400, source address refused.
- Policy: LiteLLM's token reads its entry; other entry, list, policy read, write all denied.
- Lane cannot reach openbao (not on the secrets network).
