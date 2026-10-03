# Critic extensibility in OpenHands

Research for Satat. Investigated against primary sources: the `OpenHands/OpenHands` frontend
(Agent Canvas) source, the `OpenHands/software-agent-sdk` source (the Python SDK, which now owns the
critic implementation), the OpenHands docs site (`docs.openhands.dev`), the OpenHands critic blog post
and research paper, and the multi-agent code-review literature on arXiv. Every claim carries a source
URL.

## Executive Summary

OpenHands' "critic" is **not** a separate agent, a mode of the main agent, or a prompt template — it is
a single pluggable `CriticBase` evaluator object attached to the `Agent`, invoked inside the run loop on
the `FinishAction` (or every action). The only LLM-backed implementation (`APIBasedCritic`) is a
**separate hosted classifier**: it POSTs the conversation to a vLLM `/classify` endpoint running a
dedicated 4B "critic" model with a hard-coded rubric taxonomy — it is *not* a chat-completions call and
*not* Satat's agent model. There is no built-in support for multiple critics (the `critic` field is
singular) and no public message bus, but there are four concrete extension seams: (1) subclass
`CriticBase` to write a custom evaluator, (2) the `/goal` "judge" loop (a second chat-completion LLM that
audits completion and composes with any critic), (3) Claude-Code-compatible `hooks` (including a blocking
`Stop` hook), and (4) a second **event-based automation** that reviews the PR after Satat opens it. For
Satat's LiteLLM/LLM-Gateway stack the pragmatic path is a **second-reviewer automation** (or the `/goal`
judge) rather than the built-in hosted critic, because `APIBasedCritic` requires a `/classify` endpoint
that Satat's chat-completions-only gateway does not provide.

## Primary Sources

