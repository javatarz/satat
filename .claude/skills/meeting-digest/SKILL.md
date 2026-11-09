---
name: meeting-digest
description: Digest a Satat design call's Meet transcript from docs/scratch/calls/<date>.txt into a local, unpublished summary of its decisions, directions, actions, open questions, deferrals and contradictions, with a preview of its roadmap edits, call record and draft PR, and a critique of where the call fell short. `agenda [<date>]` builds the next call's agenda from what the call records leave open.
argument-hint: "<YYYY-MM-DD> | agenda [YYYY-MM-DD]"
disable-model-invocation: true
allowed-tools: Bash(.claude/skills/meeting-digest/preflight.sh *), Bash(.claude/skills/meeting-digest/render-summary.sh *), Bash(cp docs/calls/*)
---

# Meeting digest

Turns the transcript at `docs/scratch/calls/<date>.txt` into a local summary, the call record
`docs/calls/<date>.md` with its critique, and roadmap edits. In agenda mode it builds
`docs/calls/agenda-<date>.md` for the next call instead. Nothing is published: the owner
publishes with a script, in a terminal outside Claude Code, after reading the summary or the
agenda. Never run that script.

You orchestrate only. Never read the transcript, `items.json`, the issue snapshot, the call
record, the outbox or the agenda, and never open, quote or summarise their contents yourself:
the agents read and write them, and the summary is rendered by a script that drops the quotes.

An agent fails when its reply is anything other than its expected path (a `status:` line, extra
text or another path). Then print only `<agent> failed; see docs/scratch/calls/` with the
agent's name and stop. Never echo or paraphrase the reply, do not retry, do not open any file to
diagnose, and dispatch no further agent.

When `$ARGUMENTS` starts with `agenda`, follow Agenda mode. Otherwise follow Steps.

## Steps

1. Run `.claude/skills/meeting-digest/preflight.sh $ARGUMENTS`. On a non-zero exit, stop and
   show its stderr to the owner. Otherwise keep its `key=value` lines (`root`, the absolute
   repo root, then the repo-relative `date`, `transcript`, `items`, `summary`, `repo`, `issues`,
   `record`, `outbox`).
2. Dispatch the Agent tool with `subagent_type: digest-extractor`. The prompt is the preflight
   lines, exactly as printed and including `root=`, and nothing else. The reply must be exactly
   the `items=` path. Check it before step 3: on any other reply the extractor failed, so stop
   as above and never dispatch the reconciler.
3. Dispatch the Agent tool with `subagent_type: digest-reconciler`. The prompt is the preflight
   lines, exactly as printed and including `root=`, and nothing else. The reply must be exactly
   the `record=` path. Check it before step 4: on any other reply the reconciler failed, so stop
   as above and never dispatch the critic.
4. Run `cp docs/calls/<date>.md docs/scratch/calls/<date>.pre-critic.md`. Then dispatch the
   Agent tool with `subagent_type: digest-critic`. The prompt is the preflight lines, exactly as
   printed and including `root=`, and nothing else. The reply must be exactly the `record=`
   path. On any other reply the critic failed, so stop as above.
5. Run `.claude/skills/meeting-digest/render-summary.sh <date>`. On a non-zero exit, stop and
   show its stderr.
6. Print only `Read <summary path>; its last line is the publish command`, with the `summary=`
   path. Do not print or paraphrase the summary, and never write the publish command yourself.

## Agenda mode

1. Run `.claude/skills/meeting-digest/preflight.sh $ARGUMENTS`. On a non-zero exit, stop and
   show its stderr to the owner. Otherwise keep its `key=value` lines (`root`, the absolute
   repo root, then `mode`, `date`, `repo`, `issues`, `agenda`).
2. Dispatch the Agent tool with `subagent_type: digest-agenda`. The prompt is the preflight
   lines, exactly as printed and including `root=`, and nothing else. The reply must be exactly
   the `agenda=` path. On any other reply the agenda agent failed, so stop as above.
3. Print only `Read <agenda>. To publish, run in a terminal outside Claude Code:
   scripts/publish-digest.sh agenda <repo> <date>`, with the `agenda=`, `repo=` and `date=`
   values, on one line. Do not print or paraphrase the agenda.
