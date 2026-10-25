# Per-codebase workflows

**Status:** Decided (where the config lives) · **Source:** call on 2026-10-16

- Each codebase may need to tune its workflow: extra steps, fewer steps, its own lint and test
  commands.

## Decided (2026-10-25)

- The config lives in the **target repo**, owned by its developers, not in Satat (which is deployed
  and shared). It is `.satat/commands.yaml` with a `version` at the top; a directory leaves room
  for other repo-owned config later.
- `commands` holds required `setup` and `test`, plus optional free-form hints the agent may use.
  The harness runs none of them as a gate. The repo's own hooks, installed by `setup`, are the gate.
- Satat reads it from the base commit, so the agent cannot change it.
- A repo without a valid file is not started: Satat comments once and sets `needs-info` (#175).

Built in #167, #168, #174, #175 and #176.
