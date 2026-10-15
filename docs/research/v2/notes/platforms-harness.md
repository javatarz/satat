# Research: harness layer candidates for Satat

Status: scratch research, 2026-10-15. Scope: the **harness adapter** layer from
`docs/research/v2/platform-requirements.md`. That layer runs one workflow step with one harness
inside an isolated workload. It takes repo, task, branch, and instructions or a skill, and
returns status, artifacts, and a PR. Launch harnesses are Claude Code (Claude subscription
OAuth token only), and opencode and pi (LiteLLM, OpenAI-compatible).

All facts were checked against primary sources (GitHub API and LICENSE files, official docs,
Anthropic legal and docs pages) on 2026-10-15, unless marked **UNVERIFIED**. Source numbers
like [S1] point to the list at the end.

## Summary table

| Candidate | What it is | License (SPDX, from LICENSE) | Latest release | Headless / unattended | Event output | Cancel / steer | Credentials (env) | Satat role |
|---|---|---|---|---|---|---|---|---|
| **Claude Code CLI** (`claude -p`) | Anthropic's harness, run as a subprocess | None. Proprietary, "All rights reserved", governed by Anthropic terms [S1] | v2.1.300, 2026-10-14 | Yes: `-p`, `--permission-mode`, `--permission-prompts none`, `--max-turns`, `--max-budget-usd` [S5][S6] | `--output-format stream-json` (NDJSON: `system/init`, `assistant`, `user`, `stream_event`, `system/api_retry`, final `result`) [S5] | Cancel: SIGINT ends the turn, SIGTERM exits 143. Steer: `--input-format stream-json` accepts queued user messages [S5][S6] | `CLAUDE_CODE_OAUTH_TOKEN` (from `claude setup-token`, valid 1 year). `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` take precedence over it [S3] | **Launch harness. Drive it directly (recommended).** |
| **Claude Agent SDK** (Py/TS) | Library that "runs the Claude Code binary" [S4] | Python: MIT. TS: proprietary (Commercial Terms) [S1]. SDK use is governed by Commercial Terms [S4] | py v0.2.167, ts v0.3.300, 2026-10-14 | Yes | Typed message objects (same schema as stream-json) | `interrupt()`, streaming input | Same as CLI. Docs ask third-party developers to use API keys, not claude.ai login [S4] | Not needed. Adds terms risk on the OAuth lane and gives nothing that `claude -p` lacks |
| **claude-agent-acp** (fka `@zed-industries/claude-code-acp`) | ACP adapter for Claude Code, built on the Agent SDK | Apache-2.0 adapter code. The registry lists it as "proprietary" because of the bundled SDK [S10][S11] | v0.91.0, 2026-10-15 | Yes (ACP) | ACP `session/update` | ACP `session/cancel` | Reads the CLI's credentials, including `CLAUDE_CODE_OAUTH_TOKEN` [S10] | Optional. Use only if the owner accepts the Agent SDK terms ambiguity (see section 2) |
| **opencode** | Open harness (anomalyco, fka sst) | MIT [S1] | v1.19.0, 2026-10-11 | Yes: `opencode run --format json`, `--auto`. Unanswered permissions are auto-rejected [S20][S21] | JSONL: `step-start`, `text`, `reasoning`, `tool`, `step-finish`, `aborted`, `error` [S21] | ACP: `opencode acp` (native). HTTP: `opencode serve` | Provider config in `OPENCODE_CONFIG` / `OPENCODE_CONFIG_CONTENT`. `options.baseURL` plus `apiKey: "{env:VAR}"` [S20][S22] | **Launch harness. Native ACP.** |
| **pi** (earendil-works/pi, fka badlogic/pi-mono) | Open harness | MIT [S1] | v1.2.0, 2026-10-12 | Yes: `--print`, `--mode json`, `--mode rpc`. Has no permission prompts at all [S23][S27] | JSONL: `session`, `agent_start`, `turn_*`, `message_*`, `tool_*`, `agent_end`, `agent_settled` [S24] | RPC: `abort`, `steer`, `follow_up` [S26] | `models.json`: `baseUrl`, `api: openai-completions`, `apiKey: "$VAR"`. Dir set by `PI_CODING_AGENT_DIR` [S28] | **Launch harness. Native RPC. ACP only through a third-party adapter.** |
| **pi-acp** (svkozak) | ACP adapter that spawns `pi --mode rpc` | MIT | v0.0.34, 2026-09-24 | Yes, but an "MVP", Zed-centred, "expect breaking changes" [S29] | ACP | ACP cancel | Uses pi's config | Optional. One maintainer, so prefer pi's native RPC |
| **Agent Client Protocol (ACP)** | JSON-RPC protocol between clients and coding agents, over stdio | Apache-2.0 [S12] | Schema v1.25.0, 2026-10-09. Protocol v1 stable, v2 draft since 2026-07-20 [S13][S15] | Designed for editors, but works headless: the client answers `session/request_permission` by policy | `session/update` notifications | `session/cancel` (v1). v2 adds queueing and steering semantics [S15][S16] | Not covered by the spec (RFD "Configurable LLM Providers" is in draft) [S17] | **Recommended internal contract shape for open harnesses** |
| **Goose** (aaif-goose/goose, fka block/goose) | Harness plus desktop app, CLI, server, and recipes. Part of the Linux Foundation's Agentic AI Foundation (AAIF) [S30] | Apache-2.0 | v1.54.0, 2026-10-08 | Yes: `goose run --recipe … --output-format stream-json` [S31] | `json` / `stream-json` | `goose acp` server. Can also act as an ACP client of Claude, Codex, and pi [S32] | `GOOSE_PROVIDER`, `GOOSE_MODEL`, `GOOSE_PROVIDER__HOST`, `GOOSE_PROVIDER__API_KEY` [S33] | Possible future harness. Not a fit as the contract or as orchestration |
| **OpenHands SDK** | Python agent loop, `ACPAgent` that drives external ACP agents, REST/WebSocket agent-server | MIT [S1] | v1.55.0, 2026-10-15 | Yes | OpenHands events over REST/WS | ACP cancel through `ACPAgent` | Per-provider env maps, for example it strips `ANTHROPIC_API_KEY` and `ANTHROPIC_BASE_URL` when `CLAUDE_CODE_OAUTH_TOKEN` is set [S8][S9] | Reference design. Usable as the runner if extensions are written in Python |
| **Strands Agents** (now `strands-agents/harness-sdk`) | AWS's open agent SDK plus "Strands harness" | Apache-2.0 [S1] | python/v1.61.0, 2026-10-14 | Own loop only | Own streaming events | Own cancellation | Model-provider keys | **Not harness-only**, but its upper-layer features don't fit Satat (section 1). Can't drive external CLIs |
| **coder/agentapi** | HTTP API that emulates a terminal around agent TUIs | MIT | v0.12.2, 2026-05-27. **Archived, deprecated** [S34] | n/a | n/a | n/a | n/a | Reject: deprecated |
| **A2A** | Agent-to-agent protocol (Linux Foundation) | Apache-2.0 | v1.0.1, 2026-05-28 [S35] | Service-to-service | Task/message events | Task cancel | n/a | Wrong layer: it connects agent services, not a runner to a CLI harness |

