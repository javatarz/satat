# #155 Q7: may a subscription seat go through the operator's own LiteLLM proxy?

Not legal advice. All pages read 2026-10-28. Quotes are exact. Text that says what a page says is
marked **Text**. My own reading is marked **Interpretation**.

## Verdict

**Unclear, leaning not allowed. From 2026-11-12 the risk is clearly higher.** The terms allow the
operator's own seat in the unmodified Claude Code binary for scripts and CI, `claude -p` included.
They do not cover a proxy that holds the subscription OAuth token and makes the upstream call
itself. Two texts work against that design, and neither has a "single user, own seat" exception:

1. Claude Code legal page: "developers may not collect, store, or intermediate Claude.ai
   credentials or session tokens". Unlike the sentence before it, this sentence has no
   "on behalf of their users" qualifier.
2. Usage Policy, effective 2026-11-12: "Resell, proxy, or otherwise provide access to Claude through
   unauthorized means, including services that route requests through consumer subscriptions".

The gateway docs describe only one subscription-through-gateway mode, and it is pass-through: Claude
Code sends its own claude.ai login and the gateway forwards it. They never describe a gateway that
*holds* a subscription credential.

## Sources

| # | Page | URL | Date on page |
|---|---|---|---|
| S1 | Claude Code: Legal and compliance | https://code.claude.com/docs/en/legal-and-compliance | none shown |
| S2 | Claude Code: Other LLM gateways | https://code.claude.com/docs/en/llm-gateway | none shown |
| S3 | Claude Code: Run Claude Code through a gateway | https://code.claude.com/docs/en/gateways | none shown |
| S4 | Claude Code: Gateway compatibility guide | https://code.claude.com/docs/en/llm-gateway-protocol | none shown |
| S5 | Claude Code: Connect Claude Code to an LLM gateway | https://code.claude.com/docs/en/llm-gateway-connect | none shown |
| S6 | Claude Code: Authentication | https://code.claude.com/docs/en/authentication | none shown |
| S7 | Claude Code: Run Claude Code programmatically | https://code.claude.com/docs/en/headless | none shown |
| S8 | Consumer Terms of Service | https://www.anthropic.com/legal/consumer-terms | Effective Oct 8, 2025 (latest in its version history) |
| S9 | Usage Policy (AUP) | https://www.anthropic.com/legal/aup | Effective Nov 12, 2026 (latest; previous version effective 2025-09-15) |
| S10 | 2026 Usage Policy update (news) | https://www.anthropic.com/news/2026-usage-policy-update | Published Oct 8, 2026 |
| S11 | Help Center: Use the Claude Agent SDK with your Claude plan | https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan | "Updated today" |
| S12 | Help Center: Monthly API credits for Max and Team plans | https://support.claude.com/en/articles/17154008-monthly-api-credits-for-max-and-team-plans | "Updated over a week ago" |

I fetched S8, S9, S10 and S11 as raw HTML and searched the text directly, because the WebFetch
summary cut off the key S9 sentence. Copies are in `docs/scratch/155/raw/`.

## 1. Third parties holding or intermediating subscription credentials

**Text (S1, "Authentication and credential use"):**

> **OAuth authentication** is intended exclusively for purchasers of Claude Free, Pro, Max, Team, and Enterprise subscription plans and is designed to support ordinary use of Claude Code and other native Anthropic applications.

> **Developers** building products or services that interact with Claude's capabilities, including those using the Agent SDK, should use API key authentication through Claude Console or a supported cloud provider. Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow.

> This does not restrict how customers provision and manage their own API keys or third-party inference provider credentials — for example, configuring an API key in a development environment, secrets manager, or machine image for use by the customer's own authorized users — provided the resulting usage is billed to the key owner [...]. Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code as described under *Can customers offer Claude Code in their products?* above.

> Anthropic reserves the right to take measures to enforce these restrictions and may do so without prior notice.

**Text (S1, "Can customers offer Claude Code in their products?"):**

> Customers may not pay for, resell, or intermediate Claude usage on their end users' behalf. Each end user must authenticate with their own Anthropic API key, Claude subscription plan credentials, or 3P inference provider credential [...].

**Interpretation:**

- The "route requests [...] on behalf of their users" sentence does not fit: there is one user, and
  it is the operator.
