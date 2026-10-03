# Session recording in OpenHands

Research for Satat ticket #11. Investigated against primary sources: the OpenHands docs site
(docs.openhands.dev) and the `OpenHands/software-agent-sdk` and `OpenHands/OpenHands` source
repositories.

## TL;DR recommendation

OpenHands has **two distinct kinds of "recording"**, and the ticket's `.rec` premise conflates them.
Neither is a `.rec` file in the current SDK:

1. **Conversation trajectory / event stream** — the canonical session record. Persisted as JSON
   (`base_state.json` + one `events/event-*.json` per event) and **replayable in Agent Canvas** as the
   transcript, tool calls, files, and diff. This is what Satat actually needs for audit/debug.
2. **Browser session recording** — optional rrweb JSON of DOM mutations, **not viewable in Canvas**
   (replay via rrweb-player only).

Recommendation: do **not** rely on the sandbox filesystem. Mount a persistent host directory into the
sandbox for the agent workspace and point OpenHands' persistence directory at it, then back that up /
retain it. Browser rrweb recordings are optional and low-value for Satat's code-fix workflow; if kept,
retain them under the same mounted directory with an explicit TTL.

---

## 1. Format: what is a "session recording"?

**The premise "`.rec` files" is not accurate for the current OpenHands SDK.** No `.rec` file appears
anywhere in the SDK or Agent Canvas source; the only `.rec` string in either repo is unrelated (macOS
`pyobjc_framework_discrecording`, a screen-capture framework dependency).

