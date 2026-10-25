# Lane-started runs: marking lanes, and what #129 should change

Design note for #132 (2026-10-25). Status: built in #129 (the `agent` lane key, the worker and
CLI refusal, `agent: false` on `smoke`, `sleep` and `check-python`, and ADR 0030 restated). The flow it fits into is in
[design.md](../../design.md#the-flow-end-to-end), and the section on lane-started runs is
[there too](../../design.md#lane-started-runs).

## Why this note exists

#129 adds `satat agent run <lane> [--input] [--wait]`, `ls` and `rm` (its ADR 0030, proposed). The
command pushes a `satat:agent` event to the same Hatchet workflow as issue runs. The worker then
launches one container on any lane, with the operator's JSON at `/etc/satat/input.json`. Nothing
is pushed. The result keeps the last 4 KiB of output and the output file names.

There is no issue, no label and no triage. On the `claude` lane that means the agent does real
work from an issue body someone made up, and the work is thrown away. That goes against the
roadmap rule that only issues a human moved to `ready-for-agent` reach an agent
([roadmap](../../roadmap/README.md#how-it-fits-together)).

The direction agreed on #132: keep lane-started runs, but only on lanes that run no agent
(`smoke` and `sleep` for platform smoke tests; eval lanes such as `check-python` could use them
too, though `satat-eval` calls the launcher directly today). Refuse them on agent lanes such as
`claude`.

## How a lane is marked

**Chosen: a lane that runs no agent says `agent: false`; absent means the lane runs an agent.**
One optional boolean on the lane in `satat.yaml`.

- It sits on the lane, next to the image and command that make it an agent lane or not, so a
  reviewer sees it in one place.
- It fails closed. A new lane that leaves the key out refuses lane-started runs.
- It can answer both questions: lane-started runs need `agent: false`, and, as an optional
  follow-up, issue runs need the key to be absent.
- It is small: one field in the loader, and one line on each of the three lanes that run no
  agent.

Considered and not chosen:

| Option | Why not |
|---|---|
| `agent: true` on agent lanes, absent means no agent | Fails open: an agent lane without the key accepts lane-started runs. |
| Infer from `account`: a lane that spends a credential account runs an agent | Gives `account` a second meaning. A lane without `account` is already its own account (ADR 0022, `config`), and an agent lane that leaves it out fails open. |
| Infer from `command`: no override means the image's entrypoint is the runner | Implicit, and an agent image run with an explicit command fails open. |
| A separate allowlist, such as `lane_runs: [smoke, sleep, check-python]` | Moves the fact away from the lane it describes. It does fail closed, but it is one more list to keep in step. |
| Two sections, `agent_lanes:` and `lanes:` | A bigger reshape of `satat.yaml`, which is #108's job. |

If [interchangeable agents](../../roadmap/interchangeable-agents.md) later needs a lane to name
its agent (Claude Code, OpenCode, Pi), the key can become that name, with `false` still meaning
"no agent" and absence still treated as an agent lane.

## Where it is checked

In the worker, when the run starts, as for the readiness check (#134). Anything that holds the
Hatchet token can push an event, so a check only in the CLI gates nothing. The CLI checks too,
but only to fail early with a clear message. A refused run is recorded with its reason and
launches nothing.

The reverse check, refusing an issue run on a lane with `agent: false`, belongs with the worker
gating in #134, not #129. It is optional, as a follow-up to #134. Today `satat-enqueue --lane smoke`
queues a run that launches busybox, which leaves no result, so the run fails after using a cap
slot.

## What #129 should change

1. **Add the lane key.** `config` reads an optional `agent` boolean, default true. `smoke`,
   `sleep` and `check-python` get `agent: false`.
2. **Refuse agent lanes.** The worker refuses a lane-started run on a lane without
   `agent: false`, before it launches anything, and records why. `MapAgent` refuses it too, so the
   CLI fails early. Tests: `claude` is refused and launches nothing; `smoke` runs.
3. **Restate ADR 0030.** Its context says the owner wants to "create and destroy temporary
   agents on demand". It should say these runs exist for platform smoke tests (and could serve
   eval lanes), are allowed only on `agent: false` lanes, and that agent work starts only from a
   `ready-for-agent` issue. Link [design.md](../../design.md#lane-started-runs).
4. **Update design.md with the code.** When #129 lands, drop the "planned, #129" marks on the
   lane-started run in the flow and remove its row from "What is not built".

Naming (agent vs run, CLI and event names) is left to a separate naming discussion.
