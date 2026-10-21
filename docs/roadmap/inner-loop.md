# Inner loop as a state machine

**Status:** Shaping · **Related:** #112 · **Source:** calls on 2026-10-16 and 2026-10-21

- Run the agent one-shot (`claude -p`) with JSON output so the harness can tell whether the
  agent thinks it finished.
- Stopped but not finished: re-trigger, up to N times.
- Claims it's done: a **judge** checks it really finished, then a **reviewer** (fresh session)
  checks it against the acceptance criteria. Failing goes back around.
- Model this as a finite state machine with explicit states and allowed transitions, like a
  sprint board (you can't go from in-dev to done without passing QA).
- Prefer a few well-prompted turns plus review over long `/goal`-style runs: those can spin
  for up to 99 turns and burn tokens.
- Needs session resume (so session logs are stored centrally), a way to spot an agent going in
  circles, and exit criteria.
- Testing it needs a case that reliably sends an agent in circles (a weak local model, say), since
  off-the-shelf agents are built not to.
- **Control gates** is the general class: lint, tests, security checks, review. More can plug in.
  Agents that write faster need
  [sharper review](https://karun.me/blog/2025/11/06/intelligent-engineering-building-skills-and-shaping-principles/#smarter-ai-needs-smarter-guardrails).

## Next

Document the FSM, and separately how the components talk to each other.