Recommendation in one line: put a small Satat runner inside each workload. It clones the repo,
drives the harness through a per-harness driver, streams normalised ACP-shaped events, and
then does the push and PR itself. Drive Claude Code directly as `claude -p` with stream-json.
Drive opencode over ACP (`opencode acp`). Drive pi over its native RPC, or through pi-acp if
the owner prefers a single protocol. See section 9.

---

## 1. Strands Agents (AWS)

**What it is.** An open-source SDK for building and running agents in Python and TypeScript.
The monorepo was renamed from `strands-agents/sdk-python` to `strands-agents/harness-sdk`
(GitHub redirects). It now contains a pre-assembled "Strands harness" (`create_harness()`),
the SDKs, and a `strands` CLI [S36]. License: Apache-2.0 (`LICENSE.APACHE`). Activity: python
v1.61.0, typescript v1.21.0, and harness v0.3.0 all shipped 2026-10-14. About 8.8k stars.

**Can it drive external CLIs?** No. Strands is its own model-driven loop that calls model
providers directly. A code search found no ACP client and no Claude Agent SDK integration.
The `strands` CLI does the opposite: `strands --acp-server` serves a Strands agent over ACP
on stdio [S37]. A community package, `@ryancormack/strands-acp`, does the same [S38]. So
Strands can be an ACP *agent*, but it can't drive Claude Code, opencode, or pi.

**Does it belong only in the harness layer? No.** It ships features that look like upper
layers:

- **Multi-agent orchestration:** `strands.multiagent` has `graph.py`, `swarm.py`, and `a2a/`.
  The docs list built-in "Graph, Swarm, and Workflow" orchestrators, where Workflow means
  "run a fixed task graph (DAG)… with independent tasks in parallel" [S39].
- **A2A:** an A2A server and client, plus an `A2AAgent` (`agent/a2a_agent.py`) [S39].
- **Durable execution:** `strands.experimental.checkpoint` "for durable agent execution… a
  durability provider (e.g. Temporal) can persist checkpoints". There is also a
  partner-maintained Temporal integration (`temporalio[strands-agents]`) [S40].
- **Deployment:** guides for Bedrock AgentCore, Fargate, EKS, Lambda, App Runner, Docker,
  Kubernetes, and Terraform [S39].

None of these replace Satat's scheduler, workflow engine, or control plane:

- The graph and swarm route between in-process LLM agents within one request. There is no
  work queue, no lease or claim, and no concurrency caps per lane or per repo.
- Workflows are Python or TypeScript code, not declarative files in git that repos opt into.
- There is no UI over work items.
- Durability comes from Temporal, so Temporal would be the actual workflow engine.
- AgentCore is a managed AWS runtime, which fails non-negotiables 9 and 11 as the only
  option.

**Verdict for Satat:** treat Strands as a harness-layer library only. Use it as a possible
future custom harness, served over ACP through `strands --acp-server`. Don't use it as
orchestration, and don't adopt it now: it adds nothing for the three launch harnesses. This
holds for any answer to the extension-language question.

## 2. Claude Code, the Claude Agent SDK, and the subscription OAuth token

### Relationship

The Agent SDK overview describes the SDK as "a library that runs the Claude Code binary". It
also says that from another language you should "run the CLI as a subprocess with `-p` and
`--output-format json`" [S4]. The headless page is titled "use the Agent SDK via the CLI
(`claude -p`)" [S5]. So `claude -p`, the Python SDK, the TS SDK, and claude-agent-acp all run
the same agent loop in the same binary. The SDKs add typed messages, `canUseTool` callbacks,
and `interrupt()`.

Licenses:

- The Claude Code repo and the TS SDK repo have no OSI license: "© Anthropic PBC. All rights
  reserved. Use is subject to Anthropic's Commercial Terms of Service" [S1].
- The Python SDK wrapper is MIT, but SDK use "is governed by Anthropic's Commercial Terms"
  [S4].
- **License-bar impact (open question 4):**
  - If the bar is OSI-only, Claude Code itself fails. But Claude Code is a harness the
    operator installs, not something Satat redistributes.
  - Satat can publish a recipe that installs the official binary or npm package at runtime,
    in the same way it pulls a container image. **Don't vendor or redistribute the binary.**
    The legal page also requires that "the Claude Code binary must not be modified" and that
    built-in auth methods not be removed [S2].

