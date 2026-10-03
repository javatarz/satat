# ADR 0016: Model selection and provider routing

## Status
Accepted

## Context
Satat needs three LLM roles: a main coding agent, a critic reviewer, and a
future story-refinement validator. We operate on DevPass Lite ($29/mo, $58
included usage after Oct 15 2026 plan changes). We require open-weight models
with privacy guarantees matching or exceeding Anthropic's (no training on API
data, no prompt logging).

## Decision

| Role | Model | Provider | $/M in | $/M out |
|---|---|---|---|---|
| Main agent | `deepseek-v4.1-flash` | SCX.ai | $0.15 | $0.60 |
| Critic | `glm-5.3-flash` | SCX.ai | $0.07 | $0.20 |
| Story refinement | `glm-5.3-flash` | SCX.ai | $0.07 | $0.20 |

All models routed through privacy-safe providers (no training, no logging,
0-day retention). SCX.ai holds SOC 2 Type 1 and ISO 27001 certifications.

### Provider constraints

The "No AI training" DevPass setting is enabled, which rejects requests to
providers that train on API data or log prompts. This eliminated:
- DeepSeek (direct) — trains on API data
- Meta Contributor — trains on data
- Alibaba Cloud — logs prompts
- Xiaomi — logs prompts, 30-day retention

### Backups

| Priority | Model | Provider | $/M in | When to use |
|---|---|---|---|---|
| 1 | `qwen3.8-flash` | NovitaAI | $0.15 | If DeepSeek Flash underperforms |
| 2 | `kimi-k2.7-code` | SCX.ai | $0.76 | If both Flash models insufficient |

### What we rejected

| Alternative | Rejected because |
|---|---|
| Claude Sonnet 5.5 | Not open-weight; $2/$10/M is 13x more expensive |
| GPT-6.1 Sol | Not open-weight; same cost issue |
| Kimi K3 | $2.80/$14.40 would drain $58 budget in ~20 issues |
| Muse Spark 1.3 | Provider (Meta Contributor) trains on API data |
| MiMo V2.6 Flash | Behind GLM in DevPass curation; Xiaomi host logs prompts |

## Consequences

- LiteLLM proxy configured with three cost tiers: `cheap` (GLM, $0.07),
  `standard` (DeepSeek Flash, $0.15), `expensive` (Kimi K2.7, $0.76 reserved
  for future upgrade)
- Fresh per-conversation sessions; no conversation reuse across issues
- Critic and agent use different models, both privacy-safe
- Model IDs and providers to be confirmed against the live LLM Gateway catalog
  before production deployment
