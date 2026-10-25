# Observability and conversation capture

**Status:** Idea · **Related:** #82, [ADR 0022](../adr/0022-v2-platform-requirement-decisions.md) ·
**Source:** calls on 2026-10-16 and 2026-10-21

- OpenTelemetry traces into a Langfuse-like self-hosted tool: turn-by-turn conversation graph
  and cost per turn. No paid tools.
- Conversations (agent transcripts) are captured outside the step result, so they outlive the
  container on every runtime and work across machines. The result channel is too small for
  them: #129 proposes keeping a 4 KiB tail of output for lane-started runs, and #130 caps pod
  logs on Kubernetes at 1 MiB per stream. This needs its own card later.
- Live monitoring to catch a session going in circles.
- Hatchet ships an OTel collector. LiteLLM gives token and cost data.