### Can it authenticate with `CLAUDE_CODE_OAUTH_TOKEN`? Yes, technically

- `claude setup-token` mints a one-year OAuth token "for CI pipelines, scripts, or other
  environments where interactive browser login isn't available". You set it as
  `CLAUDE_CODE_OAUTH_TOKEN`. It "requires a Pro, Max, Team, or Enterprise plan" and "can only
  make model requests" [S3].
- Precedence (highest first):
  1. Cloud provider flags (`CLAUDE_CODE_USE_BEDROCK` / `CLAUDE_CODE_USE_VERTEX` /
     `CLAUDE_CODE_USE_FOUNDRY`)
  2. `ANTHROPIC_AUTH_TOKEN`
  3. `ANTHROPIC_API_KEY`
  4. `apiKeyHelper`
  5. `CLAUDE_CODE_OAUTH_TOKEN`
  6. Profiles
  7. `/login` [S3]

  In `-p` mode, "the key is always used when present" [S3]. **So any stray
  `ANTHROPIC_API_KEY` silently defeats the subscription lane.** The workload must never
  receive one. OpenHands encodes the same rule: it strips `ANTHROPIC_API_KEY` and
  `ANTHROPIC_BASE_URL` when `CLAUDE_CODE_OAUTH_TOKEN` is present [S9].
- **`--bare` does not read `CLAUDE_CODE_OAUTH_TOKEN`** [S3][S5]. The Claude driver can't use
  bare mode. Isolate configuration another way: a fresh `CLAUDE_CONFIG_DIR` per workload,
  plus `--setting-sources`, `--strict-mcp-config`, and `--settings`.
- Runner check before every launch: `claude auth status` returns JSON with
  `authMethod ∈ {none, claude.ai, oauth_token, api_key, api_key_helper, third_party}` [S6].
  Assert `authMethod == "oauth_token"`, and fail closed otherwise. This turns the credential
  lane from a convention into an infrastructure check.
