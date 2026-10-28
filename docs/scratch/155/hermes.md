# Hermes Agent: how it uses Claude subscriptions and `claude -p` (2026-10-28)

Hermes Agent is Nous Research's open-source agent. It has two ways to run on a Claude subscription, and
the difference between them is the most useful outside evidence for #155. Not legal advice.

Sources (read this session; clones in `raw/`):
- `NousResearch/hermes-agent` @ `dc39239` (2026-10-27): `agent/anthropic_adapter.py`,
  `agent/prompt_builder.py`, `agent/credential_pool.py`, `hermes_cli/runtime_provider.py`,
  `website/docs/integrations/providers.md`
- `NousResearch/hermes-official-plugins` @ `e4e4039` (2026-10-25): `claude-subscription-directsdk/`
  (`README.md`, `AGENTS.md`, `directsdk.py`)
- GitHub threads: hermes-agent #40014, #48320, PR #40528, PR #105863, PR #117451

## Path 1: Hermes calls the API itself with the OAuth token (core, built in)

- Takes a `claude setup-token` token (`CLAUDE_CODE_OAUTH_TOKEN`) or borrows Claude Code's login from
  `~/.claude/.credentials.json` or the macOS Keychain, and calls `api.anthropic.com/v1/messages` directly.
- Poses as Claude Code: OAuth betas `claude-code-20250219` and `oauth-2025-04-20`, user-agent
  `claude-code/<installed version> (external, cli)`, `x-app: cli`, and a system prompt starting
  "You are Claude Code, Anthropic's official CLI for Claude." (`anthropic_adapter.py:238-318`, `:428`).
- Anthropic classifies this traffic as third-party anyway. Hermes' own docs: the OAuth path "only works if
  you're on a Claude Max plan and have purchased extra usage credits. The base Max plan allowance ... is not
  consumed by Hermes". Claude Pro cannot use it at all (`providers.md:140-141`, `:172-175`).
- Hermes code comments describe a server-side "OAuth billing classifier" that fingerprints tool names and
  prompt wording and answers HTTP 400 "Third-party apps now draw from extra usage" or "You're out of extra
  usage". Hermes renames tools (`memory` → `context_notes`, `mcp_x` → `mcp__x`) and rewords prompt sentences
  to avoid it (`anthropic_adapter.py:320-328`, `:546-551`; `prompt_builder.py:245-262`).
- #40014 (subscription OAuth drains extra usage, not plan quota): a maintainer closed it with "This is what
  they want to happen and what is expected". A user then confirmed that a `setup-token` token got the 400 on
  every request while plan usage sat at 12%, and a $10 extra-usage top-up fixed it with no other change.

## Path 2: the official plugin drives the real `claude -p` (Nous's answer)

- `claude-subscription-directsdk` (Experimental, MIT), in Nous's official plugin repo since 2026-09-20.
  Core half merged as hermes-agent PR #117451; earlier in-tree attempts (PR #40528, PR #105863) were closed
  so the provider lives as a plugin.
- Each model call spawns the unmodified Claude Code CLI:
  `claude -p --input-format stream-json --output-format stream-json --tools '' --system-prompt-file ...`
  in a private temp directory (`directsdk.py:825`). Native tools are off; Hermes' tools are offered through
  an inert MCP server and run by Hermes itself.
- Credentials stay with the CLI: "The plugin never opens, copies, refreshes, or prints its credential
  files." It refuses to start if `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_BASE_URL` is set
  (`directsdk.py:790`).
- It does run a local proxy: a per-request loopback relay set as `ANTHROPIC_BASE_URL`. The relay passes the
  CLI's own Authorization and identity headers straight to Anthropic, unlogged, and admits only the first
  Messages request. The token never leaves the CLI's process; the relay never holds it.
- Their own rule (`AGENTS.md:89-91`): "a knob that forwards the subscription bearer to a third-party host is
  a policy change, not a feature."
- Billing, measured by them on Pro (Sep 2026) and Max (Oct 2026): this provider "draws subscription usage at
  exactly the `claude -p` / Agent SDK rate", i.e. from the plan, not from extra usage. Interactive Claude Code
  costs about 0.58x as much plan usage per list dollar as `claude -p`.

## What this means for #155

1. **Claude Code as the client matters more than where the token sits.** Hermes' direct calls are billed as
   third-party extra usage even with Claude Code headers faked; real `claude -p` draws from the plan.
   Satat's lane runs the real CLI, so Satat is on the `claude -p` side of that line.
2. **Our pass-through result agrees.** Through LiteLLM's `/anthropic` route, Claude Code's
   `rate_limit_event` showed `rateLimitType: five_hour`, `utilization: 0.16`, `isUsingOverage: false`,
   `overageStatus: rejected` (`org_level_disabled`). Inference, not checked on a bill: the call counted
   against the plan, and since overage is off for the org, a call classed as third-party would have been
   refused with the extra-usage 400 instead.
3. **Route choice did not change billing in a direct test.** A streamed call on LiteLLM's `/v1/messages`
   route (user-agent rewritten to `litellm/1.106.4`) reported the same `five_hour` claim and utilization as
   the `/anthropic` pass-through, even with no Claude Code identity at all (`deeper-results.md`). Hermes'
   classifier triggers were specific tool names and prompt sentences, which a tiny request doesn't exercise,
   so a full session on `/v1/messages` could still differ; `/anthropic` stays the safer choice because it
   sends Claude Code's request unchanged.
4. **The terms gap is unchanged.** Hermes' official plugin keeps the token inside the CLI and calls a
   proxy that holds the subscription token a policy line. #155's design (the proxy holds the token) is
   exactly what that rule excludes. This supports option A (pass-through, token stays with the CLI) or a
   clear answer from Anthropic before option C. The Usage Policy and `legal-and-compliance` quotes in
   `terms.md` still apply.
5. **Unverified community claims, not used as evidence:** that the OpenClaw creator was banned on
   2026-04-04 for third-party OAuth use, and that "Anthropic staff confirmed `claude -p` is allowed"
   (both from #48320, no primary source given).
