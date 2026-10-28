# Claude CLI through an LLM gateway proxy (#155)

Part of #99. Checks ADR 0032's design: the long-lived Claude subscription OAuth token is held by an
LLM gateway proxy, and each step gets a short-lived, budget-capped virtual key. Builds on
`claude-lane-spike.md` (#102), which saw `ANTHROPIC_BASE_URL` honoured but only 500s upstream.

**Verdict:** technically it works. LiteLLM meets every property in the ticket, best on its
`/anthropic` pass-through route. Anthropic's terms are the open question: the design is close to
two sentences that restrict it (see [Terms](#terms)). Not legal advice.

## Setup

- Docker Compose on colima, run 2026-10-27 and 2026-10-28. The harness was `docs/scratch/155/`
  (gitignored, not committed). Stacks were removed with `down -v` after each run.
- LiteLLM 1.106.4 (`docker.io/litellm/litellm`; the GHCR image returned 403), Postgres 15.6, an
  OpenTelemetry collector with a debug exporter.
- OpenBao 2.7.2 in dev mode, laid out as ADR 0032: KV v2 mount `satat/`, entry
  `claude-oauth-token`, AppRole `litellm` bound to LiteLLM's fixed address on an internal `secrets`
  network.
- Lane: Node 22, Claude Code 2.1.301, non-root, Satat's managed settings, on an internal `lane`
  network whose only other member is LiteLLM.
- One real credential: the operator's own `claude setup-token` token, kept in the macOS Keychain
  and piped into OpenBao, never printed or written to a file. It can be revoked from claude.ai.
  Everything else was a placeholder.

## Results

| # | Question | Result |
|---|---|---|
| 1 | Upstream OAuth | **Pass.** `claude -p` worked with only a virtual key in the lane. Needs managed-settings changes (finding 1). |
| 2 | Token never in the lane | **Pass.** Not in the lane's files, env, `/proc` or process list, nor in responses on 19 routes, LiteLLM logs, Postgres or OTLP spans. A planted `ANTHROPIC_API_KEY` is refused (exit 1). |
| 3 | Per-step key | **Pass.** Models list, expiry and deletion enforced. Budget is a soft cap (finding 4). A non-admin user can mint (finding 5). |
| 4 | Reach | **Pass.** The lane has no DNS or route out and reaches only LiteLLM. Admin routes refuse a virtual key. |
| 5 | Token delivery | **Pass.** OpenBao Agent logs in and runs LiteLLM. Rotation without a rebuild in about 20 s (finding 6). |
| 6 | Observability | **Pass.** Access log, spend per key and OTLP spans. Prompts in spans unless turned off (finding 7). |
| 7 | Terms | **Unclear, leaning not allowed** for a proxy that holds the subscription token (see [Terms](#terms)). |

## Findings

1. **Managed settings must allow a custom endpoint.** With `allowedProviders: ["anthropic"]`,
   Claude Code refuses a custom `ANTHROPIC_BASE_URL`. It works with
   `allowedProviders: ["customEndpoint"]` and `ANTHROPIC_BASE_URL` pinned in the managed `env`
   block (Claude Code docs, `llm-gateway`). `forceLoginMethod: "claudeai"` can stay. The lane passes
   the virtual key as `CLAUDE_CODE_OAUTH_TOKEN`; `claude auth status` then shows `oauth_token`.
2. **Use LiteLLM's `/anthropic` pass-through route, not `/v1/messages`.** Both work. Captured with a
   fake token against a header listener:
   - `/v1/messages` sends the token as `Authorization: Bearer` with the OAuth betas
     (`claude-code-20250219`, `oauth-2025-04-20`) and `anthropic-dangerous-direct-browser-access`.
     It also rewrites the user-agent to `litellm/1.106.4`, drops `x-app` and Claude Code's session
     id, rebuilds the body, drops or renames the `anthropic-ratelimit-unified-*` headers, drops
     `retry-after` and retries a 429 twice. Claude Code then reports a bare rate-limit event with no
     window or reset time.
   - `/anthropic` forwards Claude Code's request and headers unchanged and swaps only
     `Authorization`. Rate-limit headers and 429s come back unchanged, with no retries. Claude Code
     reports the full event (`five_hour`, `resetsAt`, utilization), which #171 needs. A 3-turn
     tool-use run worked. Models, budget, deletion and spend tracking still apply
     (`call_type: pass_through_endpoint`).
   - Config: `ANTHROPIC_API_KEY` in LiteLLM's environment holds the token for this route; the lane's
     base URL is `http://litellm:4000/anthropic`.
3. **Set `allowed_routes` on every key.** `/anthropic` forwards any path with the token. The token's
   narrow scope already refuses profile, usage and organization endpoints (403).
   `allowed_routes: ["/anthropic/v1/messages"]` on the key blocks every other path, and Claude Code
   still works.
4. **The budget is a soft cap.** LiteLLM checks recorded spend before each call and writes spend
   about 12 s after it. With `max_budget` of 1e-6, the first call passed and the second got 422
   "Budget has been exceeded!". Calls in flight in that window can overshoot. How far under
   concurrent calls was not measured.
5. **The minter does not need the master key.** A virtual key cannot mint (401). A LiteLLM
   `internal_user` (here `satat-minter`, with its own model list and total budget) can create and
   delete only its own keys and cannot touch users, models or config. Its keys may ask for more,
   but calls are capped by the user's model list (403) and total budget (422 "User=satat-minter over
   budget"). The minter's own key can also call models.
6. **OpenBao Agent runs LiteLLM.** LiteLLM's built-in Vault secret manager is premium-only, so the
   `bao` binary is added to the LiteLLM image and runs as its entrypoint: AppRole auto-auth,
   `env_template` for the token, `exec` of LiteLLM's entrypoint with
   `restart_on_secret_changes = "always"`.
   - The token is not in `docker inspect`, image history or the agent's env, only in the LiteLLM
     child process's env.
   - Rotation: LiteLLM restarted 6 to 11 s after the write and served again 11 to 12 s later. A
     dummy value gave 401 upstream and the real value restored 200. Requests in flight at the
     restart are cut.
   - The AppRole credential used from another address is refused (400). The policy reads one entry;
     other reads, list and write are denied. The lane cannot reach OpenBao.
7. **Turn prompts off in traces.** The `litellm_request` span carries key alias and hash, cost,
   usage, Claude Code's session id and `used_client_oauth_token=false`. By default spans also carry
   full prompts and responses, so anything the agent reads lands in traces.
   `litellm_settings.turn_off_message_logging: true` removes them on both routes and keeps cost and
   key alias. Spend logs never held prompts.
8. **Both routes draw from the plan, not extra usage.** Streamed calls on 2026-10-28 reported
   `representative-claim: five_hour`, `overage-status: rejected` (`org_level_disabled`) and the same
   utilization on `/v1/messages`, on `/anthropic`, and on a bare `/v1/messages` call with no Claude
   Code identity. These were tiny requests; a full session on `/v1/messages` was not re-measured
   (see [Hermes](#hermes-agent)).
9. **Side finding for #174:** Claude Code 2.1.301 warns that the lane's managed `Write(...)` deny
   rules are never matched ("only Edit(path) rules are. Use Edit(...) instead").

## Terms

Pages read 2026-10-28. Not legal advice.

- Claude Code [legal and compliance](https://code.claude.com/docs/en/legal-and-compliance):
  "developers may not collect, store, or intermediate Claude.ai credentials or session tokens —
  sign-in to a Claude account must complete through Anthropic's own flow." Unlike the sentence
  before it, this one has no "on behalf of their users". The safe harbours cover the customer's own
  API keys in a secrets manager and "the unmodified Claude Code binary" signed in with the user's own
  subscription; neither covers a proxy holding a subscription token.
- [Usage Policy](https://www.anthropic.com/legal/aup), effective 2026-11-12: "Resell, proxy, or
  otherwise provide access to Claude through unauthorized means, including services that route
  requests through consumer subscriptions or misrepresent the product or client being used".
- [Authentication](https://code.claude.com/docs/en/authentication): `claude setup-token` and
  `CLAUDE_CODE_OAUTH_TOKEN` are for "CI pipelines, scripts, or other environments where interactive
  browser login isn't available". That covers Claude Code holding the token, not another program.
- [LLM gateway](https://code.claude.com/docs/en/llm-gateway) documents two shapes only: a gateway
  credential billed at API rates, or pass-through, where Claude Code sends its own login and the
  gateway forwards it. A gateway holding a subscription token is not described.

This weakens `secrets-decisions.md` §2, which read the restriction as applying only to third
parties acting "for their users".

## Hermes Agent

Nous Research's Hermes Agent (read at `NousResearch/hermes-agent@dc39239` and
`NousResearch/hermes-official-plugins@e4e4039`, 2026-10-28) is a useful outside data point:

- **Built in:** Hermes calls `api.anthropic.com` itself with a `setup-token` token, posing as
  Claude Code (OAuth betas, `claude-code/<version>` user-agent, `x-app: cli`, Claude Code's
  system-prompt opening). Its docs say this "only works if you're on a Claude Max plan and have
  purchased extra usage credits" and never uses the plan's included usage. Code comments describe an
  Anthropic classifier that sends some tool names and prompt wording to extra usage, and Hermes
  renames tools to avoid it. A maintainer closed the bug about it (hermes-agent #40014): "This is
  what they want to happen and what is expected."
- **Official plugin `claude-subscription-directsdk`:** runs the unmodified `claude -p` per model
  call with stream-json. The token stays inside the CLI; a loopback relay forwards the CLI's own
  headers and never holds the token. Measured on Pro and Max, it draws plan usage at the
  `claude -p` rate. Its contributor rules: "a knob that forwards the subscription bearer to a
  third-party host is a policy change, not a feature."

Satat's lane runs the real CLI, so its traffic looks like the plugin's, not Hermes' built-in path.
What Hermes' own plugin will not do is what #155 tests: a proxy holding the subscription token.

## Recommendation

- **Proxy:** LiteLLM 1.106.4 on the `/anthropic` pass-through route, OpenBao Agent as its
  entrypoint, `turn_off_message_logging: true`. Per-step keys with `duration`, `models`,
  `max_budget` and `allowed_routes: ["/anthropic/v1/messages"]`, minted and deleted by a LiteLLM
  `internal_user` whose own budget caps the total. No alternative proxy was needed.
- **Before building it,** the owner decides on the terms. In order of risk:
  1. The proxy holds a Console API key (or workload identity federation) instead of the
     subscription token. Covered by the API-key safe harbour; billed at API rates.
  2. Pass-through with the token in the lane: Claude Code holds `CLAUDE_CODE_OAUTH_TOKEN` and
     LiteLLM forwards it. Documented, but breaks "the token never in the lane"; it would need the
     fallback hardening #155 names (separate UID, scrubbed subprocess env, egress through the proxy
     only).
  3. This spike's design: the proxy holds the subscription token. Works, closest to the restricting
     text, riskier from 2026-11-12. The legal page suggests asking Anthropic sales for a definite
     answer.

## On k3d (not tested)

Inference from ADR 0032 and `agent-sandbox-spike.md`:

- Kubernetes auth bound to LiteLLM's service account replaces AppRole; OpenBao Agent stays the
  entrypoint.
- A NetworkPolicy replaces the internal `lane` network. The agent-sandbox spike saw policies applied
  2 to 4 s after pod start, so the lane must wait for its policy before calling LiteLLM.

## Not covered

- Budget overshoot under concurrent calls.
- The real upstream headers on the wire with the real token (a capture would log it); the fake-token
  capture stands in.
- A full Claude Code session's plan-versus-extra-usage billing on `/v1/messages`.