- Defence in depth, **UNVERIFIED**: managed settings `forceLoginMethod: "claudeai"` plus
  `allowedProviders: ["anthropic"]` block `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, and
  `apiKeyHelper` at startup [S3]. I haven't verified that a `CLAUDE_CODE_OAUTH_TOKEN` env
  token passes `forceLoginMethod` (the docs say `setup-token` "enforce[s] only
  `forceLoginMethod`"). Test this before relying on it.

### Is it permitted for Satat's use? Yes for the unmodified CLI. Grey for the SDK

Primary sources, quoted:

- Legal and compliance page [S2], "Authentication and credential use":
  - "OAuth authentication is intended exclusively for purchasers of Claude Free, Pro, Max,
    Team, and Enterprise subscription plans and is designed to support ordinary use of
    Claude Code and other native Anthropic applications."
  - "Developers building products or services that interact with Claude's capabilities,
    including those using the Agent SDK, should use API key authentication… Anthropic does
    not permit third-party developers to offer Claude.ai login into their own applications,
    or to route requests through Free, Pro, or Max plan credentials on behalf of their
    users… developers may not collect, store, or intermediate Claude.ai credentials or
    session tokens."
  - "Nor does it prevent an end user from signing in to the unmodified Claude Code binary
    with their own Claude subscription, including where a platform hosts Claude Code."
  - "Advertised usage limits for Pro and Max plans assume ordinary, individual usage of
    Claude Code and the Agent SDK."
- Agent SDK overview [S4]: "Unless previously approved, Anthropic does not allow third party
  developers to offer claude.ai login or rate limits for their products, including agents
  built on the Claude Agent SDK. Use the API key authentication methods… instead."
- Consumer Terms (effective 2025-10-08) [S7] ban access "through automated or non-human
  means, whether through a bot, script, or otherwise", "except when you are accessing our
  Services via an Anthropic API Key or where we otherwise explicitly permit it". They also
  say: "You may not share your Account login information… or make your Account available to
  anyone else."
- Explicit permission for scripted use: the authentication docs present `setup-token` for
  "CI pipelines and scripts" [S3]. The official GitHub Action supports
  `claude_code_oauth_token` for event-driven and cron automation [S14]. That counts as "where
  we otherwise explicitly permit it" for **unmodified Claude Code run headless by the
  subscriber**.

My reading (not legal advice):

1. **Allowed:** a self-hosted Satat where the **operator** mints a token with their own
   subscription and Satat runs the **unmodified `claude` CLI headless**. This is the same
   pattern as the official GitHub Action with `CLAUDE_CODE_OAUTH_TOKEN`.
2. **Grey:** driving Claude through the Agent SDK or claude-agent-acp on an OAuth token.
   It's the same binary, but the docs tell SDK builders to use API keys. Goose, OpenHands,
   and Zed all ship this path publicly; Goose says "uses your existing Claude Code
   subscription" [S32]. That shows the practice is tolerated, but it isn't permission.
3. **Not allowed:**
   - Satat as a hosted product that collects other people's tokens.
   - Several people sharing one operator's token. This matters if open question 6 (tenancy)
     becomes "several people": each person would need their own token and lane.
   - Routing the token to any non-Claude-Code harness. This is non-negotiable 3 and matches
     the "intermediate" ban.
4. **Usage-volume risk:** limits "assume ordinary, individual usage". A Satat that runs
   Claude around the clock in parallel can hit limits or draw enforcement ("may do so
   without prior notice") [S2]. Keep the Claude-lane concurrency cap low (non-negotiable 5).
   Use `system/api_retry` events with `error: "rate_limit"` [S5] as the back-off signal.

### Headless `claude -p` versus the SDK, for Satat

- `claude -p --output-format stream-json --verbose [--include-partial-messages]` gives NDJSON
  events:
  - `system/init` with model, tools, MCP status, and a `capabilities` array
  - `assistant` and `user` messages, `stream_event` deltas, and `permission_denied` events
  - `system/api_retry` with `attempt`, `error_status`, and
    `error: rate_limit | authentication_failed | …`
  - a final `result` with `session_id`, `total_cost_usd`, and `permission_denials` [S5]
- Unattended permissions: `--permission-mode auto|acceptEdits|dontAsk|bypassPermissions`
  together with `--permission-prompts none`, which denies anything that would prompt and
  tells the model not to retry [S5]. Inside a sealed container, `bypassPermissions` is the
  simplest choice. `auto` adds a classifier.
- Limits: `--max-turns` and `--max-budget-usd` (the budget is a client-side estimate) [S6].
- Cancel: SIGINT, or the SDK's `interrupt()`, ends the turn cleanly. SIGTERM exits 143 and
  leaves the turn unfinished [S5].
- Steering (open question 5): `--input-format stream-json` keeps stdin open for more user
  messages. A message still queued when a turn ends starts a new turn, and
  `--replay-user-messages` acknowledges it [S6]. That gives live steering without the SDK.
- Security note: without `--bare`, `-p` runs the repo's `.claude/settings.json` hooks and
  `.mcp.json` servers "even in a folder you've never trusted" [S5]. That's acceptable only
  because the workload is the security boundary (non-negotiable 10). Pass
  `--setting-sources` and `--strict-mcp-config` to stay deterministic.

**Verdict:** drive Claude Code as `claude -p` with stream-json in and out. The Agent SDK adds
nothing Satat needs, adds the TS SDK's proprietary terms, and sits in the grey zone above.
This holds whatever the extension language: the driver is just a subprocess plus an NDJSON
parser.

## 3. OpenHands SDK (`OpenHands/software-agent-sdk`)

- License MIT, v1.55.0 released 2026-10-15, very active [S1].
- **Own loop.** It's the Python SDK behind OpenHands, with agents, tools, workspaces, and
  events. The agent-server exposes a REST/WebSocket API and runs agents "inside ephemeral
  workspaces (e.g. in Docker or Kubernetes)". Scheduling, webhooks, and run history live in
  a separate `OpenHands/automation` repo [S8].
- **`ACPAgent`** (`openhands/sdk/agent/acp_agent.py`, about 4.7k lines) "delegates to an ACP
  server… The ACP server manages its own LLM, tools, and execution" [S8]. Built-in provider
  presets: `claude-code`, `codex`, `gemini-cli`, `kimi-code`, `pi`, `opencode`, and `custom`.
  Default commands include `npx -y @agentclientprotocol/claude-agent-acp` and pi-acp [S9].
- The provider presets record hard-won adapter facts that Satat should reuse:
  - Claude: `CLAUDE_CODE_OAUTH_TOKEN` is "dominant" and causes `ANTHROPIC_API_KEY` and
    `ANTHROPIC_BASE_URL` to be stripped.
  - opencode: "Zen's endpoint comes from the model catalogue… neither OPENCODE_BASE_URL nor
    OPENAI_BASE_URL diverts it. A per-provider `options.baseURL` in the config file is the
    only override." Its modes are `build` and `plan`.
  - pi: "ANTHROPIC_BASE_URL reaches only the vendored Anthropic SDK… Redirecting it needs
    models.json or an extension" [S9].
- Fit: if the extension language (open question 7) is **Python**, the OpenHands SDK
  (`ACPAgent` plus `openhands-workspace`) could *be* Satat's in-workload runner. If it's **Go
  or TypeScript**, treat it as a reference implementation only. Either way it's a harness or
  runner library, not orchestration. This is the stack Satat runs today, which is why the
  requirements say it can't scale out in open source.

## 4. Agent Client Protocol (ACP) as the harness contract

**Spec and governance**

- Repo `agentclientprotocol/agent-client-protocol`, Apache-2.0, about 4.5k stars, schema
  v1.25.0 released 2026-10-09 [S12].
- "The current stable ACP protocol version is `1`." **v2 has been in Draft since
  2026-07-20** [S12][S15].
- Governance: "jointly governed by Zed and JetBrains… interim governance model… working
  toward transitioning to an independent foundation". Lead maintainers are from Zed and
  JetBrains. There's an RFD process, every repo is Apache-2.0, and no CLA is required [S18].
- Official SDKs exist for Rust, TypeScript (both 1.0), Python, Kotlin, and Java. There's
  also an ACP test-compatibility kit (`acp-tck`) [S13][S19].
- Maturity: solid and widely adopted. Its single-vendor-pair governance and the pending v2
  breaking changes are the main risks.

**Transport and flow.**

- Transport: stdio carrying newline-delimited JSON-RPC 2.0. The client launches the agent as
  a subprocess. Streamable HTTP is still "in discussion" [S16].
- Flow:
  1. `initialize`
  2. `session/new` (with `cwd` and MCP servers)
  3. `session/prompt`
  4. The agent streams `session/update` notifications (message chunks, thought chunks, tool
     calls and updates, plans, usage).
  5. The agent may call `session/request_permission`.
  6. The response carries a `stopReason`: `end_turn`, `max_tokens`, `max_turn_requests`,
     `refusal`, or `cancelled`.
- Cancel: the client sends `session/cancel`. It "MUST respond to all pending
  `session/request_permission` requests with the `cancelled` outcome", and the agent "MUST
  respond… with the `cancelled` stop reason". There's also a generic `$/cancel_request`
  [S16].

**Suited to headless use? Yes, with a policy client.**

- Permission requests are answered by the Satat runner from policy: `allow_always` inside
  the sandbox, or deny when a step is read-only.
- Logs: `session/update` streams per tool call and per chunk. Usage updates are now stable.
- Live steering:
  - ACP v1 has no mid-turn input. The workaround is cancel and re-prompt, or prompting again
    after `end_turn`.
  - The v2 prompt-lifecycle RFD separates prompts from turns and adds an "idle" state, which
    explicitly enables "queueing, steering". But "This RFD does not specify queueing,
    steering" semantics [S15][S16].
  - **If the owner wants live steering, ACP v1 gives only "cancel and redirect".** True
    steering needs either native harness channels (Claude stream-json input, pi RPC
    `steer`) or ACP v2 later.
- Credentials: ACP doesn't standardise how keys or base URLs are supplied. The "Configurable
  LLM Providers" RFD (`providers/list|set|disable`) is a draft [S17]. **That's good for
  Satat:** credentials keep coming from the workload's env and config files, which is
  exactly what credential lanes enforced by infrastructure need.

**Who speaks ACP in October 2026** (from the ACP registry repo, about 45 agents listed) [S11]:

| Harness | ACP | How |
|---|---|---|
| Claude Code | Through an adapter | `@agentclientprotocol/claude-agent-acp@0.91.0`. Authors: Anthropic, Zed, JetBrains. Built on Agent SDK 0.3.300 [S10][S11] |
| opencode | Native | `opencode acp` (stdio nd-JSON) [S20][S11] |
| pi | Third-party adapter | `pi-acp@0.0.34` (svkozak, MIT), which spawns `pi --mode rpc`. Several other forks exist [S29][S11] |
| Goose | Native | `goose acp` [S31][S11] |
| Gemini CLI | Native | `gemini --acp` [S11] |
| Codex | Adapter | `@agentclientprotocol/codex-acp@2.2.2`, Apache-2.0, built on the Codex App Server. The old `zed-industries/codex-acp` is archived [S11][S41] |
| Others | various | Cursor, GitHub Copilot CLI, Junie, Cline, Kimi, Qwen Code, Mistral Vibe, factory-droid, and more [S11] |

**Alternatives compared.**

- *Plain headless CLI with JSON output:* every launch harness has one. It's the simplest
  path, but each harness has its own event schema, so you write N parsers. Steering is
  harness-specific.
- *coder/agentapi:* emulates a terminal around the TUIs. **Archived and deprecated**; Coder
  now points to its own agent harness [S34]. Reject.
- *A2A:* Linux Foundation, Apache-2.0, v1.0.1 [S35]. It's for long-lived agent *services*
  talking to each other over HTTP, not for a runner driving a CLI in the same container.
  Wrong layer.
- *OpenHands agent-server REST/WS:* only if Satat adopts OpenHands as the runner.

## 5. Goose (`aaif-goose/goose`)

- Apache-2.0. v1.54.0 released 2026-10-08. About 55k stars. "Part of the Agentic AI
  Foundation (AAIF) at the Linux Foundation" [S30].
- Headless: `goose run -t … | -i file | --recipe r.yaml --params k=v --sub-recipe …`, with
  `--no-session`, `--max-turns`, `-q`, and `--output-format text|json|stream-json` [S31].
- Recipes are YAML files with sub-recipes. They're declarative agent tasks, not a durable
  workflow engine with queueing, caps, or human gates.
- ACP: runs as a server with `goose acp`. It can also use ACP agents as "providers", wrapping
  claude-agent-acp, codex-acp, and pi-acp, which makes it an ACP *client* too [S32]. The
  limitation there: "No session fork or resume".
- Credentials: `GOOSE_PROVIDER`, `GOOSE_MODEL`, `GOOSE_PROVIDER__TYPE`,
  `GOOSE_PROVIDER__HOST`, `GOOSE_PROVIDER__API_KEY` [S33].
- **Verdict:** more than a harness (desktop app, server, recipes, ACP client), but not an
  orchestrator in Satat's sense. It's a credible future fourth harness through `goose acp`.
  Don't use it as the adapter layer: putting Goose between Satat and Claude Code would add a
  hop and put the OAuth token behind a non-Claude-Code process, which is the grey zone
  again.

## 6. pi (`earendil-works/pi`, formerly `badlogic/pi-mono`)

- MIT. v1.2.0 released 2026-10-12, a fast cadence (v1.0.4 on 10-05, v1.1.0 on 10-07). About
  115k stars. Package `@earendil-works/pi-coding-agent` [S23][S29].
- Modes [S23]:
  - `pi --print "…"`: one-shot, prints the final text.
  - `pi --mode json "…"`: one-shot, JSONL events to stdout. Diagnostics go to stderr.
  - `pi --mode rpc`: long-lived, JSONL commands on stdin, responses and events on stdout.
  - Useful flags: `--no-session`, `--session-dir`, `--model provider/id`, `--tools`.
- JSON events [S24]: a `session` header, then `agent_start`, `turn_start`, `message_start`,
  `message_update` (text deltas), `message_end`, tool events, `turn_end`, `agent_end`
  (`willRetry`), and `agent_settled` (`aborted`). **Wait for `agent_settled`, not
  `agent_end`.** Don't use Node `readline`, because U+2028 and U+2029 are valid inside JSON
  strings.
- RPC control [S26]:
  - `prompt`
  - `steer`: "delivered after the current assistant turn finishes executing its tool calls,
    before the next LLM call"
  - `follow_up`
  - `abort`
  - `get_state`, `get_session_stats`, `set_model`, and others
- **pi has the best native steering of the three harnesses.**
- No permission prompts: pi "does not ask for approval before every tool call". Its docs
  recommend container isolation as "the strongest practical option" [S27]. That fits Satat's
  model.
- Project trust gates `.pi/settings.json`, `.pi/skills`, and `.pi/extensions` [S27].
  **UNVERIFIED:** how trust resolves in `--mode json` or `rpc` without a TTY. The adapter
  must make sure repo-supplied skills load, or pass skills explicitly.
- OpenAI-compatible gateway [S28]: in `$PI_CODING_AGENT_DIR/models.json` (default
  `~/.pi/agent`), add a provider with `"baseUrl": "http://litellm:4000/v1"`,
  `"api": "openai-completions"` (or `openai-responses`), and `"apiKey": "$LITELLM_API_KEY"`.
  The env interpolation forms are `$NAME`, `${NAME}`, or `!command`. Credential precedence:
  `--api-key`, then `auth.json`, then `models.json` `apiKey`, then provider env vars.
- ACP: **not native**. pi-acp is a third-party MVP from one maintainer (v0.0.34, Zed-centred,
  "expect some minor breaking changes") [S29]. OpenHands and Goose both depend on it anyway
  [S9][S32].

## 7. opencode (`anomalyco/opencode`, formerly `sst/opencode`)

- MIT. v1.19.0 released 2026-10-11. About 214k stars [S1].
- Headless [S20]: `opencode run [message] --format json --model provider/model --agent …
  --dir …`, with `--auto` to auto-approve permissions "that are not explicitly denied".
  - Without `--auto`, permission requests are auto-rejected and the model is told "This
    non-interactive run cannot ask the user for permission" [S21].
  - Internally, `run` drives an opencode server, attached or local.
  - `opencode serve` is a headless HTTP server. Basic auth comes from
    `OPENCODE_SERVER_PASSWORD`.
- JSON output: JSONL with `type`, `timestamp`, and `sessionID`. Types seen in the source:
  `step-start`, `text`, `reasoning`, `tool`, `step-finish`, `aborted`, `error` [S21]. The
  schema isn't documented on the CLI page; treat it as unstable.
- ACP: native, `opencode acp` (stdio nd-JSON) [S20]. Modes are `build` and `plan` [S9].
- OpenAI-compatible gateway [S22]: add a provider with `"npm": "@ai-sdk/openai-compatible"`
  (or `@ai-sdk/openai` for `/v1/responses`), `"options": {"baseURL": "http://litellm:4000/v1",
  "apiKey": "{env:LITELLM_API_KEY}"}`, and a `models` map. Supply the config with
  `OPENCODE_CONFIG_CONTENT` (inline JSON) or `OPENCODE_CONFIG`. Permissions go in
  `OPENCODE_PERMISSION`.
- Credential-lane caveat:
  - Env vars don't redirect the base URL [S9].
  - Third-party keys present in env (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, …) "also enable
    their providers inside OpenCode" [S9].
  - So the opencode workload must receive **only** the LiteLLM key.
  - **UNVERIFIED:** whether `enabled_providers` / `disabled_providers` config can also turn
    off the built-in Zen provider.

## 8. Other harness-contract and adapter projects

- **ACP registry** (`agentclientprotocol/registry`, Apache-2.0): JSON manifests per agent,
  with pinned versions, `npx` or binary distribution, and sha256 checksums [S11]. Satat can
  reuse the manifest format to pin harness versions in workload images.
- **acp-tck**: a conformance kit that "launches an agent implementation as a stdio
  subprocess, drives it through… prompt turns, cancellation, error handling" [S19]. Use it in
  CI to qualify each harness and adapter version before rollout.
- **Codex App Server** (`openai/codex`, Apache-2.0): Codex's own JSON-RPC server, which
  codex-acp now builds on [S41]. Only relevant if Codex becomes a harness.
- **anthropics/claude-code-action** (MIT): the reference for running Claude Code unattended
  on `CLAUDE_CODE_OAUTH_TOKEN` [S14].

## 9. Recommended harness contract for Satat

**Shape:** a thin **Satat runner** binary as the entrypoint of every step workload. The
workload runtime driver (Docker, Kubernetes, ECS, and so on) starts it with:

- `/run/satat/request.json`, describing the step:
  - repo URL and base ref
  - work branch
  - task text and issue reference
  - instructions or skill reference
  - harness id and version
  - model
  - limits: max turns, wall-clock timeout, budget
  - permission policy
- secrets mounted per lane (below).

The runner then does the following:

1. **Prepare.** Clone the repo with a short-lived, read-scoped token, check out the work
   branch, and materialise the skill or instructions: `.claude/skills/` for Claude Code,
   `.pi/skills` for pi, and opencode's commands or agents for opencode. Then **drop the
   clone token.** The harness never sees a GitHub write credential (non-negotiable 10).
2. **Preflight the lane.**
   - Claude: assert `claude auth status` → `authMethod: oauth_token`, and assert that none of
     `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, or
     `CLAUDE_CODE_USE_*` are present.
   - Open harnesses: assert that `CLAUDE_CODE_OAUTH_TOKEN` is absent and that only the
     LiteLLM key is present.
   - Fail closed on any mismatch.