- The next sentence ("developers may not collect, store, or intermediate Claude.ai credentials or
  session tokens") stands on its own, with no "for users" qualifier. A LiteLLM proxy that stores the
  `setup-token` value and attaches it to upstream calls stores and intermediates a Claude.ai
  credential. Whether the self-hosting operator counts as a "developer" is not defined. Satat is
  public software that "interact[s] with Claude's capabilities", which points towards yes.
- The safe-harbour sentences cover (a) the customer's own **API keys** in a secrets manager and
  (b) an end user "signing in to the **unmodified Claude Code binary**" with their own
  subscription. Neither covers a non-Anthropic proxy holding a **subscription** token. The API-key
  safe harbour names API keys only, which suggests subscription credentials were left out on
  purpose.
- "Ordinary use of Claude Code and other native Anthropic applications": with the proxy design, the
  process that authenticates upstream is LiteLLM, not a native Anthropic application, even though
  the request bodies come from Claude Code.
- Correction to `docs/research/v2/secrets-decisions.md` §2: that note says the page restricts
  third parties that collect, store or intermediate credentials "for their users". The current
  text does not limit the collect/store/intermediate sentence to third parties acting for users.
  The conclusion "that does not describe this use" is weaker than recorded.

## 2. Usage Policy change effective 2026-11-12

**Text (S9, "Universal Usage Standards" > "Do Not Abuse Our Platform", list introduced by "This includes using our products or services to:"):**

> Resell, proxy, or otherwise provide access to Claude through unauthorized means, including services that route requests through consumer subscriptions or misrepresent the product or client being used

> Intentionally bypass or interfere with capabilities, restrictions, or guardrails established within our products, including by circumvention of the account sign-up or log-in process [...] without prior authorization from Anthropic

**Text (S9 header):** "Effective Nov 12, 2026". The page's embedded version history lists this
version as latest and the previous one as effective 2025-09-15.

**Text (S10):** "The updated policy takes effect on November 12." The post does not mention the
subscription-routing clause. It says: "Most of the updates in the latest version are intended to
clarify existing rules."

**Interpretation:**

- I could not get the text of the 2025-09-15 version, which is the one in force today, so I cannot
  say whether the "consumer subscriptions" wording is new. It is in force from 2026-11-12, 15 days
  from today.
- "Proxy [...] access to Claude [...] including services that route requests through consumer
  subscriptions" is the closest match in any document to Satat's design: a proxy service that sends
  requests upstream on a Pro/Max token. A narrow reading is "provide access to *others*". The
  sentence does not say "others", though, and "proxy" stands alone as a verb.
- "Misrepresent the product or client being used": the prototype `docs/scratch/155/litellm.yaml`
  sends the token through LiteLLM's `anthropic/*` provider. If LiteLLM rebuilds the request rather
  than forwarding Claude Code's headers unchanged, the upstream sees LiteLLM's client, not Claude
  Code's. That raises a second risk under this clause. I have not tested what LiteLLM sends.

## 3. Automated, scheduled and unattended use

**Text (S8, §3, under "You may not access or use [...] our Services in the following ways:"):**

> Except when you are accessing our Services via an Anthropic API Key or where we otherwise explicitly permit it, to access the Services through automated or non-human means, whether through a bot, script, or otherwise.

**Text (S6, "Generate a long-lived token"):**

> For CI pipelines, scripts, or other environments where interactive browser login isn't available, generate a one-year OAuth token with `claude setup-token`

> This token authenticates with your Claude subscription and requires a Pro, Max, Team, or Enterprise plan. It can only make model requests [...]

**Text (S6, precedence item 5):** "`CLAUDE_CODE_OAUTH_TOKEN` environment variable. A long-lived OAuth
token generated by `claude setup-token`. Use this for CI pipelines and scripts where browser login
isn't available."

**Text (S11):** "Nothing changes for your plan: when you're signed in with your Claude plan, the
Claude Agent SDK and `claude -p` still draw from your subscription limits." Also, from the 2026-06-15
update: "Claude Agent SDK, `claude -p`, and third-party app usage still draw from your subscription
limits."

**Text (S1, "Acceptable use"):** "Advertised usage limits for Pro and Max plans assume ordinary,
individual usage of Claude Code and the Agent SDK."

**Interpretation:**

- Anthropic's own docs explicitly permit scripted and CI use of the subscription through
  `setup-token` and `CLAUDE_CODE_OAUTH_TOKEN`, which meets the "explicitly permit it" exception in
  S8. Headless `claude -p` on the operator's seat, with Claude Code itself holding the token, looks
  allowed.
- That permission is tied to Claude Code consuming the token. Nothing extends it to another program
  using the token.
- "Ordinary, individual usage" is about usage limits, not permission. Heavy multi-lane parallel use
  may hit limits or get attention, but this sentence alone does not forbid it.
- The phrase "third-party app usage" in S11 shows Anthropic knows third-party apps draw on plans.
  It is a billing note, not a grant of permission. S11 does not say which third-party apps are
  allowed.

## 4. `ANTHROPIC_BASE_URL` and gateways with a subscription

**Text (S2, "Subscriptions and gateways"):**

> While a gateway credential variable or `apiKeyHelper` is active, requests carry that credential in place of a developer's claude.ai subscription login, and the subscription's usage limits don't apply to them. [...] That traffic is billed per token to whoever owns the credential the gateway forwards, such as your organization's Anthropic Console account, or your Amazon Bedrock, Google Cloud's Agent Platform, or Microsoft Foundry account when the gateway routes there.

> `ANTHROPIC_BASE_URL` is the variable that points Claude Code at the gateway. Setting only that variable, without a gateway credential, doesn't replace the subscription. Requests still route through the gateway, but a saved claude.ai login remains the active credential, so its usage limits and billing apply. Gateways that pass this traffic on to Anthropic must forward the OAuth capability in `anthropic-beta`

**Text (S3):** "When developers connect through a gateway with a gateway credential, usage is billed to
your organization's provider account at API rates, and their claude.ai subscriptions aren't used or
charged." And: "**Provider credential**: the gateway holds one credential for your provider account,
shared by all forwarded traffic".

**Text (S4, request headers):** "When the developer authenticates with a claude.ai login, which is
possible when `ANTHROPIC_BASE_URL` is set without a gateway credential variable, this header also
carries an OAuth capability that the upstream requires, and stripping it fails those requests with
`401`".

**Interpretation:**

- The docs describe exactly two shapes:
  - **(a) Gateway credential.** The gateway holds a *provider* credential (Console, Bedrock, Vertex,
    Foundry examples) and bills per token at API rates.
  - **(b) Pass-through.** Only `ANTHROPIC_BASE_URL` is set, Claude Code sends its own claude.ai
    login, and the gateway forwards it.
- Satat's plan mixes the two: shape (a) on the Claude Code side (a per-step virtual key in place of
  the login) with a subscription token as the gateway's provider credential. The docs never list a
  subscription as a provider credential and never describe this mix. They are silent rather than
  forbidding it, but given §1, silence is not permission.
