# Model and provider neutrality

**Status:** Idea · **Related:** [ADR 0016](../adr/0016-model-selection.md),
[ADR 0022](../adr/0022-v2-platform-requirement-decisions.md), #94 · **Source:** calls on
2026-10-16 and 2026-10-21

- Every component that calls an LLM is cross-tested against the major providers, with a
  component diagram showing which models each one was tested with. The diagram is how we show a
  client there's no lock-in.
- Prefer models available through accounts we already have (the big three, gateways). A niche
  provider means a new account and paperwork. Some orgs won't accept certain model origins.
  Use zero-data-retention options. Never use free tiers that train on your data.
- Inference is chosen at install time: direct API (OpenAI, Anthropic), a gateway (LLM Gateway,
  OpenRouter), a cloud provider (Bedrock, Azure AI Foundry), or a subscription.
- Subscriptions are per person under the provider's terms: sharing a token is not allowed and
  risks bans. A subscription install means one install per person, or a layer that maps an
  issue's assignee to that person's token. Commit authorship must match the token's owner.
- LiteLLM stays because it is the one proxy that speaks both the OpenAI and Anthropic protocols.

## Secrets and credentials

- Secrets map to a credential account or a person, not to an environment variable. ADR 0022
  (NN12) decided that a repo maps to an account and the launcher will mount that account's
  secret. On `main` today, accounts are only concurrency cap keys.
- #129 proposes `deploy.secrets`, a flat map from variable name to source. That is fine for one
  account. It should grow into secrets grouped per account or person.
- The shared `satat.yaml` should not name one owner's 1Password vault. Each install points at
  its own secret source, so a fork works without edits (#94).