3. **Drive the harness** through a driver interface: `start(request) → events`,
   `steer(text)`, `cancel()`, `wait() → outcome`.
4. **Normalise events** into one Satat event schema modelled on ACP `session/update`: agent
   message chunk, thought, tool call start/update/finish, plan, usage, rate-limit, and
   error. Stream them to stdout or OTLP so the control plane can show logs. Keep the raw
   harness transcript as an artifact.
5. **Finish.** Collect the diff and test results. Then **the runner, not the harness,**
   commits, pushes with a short-lived GitHub App installation token injected only at this
   stage, and opens or updates the draft PR. Write
   `/run/satat/result.json`: `status`, `stopReason`, `branch`, `headSha`, `prUrl`,
   `artifacts[]`, `usage`, `rateLimited`.

### Drivers to build

| Harness | Driver (no live steering) | Driver (with live steering) | Lane secrets given to the workload |
|---|---|---|---|
| Claude Code | `claude -p --output-format stream-json --verbose --permission-mode bypassPermissions --permission-prompts none --max-turns N --setting-sources project --strict-mcp-config --mcp-config …` with the prompt as an argument. Parse NDJSON. Map `system/api_retry{error:rate_limit}` and `result` to Satat events. Cancel with SIGINT, then SIGTERM | Same, plus `--input-format stream-json --replay-user-messages`. Steer by writing a user message to stdin | `CLAUDE_CODE_OAUTH_TOKEN` only, plus a fresh `CLAUDE_CONFIG_DIR`. Optional managed-settings guard (UNVERIFIED) |
| opencode | Through ACP: `opencode acp`, then `session/new` → `session/prompt` → map `session/update`. Cancel with `session/cancel`. (Fallback: `opencode run --format json --auto`) | ACP v1 gives cancel and re-prompt only. Real mid-turn steering needs ACP v2 or `opencode serve`'s HTTP API (**UNVERIFIED** endpoints) | `OPENCODE_CONFIG_CONTENT` (provider `@ai-sdk/openai-compatible`, `baseURL` = LiteLLM, `apiKey: {env:LITELLM_API_KEY}`), plus `LITELLM_API_KEY`. No other provider keys |
| pi | `pi --mode json --no-session --model litellm/<id> "…"`. Parse JSONL until `agent_settled`. Cancel with SIGINT/SIGTERM (**UNVERIFIED** clean-abort semantics in json mode) | `pi --mode rpc --no-session`: `prompt`, `steer`, `abort`, wait for `agent_settled` | `PI_CODING_AGENT_DIR` holding `models.json` (`baseUrl` = LiteLLM, `api: openai-completions`, `apiKey: "$LITELLM_API_KEY"`), plus `LITELLM_API_KEY` |

