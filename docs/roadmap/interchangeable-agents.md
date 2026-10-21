# Interchangeable agents

**Status:** Idea · **Related:** [ADR 0022](../adr/0022-v2-platform-requirement-decisions.md) ·
**Source:** calls on 2026-10-16 and 2026-10-21

- The harness runs at least three agents: Claude Code, OpenCode and Pi. Later, any
  ACP-compliant agent.
- Why three: no dependence on one vendor, and so they can be benchmarked against each other.
- Why not call model APIs directly: you'd rebuild bash tooling, sandboxing and approvals
  yourself, and own that attack surface. That's a solved problem.
- The harness, not the agent, is what Satat builds. See [Terms](README.md#terms).

## Later

Maybe replace the off-the-shelf agent with one built on an SDK, or a minimal one. Pi is the
likeliest base because it is minimal and extensible. Revisit when evals are strong and reviews
feel optional (roughly two months after 2026-10-21 at the earliest).
