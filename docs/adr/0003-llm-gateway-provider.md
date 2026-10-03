# LLM Gateway as model provider

Models are accessed through the LLM Gateway (api.llmgateway.io/v1) via an existing DevPass subscription, rather than directly from individual providers (OpenAI, Anthropic, DeepSeek). The gateway provides a single OpenAI-compatible endpoint, unified billing, and access to multiple providers through one API key. Alternatives: provider-direct API keys (cheaper per-token for some models, but requires managing multiple billing relationships and rotating credentials per provider) or OpenRouter (comparable gateway with different pricing and model selection).