**Why this split rather than ACP everywhere:**

- Claude Code over the unmodified CLI is the only route the Anthropic docs clearly allow for
  the OAuth token (section 2).
- pi has no first-party ACP. Its native RPC is first-party, documented, and has the best
  steering.
- opencode's ACP is first-party, so ACP is the natural driver there.
- New harnesses (Goose, Gemini CLI, Codex, Strands) mostly speak ACP. So a **generic ACP
  driver** makes "adding a harness is an adapter plus configuration" (non-negotiable 2) true
  for most future harnesses: you add a registry-style manifest and a lane config.
- Shaping the Satat event schema after ACP keeps the door open to collapsing everything onto
  ACP v2 once it's stable, and once claude-agent-acp's terms position is clear.

### Verdicts by open question

- **Q5, live steering:**
  - *No (logs plus cancel and retry):* use one-shot drivers for all three (`claude -p`
    stream-json, `opencode acp` or `run --format json`, `pi --mode json`). The runner is a
    small subprocess supervisor, about a few hundred lines per driver.
  - *Yes:* use bidirectional drivers (Claude `--input-format stream-json`, pi `--mode rpc`).
    opencode is limited to cancel and re-prompt until ACP v2. The control plane then needs a
    channel back into the running workload, such as a runner-side gRPC or WebSocket
    endpoint, or a queue topic. That's a cost in the runtime-driver and ingress layers, not
    in the harness layer.
