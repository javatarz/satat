# LiteLLM as model router

Satat routes all LLM traffic through a self-hosted LiteLLM proxy on the VM rather than calling the LLM Gateway directly. LiteLLM provides three tiered model profiles (cheap/standard/expensive) that the agent selects by tier, a hard monthly budget cap that cannot be exceeded, and spend tracking. Without LiteLLM, budget enforcement would require manual monitoring; with it, Satat can be left unattended without risk of a surprise bill. The proxy also eliminates the need to change OpenHands configuration when models are added or removed from the gateway.

## Concrete configuration

- **Model tiers** (aliases in `gateway/litellm-config.yaml.tmpl`): `cheap`, `standard`,
  `expensive`. Each routes to the LLM Gateway (`api_base: https://api.llmgateway.io/v1`)
  with the DevPass key. Model IDs are GitHub variables (`SATAT_MODEL_CHEAP` etc.); see
  ADR 0016.
- **Budget cap**: a single global hard cap — `litellm_settings.max_budget`
  (`SATAT_DAILY_BUDGET`, US$10) with `budget_duration: 1d`, resetting at midnight UTC.
  (Originally a US$100/`30d` monthly cap; [T7 / ADR 0019](0019-ntfy-hosting-and-observability.md)
  tightened it to a daily bound and added budget-alert notifications.)
- **Per-token costs are fetched, not hardcoded**: LiteLLM prices unknown model IDs at $0,
  which would leave the cap unenforced. The deploy workflow pulls `prompt`/`completion`
  pricing for each configured model from `https://api.llmgateway.io/v1/models` and renders
  them into `input_cost_per_token` / `output_cost_per_token`. A nightly cron re-runs the
  same workflow so prices stay current without manual editing.
- **Postgres is required**: LiteLLM enforces budgets from stored spend and **fails open
  without a database**, so the stack runs Postgres (ADR 0017). SQLite is not used.
- **No global soft budget**: LiteLLM exposes `soft_budget` only on keys/teams, not for the
  whole proxy. The daily budget is therefore a hard cap, but budget alerts fire on the way
  to it — `threshold_crossed` at 85%/95% — and reach ntfy via the `budget-relay` sidecar
  ([ADR 0015](0015-monitoring.md), [ADR 0019](0019-ntfy-hosting-and-observability.md)).
- **Admin UI** (`/ui`) is not exposed yet; whether to serve it (behind oauth2-proxy) is
  decided in T6.
