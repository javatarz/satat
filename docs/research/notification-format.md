# Research: how Satat's run notifications should read

For #235. The first build of notifications ([ADR 0035](../adr/0035-run-state-notifications.md)) chose
the states and channels but not the wording. On javatarz/satat-sandbox#1 the messages were terse
labels: "Satat: cloned · run `89d7dc38` / at 44f6ada". The owner found them not descriptive
enough. Before this note, nothing in the repo set a format: [ADR 0005](../adr/0005-ntfy-notifications.md)
lists notification kinds for v0.1, and [CONTEXT.md](../../CONTEXT.md) asks only that the text name
Satat as the actor ("Satat opened PR #142"). The v0.1 messages were written by the automation
prompt in the private repo, so no format survives here.

## Who reads them, and where

- **The person who labelled the issue.** They are somewhere else when the notification lands: a
  phone (ntfy), the GitHub notifications inbox, or an email from GitHub. Each message has to make
  sense alone, without the issue open.
- **Someone reading the issue later.** The comments are the run's history on the issue. They read
  top to bottom, and need each step's outcome, not Satat's internal stage names.
- **The channels truncate.** A phone shows the ntfy title and a line or two. GitHub's inbox and
  email subject show the issue title, and the comment's first line is the preview. Slack, which
  Satat may add later, uses the plain-text fallback for pushes and previews, and some clients
  cut it after about seven words.

## What the guidance says

**Notifications need more context than other messages.** NN/g separates notifications (system
events, often ones the user didn't trigger) from indicators and validations, and says notifications
should "establish more context and provide users with sufficient background information", because
the user is doing something else. It also splits them by whether action is required: those that
need a response should be intrusive, passive ones should not, and "an easy-to-ignore passive
notification can be problematic" when the information matters.
([NN/g](https://www.nngroup.com/articles/indicators-validations-notifications/))

**An error says what happened, why and what to do next.** This is consistent across style guides.
PatternFly: "A user should never feel stuck." Atlassian: a short title (three or four words) that
doesn't repeat the body; a body of one or two sentences with the reason, what to do, and what
happens if you don't; use "you", not "we"; avoid "please" and "sorry"; don't guess at the cause;
no jargon; link to detail if it is unavoidable. Dynatrace: never make a raw error code the whole
message.
([Atlassian](https://atlassian.design/foundations/content/designing-messages/error-messages),
[PatternFly](https://www.patternfly.org/ux-writing/error-messages),
[Dynatrace](https://developer.dynatrace.com/design/patterns/error-messages/),
[Apache Spark](https://spark.apache.org/error-message-guidelines.html))

**Alert fatigue comes from messages that need no action, are repeated, or are vague.** The SRE
literature agrees that each alert should be actionable, with context so the reader knows what to
do, and that repeats should be grouped. A vague "CPU is high" is the stock bad example.
([Datadog](https://www.datadoghq.com/blog/best-practices-to-prevent-alert-fatigue/),
[Atlassian Opsgenie](https://www.atlassian.com/blog/opsgenie/5-ways-to-reduce-alert-fatigue),
[incident.io](https://incident.io/blog/dealing-with-alert-fatigue-head-on))
The owner chose to notify every state (ADR 0035), so the volume is fixed. What's left is to make the
passive ones quiet and obviously passive, and the ones that need action loud and obviously
actionable.

**Put the point first, in plain words.** Short sentences, no jargon, the most important message in
the first sentence.
([Slack Block Kit](https://docs.slack.dev/block-kit/designing-with-block-kit/),
[Canadian Notify](https://notification.canada.ca/design-patterns-content-guidance),
[GOV.UK tone](https://guidance.publishing.service.gov.uk/writing-to-gov-uk-standards/tone-of-voice/right-tone))

**Never rely on colour or icons alone.** NN/g's error guidance: colour-vision deficiency is why a
state must be in the words, not only in a colour or emoji.

## What other coding agents do

- **GitHub Copilot coding agent** uses GitHub's own surfaces: an 👀 reaction on the issue when it
  picks the work up, a draft PR linked to the issue within seconds, "Copilot started work" and
  "Copilot finished work" events on the PR timeline, regular status updates in the PR body, and a
  review request when done. Session logs hold the detail. GitHub Mobile's live notifications show
  four states: in progress, completed, failed, cancelled.
  ([docs](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/troubleshoot-coding-agent),
  [changelog](https://github.blog/changelog/2026-02-26-github-mobile-track-coding-agent-progress-in-real-time-with-live-notifications))
- **Renovate** keeps one Dependency Dashboard issue and makes it actionable: each pending item has
  a checkbox that forces, retries or rebases it, so the message is also the control.
  ([docs](https://docs.renovatebot.com/key-concepts/dashboard/))

Lessons for Satat: name the actor and the outcome ("started work", "finished work"); use few,
clear states for the outcome; keep the detail one click away (session logs), not in the message;
and tell the reader the one thing they can do.

## ntfy specifics

ntfy has a title, a body (Markdown optional), tags that become emoji, one click URL, up to three
action buttons, and priorities 1 to 5. On Android: 5 (urgent) gives long vibration bursts and a
pop-over; 4 (high) a long burst and a pop-over; 3 (default) a short vibration and sound; 2 (low)
nothing until the drawer is pulled down; 1 (min) is filed under "Other notifications".
([ntfy publish docs](https://docs.ntfy.sh/publish/))
So passive progress belongs at 2, outcomes that need the reader at 4, and "no worker" at 5.

## GitHub specifics

- A new comment notifies watchers; an edit does not. The owner chose a new comment per state.
  (No current GitHub doc states the edit rule; a 2017 answer and repeated user reports agree. It
  is what we saw on javatarz/satat-sandbox#1.)
- GitHub shows each comment's time, author and avatar, so the comment needn't repeat the time or
  the repo. The issue title is the email subject.
- Comments are public on a public repo, and the `<details>` element collapses long output.

## What the first build got wrong

| First build | Problem | Guidance |
|---|---|---|
| "Satat: cloned · run `89d7dc38` / at 44f6ada" | Internal stage name, no outcome, no next step | Context; plain words |
| "Satat: step finished · changes, exit 0, 1 commit(s)" | Runner jargon ("step", "changes", exit code) | No jargon; outcome first |
| "Satat: completed · changes" | Says nothing the reader can use | Every message has a point |
| "Satat failed: clone …: git: exit status 128: fatal: Remote branch no-such-branch not found in upstream origin" | Raw error is the message; no what-to-do | What happened, why, what next |
| Same ntfy priority for queued and failed | Everything sounds alike | Match intrusiveness to whether action is needed |
| No run link | Detail not one click away | Keep detail one click away |

The proposed format is [ADR 0036](../adr/0036-notification-format.md).