- **Q4, license bar:**
  - *OSI-only:* the runner, ACP, opencode, pi, pi-acp, Goose, and Strands all pass.
  - Claude Code and the TS Agent SDK are proprietary under Commercial or Consumer Terms. Put
    Claude Code in as an operator-installed, unmodified dependency, fetched at image build or
    run time and never vendored. Satat's own code stays OSI-licensed. If even that is
    unacceptable, Claude Code can't be a launch harness, which contradicts non-negotiable 2.
    So the owner should confirm that "OSI-only" applies to Satat's components, not to the
    harnesses it launches.
  - *Source-available OK:* no change.
  - Prefer the **direct CLI over claude-agent-acp** under either answer: the adapter code is
    Apache-2.0, but it bundles the proprietary SDK.
- **Q7, extension language:**
  - *Go:* write the runner and drivers natively. There's no official Go ACP SDK, so either
    implement JSON-RPC over stdio by hand (small) or use a community library (UNVERIFIED
    quality). A single static binary is ideal for workload images.
  - *TypeScript:* official ACP TS SDK 1.0, pi's `RpcClient`, and opencode's SDK are all
    first-party. This is the lowest-effort option.
  - *Python:* official ACP Python SDK. Or adopt the OpenHands SDK `ACPAgent` as the runner
    core and inherit its provider-specific credential handling (MIT), at the cost of a
    heavier image.
  - *Rust:* official ACP Rust SDK 1.0.

### What not to do

- Don't use Strands, Goose, or the OpenHands SDK as the scheduler, workflow engine, or
  control plane. Their orchestration is in-process LLM routing, not durable work-item
  orchestration.
- Don't put any process other than the unmodified `claude` binary between Satat and the
  OAuth token.
- Don't give a harness a GitHub write token. Push and open PRs from the runner after the
  harness exits.
- Don't use `claude --bare` on the subscription lane: it ignores `CLAUDE_CODE_OAUTH_TOKEN`.

## Open items and UNVERIFIED claims

- Whether managed `forceLoginMethod: "claudeai"` accepts an env `CLAUDE_CODE_OAUTH_TOKEN`
  and blocks API keys. Test in a container.
- pi project-trust behaviour in `--mode json` and `--mode rpc`, and SIGINT/abort semantics
  in json mode.
- opencode `serve` HTTP endpoints for mid-run messages, and whether opencode's Zen provider
  can be turned off by config.
