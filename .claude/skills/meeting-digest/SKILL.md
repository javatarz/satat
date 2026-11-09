---
name: meeting-digest
description: Digest a Satat design call's Meet transcript from docs/scratch/calls/<date>.txt into a local, unpublished summary of its decisions, directions, actions, open questions, deferrals and contradictions.
argument-hint: "<YYYY-MM-DD> | agenda [YYYY-MM-DD]"
disable-model-invocation: true
allowed-tools: Bash(.claude/skills/meeting-digest/preflight.sh *), Bash(.claude/skills/meeting-digest/render-summary.sh *)
---

# Meeting digest

Turns the transcript at `docs/scratch/calls/<date>.txt` into a local summary. Nothing is
published.

You orchestrate only. Never read the transcript or `items.json`, and never open, quote or
summarise their contents yourself: the extractor reads the transcript, and the summary is
rendered by a script that drops the quotes.

## Steps

1. Run `.claude/skills/meeting-digest/preflight.sh $ARGUMENTS`. On a non-zero exit, stop and
   show its stderr to the owner. Otherwise keep its `key=value` lines (`date`, `transcript`,
   `items`, `summary`).
2. Dispatch the Agent tool with `subagent_type: digest-extractor`. The prompt is the preflight
   lines, exactly as printed, and nothing else.
3. The reply must be exactly the `items=` path. When it is anything else (a `status:` line,
   extra text or another path), stop and report the reply to the owner. Do not retry, and do
   not open `items.json` to diagnose.
4. Run `.claude/skills/meeting-digest/render-summary.sh <date>`. On a non-zero exit, stop and
   show its stderr.
5. Tell the owner to read the summary at the `summary=` path. Do not print or paraphrase the
   summary yourself.
