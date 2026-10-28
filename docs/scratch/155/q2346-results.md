# Q2, Q3, Q4, Q6 results (2026-10-28)

Same stack as Q1 plus an OTLP collector (debug exporter) and `litellm_settings.callbacks: ["otel"]`.

## Q2 token never in the lane
- Lane, after a successful `claude -p`: full token shape `sk-ant-oat<nn>-<20+ chars>` found in 0 files, 0 /proc environ/cmdline, 0 env, 0 ps.
  Bare prefix `sk-ant-oat` appears only in the Claude Code binary (string literal) and the probe script itself.
- Responses to the virtual key on 19 routes (info, admin, errors): token 0, token shape 0.
- LiteLLM container logs, Postgres dump, OTLP spans: token 0, shape 0.
- Planted ANTHROPIC_API_KEY with the managed settings: session refused, exit 1 ("unset ANTHROPIC_API_KEY ... then run: claude auth login").

## Q3 per-step key
- models list enforced: claude-opus-5-5 and unknown model -> 403 "not available for this API key".
- max_budget 1e-06: call 1 200, spend landed ~12 s later (batched write), call 2 -> 422 "Budget has been exceeded!". Budget is checked before a call against recorded spend, so in-flight calls in that window can overshoot (soft cap).
- duration 1m: call 1 200, refused after expiry -> 401 "Expired Key".
- /key/delete (master key) -> call after delete 401 "Invalid proxy server token".
- Minting needs proxy admin: a virtual key gets 401 "Only proxy admin can be used to generate, delete, update info for new keys". So the minting component (worker/launcher) holds the master key or a proxy_admin user's key, which also allows model/config changes and reading all spend. Not tested: a narrower minting role.

## Q4 reach
- Lane (internal network, only LiteLLM attached): api.anthropic.com no DNS, 1.1.1.1 no route, db and otel not resolvable, litellm:4000 200.
- Virtual key on admin routes: /key/generate, /key/update, /key/delete, /user/new, /team/new, /config/update, /get/config/callbacks, /global/spend/logs, /spend/logs -> 401; /key/list 403; /model/new 403.
- Allowed with the virtual key: /v1/models, /model/info, /v2/model/info, /key/info (own key), /health (no-op for the wildcard model, no upstream call), /routes. None return the token.

## Q6 observability
- LiteLLM access log: one line per request with status.
- Spend per key in Postgres (LiteLLM_VerificationToken.spend) and /spend/logs (admin): model, tokens, cost, call_type anthropic_messages.
- OTLP: spans Received Proxy Server Request, auth, litellm_request, raw_gen_ai_request, postgres. litellm_request carries key alias and hash, spend, max budget,
  gen_ai cost, usage (incl. cache read/write), user agent, Claude Code session id, requester IP, used_client_oauth_token=false.
- Spans include full prompts and responses by default (gen_ai.input.messages / output.messages): useful for #82, but any secret in the repo the agent reads ends up in traces.