- Shape (b) is the documented way to put a subscription through a gateway, so a pass-through
  LiteLLM is the documented path. The cost is that the token must then be with Claude Code, in the
  lane, which is what the secrets design tried to avoid.

## 5. Recent changes (2026)

- **2026-10-08 (published) / 2026-11-12 (effective):** new Usage Policy with the "route requests
  through consumer subscriptions" clause (§2). The announcement (S10) does not call it out.
- **2026-10-07:** S11 and S12 say Max and Team plans include monthly API credits that "cover `claude
  -p` and the Claude Agent SDK when you run them yourself with an API key from your linked Claude
  Console organization". Interpretation: this gives a Max seat an API-key path for `claude -p`, and
  an API key in a gateway is covered by the S1 safe harbour.
- **2026-06-15:** S11 says the earlier-announced changes to Agent SDK usage on plans are paused.
  This article does not say what those changes were.
- **Consumer Terms:** the latest version is still the one effective 2025-10-08. No 2026 version.
- **S1 legal page:** no date shown, so I cannot tell when the collect/store/intermediate sentence
  was added.

## 6. Where the terms are silent or unclear

- Whether a single person self-hosting open-source software is a "developer" under S1.
- Whether "proxy [...] access" in S9 needs a third party to receive the access.
- Whether a proxy that forwards Claude Code's exact request bodies and headers "misrepresents the
  client".
- Whether the S8 "explicitly permit it" exception extends past the Claude Code binary to a proxy
  acting for it.
- S1 suggests one way to get a definite answer: "For questions about permitted authentication
  methods for your use case, please contact sales."

## 7. Options by risk (interpretation)

1. **Lowest risk:** the gateway holds a Console API key or uses Workload Identity Federation. The S1
   API-key safe harbour covers this. On Max, the 2026-10-07 monthly API credits apply to
   `claude -p` run with a Console API key.
2. **Documented subscription path:** Claude Code in the lane holds `CLAUDE_CODE_OAUTH_TOKEN` itself,
   either going directly or through a pass-through gateway (`ANTHROPIC_BASE_URL` only). This matches
   the S6 CI guidance and the S2 pass-through mode. It breaks the "token never in the lane" goal.
3. **Current plan:** the proxy holds the subscription token and lanes get virtual keys. This is the
   case that S1's "collect, store, or intermediate" and S9's "route requests through consumer
   subscriptions" are closest to. Unclear today, riskier from 2026-11-12.

## Leads (not sources)

- https://github.com/anthropics/claude-code/issues/27125 (named in `docs/research/v2/notes/secrets-taxonomy.md` row 2, community question about subscription tokens for scheduled automation). Not opened this session.

## See also
- `hermes.md`: how Hermes Agent uses Claude subscriptions and `claude -p`, and what it means for the options above.
