# Secrets decisions log (draft)

Running log of decisions from the per-secret-type conversations (#105 and follow-ups). It is consolidated into ADRs once the conversations are done. Evidence is in `notes/secrets-*.md`.

Principles: secrets are as short-lived and as narrowly scoped as possible, minted per step where the provider allows it. Where it does not, the credential is narrow and long-lived, held outside the lane pod, and rotated. The agent never holds or sees a secret (the pod gets placeholders; trusted components inject real values).

## 1. GitHub: decided

[ADR 0025](../../adr/0025-github-token-broker.md), Accepted 2026-10-16.

## 2. Claude subscription token: decided 2026-10-17

- Tokens are the operator's own seat, on the operator's own infrastructure, single user. Anthropic's terms (code.claude.com legal and compliance page, read 2026-10-17) restrict third parties that collect, store or intermediate credentials for their users. That does not describe this use. Not legal advice; revisit if anyone else's seat or a second user is ever added. Corrected 2026-10-28 (#155): the page's "collect, store, or intermediate Claude.ai credentials or session tokens" sentence has no "for their users" limit, and the Usage Policy from 2026-11-12 restricts services "that route requests through consumer subscriptions". Whether Option A is allowed is open; see [claude-gateway-proxy-spike.md](claude-gateway-proxy-spike.md#terms).
- Option A: the lane pod holds a placeholder `CLAUDE_CODE_OAUTH_TOKEN`, and `ANTHROPIC_BASE_URL` points at the Satat proxy, which injects the real token. The real token lives only with the proxy.
- Fallback B if the placeholder does not work end to end: the token is in the harness process environment, with a dedicated seat, a separate UID from the agent's shell, `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB`, and egress to `api.anthropic.com` only (through the proxy).
- Credential accounts (NN12) get a type: `subscription` or `api-federated` (Anthropic Workload Identity Federation: no stored secret, token at most 1 hour, API-billed). Only `subscription` is needed now.
- To verify: the proxy forwards to the real API with a real token (the #102 probe only saw 500s); the CLI accepts the placeholder. Verified 2026-10-28 in #155 with LiteLLM.

## 3. LLM gateway (LiteLLM): decided 2026-10-19

- The upstream LLM Gateway key is a static root secret. It lives only in the LiteLLM pod, and is rotated every 90 days by the deploy pipeline (update the Secret, restart LiteLLM). It never enters a lane pod.
- Gateway lanes get a per-step LiteLLM virtual key in the pod environment: short `duration` (about the step timeout), `models` per lane, small `max_budget`, deleted at step end. LiteLLM is reachable only from inside the cluster, so a leaked key is useless outside it and bounded by its budget. No proxy injection is needed for this lane.
- Rejected: fully unauthenticated LiteLLM. It would lose per-step budget and rate caps, per-key attribution (#89, NN12 spend per account) and per-lane model limits, and it leans on NetworkPolicy alone.
- Spike needed: the Claude proxy (below).

## 4. Platform static secrets: open

## 5. Cloud, database and registry: open

## 6. Third-party SaaS keys: open

## 7. Cross-cutting (proxy, scrubbing, canaries): open
