# ADR 0020: Automation definition, GitHub identity, and trigger

## Status

Accepted (2026-10-08). Supersedes [ADR 0006](0006-github-app-identity.md) (GitHub App
identity) and [ADR 0010](0010-webhook-trigger.md) (webhook trigger) for the shipped
issue-to-PR automation.

## Context

T8 ships Satat's first automation: watch `javatarz/satat` for issues labelled
`ready-for-agent`, implement each in a Docker sandbox, and open a draft PR on a
`satat/issue-N` branch. Two earlier decisions no longer hold against the actual
OpenHands Agent Canvas implementation (verified in source; see
[research](../research/agent-automation.md)):

1. **ADR 0006 chose a GitHub App as Satat's GitHub identity.** Self-hosted Agent Canvas
   has no GitHub App support: `GITHUB_APP_ID`/`GITHUB_APP_PRIVATE_KEY` exist only in
   OpenHands Cloud and enterprise charts. The open-source automation service and its
   bundles authenticate with a token saved as an Agent Server secret — the same reversal
   already recorded for `AUTOMATION_GIT_SYNC_TOKEN`.
2. **ADR 0010 chose GitHub webhook events over a cron poll.** An automation is a code
   bundle; OpenHands' supported issue-to-PR automation (`github-issue-to-pr`, a scanner
   entrypoint) ships as a **cron** automation in the catalog. The scanner dispatches an
   agent conversation per ready issue and the agent opens the PR itself, but the same
   scan also sweeps for revisions, so an event-only trigger would drop that work.

The old `automations/satat-automation.yaml.tmpl` shape was never a real schema and is
dropped. Automation definitions live in the private `javatarz/satat-automations` repo,
pulled by Canvas Git Sync.

## Decision

### Automation definition lives in `satat-automations`

Satat vendors the OpenHands catalog `github-issue-to-pr` bundle (pinned `1.2.2`) into
`javatarz/satat-automations` as `satat-issue-to-pr/automation.yaml` +
`satat-issue-to-pr/tarball/`. `tarball/config.json` carries the deployment's settings
(`repos: [javatarz/satat]`, `trigger_label: ready-for-agent`, `branch_prefix:
satat/issue`, `pull_request_mode: draft`). Editing the automation is a reviewed PR in
that repo; Canvas imports it on the next sync cycle.

### GitHub identity is a fine-grained PAT

The agent authenticates with a fine-grained personal access token scoped to the target
repository (Contents: RW, Pull Requests: RW, Issues: RW, Metadata: RO), saved in Agent
Canvas as the `GITHUB_PERSONAL_ACCESS_TOKEN` secret. It is never a value in this repo or
in `satat-automations`. The bundle forwards only that named secret to the conversation
(least privilege).

### The shipped automation is cron-polled

`satat-issue-to-pr` triggers every 15 minutes (the catalog default). The GitHub webhook
endpoint `POST /api/automation/v1/events/github` and its
`AUTOMATION_WEBHOOK_SECRET` are still wired (Canvas env from the `WEBHOOK_SECRET`
GitHub secret, reachable through the ungated Caddy `/api/*` route), so a later
event-driven automation can use them without further infrastructure. No `/webhook`
alias is added — the Canvas ingress only routes `/api/automation/*` and GitHub does not
follow redirects.

### Model and sandbox are Agent Server settings

The LiteLLM endpoint (`http://litellm:4000`), the standard-tier model
(`deepseek-v4.1-flash`), the Docker sandbox, and the agent profile are configured in
Agent Canvas / Agent Server, not in this repo or in `automation.yaml`.

## Consequences

- **ADR 0006 and ADR 0010 are superseded for this automation** but their concerns remain:
  the PAT is per-repo and loses the App's audit attribution, and cron adds up to 15
  minutes of pickup latency vs. a webhook. Amending either is a deliberate future change.
- **`AUTOMATION_WEBHOOK_SECRET` is wired but currently unused** by the shipped
  automation. It becomes load-bearing the moment an event-triggered automation is added;
  until then it is inert config carried by the deploy pipeline.
- **Automation edits are Git-reviewable** in `satat-automations`, but their runtime
  behaviour (model, secrets, profile) is Canvas state and is not.
- **Re-pointing at another repository** is a `config.json` edit in `satat-automations`,
  not a GitHub variable change.
