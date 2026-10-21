# Observability and conversation capture

**Status:** Idea · **Related:** #82, [ADR 0022](../adr/0022-v2-platform-requirement-decisions.md) ·
**Source:** calls on 2026-10-16 and 2026-10-21

- OpenTelemetry traces into a Langfuse-like self-hosted tool: turn-by-turn conversation graph
  and cost per turn. No paid tools.
- Conversation logs outlive the container, and work across machines.
- Live monitoring to catch a session going in circles.
- Hatchet ships an OTel collector. LiteLLM gives token and cost data.