| Source | Type | What it tells us |
|---|---|---|
| [`openhands/sdk/critic/`](https://github.com/OpenHands/software-agent-sdk/tree/main/openhands-sdk/openhands/sdk/critic) | Source | The critic is a `CriticBase` abstract class + `APIBasedCritic`, `AgentFinishedCritic`, `EmptyPatchCritic`, `PassCritic` impls; `evaluate(events, git_patch) -> CriticResult` interface |
| [`base.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/base.py) | Source | `mode` (`finish_and_message`/`all_actions`), `IterativeRefinementConfig` (threshold, max_iterations), `should_refine()`, `get_followup_prompt()` overridable |
| [`impl/api/client.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/impl/api/client.py) | Source | Default critic server is `https://llm-proxy.app.all-hands.dev/vllm`, model `"critic"`, tokenizer `Qwen/Qwen3-4B-Instruct-2507`; calls a vLLM `/classify` endpoint with a fixed label space |
| [`impl/api/critic.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/impl/api/critic.py) | Source | `APIBasedCritic` renders the transcript, calls `/classify`, extracts a success probability + categorized rubrics; overrides `should_refine()` with issue-threshold logic |
| [`agent/critic_mixin.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/agent/critic_mixin.py) | Source | The run-loop integration: critic is consulted on `FinishAction`; iterative refinement re-prompts the same agent in-place |
| [`settings/model.py` (VerificationSettings + build_critic)](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/settings/model.py) | Source | Full config surface: `critic_enabled`, `critic_mode`, `enable_iterative_refinement`, `critic_threshold`, `max_refinement_iterations`, `critic_server_url`, `critic_model_name`, `critic_api_key` |
| [`profiles/agent_profile.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/profiles/agent_profile.py) | Source | Per-agent-profile `ProfileVerificationSettings` (secret-free) carries the same critic fields per profile |
| [`conversation/goal/{judge,controller,runner}.py`](https://github.com/OpenHands/software-agent-sdk/tree/main/openhands-sdk/openhands/sdk/conversation/goal) | Source | The `/goal` loop: a second `judge_llm` (plain chat completion) audits the transcript and re-prompts until complete; composes with any critic |
| [Critic (Agent Canvas)](https://docs.openhands.dev/openhands/usage/agent-canvas/critic.md) | Docs | Canvas UI surface (`Settings > Verification`); confirms default hosted critic is free and reuses the OpenHands provider key; critic results shown as score + issue labels |
| [Critic (SDK)](https://docs.openhands.dev/sdk/guides/critic.md) | Docs | Critic semantics, `IterativeRefinementConfig`, custom `get_followup_prompt()` via subclassing |
| [Goal Completion Loop](https://docs.openhands.dev/sdk/guides/convo-goal.md) | Docs | `/goal` + judge as a second reviewer; "critic governs each inner run, the goal loop governs the overall objective" |
| [Event-Based Automations](https://docs.openhands.dev/openhands/usage/automations/event-automations.md) | Docs | GitHub event triggers (`pull_request.opened`, `labeled`, `review_requested`, `push`, …) and JMESPath filters → a second automation can fire after Satat's PR opens |
| [Hooks](https://docs.openhands.dev/openhands/usage/customization/hooks.md) | Docs | Claude-Code-compatible lifecycle hooks incl. a blocking `Stop` hook; per-repo `.openhands/hooks.json` |
| [Automated Code Review](https://docs.openhands.dev/openhands/usage/use-cases/code-review.md) | Docs | PR-review plugin + GitHub Action; `llm-model` accepts comma-separated models (A/B), `use-sub-agents` for file-level review |
| [PR Review (SDK)](https://docs.openhands.dev/sdk/guides/github-workflows/pr-review.md) | Docs | Review via `/codereview` + `/github-pr-review` skills; custom review guidelines via a repo skill |
| [Ask Oracle](https://docs.openhands.dev/sdk/guides/agent-ask-oracle.md) | Docs | `ask_oracle` tool: stateless second opinion from a stronger/specialized `oracle` LLM profile |
| [SOTA on SWE-Bench … Critic Model](https://openhands.dev/blog/sota-on-swe-bench-verified-with-inference-time-scaling-and-critic-model) | Blog (primary) | Best-of-N rollout + trained critic reranker (Qwen2.5-Coder-32B, TD objective); the genesis of the critic feature |
| [A Rubric-Supervised Critic … (arXiv:2603.03800)](https://arxiv.org/abs/2603.03800) | Paper (primary) | Critic Rubrics: 24 behavioral features; critics improve best-of-N reranking and enable early stopping |
| [Self-Refine (arXiv:2303.17651)](https://arxiv.org/abs/2303.17651) | Paper | The generator-critic loop: same LLM generates, critiques, refines (no training) |
| [CodeAgent (arXiv:2402.02172)](https://arxiv.org/abs/2402.02172) | Paper (EMNLP 2024) | Multi-agent communicative code review (separate reviewer agents) |
| [LLM-Based Multi-Agent Systems for SE (arXiv:2404.04834)](https://arxiv.org/abs/2404.04834) | Survey | Landscape of multi-agent SE systems incl. code review/debugging/security agents |
| [RefineCoder (arXiv:2502.09183)](https://arxiv.org/abs/2502.09183) | Paper | Iterative critique-refinement for code generation |
| [CTRL: Critic Training via RL (OpenReview)](https://openreview.net/pdf?id=CUEq6ZPSp7) | Paper (ICLR 2025) | Trained LLM critics for code generation; generator-critic framing |

## Findings

### 1. What the built-in critic actually is

The critic is a **separate evaluator object, not a separate agent, not a mode, not a prompt template**.
`Agent` holds a single `critic: CriticBase | None` field, and the run loop calls `critic.evaluate(events,
git_patch) -> CriticResult` — a `{score, message, metadata}` where `score` is a predicted success
probability 0–1 ([`agent/critic_mixin.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/agent/critic_mixin.py),
[`critic/base.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/base.py)).

There are four shipped implementations ([`critic/__init__.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/__init__.py)):

