# Satat

Satat (सतत, "continuous / constant") is a self-hosted, always-on AI coding agent that
watches a GitHub backlog, picks up labeled issues, writes code, runs tests, and opens draft
pull requests for human review.

## Language

**Satat**:
The name of the whole system — the agent, its infrastructure, its repos, its identity.
Used in docs, commit messages, and notification text (e.g. "Satat opened PR #142").
_Avoid_: "the agent", "the bot", "openhands"

**Agent**:
The OpenHands agent process that clones a repo, implements a fix, and opens a PR.
Runs in a Docker sandbox on the VM.
_Avoid_: "bot", "worker"

**Canvas**:
The OpenHands Agent Canvas web UI — the control plane for conversations and automations.
Served behind nginx at the domain. Protected by an API key.
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
