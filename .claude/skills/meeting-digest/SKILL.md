---
name: meeting-digest
description: Digest a Satat design call's Meet transcript from docs/scratch/calls/<date>.txt into a local, unpublished summary of its decisions, directions, actions, open questions, deferrals and contradictions, with a preview of its roadmap edits, call record and draft PR.
argument-hint: "<YYYY-MM-DD> | agenda [YYYY-MM-DD]"
disable-model-invocation: true
allowed-tools: Bash(.claude/skills/meeting-digest/preflight.sh *), Bash(.claude/skills/meeting-digest/render-summary.sh *)
---

# Meeting digest

Turns the transcript at `docs/scratch/calls/<date>.txt` into a local summary, the call record
`docs/calls/<date>.md` and roadmap edits. Nothing is published: the owner publishes with a
script, in a terminal outside Claude Code, after reading the summary. Never run that script.

You orchestrate only. Never read the transcript, `items.json`, the issue snapshot, the call
record or the outbox, and never open, quote or summarise their contents yourself: the agents
read and write them, and the summary is rendered by a script that drops the quotes.

## Steps

1. Run `.claude/skills/meeting-digest/preflight.sh $ARGUMENTS`. On a non-zero exit, stop and
   show its stderr to the owner. Otherwise keep its `key=value` lines (`root`, the absolute
   repo root, then the repo-relative `date`, `transcript`, `items`, `summary`, `repo`, `issues`,
   `record`, `outbox`).
2. Dispatch the Agent tool with `subagent_type: digest-extractor`. The prompt is the preflight
   lines, exactly as printed and including `root=`, and nothing else. The reply must be exactly
   the `items=` path.
3. Dispatch the Agent tool with `subagent_type: digest-reconciler`. The prompt is the preflight
   lines, exactly as printed and including `root=`, and nothing else. The reply must be exactly
   the `record=` path.
4. When an agent's reply is anything other than its expected path (a `status:` line, extra text
   or another path), print only `<agent> failed; see docs/scratch/calls/` with the agent's name
   and stop. Never echo or paraphrase the reply, do not retry, and do not open any file to
   diagnose.
5. Run `.claude/skills/meeting-digest/render-summary.sh <date>`. On a non-zero exit, stop and
   show its stderr.
6. Print only `Read <summary path>; its last line is the publish command`, with the `summary=`
   path. Do not print or paraphrase the summary, and never write the publish command yourself.
