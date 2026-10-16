# Claude lane spike (#102)

Part of #99. Checks the five questions the docs left open about running `claude -p` unattended on `CLAUDE_CODE_OAUTH_TOKEN` in a container. The rest of the lane is already covered in `notes/platforms-harness.md`.

## Setup

- Claude Code 2.1.301, installed from npm into `node:22-bookworm-slim`, self-update off.
- Non-root `agent` user, except where a check needed root.
- Clean environment: no `ANTHROPIC_*` variables except where a check set one.
- Token from `claude setup-token`, passed by name into the container (never written to disk or shown in output).
- Run on 2026-10-16. The harness was `docs/scratch/102/` (gitignored, not committed).

## Results

| # | Check | Result |
|---|-------|--------|
| 1 | `ANTHROPIC_BASE_URL` honoured with an OAuth token | **Pass.** A local listener received `HEAD /api/hello` and `POST /v1/messages?beta=true`. |
| 2 | `forceLoginMethod: claudeai` + `allowedProviders: ["anthropic"]` | **Pass.** Env OAuth token accepted. With a planted `ANTHROPIC_API_KEY` the session refuses to start. |
| 3 | Empty `CLAUDE_CONFIG_DIR`, no TTY | **Pass.** Runs with no onboarding or trust prompt. `claude auth status` reports `authMethod: oauth_token`. |
| 4 | `bypassPermissions` as root | **Refused**, exit 1. Both `--permission-mode bypassPermissions` and `--dangerously-skip-permissions` fail. Works as a non-root user. |
| 5 | Settings isolation | **Pass.** A planted `~/.claude/settings.json` model leaked without the flags and was ignored with `--setting-sources project --strict-mcp-config`. |

## Findings

1. **Proxy capture is viable on the Claude lane.** This contradicts the inference from OpenHands stripping `ANTHROPIC_BASE_URL` when the OAuth token is present: Claude Code itself still sends to the configured base URL. The ADR 0022 runner-transcript fallback is therefore not forced. Not tested: what the proxy needs to forward, and whether the OAuth token is accepted when the upstream is the real API through a proxy. The probe returned 500s, so a full response was never seen. The token is sent to whatever the base URL is, so the proxy must be trusted and inside the sandbox boundary. Claude Code retried the POST 8 times against the 500s, so the proxy needs to return real responses, not errors.
2. **A planted API key is blocked by managed settings, with a clear message.** `claude auth status` showed `authMethod: api_key` for the planted-key case and the forced login method `claudeai`. This backs up the runner's `authMethod == oauth_token` assertion. Managed settings enforce the same rule at the CLI itself.
3. **No first-run setup is needed.** A fresh config dir and a token in the environment are enough.
4. **The lane image needs a non-root user.** Root cannot use `bypassPermissions`.
5. **User-level settings do leak into `-p` runs by default.** The lane must always pass `--setting-sources project --strict-mcp-config`, or avoid mounting a home directory with settings.

## Not covered

- Rate-limit event shape: runner unit test with a recorded fixture (build step 6).
- Preflight against a planted key: ordinary code with tests (build step 1). Check 2 shows managed settings give a second line of defence.
- Whether proxy capture sees enough to reconstruct conversations (request bodies, streaming responses). Worth a follow-up in the Hatchet or runner design.

## Feeds

- #107, Hatchet ADR, and build step 1.
- Revisit the "proxy capture won't work" assumption recorded in `notes/platforms-harness.md` and ADR 0022.
