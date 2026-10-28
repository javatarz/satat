# Follow-up checks (2026-10-28)

## What LiteLLM sends upstream (fake token, header listener)
- /v1/messages route (`anthropic/*` model): Authorization: Bearer <token>, anthropic-beta = client betas merged and sorted (oauth-2025-04-20 kept),
  anthropic-dangerous-direct-browser-access: true. Rewrites user-agent to litellm/1.106.4, drops x-app and X-Claude-Code-Session-Id. Body rebuilt (keys: max_tokens, messages, model, stream, system).
- Response: anthropic-ratelimit-unified-* dropped (plain) or renamed llm_provider-* (stream). 429: status and rate_limit_error kept, retry-after and unified headers dropped, LiteLLM retries 2x (3 upstream calls).
- Claude Code on this route emits a bare rate_limit_event: {"status":"allowed","isUsingOverage":false}, no type, reset or utilization.

## /anthropic pass-through route (ANTHROPIC_API_KEY = token via the same OpenBao env_template)
- Forwards client headers verbatim (user-agent claude-cli/..., x-app cli, session id, betas), replaces only Authorization with the server token. Body untouched.
- Response headers verbatim incl. anthropic-ratelimit-unified-*; 429 with retry-after and unified headers verbatim, no retries.
- Real token: `claude -p` with ANTHROPIC_BASE_URL=http://litellm:4000/anthropic (pinned in managed settings) works; multi-turn tool use (Write, Bash) works, 3 turns.
  Full rate_limit_event (rateLimitType five_hour, resetsAt, utilization, overage status).
- Key models list, max_budget, deletion enforced; spend tracked (call_type pass_through_endpoint).
- Forwards any path under /anthropic/* with the token: /v1/models 200; /api/oauth/profile, usage, claude_cli/roles 403 (token lacks user:profile); /v1/organizations/me 403.
  Key `allowed_routes: ["/anthropic/v1/messages"]` blocks every other path (403), claude -p still works (HEAD /api/hello gets 403/405, harmless).

## Narrower minting identity
- LiteLLM internal_user `satat-minter` (user max_budget, user models list) with its own key:
  can /key/generate and /key/delete its own keys only (other user_id -> 403), cannot /user/new, /model/new, /config/update (401/403); /spend/logs and /key/list show only its own.
- Minting accepts wider budget/models than the user's, but calls are capped by the user's models (403) and total budget (422 "User=satat-minter over budget").
- The minter key can itself call models within the user's limits.

## Traces without prompts
- Spans carried full prompts on both routes by default. `litellm_settings.turn_off_message_logging: true` removes gen_ai.input/output.messages on both; key alias and cost stay. Spend logs never held prompts.

## Side finding (#174)
- Claude Code 2.1.301 warns that the lane's managed `Write(...)` deny rules are never matched ("only Edit(path) rules are. Use Edit(...) instead").

## Not done
- Budget overshoot size under concurrent calls (mechanism known: checked before each call, spend written ~12 s later).

## Does /v1/messages draw from the five-hour plan window? (real token, streamed curl, Haiku 4.5, 16 max tokens)
- /v1/messages with Claude Code headers and system prompt: 200 "ok". Renamed headers report
  `representative-claim: five_hour`, `5h-utilization: 0.03`, `7d-utilization: 0.01`, `unified-status: allowed`,
  `overage-status: rejected`, `overage-disabled-reason: org_level_disabled`.
- /anthropic pass-through, same request (control): identical values under the original header names.
- /v1/messages bare (no system prompt, no x-app, LiteLLM's own user-agent): 200 "ok", same five_hour claim, 0.03, overage rejected.
- So requests on both routes count against the plan's five-hour window; with overage off nothing could bill as extra usage.
  A trivial request is not classed as third-party even with no Claude Code identity at all. Hermes' classifier
  evidence is about specific tool names and prompt wording, which these tiny requests don't exercise; a full
  Claude Code session on /v1/messages was not re-measured.
- Stack stopped with `down -v` afterwards.

## Gap closing (2026-10-28, second run)
- Worker as minter: operator creates internal_user satat-minter (max_budget 2, models list), its key piped into OpenBao
  `litellm-minter-key`; policy `worker` reads only that; AppRole `worker` bound to 172.31.155.20. A container at that address
  logs in, cannot read claude-oauth-token (denied), reads the minter key, mints a step key, lane `claude -p` via /anthropic -> "worker-ok";
  worker deletes the key -> next call 401 "Invalid proxy server token". Same AppRole from 172.31.155.21 -> refused (source address).
- A non-admin minter cannot set allowed_routes: 403 "Only proxy admins can set `allowed_routes` on a key. Use `key_type` to pick a preset
  route bucket instead." key_type llm_api works; such a key reaches any /anthropic/* path: /v1/models 200, /api/oauth/profile 403,
  /v1/organizations/me 403, /v1/files 403 (token scope), /key/generate 405.
- Budget overshoot, /anthropic, Haiku 4.5, ~$0.0015 per call, N concurrent then N more after spend landed:
  N=20 B=0.002: 2 x 200, 18 x 422, spend 0.003038; wave 2 all 422.
  N=20 B=0.01: 7 x 200, 13 x 422, spend 0.010633; wave 2 all 422.
  N=40 B=0.005: 4 x 200, 36 x 422, spend 0.006076; wave 2 all 422.
  Overshoot 0.0006 to 0.0011, under one call's cost. Mechanism not read.
- Full Claude Code session on /v1/messages (lane image, Sonnet 5.5): 3 turns, tool use (Write, Bash), 6 upstream calls in spend logs,
  no error, $0.0144. Overage is disabled for the org, so a call classed as extra usage would have been refused; none was.
  Bash itself was refused inside the lane (not investigated; unrelated to the proxy).