- **Browser session recordings are rrweb JSON.** The recorder captures "DOM mutations, mouse
  movements, scrolling, and other browser events" and saves them as JSON files replayed with
  rrweb-player or the rrweb.io viewer
  ([Browser Session Recording](https://docs.openhands.dev/sdk/guides/browser-session-recording.md)).
  Files are written one per flush (default 5-second interval), each a JSON array of events named
  `{timestamp}.json`
  ([`event_storage.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-tools/openhands/tools/browser_use/event_storage.py),
  [`recording.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-tools/openhands/tools/browser_use/recording.py)).
- **Conversation state (the real session record) is JSON.** It persists as a `base_state.json`
  (agent config, status, stats, secrets) plus an `events/` directory of one
  `event-000NN-<event-id>.json` per event. The docs explicitly note this event collection "represents
  the same trajectory data you would find in the `trajectory.json` file from OpenHands V0"
  ([Persistence](https://docs.openhands.dev/sdk/guides/convo-persistence.md)). This is the event
  stream Agent Canvas renders.

So for Satat, "session recording" = the **conversation event stream** (JSON), optionally plus
**rrweb browser recordings** (JSON). Neither uses a `.rec` extension.

## 2. Where recordings live by default (inside the sandbox?)

Yes — by default everything lives inside the agent's workspace, which for a Docker-sandboxed agent is
inside the container and is lost when the container is removed.

- Browser recordings are written to a relative path
  `BROWSER_RECORDING_OUTPUT_DIR = .agent_tmp/browser_observations` under the workspace
  ([`definition.py:35`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-tools/openhands/tools/browser_use/definition.py)).
- Conversation persistence defaults to `workspace/conversations/` "unless you specify a custom path"
  ([Persistence](https://docs.openhands.dev/sdk/guides/convo-persistence.md)).
- The Docker sandbox context manager starts a container, runs the agent server, and
  "cleans up the container when done"
  ([Docker Sandbox](https://docs.openhands.dev/sdk/guides/agent-server/docker-sandbox.md)).

Satat's ADR 0009 deliberately gives the agent a Docker sandbox with **no host filesystem mount**, so
today recordings and conversation state die with the container.

## 3. Can recordings be persisted outside the sandbox?

Yes — via filesystem mounts and directory configuration. There is no first-party S3/object-store
integration in the OSS self-hosted path; persistence is filesystem-based.

- **Mount a host directory into the sandbox.** `SANDBOX_VOLUMES` mounts host dirs into the sandbox,
  e.g. `SANDBOX_VOLUMES="/host/workspace:/workspace:rw"` (this replaces the deprecated
  `WORKSPACE_BASE`/`WORKSPACE_MOUNT_PATH` variables)
  ([Environment Variables](https://docs.openhands.dev/openhands/usage/environment-variables.md)).
- **Point persistence at a mounted path.** `OH_PERSISTENCE_DIR` sets where OpenHands stores local
  state, defaulting to `~/.openhands`
  ([Configuration Options](https://docs.openhands.dev/openhands/usage/advanced/configuration-options.md)).
  The SDK `Conversation(persistence_dir=...)` accepts any path
  ([Persistence](https://docs.openhands.dev/sdk/guides/convo-persistence.md)).
- **The official Agent Canvas Docker recipe already demonstrates the pattern** — it bind-mounts
  `~/.openhands` (settings, secrets, conversation history) and a `~/projects` workspace dir
  ([Use Docker with Agent Canvas](https://docs.openhands.dev/openhands/usage/agent-canvas/backend-setup/docker.md)).

So Satat should mount a persistent host volume for the workspace + persistence directory. Pushing to a
Git repo or uploading to S3 would be a **separate export step** (there's a transcript export to
Markdown/HTML in Canvas, but no automatic object-store upload in OSS).

## 4. Can recordings be replayed/viewed in Agent Canvas?

- **Conversation event stream: yes.** Canvas shows the full transcript, tool inputs/outputs, files,
  diffs, and supports branching and transcript export to Markdown/HTML
  ([Conversations](https://docs.openhands.dev/openhands/usage/agent-canvas/conversations.md)). The
  enterprise V1 API also exposes a `GET /api/v1/app-conversations/{id}/download` "trajectory" download
  and an events endpoint
  ([Conversations And Sandboxes](https://docs.openhands.dev/enterprise/conversations-and-sandboxes.md)).
- **Browser rrweb recordings: no.** They must be replayed with rrweb-player or the rrweb.io online
  viewer; there is no built-in Canvas replay
  ([Browser Session Recording](https://docs.openhands.dev/sdk/guides/browser-session-recording.md)).

## 5. Storage requirements (size per session)

**No per-session size is documented in any primary source.** The official example only prints file
counts and per-file byte sizes at runtime without stating expected totals
([Browser Session Recording](https://docs.openhands.dev/sdk/guides/browser-session-recording.md)).

What can be stated from the format:

- Conversation events are small JSON documents (one file per event), so the event stream grows with
  the number of actions/observations, not with content size — typically far smaller than the repo.
- rrweb recordings can be large: they log every DOM mutation, mouse move, and scroll at a 5s flush
  cadence, so long browser sessions produce many event files
  ([`recording.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-tools/openhands/tools/browser_use/recording.py)).

Satat's agents do code-fix work (terminal + file edits), not long browser sessions, so the dominant
cost is the conversation event stream. Budget conservatively (see recommendation) and instrument real
usage before committing to numbers.

## 6. Retention / cleanup

- **OSS self-hosted has no built-in recording retention.** Container lifecycle knobs
  (`SANDBOX_KEEP_RUNTIME_ALIVE`, `SANDBOX_PAUSE_CLOSED_RUNTIMES`, `SANDBOX_CLOSE_DELAY`,
  `SANDBOX_RM_ALL_CONTAINERS`) control whether sandboxes persist, not whether recordings are pruned
  ([Environment Variables](https://docs.openhands.dev/openhands/usage/environment-variables.md)).
- **Enterprise has automatic cleanup** but it is coupled to sandbox lifetime: an idle timeout pauses
  the sandbox, a deletion timeout permanently deletes conversation + storage, sessions cap at 12h, and
  on cleanup a workspace archive is captured (internal-only, for debugging/support/audit)
  ([Conversations And Sandboxes](https://docs.openhands.dev/enterprise/conversations-and-sandboxes.md)).

Conclusion: Satat must define its **own** retention policy; OpenHands OSS will not prune recordings.

---

## Recommendation for Satat

1. **Mount a persistent host directory into the agent sandbox** (e.g. `/var/lib/satat/workspace`,
   mounted at `/workspace`) via `SANDBOX_VOLUMES`, so conversation state and any recordings survive
   container teardown. This overrides the "lost on container stop" default and is compatible with ADR
   0009's isolation (the agent still only sees its own mounted workspace, not host secrets).
2. **Set a dedicated persistence directory** (`OH_PERSISTENCE_DIR` / SDK `persistence_dir`) under that
   mount, e.g. `/workspace/conversations`. Organize by `conversation_id` (already the SDK's layout:
   `<id>/base_state.json` + `<id>/events/*.json`), keyed back to the GitHub issue/PR for traceability.
3. **Treat the conversation event stream as the authoritative record**, not browser recordings. It is
   the only artifact replayed in Canvas and is what a human reviewer needs to audit why an agent did
   what it did.
4. **Skip browser rrweb recording** for Satat's code-fix agents. It is opt-in (requires explicit
   `browser_start_recording`), not viewable in Canvas, and adds rrweb JSON churn without audit value.
   If ever enabled, store it under the same mounted directory and replay off-box with rrweb-player.
5. **Add an explicit retention policy** (a cron/systemd timer on the VM, not OpenHands): keep the
   event stream for N days (e.g. 30), compress older ones, delete after M days (e.g. 90), and retain
   indefinitely only for conversations whose PR is still open. Optionally ship a copy to object
   storage (S3) as the backup tier if off-VM durability is required — there is no built-in path, so
   this is a small export job.

### Sources

- Browser Session Recording — https://docs.openhands.dev/sdk/guides/browser-session-recording.md
- `browser_use/definition.py` (output dir) — https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-tools/openhands/tools/browser_use/definition.py
- `browser_use/event_storage.py` — https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-tools/openhands/tools/browser_use/event_storage.py
- `browser_use/recording.py` — https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-tools/openhands/tools/browser_use/recording.py
- Persistence — https://docs.openhands.dev/sdk/guides/convo-persistence.md
- Conversation architecture — https://docs.openhands.dev/sdk/arch/conversation.md
- Docker Sandbox — https://docs.openhands.dev/sdk/guides/agent-server/docker-sandbox.md
- Use Docker with Agent Canvas — https://docs.openhands.dev/openhands/usage/agent-canvas/backend-setup/docker.md
- Configuration Options — https://docs.openhands.dev/openhands/usage/advanced/configuration-options.md
- Environment Variables Reference — https://docs.openhands.dev/openhands/usage/environment-variables.md
- Conversations (Agent Canvas) — https://docs.openhands.dev/openhands/usage/agent-canvas/conversations.md
- Conversations And Sandboxes (Enterprise) — https://docs.openhands.dev/enterprise/conversations-and-sandboxes.md
