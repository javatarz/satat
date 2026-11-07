# Claude CLI through an LLM gateway proxy (#155)

Part of #99. This spike checks the design in [ADR 0032](../../adr/0032-secret-store.md). In that
design, an LLM gateway proxy holds the long-lived Claude subscription OAuth token. Each agent run
gets a short-lived virtual key with a budget (the run's virtual key). The spike continues from
[claude-lane-spike.md](claude-lane-spike.md) (#102). In #102, Claude Code used `ANTHROPIC_BASE_URL`,
but the upstream API sent only 500 errors. The decision from this spike is
[ADR 0034](../../adr/0034-llm-gateway-holds-model-credentials.md).

**Verdict:** the design works. LiteLLM satisfies all properties in the ticket. The `/anthropic`
pass-through route is the best route. With this design, the agent run holds no critical secret.
LiteLLM is also one location for cost, traces and, at a later time, conversation export.

## Setup

- Docker Compose on colima, on 2026-10-27 and 2026-10-28. The harness and the raw results are on
  the throwaway branch `prototype/155-gateway-proxy` in `docs/scratch/155/`. That branch does not
  merge. After each run, `down -v` removed the stack.
- LiteLLM 1.106.4 from `docker.io/litellm/litellm`. The GHCR image sent 403. Postgres 15.6. An
  OpenTelemetry collector with a debug exporter.
- OpenBao 2.7.2 in dev mode, with the layout from [ADR 0032](../../adr/0032-secret-store.md): KV v2
  mount `satat/`, entry `claude-oauth-token`, and AppRole `litellm`. The AppRole accepts logins only
  from the fixed address of LiteLLM on an internal `secrets` network.
- Lane: Node 22, Claude Code 2.1.301, a non-root user and Satat's managed settings. The lane is on
  an internal `lane` network. The only other member of that network is LiteLLM.
- One real credential: a Claude token from the subscription of the developer who ran the spike.
  `claude setup-token` made this token. All other credentials were placeholders. The raw results
  do not contain the real token.

## Results

| # | Question | Result |
|---|---|---|
| 1 | Upstream OAuth | **Pass.** `claude -p` worked with only a virtual key in the lane. The managed settings need changes (finding 1). |
| 2 | Claude token never in the lane | **Pass.** The Claude token is not in the lane's files, environment, `/proc` or process list. It is not in the responses on 19 routes. It is not in LiteLLM logs, Postgres or OTLP spans. Claude Code refuses a planted `ANTHROPIC_API_KEY` (exit 1). |
| 3 | Run's virtual key | **Pass.** LiteLLM applies the model list, the expiry and the deletion. The budget holds under concurrent calls (finding 4). The worker creates and deletes virtual keys with the minter key, the key of a non-admin LiteLLM user, from OpenBao (finding 5). |
| 4 | Reach | **Pass.** The lane has no DNS and no route out. It can connect only to LiteLLM. Admin routes refuse a virtual key. |
| 5 | Token delivery | **Pass.** OpenBao Agent logs in and runs LiteLLM. A new Claude token in OpenBao gets to LiteLLM in about 20 s, without a rebuild (finding 6). |
| 6 | Observability | **Pass.** Access log, cost for each key, and OTLP spans. By default, the spans contain the full conversation (finding 7). |
| 7 | Terms | **Decided** for personal mode (see [Terms](#terms)). |

## Findings

1. **The managed settings must allow a custom endpoint.** With `allowedProviders: ["anthropic"]`,
   Claude Code refuses a custom `ANTHROPIC_BASE_URL`. Claude Code works with
   `allowedProviders: ["customEndpoint"]` and with `ANTHROPIC_BASE_URL` set in the managed `env`
   block (Claude Code docs, `llm-gateway`). `forceLoginMethod: "claudeai"` can stay. The lane gives
   the virtual key to Claude Code as `CLAUDE_CODE_OAUTH_TOKEN`. Then `claude auth status` shows
   `oauth_token`.
2. **Use LiteLLM's Anthropic pass-through route (`/anthropic`), not LiteLLM's `/v1/messages`
   route.** Both routes work. To see what each route sends to Anthropic, the spike sent LiteLLM's
   upstream calls to a test listener instead of Anthropic. The listener recorded each request and
   its headers. LiteLLM held a fake Claude token for this test, so the records contain no real
   credential. The records showed these differences:
   - `/v1/messages` sends the Claude token as `Authorization: Bearer` with the OAuth betas
     (`claude-code-20250219`, `oauth-2025-04-20`) and `anthropic-dangerous-direct-browser-access`.
     It also changes these items:
     - It changes the user-agent to `litellm/1.106.4`.
     - It removes `x-app` and the session id of Claude Code.
     - It makes a new request body.
     - It removes or renames the `anthropic-ratelimit-unified-*` headers, and it removes
       `retry-after`.
     - It sends a 429 again two times.

     Then Claude Code shows a rate-limit event with no window and no reset time.
   - `/anthropic` sends the request and headers of Claude Code without change. It replaces only
     `Authorization`. The rate-limit headers and the 429 responses come back without change, and
     LiteLLM does not try again. Claude Code shows the full event (`five_hour`, `resetsAt`,
     utilization). #171 needs this event. A run with 3 turns and tool use worked. The model list,
     the budget, the deletion and the cost records also apply on this route
     (`call_type: pass_through_endpoint`).
   - Configuration: `ANTHROPIC_API_KEY` in the environment of LiteLLM holds the Claude token for
     this route. The base URL of the lane is `http://litellm:4000/anthropic`.
3. **The pass-through route sends requests on any path to Anthropic. Only an admin can limit a
   virtual key to some paths.** The scope of the Claude token allows only inference. Thus Anthropic
   refuses the profile, usage, organization and files endpoints (403). `/v1/models` works.
   - `allowed_routes: ["/anthropic/v1/messages"]` on a virtual key stops all other paths. Claude
     Code continues to work.
   - A non-admin user gets 403 for this setting: "Only proxy admins can set `allowed_routes` on a
     key. Use `key_type` to pick a preset route bucket instead." The keys of a non-admin user use
     `key_type: "llm_api"`. These keys can call any path through the pass-through route.
   - Thus there are two options for the minter key, the LiteLLM key that the worker uses to create
     virtual keys (finding 5). It can belong to a proxy admin. A proxy admin can change models and
     configuration, and can read all cost records. Or it can belong to a non-admin user. Then, for
     paths other than `/v1/messages`, the virtual keys depend on the inference-only scope of the
     Claude token.
4. **The budget holds under concurrent calls.** The test used Haiku 4.5 on `/anthropic`, at about
   $0.0015 for each call. LiteLLM accepted only a few calls and refused the others immediately
   (422). A second wave got only 422:

   | Concurrent calls | Budget | Accepted | Final cost |
   |---|---|---|---|
   | 20 | $0.002 | 2 | $0.003038 |
   | 20 | $0.01 | 7 | $0.010633 |
   | 40 | $0.005 | 4 | $0.006076 |

   Before each call, LiteLLM reserves the maximum cost of the call against the budget, in memory
   (`budget_reservation.py` in LiteLLM 1.106.4). The final cost can go over the budget by part of
   one call.
5. **The worker creates keys with a non-admin key from OpenBao.** A virtual key cannot create keys
   (401). A LiteLLM `internal_user` can create and delete only its own keys. Here the user is
   `satat-minter`, with its own model list and total budget. This user cannot change users, models
   or configuration. Its keys can ask for more. But the model list of the user limits the calls
   (403), and the total budget of the user limits them too (422 "User=satat-minter over budget").
   The key of the user can also call models.
   - The spike tested this from start to end. The operator sent the minter key (the key of
     `satat-minter`) into OpenBao through a pipe, as `litellm-minter-key`. Only a `worker` AppRole
     can read it. That AppRole accepts logins only from the address of the worker. A container at
     that address did these actions:
     1. It logged in.
     2. It tried to read `claude-oauth-token`, and OpenBao refused.
     3. It read `litellm-minter-key`.
     4. It created a run's virtual key. `claude -p` in the lane answered with that key.
     5. It deleted the run's virtual key. The next call got 401.

     The same AppRole from a different address got a refusal.
6. **OpenBao Agent runs LiteLLM.** The Vault secret manager in LiteLLM is a paid feature. Thus the
   spike adds the `bao` binary to the LiteLLM image as its entrypoint. The configuration has AppRole
   auto-auth, an `env_template` for the Claude token, and an `exec` of the LiteLLM entrypoint with
   `restart_on_secret_changes = "always"`.
   - The Claude token is not in `docker inspect`, in the image history or in the environment of
     OpenBao Agent. It is only in the environment of the LiteLLM child process.
   - New Claude token (a new `claude-oauth-token` value in OpenBao): LiteLLM restarted 6 to 11 s
     after the write. It served again 11 to 12 s after the restart. A dummy value caused 401 from
     Anthropic. The real value caused 200 again. The restart stops the requests that are in
     progress.
   - OpenBao refuses the AppRole credential from a different address (400). The policy allows reads
     of one entry only. OpenBao refuses all other reads, lists and writes. The lane cannot connect
     to OpenBao.
7. **By default, the traces contain the full conversation.** LiteLLM sends one
   `litellm_request` span for each call. The span contains:
   - the full request and the full response;
   - the key alias and the key hash;
   - the cost and the token usage;
   - the session id of Claude Code.

   Thus LiteLLM has all the data to make the conversation of an agent run again. Retrospectives
   and self-improvement loops can use this data. The spike did not make an export.

   On both routes, the setting `turn_off_message_logging: true` removes the request and the
   response from the span. The cost and the key alias stay. The cost records in Postgres do not
   contain prompts.
8. **Calls on both routes use the plan's usage, not extra usage.** Extra usage was off for the
   account. Thus Anthropic would refuse a call that it puts in extra usage (see the refusal below).
   Anthropic refused none of these calls (2026-10-28):
   - streamed calls through `/anthropic`;
   - streamed calls through `/v1/messages`;
   - a streamed call through `/v1/messages` with no Claude Code identity;
   - a full Claude Code session through `/v1/messages`: 3 turns, tool use and 6 upstream calls.

   The streamed calls also returned the same rate-limit headers: `representative-claim: five_hour`,
   `overage-status: rejected` (`org_level_disabled`) and the same utilization.

   Hermes Agent, a third-party client, gets a different result. It calls Anthropic directly with a
   subscription token. Its docs say that these calls use only extra usage, never the plan's usage
   ([providers.md](https://github.com/NousResearch/hermes-agent/blob/dc3923961431892f44bdf0bc10f8682e39485682/website/docs/integrations/providers.md?plain=1#L172-L175)).
   Its code comments say that Anthropic refuses some tool names
   ([anthropic_adapter.py](https://github.com/NousResearch/hermes-agent/blob/dc3923961431892f44bdf0bc10f8682e39485682/agent/anthropic_adapter.py#L546-L551)) and some prompt sentences
   ([prompt_builder.py](https://github.com/NousResearch/hermes-agent/blob/dc3923961431892f44bdf0bc10f8682e39485682/agent/prompt_builder.py#L245-L253)) with a billing-shaped HTTP 400, for
   example "Third-party apps now draw from extra usage". Inference, not tested: the spike's calls
   did not contain these tool names or prompt sentences. Claude Code's own calls through
   `/anthropic` are not changed, so they look like any Claude Code call.
9. **LiteLLM uses about 620 to 700 MiB of RAM, and adds tens of milliseconds to each call.**
   The measurement used colima with 4 CPUs and 4 GiB (2026-11-07). It used Satat's Postgres and
   Hatchet from `compose.yaml`, and a fake Anthropic upstream that streams for 4 s. LiteLLM
   shared the Postgres of Hatchet. The host did not swap.

   | Measurement | Value |
   |---|---|
   | LiteLLM 1.106.4 with OpenBao Agent, idle | about 620 MiB, 0.01 cores or less |
   | LiteLLM at 32 concurrent calls | about 700 MiB, 0.12 cores at most |
   | Time from start to ready | 17 to 18 s |
   | RAM that LiteLLM adds to the Postgres of Hatchet | about 90 MiB |
   | Added time to first byte, p50 | 20 to 55 ms |
   | Added time to first byte, p95 | 200 to 250 ms |
   | Added time for a whole 4 s streamed call, p50 | 10 to 55 ms |
   | Image of LiteLLM with OpenBao Agent | about 500 MB to download, about 1.6 GB unpacked |

   The latency rows compare calls to the upstream directly and through LiteLLM, at 1, 8 and 32
   concurrent calls. All calls succeeded. CPU did not limit LiteLLM. After the load, LiteLLM kept
   about 80 MiB more RAM than before. The run was too short to show if this RAM continues to
   increase. [platform-ram-spike.md](platform-ram-spike.md) measured LiteLLM idle at 590 to
   760 MiB.

## Terms

Decision on 2026-10-28: in personal mode, a gateway can hold the subscription token. Personal mode
is Satat on a developer's own machine with that developer's own subscription. The spike read the
Claude Code [legal and compliance](https://code.claude.com/docs/en/legal-and-compliance) page and
the [Usage Policy](https://www.anthropic.com/legal/aup) (effective 2026-11-12) on 2026-10-28. This note
does not cover organisation mode, where an organisation runs Satat autonomously on ephemeral
machines.

## Recommendation

- **Proxy:** LiteLLM 1.106.4 on the `/anthropic` pass-through route, with OpenBao Agent as its
  entrypoint. The spike did not need a different proxy. ADR 0034 selects LiteLLM because
  Satat v0.1 already used it. Other self-hostable gateways also accept Anthropic's protocol (see
  [llm-gateway-landscape.md](llm-gateway-landscape.md)), but the spike checked only LiteLLM. The
  spike shows that `/anthropic` keeps the request and the rate-limit headers of Claude Code without
  change. `/v1/messages` does not.
- **Key creation:** the worker runs the launcher. The worker reads the key of a LiteLLM
  `internal_user` from OpenBao (the minter key). With it, the worker creates each run's virtual key
  with `duration`, `models`, `max_budget` and `key_type: "llm_api"`. The worker deletes the run's
  virtual key at the end of the agent run. The budget of the user limits the total cost. For paths
  other than `/v1/messages`, the virtual keys depend on the inference-only scope of the Claude
  token. To limit virtual keys to `/anthropic/v1/messages`, the minter key must belong to an admin.

## On k3d (not tested)

These items are inferences from [ADR 0032](../../adr/0032-secret-store.md) and
[agent-sandbox-spike.md](agent-sandbox-spike.md):

- Kubernetes auth with the service account of LiteLLM replaces AppRole. OpenBao Agent stays the
  entrypoint.
- A NetworkPolicy replaces the internal `lane` network. In the agent-sandbox spike, the policies
  came into effect 2 to 4 s after the pod started. Thus the lane must wait for its policy before
  it sends a request to LiteLLM.

## Not covered

- The headers that LiteLLM sends to Anthropic with the real token. Finding 2 recorded these headers
  only with a fake token, sent to a test listener instead of Anthropic. A record with the real token
  would also contain the Claude token.
- Conversation export. The spike did not make it (finding 7).
- The RAM of a separate Postgres for LiteLLM. In the run with a separate Postgres, all containers
  showed 3 to 5 times more RAM, also OpenBao and Hatchet. Thus this note does not use that run.
- A real bill. The spike found plan usage or extra usage from the rate-limit headers. It also
  used the fact that Anthropic did not refuse calls while extra usage was off.
