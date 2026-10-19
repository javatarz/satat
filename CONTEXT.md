# Satat

Satat (सतत, "continuous / constant") is a self-hosted, always-on system that runs AI coding
agents: it watches a GitHub backlog, picks up labeled issues, has an agent write code and run
tests, and opens draft pull requests for human review. Where it is heading is in
[docs/roadmap/](docs/roadmap/).

## Versions

Satat is pre-1.0: no version is a finished release. 1.0 means the factory vision works.

**v0.1**:
The Canvas/OpenHands stack on an EC2 VM with Docker Compose. Kept on the `v0.1` branch,
tagged `v0.1.0-final` (`8bba425`). Older docs describe this as "v1".

**v0.2**:
The platform on `main`: Hatchet, one-shot steps behind a launcher interface. Docker on a laptop
first (ADR 0026), Kubernetes with agent-sandbox later (ADR 0024). Older docs,
paths and issues call this "v2" (for example `docs/research/v2/`, #99). The terms mean the
same thing.

## Language

**Satat**:
The name of the whole system: the harness it runs agents in, its infrastructure, its repos, its
identity. Used in docs, commit messages, and notification text (e.g. "Satat opened PR #142").
_Avoid_: "the agent", "the bot", "openhands"

**Agent**:
The coding agent program that does the work: Claude Code, OpenCode or Pi, off the shelf today.
Its built-in tools (shell, file edits) come with it; the skills and tools Satat gives it are
part of the harness. Satat runs it one-shot in an isolated container for each step. In
v0.1 this was the OpenHands agent.
_Avoid_: "bot", "worker", "binary", "harness"

**Model**:
The LLM the agent calls, reached through LiteLLM. Chosen separately from the agent, so any
agent can be paired with any model it supports.
_Avoid_: "agent" for the model

**Harness**:
Everything around the agent that makes it likely to succeed. It has two halves: context and
loops. Satat builds and runs the harness; it does not build the agent.
_Avoid_: using "harness" for the agent itself (e.g. "Claude Code is the harness")

**Context**:
The half of the harness that primes the agent: intent made durable, versioned and visible, such
as skills, docs and memory. Advisory: the agent can read it and still ignore it.

**Loop**:
The half of the harness that enforces: a check wired so that failing it goes back to the agent.
An **inner loop** answers while the agent can still fix the work (lint, tests, the judge and
reviewer before the draft PR). An **outer loop** answers after the work has shipped, so it can
only change the next piece of work (conversation retros that raise tickets).
_Avoid_: "loop" for an agent repeating itself; say it is going in circles

**Control gate**:
An inner-loop check a step must pass before its work moves on: lint, tests, security checks or
review.

**Canvas**:
The OpenHands Agent Canvas web UI — the control plane for conversations and automations.
Served behind Caddy at the domain, protected by oauth2-proxy (GitHub OAuth) and an API key.
_Avoid_: "dashboard", "control panel"

**Automation**:
A scheduled or event-driven rule that triggers an agent conversation.
Defined in an `automation.yaml` file and synced via Git Sync.

**Automations repo**:
The private `satat-automations` repo that Git Sync synchronizes with Agent Canvas.
Contains `automations/*/automation.yaml` files, one per watched repo.

**Infra repo**:
This public `satat` repo. Contains Terraform, docs, CI, and service configs.
The recipe anyone can use to deploy their own Satat.

**Git Sync**:
OpenHands' native bidirectional synchronization between Agent Canvas automations
and a Git repo. Changes in either direction are merged automatically.
_Avoid_: "automation backup"

**LLM Gateway**:
The managed API gateway (api.llmgateway.io/v1) that provides DevPass-subscription
access to multiple LLM providers through a single OpenAI-compatible endpoint.

**LiteLLM**:
The self-hosted proxy on the VM that routes agent LLM requests through the LLM Gateway
with cost-based model selection and a hard monthly budget cap.

**Webhook**:
The GitHub event delivery mechanism that triggers automations in near-real-time,
replacing polling.
