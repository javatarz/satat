# Agent quality gates: complete pipeline

Pre-work, in-flight, post-work, and human-review checks recommended across multiple agent factory implementations.

## Pre-work (pre-flight)

| Check | How | Source |
|---|---|---|
| Ticket passes readiness gate | Layer 1 (deterministic) + Layer 2 (semantic oracle check) | This repo |
| Model/key/endpoint verified | Config validation before agent dispatch | OpenHands automation preset |
| Spending limits set | LiteLLM budget cap, fail-closed | Stripe, Uber |
| Confirmation policy | `ConfirmRisky(HIGH)` or `NeverConfirm` for automation | OpenHands SDK |
| Sandbox isolation | Docker, no host mounts, no privileged | ADR 0009 |
| Secrets via Secret Registry | Never in env, never in state | Anthropic proxy approach |
| Hooks registered | `PreToolUse` + `Stop` + `UserPromptSubmit` | OpenHands |
| Trigger webhook secret | HMAC signature validation | GitHub docs |

## During-work (in-flight)

| Check | How | Source |
|---|---|---|
| Dangerous command blocking | `PreToolUse` hooks deny (exit 2) on rm -rf, chmod 777, etc. | OpenHands hooks |
| Stuck detection | Repeat/error/monologue/alternating loop detector | OpenHands stuck detector |
| Rate limits by tier | Gateway tpm/rpm per agent | LLM Gateway |
| Iteration cap | `max_iteration_per_run` (default 500) | OpenHands SDK |
| CI-round cap | Max retries before human handoff (Stripe: 2, Satat: 3) | Spotify, Stripe |
| Scope-drift judge | LLM compares diff vs original ticket, vetoes if mismatched | Spotify (~25% veto rate) |
| Budget cap | Spend-per-task ceiling, fail-closed | LiteLLM |
| Fresh context | Progress lives in files/git, not one long conversation | Anthropic, Huntley |

## Post-work (before PR opens)

| Check | How | Source |
|---|---|---|
| Lint + format pass | `Stop` hook blocks finish if fail | OpenHands hooks |
| Typecheck pass | `Stop` hook blocks if fail | OpenHands hooks |
| Relevant tests pass | `Stop` hook blocks if fail | OpenHands hooks |
| Oracle command passes | Ticket's named check now passes | Agent factory design tests doc |
| Scope-drift check passed | Judge LLM confirms diff matches ticket | Spotify |
| Evidence bundled | One line per criterion in PR description | Spec Kit, agent-ready |
| Critic score above threshold | Iterative refinement re-runs below threshold | OpenHands critic (rejected for Satat) |
| Tamper check | Agent didn't edit tests, CI config, or lint rules | StrongDM, Anthropic |

## Human review

| Check | How | Source |
|---|---|---|
| Automated review | Second agent reviews PR (severity: critical/block-merge) | Ramp, Cognition |
| Plan review for risky tasks | Architect reviews plan before implementation | Horthy ("review plans, not just code") |
| Merge lanes | Tiny mechanical PRs auto-merge; risky PRs get full review | Stripe, Ramp |
| PR size cap | Unbounded PRs are a review bottleneck | Faros (+154% PR size with high AI adoption) |
| Review capacity tracked | Reviewers/week vs agent PRs expected/week | Faros |
| Escalation | Named human receives stuck agent with context | All sources |

## Satat's current state

Satat covers the bolded items. Remaining gaps filed as follow-up tickets.

### Pre-work
- ✅ Ticket readiness gate (Layer 1 + Layer 2)
- ✅ Sandbox isolation (Docker, no host mounts)
- ✅ Secrets via TFC/GH Secrets (never in state)
- ✅ LiteLLM budget cap
- ⚠️ PreToolUse hooks not designed yet
- ⚠️ Confirmation policy: TBD

### During-work
- ✅ Iteration cap (OpenHands default)
- ✅ Stuck detection (OpenHands built-in)
- ✅ Rate limits (LLM Gateway)
- ⚠️ CI-round cap: TBD (3, per grilling decision)
- ⚠️ Scope-drift judge: designed but not built
- ⚠️ Dangerous command blocking: not designed

### Post-work
- ✅ Stop hook blocks on lint/test fail
- ⚠️ Oracle check: designed but not built
- ✅ Second PR-review automation (critic #15)
- ⚠️ Tamper protection: not designed
- ⚠️ Evidence bundling: not designed

### Human review
- ⚠️ Plan review lane: not designed
- ⚠️ PR size cap: not set
- ⚠️ Escalation: designed (ntfy notifications) but needs formalization

## Sources

- Spotify: engineering.atspotify.com/2025/11/context-engineering-background-coding-agents-part-2, /2025/12/feedback-loops-background-coding-agents-part-3
- Stripe: stripe.dev/blog/minions-stripes-one-shot-end-to-end-coding-agents
- OpenAI: openai.com/index/harness-engineering/
- Anthropic: anthropic.com/engineering/effective-harnesses-for-long-running-agents
- Ramp: builders.ramp.com/post/why-we-built-our-background-agent
- StrongDM: factory.strongdm.ai
- Cursor: cursor.com/blog/scaling-agents
- Faros: faros.ai/blog/ai-software-engineering
- Osmani: addyosmani.com/blog/factory-model/
- Willison: simonwillison.net/2025/Sep/30/designing-agentic-loops/
- 33,596 agent PRs study: arxiv.org/html/2601.15195
