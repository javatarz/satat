# GitHub App as identity

> **Superseded by [ADR 0020](0020-automation-definition-auth-trigger.md) (2026-10-08).**
> Self-hosted Agent Canvas has no GitHub App support; the automation authenticates
> with a fine-grained PAT saved as an Agent Server secret.

Satat authenticates to GitHub as a GitHub App named "Satat" rather than a personal access token or a machine user. The app is installed only on the configured target repository (and any additional repos Satat is explicitly allowed to work on) with Contents: Read/Write and Pull Requests: Read/Write scopes. This gives the agent the narrowest possible permissions — it can read code, push branches, and open draft PRs on exactly one repo — and produces audit-trailed activity (commits and PRs attributed to the app, not a person). Alternatives: a personal PAT (ties the agent to a human account, tokens expire, permissions are per-user not per-repo) or a machine user (requires a separate GitHub account with a password and 2FA to manage).
