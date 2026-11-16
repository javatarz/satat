# ADR 0036: How run notifications read

## Status

Accepted (2026-11-16). Refines [ADR 0035](0035-run-state-notifications.md), which chose the states
and channels. The research is in [notification-format.md](../research/notification-format.md).

## Context

The first notifications were terse labels built from Satat's internal stage names ("Satat: step
finished · changes, exit 0, 1 commit(s)"). They named a state but not its outcome, gave no next
step, put a raw git error in front of the reader, and rang a phone just as loudly for "cloned" as
for "failed". Nothing in the repo set a format.

The reader is somewhere else when a notification lands: a phone, GitHub's inbox, or an email.
Notifications need more context than other messages (NN/g), errors must say what happened, why and
what to do (Atlassian, PatternFly), and fatigue comes from messages that need no action or say
nothing specific (SRE practice). The owner chose to notify every state, so the volume is fixed;
the format has to carry the difference between "for your information" and "you need to act".

## Decision

### Every notification has the same four parts

1. **Headline: what happened, with Satat (or the agent) as the actor.** One sentence, past or
   present tense, the outcome first, at most about 60 characters, so it survives a phone title and
   a preview. It's the ntfy title and the first, bold line of the GitHub comment. "Satat opened
   draft PR #14", not "Satat: published".
2. **Detail: the facts that matter for this state, in plain words.** One or two short sentences.
   No internal names: "step" becomes the agent's name ("Claude Code"), "changes" becomes "1
   commit", exit codes and SHAs only where they help, never on their own.
3. **Next: what Satat does next, or what you can do.** Passive messages say what happens next
   ("Next: Satat opens a draft PR"). A message that needs action ends with one imperative ("Review
   it and mark it ready for review"). A failure always says how to recover.
4. **Links: one click to the detail.** The PR when there is one, else the run log; the issue on
   ntfy (on GitHub the reader is already there). Full error output goes in a collapsed Details
   block on GitHub and is never the message itself.

### Two kinds of notification, set apart in words and in how loud they are

| Kind | States | ntfy priority | Marker |
|---|---|---|---|
| **Needs you** | draft PR opened, failed, not published, skipped, worker down | 4 (5 for worker down) | ✅ or ❌ or ⚠️, plus words that say so |
| **For your information** | queued, started, cloned, agent started, agent finished, rate limited, completed, worker back | 2 (3 for started and completed) | ⏳ ▶️ ⏸️, plus words |

The emoji is a scanning aid only: the headline always says the state in words.

### Messages

These are the templates. `{…}` is filled from the run.

| State | Headline | Detail and next |
|---|---|---|
| queued | ⏳ Satat queued this issue | Found by the {poller / webhook}. It starts when a slot is free (at most {global} at once, {per_repo} per repo). |
| started | ▶️ Satat started work on this issue | Run `{run}` with {agent} on the `{lane}` lane, from `{base}`. {Retry {n} after the usage limit reset.} [Run log]({link}) |
| skipped | ⚠️ Satat skipped this issue | {Reason in plain words, for example "It no longer has `ready-for-agent`"}. Nothing ran. Add `ready-for-agent` to queue it again. |
| cloned | Satat cloned `{repo}` | `{base}` at `{sha7}`. Next: {agent} starts on the issue. |
| agent started | ▶️ {agent} is working on the issue | It has the issue's title and body and the repo's commands from `.satat/commands.yaml`. Next: it commits, then Satat checks the push. |
| agent finished | {agent} finished: {n} commit(s) / no changes / failed | Took {duration}. {The repo's pre-push checks passed / failed {n} times}. Next: {Satat opens a draft PR / nothing to publish}. |
| rate limited | ⏸️ Satat paused: Claude usage limit reached | Retry {n} of {max} is queued for {reset time}, when the limit resets. Nothing to do. |
| published | ✅ Satat opened draft PR #{pr}: {pr title} | {files} files changed on `{branch}`. Review it and mark it ready for review when it's right. Satat never merges. |
| not published | ❌ Satat couldn't open a PR: {cause in plain words} | The commits stay with the run; nothing was pushed. {What to do for this cause}. Then add `ready-for-agent` again. Details ▸ |
| completed | Satat finished: draft PR #{pr} is ready for review / no changes | Took {duration} in all. {For no changes: {agent}'s summary, then "Clarify the issue and add `ready-for-agent` again to retry."} |
| failed | ❌ Satat failed: {cause in plain words} | {What it tried}. `ready-for-agent` was swapped for `satat:failed`. {What to do for this cause}, then add `ready-for-agent` again. Details ▸ |
| worker down | 🚨 Satat's worker is down | No worker has had a heartbeat since {time}. Queued issues wait until one is back. Check `just status` or the worker's logs. |
| worker back | Satat's worker is back | Queued issues start again. |

### Causes in plain words

Each known failure has a plain cause and a fix, in one table in the code:

| Raw | Cause | What to do |
|---|---|---|
| clone: remote branch not found | the base branch `{base}` doesn't exist | fix the base in `satat.yaml` or create the branch |
| pre-push hook still failing after N retries | the repo's pre-push checks kept failing | read the details; fix the checks or clarify the issue |
| step timed out | {agent} ran out of time ({timeout}) | split the issue into smaller ones |
| step ran out of memory | {agent} ran out of memory ({memory}) | raise the lane's memory or split the issue |
| refusal: protected path / workflow / gitleaks | the change touched {what} | fix it by hand; Satat won't publish that |
| anything else | something went wrong in {stage} | read the details |

### How it's built

- One message builder in `notify` turns an event into a headline, detail, next and links; both
  channels render from it, so they always say the same thing. Golden-file tests cover every state.
- Events gain the fields the templates need: the agent's name, base, duration, PR number and title,
  files changed, retry count and limit, the cause class, and a run-log link.
- GitHub comments don't repeat what GitHub shows (time, author, repo). ntfy messages name the issue
  (`{repo}#{n}: {issue title}`) in the body, because the reader isn't on the issue.
- Tone follows CONTEXT.md and Atlassian: Satat is the actor; "you" for the reader; no "please",
  "sorry", or guessed causes.

## Consequences

- The messages grow from one line to two or three. GitHub comments stay short; the long output is
  collapsed.
- New event fields are needed from the pusher (PR title, files) and the handler (durations, retry
  limit). The run-log link needs Hatchet's dashboard URL as a browser reaches it, set as
  `notify.dashboard`; the worker reads the tenant from its Hatchet token.
- A new failure that isn't in the cause table reads "something went wrong in {stage}" until it's
  added. That keeps the reader from a raw error, but the table needs upkeep.
- Wording lives in one place and is tested, so changing it is a single, reviewed change.
