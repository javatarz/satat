# Open-source gateways that accept both OpenAI and Anthropic protocols

Researched 2026-11-07, for [ADR 0034](../../adr/0034-llm-gateway-holds-model-credentials.md) (#155).
Claude Code speaks only Anthropic's protocol (`POST /v1/messages`, set with `ANTHROPIC_BASE_URL`).
Question: which gateways are open source, self-hostable, accept OpenAI input, and accept Anthropic
input?

Method: a subagent read each project's docs and source (route handlers, schemas, licence files) on
GitHub. **No gateway was run with Claude Code.** Streaming, tool-use and prompt-caching claims come
from each project's own docs and tests.

## Result

Seven meet all four requirements.

| Gateway | Licence | Translates Anthropic input to other providers? | Caveat |
|---|---|---|---|
| LiteLLM | MIT; `enterprise/` dir under a separate licence | Yes, any provider | `/v1/messages` is tagged beta. Some admin features are enterprise. |
| Bifrost | Apache-2.0 | Yes (provider-prefixed model) | Clustering, guardrails, MCP gateway, OIDC are enterprise. |
| agentgateway | Apache-2.0 | Yes (to Chat Completions, Responses, Bedrock) | Conversion to Responses drops `stop_sequences`, `top_k`, server tools, thinking history. Claude Code also calls `/v1/models`, so add a passthrough route. |
| Envoy AI Gateway | Apache-2.0 | Yes (to OpenAI, Bedrock, GCP Anthropic) | Needs Kubernetes and Envoy Gateway. |
| Higress | Apache-2.0 | Yes (falls back to OpenAI conversion) | Docs are Chinese-first. |
| Apache APISIX `ai-proxy` | Apache-2.0 | Anthropic and OpenAI only | Verified on `master`. CHANGELOG suggests it shipped in 3.19.0 (2026-09-28); the tag was not opened. |
| Portkey gateway (OSS) | MIT | Only to Claude-hosting providers (Anthropic, Bedrock, Vertex, Azure) | Coverage inferred from which providers ship a `messages.ts`. Last `main` commit 2026-05-25; "Gateway 2.0" work is on a `2.0.0` branch (not checked). Dashboard and guardrails are hosted. |

Evidence (permalinks, read this session):

- LiteLLM: [`/v1/messages`](https://github.com/BerriAI/litellm/blob/5103d8a0ee88bfa2f5118c40f0b90fcf239c7f5f/litellm/proxy/anthropic_endpoints/endpoints.py#L130),
  [`/v1/messages/count_tokens`](https://github.com/BerriAI/litellm/blob/5103d8a0ee88bfa2f5118c40f0b90fcf239c7f5f/litellm/proxy/anthropic_endpoints/endpoints.py#L321);
  <https://docs.litellm.ai/docs/anthropic_unified>
- Bifrost: [Anthropic route config](https://github.com/maximhq/bifrost/blob/03ead1958847eaa50158309331773b21d897e4b2/transports/bifrost-http/integrations/anthropic.go#L119-L124)
- agentgateway: [`RouteType::Messages` / `InputFormat::Messages`](https://github.com/agentgateway/agentgateway/blob/d327d10bc92bb5459482a19a79d0b3b6f83c3636/crates/llm/src/lib.rs#L144-L156);
  <https://agentgateway.dev/docs/standalone/latest/documentation/llm/api-types/messages/>
- Envoy AI Gateway: [supported endpoints](https://github.com/envoyproxy/ai-gateway/blob/d41f08217b8d8c362f85f0d5094b2fd072fbb4c4/site/docs/capabilities/llm-integrations/supported-endpoints.md?plain=1#L62),
  [Anthropic-to-OpenAI translator](https://github.com/envoyproxy/ai-gateway/blob/d41f08217b8d8c362f85f0d5094b2fd072fbb4c4/internal/translator/anthropic_openai.go)
- Higress: [`ai-proxy` README, protocol auto-detection](https://github.com/higress-group/higress/blob/6e004226ffbaca782f8aaf5c5abb4ee5f736a3ef/plugins/wasm-go/extensions/ai-proxy/README.md?plain=1#L17-L25)
- APISIX: [protocol-conversion tests](https://github.com/apache/apisix/blob/4a9d5d5679eeb52fb9d0d7a753eb1ead84adb509/t/plugin/ai-proxy-protocol-conversion.t#L42),
  [`ai-proxy` docs](https://github.com/apache/apisix/blob/4a9d5d5679eeb52fb9d0d7a753eb1ead84adb509/docs/en/latest/plugins/ai-proxy.md?plain=1#L91)
- Portkey: [`/v1/messages` route](https://github.com/Portkey-AI/gateway/blob/669825cbe89ee51569918b8f78a9db486fd69dd4/src/index.ts#L133-L135)

## Fail requirement 4 (no Anthropic input)

- **Kong AI Gateway (OSS):** `llm_format` is `openai`, `bedrock` or `gemini`. Anthropic-format input
  appears to be in the enterprise `ai-proxy-advanced` plugin. The `preserve` route type passes
  bodies through, but this is undocumented for Anthropic and was not tested.
- **TensorZero:** OpenAI-compatible endpoint only. Repo archived 2026-06-12.
- **openziti/llm-gateway:** registers only `/v1/models`, `/v1/chat/completions`, `/health`.

## Unclear

- **Helicone Cloudflare worker:** passes `/v1/messages` through to `api.anthropic.com` only. Its
  self-host docs say LLM proxying was removed from Jawn, so self-hosting is unclear.
- **Helicone/ai-gateway (Rust):** GPL-3.0 (the README badge says Apache). Last commit 2025-11-21,
  "Public Beta". Anthropic `v1/messages` appears only as an upstream target.
- Hosted-only gateways (Cloudflare AI Gateway, LLM Gateway, ngrok AI Gateway, Respan) fail
  self-hosting and were excluded.

## What this does not cover

ADR 0034 needs more than "accepts `/v1/messages`". It uses an Anthropic **pass-through** route that
forwards Claude Code's request and headers unchanged and swaps only the credential, so the
subscription rate-limit headers reach Claude Code (#171). This survey did not check pass-through
behaviour or header fidelity for any gateway other than LiteLLM (whose behaviour is in the
[spike](claude-gateway-proxy-spike.md)). A replacement for LiteLLM must be checked against that
requirement, not only the four above.

## Not verified

- No gateway was run end to end with Claude Code.
- Portkey's Anthropic-input provider coverage (inferred), APISIX release version, Kong `preserve`
  behaviour with Anthropic bodies.
