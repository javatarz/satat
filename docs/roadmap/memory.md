# Memory

**Status:** Idea · **Source:** call on 2026-10-21

- Don't rely on Claude's auto-memory: it doesn't survive one-shot or SDK runs.
- Later: a memory layer, a nightly job that summarises and reclassifies what agents did (as
  Claude and Hermes do). Third-party candidates exist (MemPalace on Qdrant, others), not benchmarked.
- Comes after the inner loop works.

## Open questions

- One store for observability and memory, or two? Memory needs to be queryable so agents can
  learn from it and see their own cost.
