# Secrets: leak prevention and detection for Satat step pods

Read 2026-10-16. Scope: stop secrets being printed, logged, committed or exposed to the agent, and limit damage when one leaks. Context: ADR 0024 (one-shot `SandboxClaim`, `envVarsInjectionPolicy: Disallowed`, no SA token, egress by network policy, gVisor), `session-recording.md`, `octo-sts-fit.md`.

Method note: Exa MCP hit its free rate limit mid-session, so sources are WebFetch/WebSearch of primary docs. Items marked **UNVERIFIED** come from background knowledge or secondary sources and were not checked against a current doc.

## Headline

Masking and scanning are the **last** layers, not the first. Every control that works on strings (masking, regex, entropy) has known bypasses (encoding, splitting, new token formats). The strong controls are structural:

1. The agent never holds the long-lived secret (credential-injecting proxy; Anthropic and Docker both ship this pattern).
2. What the agent does hold is short-lived, repo-scoped and revoked at step end.
3. Egress is closed, so a leaked value has nowhere to go.

String-level redaction and scanning then catch accidents and give detection.

## 1. Redacting known values from output streams

**Facts**
- GitHub Actions `::add-mask::` replaces a registered value with `***` in the log, once per value per job, and the value must be registered *before* it is printed ([workflow commands](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands)). The doc says nothing about encodings; GitHub's secret-handling guidance says structured data (JSON, XML, YAML) should not be used as a secret and that derived values (base64, URL-encoded) need their own masks (**UNVERIFIED**: from memory of the security-hardening doc, not re-fetched).
- Claude Code hooks ([hooks reference](https://code.claude.com/docs/en/hooks)): `PreToolUse` can block (`permissionDecision: deny`, exit 2) or rewrite input (`updatedInput`); `PostToolUse` can replace the tool result via `updatedToolOutput`, add context, or block. **UNVERIFIED:** whether `updatedToolOutput` applies to built-in tools (Bash/Read) or MCP tools only; the page was truncated at 100k of 270k chars and the field was only seen in the summary. Check before relying on it.
- Hooks sit between tool and model. They do **not** redact the transcript JSONL that Claude Code writes, nor stdout of `claude -p`. **UNVERIFIED.** Plan the redactor as a stream filter outside the agent process.
- LiteLLM `hide-secrets` guardrail: Enterprise-only, `detect-secrets` based, replaces with `[REDACTED]`, documented only in `pre_call` mode (requests to the LLM). The doc does not say responses are scanned ([LiteLLM secret detection](https://docs.litellm.ai/docs/proxy/guardrails/secret_detection)). Useful as a backstop against the agent sending a secret to the model provider, but it is licence-gated.
- Hatchet, Langfuse, OTel: no redaction feature verified. **UNVERIFIED** that Hatchet has any log redaction; assume none and redact before the log reaches Hatchet. OTel Collector has `redaction` and `transform` processors (**UNVERIFIED** in this session).

**Limits of exact-value masking**
- Misses base64, hex, URL-encoding, reversed, split across lines or tool calls, chunked across stream buffers, JSON-escaped, or embedded in a longer string with different quoting.
- Needs the value list up front. Anything the agent *derives* (e.g. a signed URL, a token exchanged from the first) is not in the list.
- Fix partly: mask a small set of encodings of each known value (raw, base64, base64url, hex, URL-encoded, JSON-escaped), match across chunk boundaries with a rolling buffer of `maxlen-1`, and flush on newline. Still defeatable by a prompt-injected agent that wants to exfiltrate; it only stops accidents.

**Build vs adopt**: build a ~100-line stream filter in the launcher/log-reader (Satat already reads `pods/log`). It knows exactly which values it minted for the step (token, run-scoped LiteLLM key), so exact masking is feasible there. Adopt gitleaks/trufflehog (section 2) as the pattern layer on top of the same stream.

## 2. Pattern and entropy scanning before storage, push, comment

| Tool | Verified facts | Fit |
|---|---|---|
| gitleaks | `stdin`, `git`, `dir` modes; `--max-decode-depth` decodes percent, hex (32+ chars) and base64 (16+ chars), off by default; baseline and custom TOML config. README says it is feature-complete, security patches only, author moving to "Betterleaks" ([repo](https://github.com/gitleaks/gitleaks)). | Fast, single binary, good for streams and diffs. Maintenance status is a risk; check Betterleaks. |
| trufflehog | `git`, `filesystem`, `stdin`, GitHub sources; verifies findings against the live API (verified / unverified / unknown); `--fail` exits 183; pre-commit and CI use ([repo](https://github.com/trufflesecurity/trufflehog)). | Verification reduces false positives, but verification **calls the provider with the found secret**; run it only on trusted infra, never inside the pod. |
| detect-secrets | Used by LiteLLM guardrail. Plugin and entropy based. | Weaker on prefix-less secrets; fine as a library. **UNVERIFIED** otherwise. |

Where to run:
- **Diff gate (must have):** before the launcher pushes or opens a PR, scan `git diff base..HEAD` (added lines only) plus the full file contents of new files, from the **worker**, not from inside the pod. Fail the step on a hit. This is the only gate that sees what the agent actually commits; a pod-side hook can be bypassed by the agent (`--no-verify`, editing `.git/hooks`).
- **Output gate:** pipe step stdout/stderr/transcript through the same scanner before writing to Hatchet, ntfy or a PR comment. PR comment bodies and ntfy messages should be built from a short, sanitised summary, not raw agent output.
- Satat-specific: pre-register custom patterns for LiteLLM virtual keys and the GitHub App installation token prefix.

**GitHub push protection / secret scanning** ([about push protection](https://docs.github.com/en/code-security/secret-scanning/introduction/about-push-protection)):
- Repo-level push protection "requires GitHub Secret Protection" (a paid product for private repos). Public repos get secret scanning free. The `javatarz/satat` repo is public; `satat-automations` is private (cost applies there; **UNVERIFIED** current pricing).
- It covers CLI pushes, web UI, REST API requests. Anyone with write access can bypass with a reason (test / false positive / fix later); bypass creates an alert, audit event and emails. **Delegated bypass** restricts who may bypass and is the setting to use: a GitHub App token can't pass the bypass prompt interactively, so the push is simply rejected (**UNVERIFIED** how the API-level rejection looks for apps; test it).
- Supported patterns include "GitHub App Installation Access Token" and "GitHub Personal Access Token" with push protection ([supported patterns](https://docs.github.com/en/code-security/secret-scanning/introduction/supported-secret-scanning-patterns)); only recent token formats are covered. Custom patterns need an org (not available on personal repos).
- It is provider-pattern-based, so it will not catch a LiteLLM virtual key or a Satat-specific secret without a custom pattern. Treat it as a free second opinion, not the gate.
- Pre-receive hooks are GitHub Enterprise Server only (**UNVERIFIED**); not available on github.com. The worker-side diff scan replaces them.
- Also set: leaked-token auto-revocation is built into GitHub for its own tokens when found in public repos (**UNVERIFIED** detail).

## 3. Canaries and honeytokens

- Canarytokens AWS API key token: a fake credentials file; any use triggers an alert; alerts pass through AWS logging with a 2 to 30 minute delay; AWS logging is not always complete so some uses may not alert ([Thinkst help](https://help.canary.tools/hc/en-gb/articles/360013002617-How-do-I-create-an-AWS-API-Key-Canarytoken)). Grafana places canary keys in GitHub org/repo secrets as a breach tripwire ([Grafana](https://grafana.com/blog/2025/08/25/canary-tokens-learn-all-about-the-unsung-heroes-of-security-at-grafana-labs)). Free on canarytokens.org.
- GitGuardian and Infisical honeytokens: **UNVERIFIED**, not fetched.
- Honeytokens for agents: a search found only a pointer (Zeltser lists a decoy MCP server entry as an idea) and no published implementation. Treat the agent use case as an untested idea.

Satat use:
- Put 1 to 3 decoys in each pod where a curious or injected agent would look: `~/.aws/credentials`, `.env.example`-style file in the workspace root, a fake `~/.config/gh/hosts.yml`. A legitimate step never touches them, so any use is a high-signal alert to ntfy.
- Also a decoy *string* canary: a unique fake token in the system prompt or workspace, scanned for in output and in any egress logs. If it shows in a PR diff or comment, an injection is exfiltrating context.
- Limits: the alert fires only if the thief **uses** the key from the internet. With egress blocked (our design) a canary will not fire; the output scanner must catch the *reading*. So the canary is mainly a detector for leaks that already got out via an allowed channel (PR, logs).
- Cost: free tier; one token per lane so an alert says which lane.

## 4. Exposure surfaces inside the pod

| Surface | Risk | Mitigation (status) |
|---|---|---|
| Env vars | Visible to every process of the same uid via `/proc/<pid>/environ`, and inherited by agent-spawned shells, `env`, `printenv`, crash reports. | ADR 0024 already disallows `env` injection. Keep it that way for secrets. |
| `/proc/*/environ`, `ps` | Same-uid readable. `hidepid=2` and uid separation help on a normal kernel. | **UNVERIFIED** under gVisor: gVisor implements its own procfs; check whether `hidepid` and per-uid permissions behave as on Linux. The real fix is not having the secret in any process the agent can inspect. |
| Mounted secret files | Readable by the agent (same uid). | Mount only what the agent needs; a Secret volume with mode 0400 owned by a different uid than the agent, read by a small helper (setuid-free: sidecar or init). Prefer the proxy pattern. |
| Downward API | ADR 0024 puts non-secret inputs there. Annotation visibility to other pod-API readers is unverified. | Keep the rule: no secrets in annotations. Add a lint in the launcher that scans the annotation payload with the scanner before create. |
| `git config` / `.git/config` remote URLs with tokens | Token ends up in `git remote -v`, `.git/config`, error messages, committed `.gitmodules`. | Use a credential helper or `http.extraHeader` supplied at runtime, or better, a git proxy (below). Never `https://x-access-token:TOKEN@github.com/...`. |
| npm/pip/cargo config with tokens | Readable files, sometimes printed in logs. | Do not give the lane registry tokens; use read-only mirror without auth. |
| Shell history, crash dumps, core dumps | Persist secrets to disk. | `HISTFILE=/dev/null`, `ulimit -c 0`; root filesystem read-only with tmpfs workspace (**UNVERIFIED** whether lane images do this). |
| Transcripts | `session-recording.md` stores transcripts and conversation state. OpenHands persistence also writes secrets in `base_state.json` ("agent config, status, stats, secrets"). | Scan before store; never persist OpenHands state containing secrets; store transcripts private with a TTL. |

**The structural fix (adopt the pattern):**
- Anthropic's own hosted design: git credentials and signing keys stay outside the VM; the VM holds a short-lived credential scoped to the session and a proxy attaches the real credential server-side; "network secrets" are attached to matching requests after they leave the session ([security](https://code.claude.com/docs/en/security), [cloud sessions](https://code.claude.com/docs/en/claude-code-on-the-web)).
- Docker Sandboxes: a host HTTP/HTTPS proxy intercepts outbound requests and swaps a sentinel value for the real credential; "the sandbox sees only a sentinel value" ([Docker credentials](https://docs.docker.com/ai/sandboxes/security/credentials/)).
- Satat equivalent: an in-cluster egress proxy per lane (also the hostname egress allowlist ADR 0024 lists as untested) that (a) injects the GitHub installation token on `github.com` git and API requests and (b) injects the LiteLLM key. The pod gets only sentinels. This removes the biggest class of exposure and doubles as the audit point. Cost: TLS interception inside the pod trust store, or use a git-over-HTTP proxy and an API-gateway endpoint instead of MITM. Must be prototyped.
- Even with the proxy, the agent can *use* the credential through the proxy (push to any allowed repo). So scope still matters.

## 5. Blast-radius limits

- **Token TTL and revocation:** GitHub installation tokens last at most 1 hour. `DELETE /installation/token` revokes the token you authenticate with and returns 204 ([REST docs](https://docs.github.com/en/rest/apps/installations#revoke-an-installation-access-token)). The launcher should call it in its `finally` on every exit path (same as claim deletion in ADR 0024). Mint tokens narrowed to the single repo and minimal permissions (`contents:write`, `pull_requests:write` at most; no `workflows`).
- octo-sts is lane-granular (`octo-sts-fit.md`): the subject is the ServiceAccount, so any pod in the lane could request tokens for any repo the lane trusts. A Satat-owned broker that mints per step and passes only the token (or only the proxy) fits better and lets the launcher revoke.
- **Git side:** branch protection or rulesets so the token can push only `agent/*` branches, never the default branch; require PR review for merges; the Anthropic proxy also rejects branch deletes and tag pushes ([security](https://code.claude.com/docs/en/security)); Satat should do the same in its git proxy or via rulesets (tag and delete rules).
- **Model spend:** LiteLLM virtual keys with `max_budget`, `budget_duration`, `rpm/tpm` limits per run (**UNVERIFIED**: from memory of LiteLLM docs; confirm parameter names). Issue one key per step with a small budget, delete it at step end.
- **Egress:** default-deny network policy with the named backends only (ADR 0024). Remember the exfil paths that remain through *allowed* hosts: the LLM provider (prompt can carry secrets; LiteLLM `hide-secrets` helps), GitHub (push to a branch, comment, gist, issue, even repo creation if the token allows), and Hatchet/ntfy callbacks. Anthropic notes even with network disabled the API channel can still carry data out ([cloud sessions](https://code.claude.com/docs/en/claude-code-on-the-web)). Constrain by token scope (single repo, no gists) rather than hoping the allowlist is enough.
- **Audit:** log every credential mint, proxy-injected request (host, method, path, status, not bodies), revocation, and canary alert, keyed by run and step ID. GitHub audit log records push-protection bypasses.

## 6. Guidance in 2026

- **OWASP Top 10 for Agentic Applications 2026:** ASI01 Goal Hijack, ASI02 Tool Misuse, ASI03 Identity and Privilege Abuse, through ASI10 Rogue Agents. ASI03 mitigations: unique bounded agent identity, short-lived credentials, re-authorisation for escalation; ASI02: least privilege per tool, sandboxing with egress controls. Only secondary summaries were read (the OWASP page did not list items) ([summary](https://auth0.com/blog/owasp-top-10-agentic-applications-lessons/), [Teleport](https://goteleport.com/blog/owasp-top-10-agentic-applications)); verify wording at genai.owasp.org.
- **Anthropic:** credentials stay out of the sandbox, proxy attaches them, scoped short-lived session credential, push restrictions, egress allowlists, audit logging ([security](https://code.claude.com/docs/en/security)). Claude Code on Linux stores its own credentials in a 0600 file, i.e. readable by the same uid, so the harness's own API credential must also be a sentinel plus proxy (point `ANTHROPIC_BASE_URL` at LiteLLM with a per-step key).
- **Docker Sandboxes:** host-side proxy with sentinel values; do not set API keys manually inside the sandbox ([Docker](https://docs.docker.com/ai/sandboxes/security/credentials/)).
- **OpenAI, E2B:** not fetched. **UNVERIFIED.**

## Layered checklist for Satat

Legend: B = build, A = adopt.

Layer 0, do not hold it (highest value)
1. A/B Credential-injecting egress proxy per lane; pod gets sentinels only (GitHub token, LiteLLM key, Claude API key via `ANTHROPIC_BASE_URL`). Prototype first; it is also the hostname allowlist.
2. A Keep `envVarsInjectionPolicy: Disallowed`; no secret in annotations (lint).
3. B Harness config rule: no tokens in `.git/config`, npm/pip config; `HISTFILE=/dev/null`, `ulimit -c 0`, read-only rootfs.

Layer 1, scope and lifetime
4. B Per-step minted GitHub installation token, one repo, minimal permissions; `DELETE /installation/token` in launcher `finally`.
5. A Rulesets: agent pushes only `agent/*`, no tag or branch deletes, no default-branch push, no workflow-file changes.
6. A LiteLLM per-step virtual key with `max_budget`, rate limit, deleted at end (confirm params).
7. A Default-deny egress (ADR 0024) plus fail-closed readiness check.

Layer 2, scrub on the way out
8. B Worker-side stream filter masking the step's own known values (raw plus base64, base64url, hex, URL-encoded; rolling buffer).
9. A gitleaks (or Betterleaks) with `--max-decode-depth` on stdout/stderr/transcript before Hatchet, ntfy, PR comments, storage. Custom rules for Satat tokens.
10. B Diff gate in the worker: gitleaks on added lines and new files before push or PR; fail the step. Optionally trufflehog verification on the worker only.
11. A GitHub push protection on public `javatarz/satat` (free); delegated bypass; custom pattern only if org. Paid on private repos (**UNVERIFIED** price): decide later.
12. B Only post sanitised summaries to PR comments and ntfy, not raw output.

Layer 3, detect
13. A Canarytokens (free): decoy AWS key and fake `gh` config per lane, alert to ntfy; plus a string canary scanned for in outputs.
14. B Audit log: mint, proxy request metadata, revoke, canary hits, scanner hits, keyed by run and step.
15. B Alert and auto-cancel the run on any scanner hit or canary read.

Layer 4, verify
16. B Red-team test in CI: a step that prints, base64-encodes, splits and commits a sentinel secret; assert each layer catches or never had it.
17. B Check under gVisor on real k3s: `/proc/<pid>/environ` across uids, `hidepid`, tmpfs semantics (**UNVERIFIED**; folds into the ADR 0024 gVisor item).

## Open questions
- Does `updatedToolOutput` work for built-in tools? Does Claude Code write transcripts after hooks run?
- Can the proxy avoid TLS interception (git smart-HTTP proxy plus `ANTHROPIC_BASE_URL` gateway)?
- Betterleaks status and rule parity with gitleaks.
- Exact LiteLLM budget parameters and whether `hide-secrets` is available without Enterprise.
- Push protection behaviour for an App token on a private repo (reject message, bypass).
