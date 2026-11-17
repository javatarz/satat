# ADR 0035: The worker pushes every run state to the GitHub issue and ntfy

## Status

Accepted (2026-11-16). Decides #235. The states come from the live run on javatarz/satat-sandbox#1
([docker-prototype.md](../research/v2/docker-prototype.md#live-run-poller-to-draft-pr-in-local-mode-2026-11-03)).
v0.1 sent ntfy messages from the automation prompt ([ADR 0005](0005-ntfy-notifications.md),
[ADR 0019](0019-ntfy-hosting-and-observability.md)). v0.2 had no notifier until this decision.

## Context

In the live run, the person who labelled the issue heard nothing: no comment, no label change, no
message, on success or on failure. A failed run kept `ready-for-agent`, so the issue looked queued,
but the poller never retries a failed run. The PR URL was only in Hatchet's run output.

The worker already knows almost every state in-process: queued (poller, intake), started, skipped,
cloned, step started and finished, rate-limited, published, completed and failed. Only two things
can't be seen from inside it: a worker that isn't running, and a run waiting in Hatchet's queue.

## Decision

- **Push, plus a small pull.** The worker sends an event at every state it knows. A separate check
  outside the worker reports only what the worker can't: no worker running, or a stuck queue.
- **Every state notifies.** Queued, started, skipped, cloned, step started, step finished,
  rate-limited, published or not published, completed and failed.
- **Two channels behind one `notify.Notifier` interface**, each off unless `satat.yaml` turns it on:
  - **GitHub:** a new comment on the issue for every state, one after another, like ntfy. GitHub
    notifies watchers of new comments but not of edits, so a single edited status comment would ping
    once and then go quiet. This is noisy (about 7 comments per run) and that's accepted. It needs
    `SATAT_NOTIFY_TOKEN`, with Issues: write.
  - **ntfy:** one message per state, for a phone. The topic is the secret `SATAT_NTFY_TOPIC`,
    because the topic is the password ([ADR 0019](0019-ntfy-hosting-and-observability.md)).
- **A failed run swaps `ready-for-agent` for `satat:failed`.** Adding `ready-for-agent` again runs
  the issue again: the poller queues an issue whose label is newer than its last failed run. A run
  that starts removes `satat:failed`.
- **Best effort.** A channel that fails is logged and never fails or blocks a run. Each event gets
  its own timeout, detached from the run, so a cancelled or timed-out run is still reported.
  Secrets are replaced with `***` before an event leaves the worker.
- **Only allowed repos.** Agent runs, and runs for a repo not in the poller or intake lists, send no
  events. Otherwise a forged event could make Satat write on any issue the notify token reaches.

## Consequences

- The notify token is a third GitHub credential in the worker, beside the read and push tokens
  ([ADR 0025](0025-github-token-broker.md)). It writes only issue comments and labels. When the
  pusher splits out (#156), the notifier stays in the worker.
- The comments are public on a public repo. Failure detail is scrubbed of known secret values,
  but it can include output from the repo's own hooks.
- Progress inside a step (toolchain install, agent sessions, rehearsal retries) stays invisible
  until the step ends: the step has no channel to the worker. That's a later card.
- A comment's failure detail is cut at 20,000 characters, under GitHub's limit.
