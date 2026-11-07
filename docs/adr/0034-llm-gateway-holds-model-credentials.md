# ADR 0034: An LLM gateway holds the model credentials, so no agent run holds one

## Status

Accepted (2026-10-28). Decides #155. The spike in
[claude-gateway-proxy-spike.md](../research/v2/claude-gateway-proxy-spike.md) checks this design on
Docker Compose with Claude Code. This ADR gives the details of the gateway proxy that
[ADR 0032](0032-secret-store.md) names.

## Context

An agent run works on a repo that Satat does not trust. A design principle of Satat is that an agent
run can read no critical secret. GitHub credentials are already out of the agent run
([ADR 0025](0025-github-token-broker.md)). Model credentials are the one known gap. Today the worker
passes the Claude subscription token into the lane as `CLAUDE_CODE_OAUTH_TOKEN`
([design](../design.md#why-the-write-token-lives-only-in-the-pusher)).

Satat is to run at least three agents: Claude Code, OpenCode and Pi
([interchangeable agents](../roadmap/interchangeable-agents.md)). Thus this decision must apply to
all agents, not only to Claude Code. Claude Code is the first agent, and the only agent that the
spike tested.

Satat has two modes:

- **Personal mode:** a developer runs Satat on the developer's own machine, with the developer's
  own model subscription. The developer watches the runs.
- **Organisation mode:** an organisation deploys Satat at a larger scale. Satat runs autonomously
  on ephemeral machines. The organisation can replace any of these machines at any time. No
  developer watches the runs. This ADR does not decide this mode.

This ADR covers personal mode only. In personal mode, a gateway can hold the developer's
subscription token.

All model calls from all agent runs go through the gateway. Thus Satat can use the gateway to:

- record the cost of each agent run;
- send traces of each call;
- export each conversation, for automatic retrospectives and self-improvement loops
  ([roadmap](../roadmap/README.md)).

The gateway must accept the wire protocol of each agent. Claude Code uses Anthropic's protocol, and
many gateways use only OpenAI's protocol. Several open-source, self-hostable gateways accept both
(see [llm-gateway-landscape.md](../research/v2/llm-gateway-landscape.md)). This ADR selects LiteLLM
because Satat v0.1 already used it ([ADR 0002](0002-litellm-model-routing.md)). Like other
components of Satat, LiteLLM can be replaced later.

## Decision

### For all agents

- **LiteLLM holds the model credentials. The agent run holds only the run's virtual key.** The
  run's virtual key is a short-lived LiteLLM virtual key for one agent run. LiteLLM reads the model
  credentials from OpenBao with its own identity ([ADR 0032](0032-secret-store.md)). The worker
  passes the run's virtual key into the lane, in the variable that the agent reads for its
  credential.
- **The worker creates the run's virtual key before the agent run starts, and deletes it when the
  agent run ends.**
  - To create virtual keys, the worker uses the minter key. The minter key is the key of a LiteLLM
    non-admin user. The worker reads the minter key from OpenBao as `litellm-minter-key`.
  - Each run's virtual key has a `duration` of about the lane's `timeout`, a `models` list, a
    `max_budget`, and `key_type: "llm_api"`.
  - The model list and the budget of the minter key's user are the upper limits for the virtual
    keys of all agent runs.
- **The agent uses a LiteLLM pass-through route for its provider, when LiteLLM has one.** A
  pass-through route sends the agent's request and headers to the provider without change. It
  replaces only the credential. Thus the provider's rate-limit headers get to the agent.
- **OpenBao Agent is the entrypoint of LiteLLM.** OpenBao Agent logs in with the LiteLLM AppRole. It
  puts the model credentials into the environment of LiteLLM. When a credential changes, OpenBao
  Agent restarts LiteLLM. LiteLLM has its own Vault support, but that support is a paid feature.
- **The lane can connect only to LiteLLM** on its network. The lane cannot connect to OpenBao.

### For Claude Code

- LiteLLM reads the Claude subscription token from OpenBao as `claude-oauth-token`. Claude Code
  reads the run's virtual key as `CLAUDE_CODE_OAUTH_TOKEN`.
- Claude Code uses LiteLLM's Anthropic pass-through route, not LiteLLM's `/v1/messages` route. The
  lane sets `ANTHROPIC_BASE_URL` to `http://litellm:4000/anthropic`. Thus Claude Code sends its
  calls to `/anthropic/v1/messages`. The subscription rate-limit headers get to Claude Code, and
  Hatchet back-off (#171) uses them. The `/v1/messages` route changes the client identity and
  removes these headers.
- The managed settings of the lane allow `customEndpoint` and set `ANTHROPIC_BASE_URL`. Thus the
  agent cannot send Claude Code to a different endpoint.

## Consequences

- No agent run holds a long-lived credential. If a run's virtual key leaks, it works only until the
  agent run ends. It works only for the listed models and only up to its budget.
- LiteLLM and its Postgres are new services in the Docker Compose stack. If LiteLLM stops, all
  agent runs stop. After OpenBao, LiteLLM is the service in the stack with the highest security
  risk.
- LiteLLM uses about 620 to 700 MiB of RAM. It adds about 90 MiB to the Postgres of Hatchet.
  LiteLLM adds about 20 to 55 ms to the first byte of each model call
  ([finding 9](../research/v2/claude-gateway-proxy-spike.md#findings)).
- When the operator writes a new model credential to OpenBao, OpenBao Agent restarts LiteLLM.
  LiteLLM serves again after about 20 s. The restart stops the model requests that are in progress.
- A replacement for LiteLLM must accept the wire protocol of each agent, and must have pass-through
  routes that send requests and headers without change.
- Only a proxy admin can limit a virtual key to one path, for example `/anthropic/v1/messages`. The
  minter key cannot. Thus a run's virtual key can call other provider API paths through the
  pass-through route. For Claude Code, the scope of the Claude token allows only inference. Thus
  Anthropic refuses most of these paths, for example profile, organisations and files (403).
- In the spike, Claude Code calls through the gateway used the five-hour window of the plan, not
  extra usage.

## Not verified

- OpenCode and Pi through LiteLLM: which wire protocol each uses, which pass-through route fits, and
  how each reads the run's virtual key.
- The gateway on k3d. This includes Kubernetes auth for LiteLLM. It also includes a NetworkPolicy
  that is in effect before the agent run sends a request to LiteLLM.
- Conversation export. The open items are the sink, the retention and the source. The source can
  be LiteLLM or the transcript that the runner records.
- The headers that LiteLLM sends to Anthropic with the real Claude token. The spike recorded these
  headers only with a fake token, sent to a test listener instead of Anthropic. A record with the
  real token would also contain the token.
- Organisation mode, and the credentials that the gateway holds in that mode.

Do this decision again before Satat runs in organisation mode. Also do it again if LiteLLM
changes how its pass-through routes send requests.
