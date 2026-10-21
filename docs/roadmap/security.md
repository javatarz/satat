# Security

**Status:** Backlog (parts) · **Related:** [ADR 0025](../adr/0025-github-token-broker.md), #97 ·
**Source:** call on 2026-10-16

- Agents never hold tokens (ADR 0025 token broker). Guard against prompt injection in issue text.
- Prove it with container tests: start a process in the container and check which secrets it can see.
- Staged: make it work, then right, then complete.
