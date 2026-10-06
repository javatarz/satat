# LiteLLM as model router

Satat routes all LLM traffic through a self-hosted LiteLLM proxy on the VM rather than calling the LLM Gateway directly. LiteLLM provides three tiered model profiles (cheap/standard/expensive) with cost-based automatic selection, a hard monthly budget cap that cannot be exceeded, and spend tracking. Without LiteLLM, budget enforcement would require manual monitoring; with it, Satat can be left unattended without risk of a surprise bill. The proxy also eliminates the need to change OpenHands configuration when models are added or removed from the gateway.

## Concrete configuration

- **Model tiers** (aliases in `gateway/litellm-config.yaml.tmpl`): `cheap`, `standard`,
  `expensive`. Each routes to the LLM Gateway (`api_base: https://api.llmgateway.io/v1`)
  with the DevPass key. Model IDs are GitHub variables (`SATAT_MODEL_CHEAP` etc.); see
  ADR 0016.
- **Budget cap**: a single global hard cap — `litellm_settings.max_budget`
  (`SATAT_MONTHLY_BUDGET`, US$100) with `budget_duration: 30d`. `30d` resets on the 1st of
  the month at midnight UTC, matching the LLM Gateway billing month.
- **Postgres is required**: LiteLLM enforces budgets from stored spend and **fails open
  without a database**, so the stack runs Postgres (ADR 0017). SQLite is not used.
- **No global soft budget**: LiteLLM exposes `soft_budget` only on keys/teams, not for the
  whole proxy. The desired US$10/day non-blocking *warning* is therefore deferred to the
  alerting work (ADR 0015 / T7), not enforced by this cap.
- **Admin UI** (`/ui`) is not exposed yet; whether to serve it (behind oauth2-proxy) is
  decided in T6.
