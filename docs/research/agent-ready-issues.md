# What makes an issue ready for an AI agent?

## The core problem: agents fill silence

Across all sources, the same rule emerges: **"If a second person cannot tell whether the job is done without asking you, an agent cannot either."** A vague ticket is worse for an agent than a junior human — a human asks before coding; an agent produces a confident branch before anyone notices the missing decision.

## Two schools of readiness

### Human gate (OpenHands, Copilot coding agent)
A label/state is the readiness trigger. The issue body is passed straight to the agent. Richness of the issue determines output quality. No semantic pre-check.

### Validation gate (Claude Code plan-mode, Factory, Spec Kit)
An explicit pre-flight step — ask clarifying questions, score readiness, validate the plan — before autonomous work starts.

Claude Code's plan mode (`claude --permission-mode plan`) explores and asks questions first, no edits until approved. BUT: in autonomous/scheduled mode, docs explicitly say the agent "can't ask clarifying questions" — success criteria must be baked into the prompt.

## Best practices for agent-ready issues

1. **One concrete user-visible sentence first.** "When a new workspace has no projects, show an empty state linking to project creation." Not "improve onboarding."
2. **Name the relevant files/components** as navigational hints, not prescribed patches.
3. **Bound to one path** through the product (one workflow, role, entry point, result).
4. **Declare non-goals.** Agents invent context and add "helpful extra" behavior nobody asked for.
5. **Give the agent a check it can run** — tests, build, linter, or screenshot.
6. **Write "what must disappear"** for migrations/replacements, not just "what must change."

## Common failure modes on under-specified issues

| Failure | Description |
|---|---|
| **Green but wrong** | Agent keeps old behavior and still passes tests. SWE Refactor Bench: only 5.4% actually completed migration correctly. |
| **Old implementation retained** | Migrations/renames — agent adds new code alongside old, both coexist. |
| **Happy path only** | Empty states, permission failures, duplicates, idempotency never built. |
| **Blindness** | "Building to the Test" names the habit: an honest oracle, wrong artifact. |
| **Scope drift** | Agent adds helpful extra behavior nobody asked for. Spotify's judge vetoes ~25% of sessions for work outside the prompt. |

## Recommended ticket fields

Adapted from multiple sources (Factory, Spec Kit, Anthropic):

- **Outcome**: one sentence a PM could verify.
- **Acceptance criteria**: Given/When/Then, each with a check path. Write `check: MISSING` where none exists.
- **Oracle**: a specific test or command that fails now and passes when done.
- **Non-goals**: paths the agent must not edit (tests, CI, contracts).
- **Blast radius and reversibility**: what can break, and can it be undone?
- **Stop conditions**: forbidden path needs changing, oracle needs editing, CI fails twice, ambiguous criterion.
- **Budget**: max CI rounds, time, spend.
- **Evidence the PR must carry**: one line per criterion.
- **Named human owner**: the agent is delegated work, never assigned it.

## Routing rule

**Route by blast radius and reversibility**, not just ticket size. Auth, money, schema changes, and data migrations go to a human lane. Fully automate only work whose result you can verify cheaply and reliably.

## Template to start with

```markdown
## User-visible change
One concrete sentence.

## Scope
- Entry point:
- User role:
- Files or components likely involved:

## Oracle
- Command that fails now, passes when done:

## Acceptance criteria
- Behavior: (what the user sees/can do)
- Regression boundary: (what must not change)
- Verification: (test / lint / build / screenshot / manual path)

## Non-goals
- Do not:
- Paths not to touch:

## Review notes
- Risk: / Suggested reviewer:
```

## Sources

- Github Copilot coding agent: github.blog/ai-and-ml/github-copilot/assigning-and-completing-issues-with-coding-agent-in-github-copilot
- Factory Agent Readiness Model: docs.factory.com/agent-readiness/overview.md
- Spec Kit: github.com/github/spec-kit
- agentlane/agent-ready: github.com/agentlane/agent-ready (12-rule deterministic linter)
- Claude Code best practices: code.claude.com/docs/en/best-practices
- Anthropic "Best practices for Claude Code"
- "What Makes a GitHub Issue Ready for Copilot?" — Mohammed Sayagh, arXiv:2512.21426
- SWE Refactor Bench: arXiv:2608.23564
- Building to the Test: arXiv:2606.28430
- Osmani: addyosmani.com/blog/good-spec, addyosmani.com/blog/factory-model
- Spotify: engineering.atspotify.com/2025/11/context-engineering-background-coding-agents-part-2
