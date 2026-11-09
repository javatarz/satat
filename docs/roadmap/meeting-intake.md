# Meeting intake

**Status:** Shaping · **Related:** #227 · **Source:** intent for #227

- A Satat design call's Google Meet transcript is downloaded by hand into `docs/scratch/calls/`
  (gitignored). Raw transcripts are never committed: javatarz/satat is public.
- The owner runs `/meeting-digest <date>` in a local Claude Code session. An extractor agent
  turns the transcript into an item list (decisions, directions, actions, open questions,
  deferrals and contradictions, each with speaker, timestamp, confidence and a verbatim quote),
  and a script renders a summary without the quotes.
- Everything stays local. Only the owner publishes, and what gets published is drafts: a draft
  PR with roadmap edits and a call record, and `needs-triage` issues. Only a human labels an
  issue `ready-for-agent`.
- The transcript is untrusted input: anyone on the call can try to steer the agent. The agents
  only turn files into files, with no shell, web or MCP tools.
- A decision is recorded only when the transcript shows explicit agreement. When a call
  decides something, its theme gets a line "Decided in call <date> (no ADR yet)" linking the
  call record. The digest never sets a theme's status to Decided: that waits for an ADR.
- This workflow is tied to Claude Code (project agents and skills in `.claude/`). That goes
  against [interchangeable agents](interchangeable-agents.md) and
  [skill distribution](skill-distribution.md), which want skills that work for every agent from
  one central source. It is accepted while the workflow is proved locally.
- A committed `.claude/` (agents, skills, settings) is loaded by every Satat issue run on
  javatarz/satat, since the `claude` lane runs Claude Code in the repo. That is why `.claude/`
  and the digest scripts are in `protected.add` in `.satat/commands.yaml`, and why
  `.claude/settings.json` denies Claude Code edits to them, to `.git/` and to the mise and
  direnv config.
- The speaker map `.claude/skills/meeting-digest/speakers.json` is committed and public, so anyone can see the names and handles in it.