- Whether Anthropic would treat claude-agent-acp on an OAuth token as "ordinary use of
  Claude Code" or as "third-party developers offer[ing] claude.ai login". Only Anthropic can
  answer that; the legal page links to sales for questions about permitted authentication.
- The opencode JSON event schema isn't documented and is inferred from the source.

## Sources

- [S1] GitHub API repo and LICENSE checks, 2026-10-15:
  - https://github.com/anthropics/claude-code (LICENSE.md)
  - https://github.com/anthropics/claude-agent-sdk-typescript (LICENSE.md)
  - https://github.com/anthropics/claude-agent-sdk-python
  - https://github.com/OpenHands/software-agent-sdk
  - https://github.com/anomalyco/opencode
  - https://github.com/earendil-works/pi
  - https://github.com/strands-agents/harness-sdk
  - https://github.com/aaif-goose/goose
- [S2] Claude Code legal and compliance: https://code.claude.com/docs/en/legal-and-compliance
- [S3] Claude Code authentication (setup-token, precedence, bare mode):
  https://code.claude.com/docs/en/authentication
- [S4] Agent SDK overview (note on claude.ai login, license and terms):
  https://code.claude.com/docs/en/agent-sdk/overview
- [S5] Run Claude Code programmatically (headless):
  https://code.claude.com/docs/en/headless
- [S6] Claude Code CLI reference: https://code.claude.com/docs/en/cli-reference
- [S7] Anthropic Consumer Terms: https://www.anthropic.com/legal/consumer-terms
- [S8] OpenHands SDK README and ACPAgent:
  - https://github.com/OpenHands/software-agent-sdk
  - https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/agent/acp_agent.py
- [S9] OpenHands ACP provider presets:
  https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/settings/acp_providers.py
- [S10] claude-agent-acp (LICENSE, package.json, src/auth-status.ts):
  https://github.com/agentclientprotocol/claude-agent-acp
- [S11] ACP registry (claude-acp, opencode, goose, pi-acp, codex-acp, gemini manifests):
  https://github.com/agentclientprotocol/registry
- [S12] ACP spec repo README and GOVERNANCE:
  https://github.com/agentclientprotocol/agent-client-protocol
- [S13] ACP docs index: https://agentclientprotocol.com/llms.txt
- [S14] Claude Code GitHub Actions (`CLAUDE_CODE_OAUTH_TOKEN`):
  - https://code.claude.com/docs/en/github-actions
  - https://github.com/anthropics/claude-code-action
- [S15] ACP v2 Draft announcement:
  https://agentclientprotocol.com/announcements/acp-v2-draft
- [S16] ACP prompt turn, cancellation, transports, and the v2 prompt-lifecycle RFD:
  - https://agentclientprotocol.com/protocol/v1/prompt-turn
  - https://agentclientprotocol.com/protocol/v1/cancellation
  - https://agentclientprotocol.com/protocol/v1/transports
  - https://agentclientprotocol.com/rfds/v2/prompt
- [S17] ACP RFD Configurable LLM Providers:
  https://agentclientprotocol.com/rfds/custom-llm-endpoint
- [S18] ACP governance: https://agentclientprotocol.com/community/governance
- [S19] ACP test compatibility kit: https://github.com/agentclientprotocol/acp-tck
- [S20] opencode CLI docs: https://opencode.ai/docs/cli/
- [S21] opencode run implementation:
  - https://github.com/anomalyco/opencode/blob/dev/packages/cli/src/run/run.ts
  - https://github.com/anomalyco/opencode/blob/dev/packages/cli/src/run/noninteractive.ts
- [S22] opencode providers docs: https://opencode.ai/docs/providers/
- [S23] pi CLI docs:
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/cli.md
- [S24] pi JSON event stream:
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/json.md
- [S25] pi RPC mode:
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md
- [S26] pi RPC commands:
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc-commands.md
- [S27] pi security:
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/security.md
- [S28] pi models and providers:
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md
- [S29] pi-acp: https://github.com/svkozak/pi-acp
- [S30] Goose README (AAIF, license): https://github.com/aaif-goose/goose
- [S31] Goose CLI commands (run, acp):
  https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/goose-cli-commands.md
- [S32] Goose ACP providers:
  https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/acp-providers.md
- [S33] Goose environment variables:
  https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/environment-variables.md
- [S34] coder/agentapi (archived, deprecation notice): https://github.com/coder/agentapi
- [S35] A2A: https://github.com/a2aproject/A2A
- [S36] Strands harness-sdk README: https://github.com/strands-agents/harness-sdk
- [S37] Strands CLI README (`--acp-server`):
  https://github.com/strands-agents/harness-sdk/blob/main/strands-cli/README.md
- [S38] strands-acp catalog entry:
  - https://github.com/strands-agents/harness-sdk/blob/main/site/src/content/catalog/strands-acp.yaml
  - https://github.com/ryancormack/strands-acp
- [S39] Strands multi-agent and deploy docs:
  - https://github.com/strands-agents/harness-sdk/tree/main/site/src/content/docs/user-guide/sdk/multi-agent
  - https://github.com/strands-agents/harness-sdk/tree/main/site/src/content/docs/user-guide/sdk/deploy
  - https://github.com/strands-agents/harness-sdk/tree/main/strands-py/src/strands/multiagent
- [S40] Strands checkpoint and Temporal:
  - https://github.com/strands-agents/harness-sdk/blob/main/strands-py/src/strands/experimental/checkpoint/__init__.py
  - https://github.com/strands-agents/harness-sdk/blob/main/site/src/content/catalog/temporal.yaml
- [S41] codex-acp move notice and new repo:
  - https://github.com/zed-industries/codex-acp
  - https://github.com/agentclientprotocol/codex-acp
