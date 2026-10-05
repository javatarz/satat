# Deterministic vs LLM issue readiness checks

## Summary

Structure checks are all deterministic. Only semantic judgments need an LLM.

## Check matrix

| Check | Deterministic or LLM | How |
|---|---|---|
| Body > N chars | Deterministic | `body.length`, or `min_length` form validation |
| Has checkboxes | Deterministic | regex for `- [ ]` |
| Checkboxes completed | Deterministic | `[x]` vs `[ ]` |
| File paths mentioned | Deterministic | regex for `/[\w/-]+\.\w+/`; validating against tree is API |
| `##` sections present | Deterministic | regex for `## ` headers |
| Acceptance Criteria section | Deterministic | regex for header |
| No tribal-knowledge phrases | Deterministic | regex for "as discussed", "the usual way" |
| No vague verbs | Deterministic | regex for "improve", "optimize", "refactor", "enhance" |
| Linked PRs / refs | Deterministic | regex + GitHub timeline/events API |
| Acceptance criteria testable | **LLM** | Semantic: is this Given/When/Then actually verifiable? |
| Oracle exists | **LLM** | Semantic: can the agent infer a command that fails/passes? |
| Scope feasible for one task | **LLM** | Semantic: too big for one PR? |
| Description matches title | **LLM** | Semantic: title says "fix auth" but body describes UI change |
| Duplicate of another issue | **LLM** | Fuzzy dedup; regex can flag candidates only |
| Missing-info follow-up ask | **LLM** | Generating specific clarifying questions |

## Satat's two-layer design

**Layer 1 — Deterministic (zero tokens, GH Actions)**
Catches the structural checks: body length, sections, file paths, vague verbs, tribal knowledge phrases. If failed: comment + `needs-refinement` + remove `ready-for-agent`. Agent never fires.

**Layer 2 — Semantic (agent's first turn, one LLM call)**
Catches what scripts can't: testability of acceptance criteria, oracle existence, scope feasibility. Agent posts clarifying questions using `[NEEDS CLARIFICATION]` markers (Spec Kit pattern). Calls `finish` if issue isn't ready. Same model as implementation (DeepSeek V4.1 Flash) — one-turn cost is negligible.

## Tools referenced

- `agentlane/agent-ready`: 12-rule deterministic linter (50ms), GitHub Action
- GitHub issue forms: `required`/`min_length` enforce most structural checks at submit time
- Danger/Peril: PR-focused; Peril can do issue rules
- GitHub Actions: `jq` on issue body via `gh issue view --json body`

## Sources

- agentlane/agent-ready: github.com/agentlane/agent-ready
- GitHub issue forms: docs.github.com/en/communities/using-templates-to-encourage-useful-issues
- Peril: github.com/danger/peril