- `APIBasedCritic` — the LLM-backed one. It does **not** use the agent's LLM and does **not** make a
  chat-completions call. It renders the conversation with a Qwen3-4B chat template and POSTs it to a
  **vLLM `/classify`** (token-classification) endpoint, then maps the returned probability vector onto a
  fixed rubric taxonomy (`success`, 3 sentiment, 13 agent-issue, 2 infra, 8 user-follow-up labels)
  ([`impl/api/client.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/impl/api/client.py)).
  Default endpoint: `https://llm-proxy.app.all-hands.dev/vllm`, model name `"critic"`
  ([`client.py:74-75`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/impl/api/client.py)).
- `AgentFinishedCritic` / `EmptyPatchCritic` — cheap, non-LLM heuristics (non-empty patch; `FinishAction`
  present).
- `PassCritic` — always returns 1.0.

So the critic is best understood as a **classifier-in-the-loop**: a separate, rubric-scoring model that
watches the agent, not a second coding agent.

### 2. Configuration options

The Canvas/settings surface (`VerificationSettings`, and the secret-free `ProfileVerificationSettings`
per agent profile) exposes exactly these fields, built into a critic by `build_critic()`
([`settings/model.py:357`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/settings/model.py),
[`profiles/agent_profile.py:48`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/profiles/agent_profile.py)):

| Option | Default | Meaning |
|---|---|---|
| `critic_enabled` | `false` | Turn the critic on |
| `critic_mode` | `finish_and_message` | When to evaluate: on `FinishAction`/message, or `all_actions` (every action, much slower) |
| `enable_iterative_refinement` | `false` | **Blocking** mode: re-prompt the agent until score ≥ threshold |
| `critic_threshold` | `0.6` | Success score required to stop refining |
| `max_refinement_iterations` | `3` | Retry cap |
| `critic_server_url` | `null` | Override the `/classify` service URL |
| `critic_model_name` | `null` | Override the model name (default `"critic"`) |
| `critic_api_key` | `null` | Separate key for the critic service (else reuse the LLM key) |

**Blocking vs advisory:** by default the critic is **advisory** — it only annotates the timeline with a
score. It becomes **blocking** only when `enable_iterative_refinement` is on, at which point a score below
`critic_threshold` sends a follow-up prompt back into the *same* agent conversation (in-place, no fork)
until the score passes or `max_refinement_iterations` is hit
([`critic/base.py:109`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/base.py),
[`critic_mixin.py:76`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/agent/critic_mixin.py)). The
critic never rejects an action outright; for that you use the `Stop` hook (see §5).

### 3. Extension points for custom review logic

There are four real seams, in decreasing order of "stays inside OpenHands":

1. **Subclass `CriticBase`** (SDK only — not reachable from the Canvas UI). Override `evaluate()` to run
   any logic and return a `CriticResult`, and optionally override `get_followup_prompt()` /
   `should_refine()` to control refinement. This is how you'd write a critic that calls a normal
   chat-completions endpoint (e.g. Satat's LiteLLM) instead of vLLM `/classify`
   ([`critic/base.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/base.py),
   [SDK critic guide](https://docs.openhands.dev/sdk/guides/critic.md)).
2. **The `/goal` judge** — a *second* LLM (plain chat completion, so compatible with LiteLLM) that audits
   the transcript for objective completion and re-prompts the agent. It explicitly composes with any
   critic: "critic governs each inner run, the goal loop governs the overall objective"
   ([`goal/runner.py`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/conversation/goal/runner.py),
   [Goal Completion Loop](https://docs.openhands.dev/sdk/guides/convo-goal.md)).
3. **Lifecycle hooks** — Claude-Code-compatible `hooks.json` with a blocking `Stop` hook that can run a
   custom quality gate (lint, tests, or an arbitrary script/LLM call) and deny the agent from finishing.
   Per-repo, no code fork ([Hooks](https://docs.openhands.dev/openhands/usage/customization/hooks.md)).
4. **A second automation / sub-agent** — a separate event-driven conversation (PR review) or an
   `ask_oracle` second-opinion tool inside the agent (see §8).

There is **no plugin API, no critic registry, and no prompt-injection field** for the *built-in* critic
in the Canvas UI: the UI exposes thresholds/model/URL only, not custom rubric prompts. Custom critic
prompts therefore require either subclassing (SDK) or a custom `/classify` server, whereas PR-review
prompts can be customized with a repo skill (`/codereview`).

### 4. Multiple critics in sequence / parallel?

**No first-class support.** The `Agent.critic` field is a single `CriticBase | None`; there is no list, no
pipeline, and no orchestrator. What exists instead:

- **Sequential (built-in):** iterative refinement and the `/goal` loop both iterate a single agent +
  single evaluator over multiple rounds until a threshold/verdict is met. That is "review → refine →
  review" but always with one reviewer identity per layer.
- **Parallel (by construction, outside the critic):** the OpenHands SWE-bench result is *best-of-N +
  critic reranking* — generate N independent solutions, score each with the critic, pick the best
  ([blog](https://openhands.dev/blog/sota-on-swe-bench-verified-with-inference-time-scaling-and-critic-model)). The PR-review GitHub
  Action independently supports **comma-separated `llm-model` values** to run and compare multiple reviews
  (A/B testing), and `use-sub-agents` for per-file reviewers
  ([Automated Code Review](https://docs.openhands.dev/openhands/usage/use-cases/code-review.md)).

So "multiple reviewers" today means either *one critic per layer composed* (critic inside, judge outside),
or *multiple independent review runs you orchestrate yourself* (N automations/actions). Nothing runs N
critics against one trajectory out of the box.

### 5. Review "tool" / message bus

There is no public "review tool" or message bus for a custom critic to subscribe to. The closest things:

- **Events**: conversations emit a typed event stream (`ActionEvent`, `MessageEvent`, etc.), and a
  `CriticResult` is attached to `ActionEvent`/`MessageEvent` when a critic runs. SDK callbacks can read
  `event.critic_result` ([SDK critic guide](https://docs.openhands.dev/sdk/guides/critic.md),
  [Events](https://docs.openhands.dev/sdk/arch/events.md)). This is a read path, not a publish/subscribe bus.
- **Hooks** receive a JSON payload on stdin at lifecycle points and can inject `additionalContext` back
  into the agent ([Hooks](https://docs.openhands.dev/openhands/usage/customization/hooks.md)).
- **`ask_oracle`** is a *tool* the agent itself can call for a stateless second opinion from a saved
  `oracle` LLM profile ([Ask Oracle](https://docs.openhands.dev/sdk/guides/agent-ask-oracle.md)).

None of these is a clean "external critic plugs in here" bus; the realistic integration points are
subclassing, hooks, or a sibling automation.

### 6. Multi-reviewer / generator-critic patterns in the literature

- **Best-of-N + critic reranking** is the canonical SWE-bench inference-time-scaling move: sample many
  solutions, use a trained critic to rank, keep the best. OpenHands reports +15.9 Best@8 over Random@8 on
  SWE-bench reranking and 83% fewer attempts for early stopping
  ([arXiv:2603.03800](https://arxiv.org/abs/2603.03800),
  [blog](https://openhands.dev/blog/sota-on-swe-bench-verified-with-inference-time-scaling-and-critic-model)).
- **Generator–critic / actor–critic**: Self-Refine showed one LLM can generate, critique, and refine its
  own output with no extra training ([arXiv:2303.17651](https://arxiv.org/abs/2303.17651)). CTRL frames
  code generation as generator + trained critic and trains the critic via RL
  ([OpenReview](https://openreview.net/pdf?id=CUEq6ZPSp7)); ReVeal alternates generation and verification
  turns. RefineCoder iterates code via critique refinement ([arXiv:2502.09183](https://arxiv.org/abs/2502.09183)).
- **Multi-agent review**: CodeAgent introduces separate, *communicating* reviewer agents rather than a
  single generative model ([arXiv:2402.02172](https://arxiv.org/abs/2402.02172)); a broad survey documents the
  multi-agent SE space (review/debug/security agents) ([arXiv:2404.04834](https://arxiv.org/abs/2404.04834)).

Takeaway for Satat: the field's two durable patterns are (a) **independent reviewers then combine**
(best-of-N, A/B), and (b) **a dedicated evaluator loop feeding one generator** (critic/judge). Both are
reproducible in OpenHands today — (a) via N automations/actions, (b) via critic + `/goal`.

### 7. The LiteLLM compatibility gap (most important for Satat)

Satat routes all LLM traffic through LiteLLM → LLM Gateway as OpenAI-compatible **chat completions**
(ADR 0016). The built-in `APIBasedCritic` expects a **vLLM `/classify`** endpoint with a fixed label
space and the `"critic"`/Qwen3-4B tokenizer — not a chat endpoint. You therefore cannot point
`critic_server_url` at LiteLLM and expect it to work. Satat's options are:

1. **Use the free All-Hands hosted critic** (`critic_server_url` = All-Hands `/vllm`) — zero infra but
   sends Satat's agent traces off-VM to All-Hands, conflicting with Satat's privacy stance (no training /
   no logging) and self-hosted ethos. It also needs an OpenHands provider key.
2. **Self-host a critic-compatible `/classify` server** (vLLM + the openweights critic model, e.g.
   `all-hands/openhands-critic-32b-exp-20250417` or a Qwen3-4B critic) — heavy GPU, but keeps everything
   on-VM.
3. **Write a custom `CriticBase` subclass** that calls chat completions through LiteLLM and returns a
   `CriticResult` — SDK-level, keeps Satat's privacy + cost routing, but is code Satat must maintain.
4. **Skip the built-in critic and use the `/goal` judge** (plain chat completion → LiteLLM-compatible) or
   a **second-reviewer automation** (see §8) instead.

### 8. Practical implementation paths for Satat

- **Custom SME critic as a separate automation conversation — yes.** An event-based automation keyed on
  `pull_request.opened` / `pull_request.ready_for_review` / a label (Satat's own PR) runs a *second*
  conversation with its own agent profile and model, using the `pr-review` plugin or a custom `/codereview`
  skill ([Event-Based Automations](https://docs.openhands.dev/openhands/usage/automations/event-automations.md),
  [Automated Code Review](https://docs.openhands.dev/openhands/usage/use-cases/code-review.md)). Trade-off: it reviews the PR *diff*,
  not the agent's trajectory, and can't feed refinement back into the original conversation — it posts
  comments/change-requests instead.
- **Different models per review pass — yes, two ways.** (a) The critic model is already decoupled from
  the agent model via `critic_model_name` (though only via the `/classify` path). (b) The PR-review action
  takes a distinct `llm-model` and even comma-separated models for A/B. A cheap-fast-first pass + an
  expensive-architectural-second pass maps to **two automations/actions** (or one action with
  `use-sub-agents`), both already supported by Satat's LiteLLM cheap/standard/expensive tiers.
- **Inject custom review prompts into the critic — only via SDK subclassing or a custom server**, *not*
  via the Canvas UI (which exposes threshold/mode/URL/model only). For the PR-review path, custom
  guidelines are injected via a repo skill (`.agents/skills/custom-codereview-guide.md` with `/codereview`
  trigger), which is fully supported and no-code.
- **Post-commit hooks / conversation chaining — partial.** There is no native "conversation A triggers
  conversation B" primitive in OSS Canvas, and no post-commit hook in the agent loop. The chaining is done
  via GitHub events (an automation fires on `push`/`pull_request` after Satat commits/opens the PR) or a
  custom webhook. Within a *single* conversation, the `Stop` hook and `/goal` judge provide the
  "don't finish until quality passes" gate.

## Recommendation

**Adopt a layered two-reviewer design using mechanisms Satat already owns, and avoid the built-in
`APIBasedCritic`.** Specifically:

1. **In-loop gate (cheap, blocking): use the `Stop` hook, not the hosted critic.** Add
   `.openhands/hooks.json` to the **target repo** (the repo being worked on) — hooks are per-repo and run
   in the agent's conversation context. Satat can enforce this by including the hooks file as part of its
   issue workflow (committing it to the repo, or requiring it as a setup step). Alternatively, use the
   `/goal` judge (configured in the automation YAML, on Satat's side) for the same blocking gate without
   touching the target repo.
2. **Second reviewer (SME, advisory, separate model): a second event-based automation.** Trigger on
   `pull_request.opened` with a **JMESPath filter on `pull_request.user.login`** — restricted to Satat's
   GitHub App identity (e.g. `satat[bot]`) so it never fires for other contributors' PRs. Runs the
   `pr-review` plugin with a distinct "review" LLM profile (`glm-5.3-flash` per ADR 0016). Optional third
   (expensive, architectural) pass as a *third* automation on a manual `deep-review` label using the
   `expensive` LiteLLM tier.
3. **Only if in-loop score-driven refinement is later needed**, implement a custom `CriticBase` subclass
   that calls Satat's LiteLLM chat-completions (or adopt the `/goal` judge with a separate `judge_llm`),
   rather than the hosted `APIBasedCritic`.

Trade-offs: this design keeps everything on-VM and privacy-safe and reuses Satat's existing LiteLLM cost
tiers, at the cost of (a) a custom skill + automation definition to maintain, (b) the SME reviewer acting
on the PR diff rather than the full trajectory, and (c) no per-action critic score in the Canvas timeline
(which the built-in critic would give). The hosted critic remains the zero-effort path if Satat accepts
sending traces to All-Hands infra — rejected here on privacy grounds (consistent with ADR 0016's no-logging
provider constraints).

### Sources

- Critic module — https://github.com/OpenHands/software-agent-sdk/tree/main/openhands-sdk/openhands/sdk/critic
- `critic/base.py` — https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/base.py
- `critic/impl/api/client.py` — https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/impl/api/client.py
- `critic/impl/api/critic.py` — https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/critic/impl/api/critic.py
- `agent/critic_mixin.py` — https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/agent/critic_mixin.py
- `settings/model.py` — https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/settings/model.py
- `profiles/agent_profile.py` — https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/profiles/agent_profile.py
- `conversation/goal/` — https://github.com/OpenHands/software-agent-sdk/tree/main/openhands-sdk/openhands/sdk/conversation/goal
- Critic (Agent Canvas) — https://docs.openhands.dev/openhands/usage/agent-canvas/critic.md
- Critic (SDK) — https://docs.openhands.dev/sdk/guides/critic.md
- Goal Completion Loop — https://docs.openhands.dev/sdk/guides/convo-goal.md
- Event-Based Automations — https://docs.openhands.dev/openhands/usage/automations/event-automations.md
- Hooks — https://docs.openhands.dev/openhands/usage/customization/hooks.md
- Automated Code Review — https://docs.openhands.dev/openhands/usage/use-cases/code-review.md
- PR Review (SDK) — https://docs.openhands.dev/sdk/guides/github-workflows/pr-review.md
- Ask Oracle — https://docs.openhands.dev/sdk/guides/agent-ask-oracle.md
- OpenHands critic blog — https://openhands.dev/blog/sota-on-swe-bench-verified-with-inference-time-scaling-and-critic-model
- Critic paper — https://arxiv.org/abs/2603.03800
- Self-Refine — https://arxiv.org/abs/2303.17651
- CodeAgent — https://arxiv.org/abs/2402.02172
- Multi-Agent SE survey — https://arxiv.org/abs/2404.04834
- RefineCoder — https://arxiv.org/abs/2502.09183
- CTRL (critic RL) — https://openreview.net/pdf?id=CUEq6ZPSp7
