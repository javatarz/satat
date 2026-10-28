# Q1 results (2026-10-28)

- Setup: LiteLLM 1.106.4 (docker.io/litellm/litellm:latest), config `model_name: "*"` -> `anthropic/*`, `api_key: os.environ/CLAUDE_OAUTH_TOKEN` (sk-ant-oat token from Keychain via env, Q5 replaces with OpenBao).
  Lane: node 22 + claude-code 2.1.301, Satat managed settings, lane network internal (no DNS for api.anthropic.com).
- Lane env: CLAUDE_CODE_OAUTH_TOKEN=<LiteLLM virtual key>. `claude auth status`: authMethod oauth_token, apiProvider firstParty.
- First run refused by managed settings: allowedProviders ["anthropic"] blocks a custom ANTHROPIC_BASE_URL.
  Fix (docs llm-gateway): allowedProviders ["customEndpoint"] + env.ANTHROPIC_BASE_URL pinned in managed-settings.json. forceLoginMethod claudeai kept.
- Result: `claude -p` succeeded, result "proxy-ok", model claude-sonnet-5-5. LiteLLM: HEAD /api/hello 404 (x2, harmless), POST /v1/messages?beta=true 200.
  Key spend 0.109376 recorded (after LiteLLM's batched spend write), spend log call_type anthropic_messages, matches CLI total_cost_usd.
- Client headers (listener stand-in): Authorization: Bearer <virtual key>; anthropic-beta includes claude-code-20250219, oauth-2025-04-20 (+ others);
  anthropic-dangerous-direct-browser-access: true; anthropic-version 2023-06-01; x-app: cli; User-Agent claude-cli/2.1.301 (external, sdk-cli).
- Upstream: LiteLLM sees api_key prefix sk-ant-oat and sends Authorization: Bearer <token>, adds anthropic-beta oauth-2025-04-20 and
  anthropic-dangerous-direct-browser-access: true (litellm/llms/anthropic/common_utils.py optionally_handle_anthropic_oauth / _credential_headers). Not captured on the wire (would log the token).
