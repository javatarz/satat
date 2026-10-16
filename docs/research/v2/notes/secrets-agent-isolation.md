# Keeping credentials away from the agent: architectures and products

Part of the secrets-on-Kubernetes question (feeds the planned secrets ADR and build step 7, token broker). Researched 2026-10-16 from vendor docs and READMEs; nothing was run. Items marked **UNVERIFIED** were inferred, came from a single secondary source, or were not checked against current docs. Context read first: ADR 0024, `octo-sts-fit.md`, `claude-lane-spike.md`.

## Answer in brief

1. The only pattern that keeps a secret away from an agent that can run arbitrary shell is one where **the secret never enters the pod**. Anything in env, a file, a mounted Secret, a credential-helper response or tool output is readable by the agent and will reach the transcript.
2. The industry has converged on two patterns: a **credential-injecting egress proxy** (Anthropic, Vercel, Cloudflare, Daytona, E2B, Aembit, Infisical, iron-proxy) and a **command or MCP broker** (agent asks for an action, trusted service executes). Proxies work for HTTP APIs with header auth; brokers are needed for everything else (SigV4, DB wire protocols, SSH, side-effectful ops).
3. Neither pattern stops **abuse of the capability itself** (a proxy-injected GitHub token can still push to any branch the token allows) or **exfiltration through allowed hosts**. These need narrow token scope, server-side policy (branch rules), path/method filtering in the proxy and a short allowlist.
4. Claude subscription OAuth is the awkward one: the CLI needs a token to start. A placeholder token plus `ANTHROPIC_BASE_URL` pointing to an in-cluster injecting gateway is plausible (the spike proved the base URL is honoured), but the full path is **not proven** and there is a terms-of-service question.

## 1. Credential-injecting egress proxy

How it works: the pod holds a placeholder (or nothing). All HTTP(S) traffic is forced through a proxy outside the pod. For allowed hosts the proxy terminates TLS with a CA the pod trusts, then sets or replaces the auth header with the real value, and forwards over verified TLS. Non-allowed hosts get a 403.

| Implementation | Mechanism | Licence / maturity | Notes |
|---|---|---|---|
| Anthropic `sandbox-runtime` (srt) and Claude Code sandbox | Opt-in `network.tlsTerminate` (experimental) with a MITM CA. `credentials.envVars/files` modes `deny` or `mask`: sentinel in the sandbox, proxy swaps the real value only for `injectHosts`. Re-signs AWS SigV4. | Apache-2.0. Beta research preview; "APIs and config formats may change". | Needs Node or Bun. Per-command network ids are attribution, not a boundary. Linux file masks only; claude doc needs v2.1.221+ for file/SigV4, v2.1.246+ for scoped settings. Sources: [srt README](https://github.com/anthropic-experimental/sandbox-runtime), [Claude Code sandboxing](https://code.claude.com/docs/en/sandboxing). It is a library for a host process wrapping a command, not a Kubernetes service, so Satat would reuse the idea or embed it in its own proxy, not deploy it as is (UNVERIFIED that it can run standalone as a cluster proxy). |
| Claude Code on the web | Git credentials and signing keys are never in the sandbox. Git goes through a custom proxy; the git client authenticates with a scoped credential; the proxy checks the operation (push only to the configured branch) and attaches the real GitHub token. | Closed. | Best public statement of the design, and it adds **operation-level policy**, not only header injection. Sources: [Anthropic engineering](https://anthropic.com/engineering/claude-code-sandboxing), [Claude Code security](https://code.claude.com/docs/en/security). How the scoped credential is built is not published. |
| Infisical Agent Vault | One binary: MITM proxy (port 14322) plus management API (14321). Agent sets `HTTPS_PROXY`, trusts the CA, uses dummy values such as `__anthropic_api_key__`. Service rules map hosts to credentials; `unmatched_host_policy=deny` blocks the rest. Orchestrator can mint short-lived vault-scoped agent tokens. | MIT, except `ee/` (Infisical licence). Research preview, API unstable, about 2.4k stars. Launched 2026-04-22. | No dedicated Kubernetes docs; run as a Deployment on a separate node/namespace. SQLite by default, Postgres for production. Sources: [repo](https://github.com/Infisical/agent-vault), [launch post](https://infisical.com/blog/agent-vault-the-open-source-credential-proxy-and-vault-for-agents). |
| iron-proxy (`ironsh/iron-proxy`) | Go egress proxy, allowlist plus `secrets` transform swapping a placeholder for the real value in headers (`Authorization`, `x-api-key`, etc.). Local CA. | Apache-2.0. Docs pin v0.39.0, so pre-1.0. | Cannot handle SigV4 or OAuth-minted schemes; raw sockets bypass it unless the network forces the proxy; real creds in process memory. Source: [Hermes docs](https://hermes-agent.nousresearch.com/docs/user-guide/egress/iron-proxy). Description comes from a third-party doc; repo not read (UNVERIFIED). |
| BotBox (`reoring/botbox`) | Rust Kubernetes sidecar, iptables redirect, creds from Kubernetes Secrets. | MIT, 2 stars, last push 2026-02. | Too immature to adopt; useful as a design reference. Source: [ecosyste.ms listing](https://awesome.ecosyste.ms/projects/github.com%2Freoring%2Fbotbox) (secondary, UNVERIFIED). |
| Aembit Agent Proxy | Kubernetes native sidecar (1.29+), gathers identity evidence (service-account token), fetches short-lived creds from Aembit Cloud, injects just in time. Placeholder only where a library demands one. | Commercial SaaS. | Proxy runs in the same pod as a sidecar, so the sidecar's memory and the service-account token are in the pod's trust domain; gVisor and separate UID help. Sources: [Aembit Edge](https://docs.aembit.io/get-started/concepts/aembit-edge/), [sidecar changelog](https://docs.aembit.io/changelog/entry/2024-08-09-agent-proxy-now-injected-as-a-native-kubernetes-sidecar). |
| Alibaba Agent Sandbox credential injection, Lens Agents, NVIDIA OpenShell | Sidecar or gateway swaps placeholders; per-sandbox ephemeral CA (Lens). | Vendor / mixed. | Shows the pattern is widespread. Sources: [Alibaba](https://help.aliyun.com/en/cs/user-guide/inject-credentials-for-agent-sandbox-egress), [Lens](https://docs.lenshq.io/lens-agents/concepts/how-it-works/), [OpenShell](https://docs.nvidia.com/openshell/v0.0.116/kubernetes/topology) (not read in depth, UNVERIFIED). |
| Vercel Sandbox | Firewall `transform` rules add or replace headers for matching domains, scoped by path, method, query, header. TLS terminated only for hosts with rules, per-sandbox CA. The outside proxy **overwrites** any auth header the sandbox sets. Rules can be changed on a running sandbox. | Hosted, Pro/Enterprise. | Source: [changelog](https://vercel.com/changelog/safely-inject-credentials-in-http-headers-with-vercel-sandbox), [firewall docs](https://vercel.com/docs/vercel-sandbox/concepts/firewall). v0 uses a fixed public placeholder and rejects requests where it appears outside the auth field ([agentpatterns summary](https://agentpatterns.ai/security/public-placeholder-credentials-injecting-proxy/), secondary). |
| Cloudflare Sandbox SDK | `outbound` / `outboundByHost` handlers run in a Worker, set headers from Worker env; per-instance lookup by `ctx.containerId`; ephemeral per-sandbox CA; ports other than 80/443 are not routed through handlers. | Hosted. | [Outbound traffic guide](https://developers.cloudflare.com/sandbox/guides/outbound-traffic/index.md), [security](https://developers.cloudflare.com/sandbox/sdk/concepts/security/). |
| Daytona Secrets | Env var set to an opaque placeholder `dtn_secret_...`; proxy substitutes **only in HTTPS request headers** and only for the secret's `hosts` allowlist. No body, query, plain HTTP or Base64 Basic. | Hosted (self-host status not stated). | [Docs](https://www.daytona.io/docs/en/secrets/). The Basic-auth gap matters for git over HTTPS. |
| E2B Secrets | Stored write-only; referenced by name in a network rule; egress proxy adds the value to matching HTTPS requests. Docs warn the destination receives the value and could echo it back. | Hosted; beta status UNVERIFIED. | [Docs](https://docs.e2b.dev/secrets). |
| Modal | No credential-injecting proxy found in this research. | | UNVERIFIED either way. |
| OpenHands | Not researched here. | | UNVERIFIED. |
| Generic building blocks: mitmproxy, Envoy (Lua/ext_authz + TLS inspection), Squid ssl-bump | Can all do it, with a CA and a small addon; none has agent-aware policy out of the box. | Open source, mature engines. | A Satat-owned proxy (Go, `goproxy`-style, or mitmproxy addon) is the "custom" option. Not researched in depth (UNVERIFIED). |

Practical lessons across the products:

- **Overwrite, do not substitute.** A proxy that unconditionally sets `Authorization` for a host (Vercel) is simpler and safer than finding placeholders in requests. Substitution breaks on Base64 Basic auth (Daytona, `git` over HTTPS), signed requests (SigV4: only srt re-signs) and bodies. Substitution can also leak the real value if the upstream echoes it (v0's first Snowflake proxy; E2B warning).
- **TLS interception is mandatory** for header injection, so the CA key is the crown jewel. Keep it in the proxy only. Clients with certificate pinning or their own bundle (some Node/Java/Go tools) need the CA in the right env var (`SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `GIT_SSL_CAINFO`).
- **Raw sockets and non-HTTP protocols bypass the proxy** unless the network forces it. Enforcement must be NetworkPolicy (pod can reach only the proxy), not proxy env vars (Brightwave, [blog](https://www.brightwave.io/blog/how-we-defend-against-prompt-injection-at-brightwave)).
- **Identity at the proxy.** The proxy must know which pod/step is calling to choose the credential. Options: a per-pod proxy token (low value, only useful inside the cluster), or source-pod-IP lookup against the Kubernetes API (needs CNI that prevents IP spoofing; UNVERIFIED for k3s flannel).

### What the agent can still do with an injecting proxy

- Anything the injected credential allows against any allowed host, including exfiltrating repo content into an allowed repo, creating issues, pushing to unprotected branches.
- Send data to any allowed host in a request body (iron-proxy docs say this outright).
- Try to make an upstream echo the credential (reflection endpoints, query results).
- Use allowed hosts as exfil sinks: public GitHub repos and gists, Google Forms, wildcard cloud storage ([PromptArmor](https://www.promptarmor.com/resources/what-domains-should-i-add-to-my-allowlist)). A GitHub-scoped token still lets the agent write to a **different** repo if the token covers it, so scope per step.
- Allowlist-bypass bugs have been found in Anthropic's own proxy (a SOCKS5 hostname null-byte bypass of wildcard allowlists, fixed in Claude Code 2.1.88; CVE-2025-66479 earlier; [Penligent write-up](https://www.penligent.ai/hackinglabs/claude-code-sandbox-bypass/), secondary, UNVERIFIED details). Do not rely on a hostname allowlist alone; use exact hosts, no wildcards.

## 2. Git credential helpers and `gh` calling a broker

- A `credential.helper` or `GIT_ASKPASS` that asks a broker returns the token **to git inside the pod**. The agent can run `git credential fill` or read the helper's output, so the secret is visible. It narrows lifetime but does not hide it. Same for `gh auth` (`gh` reads `GH_TOKEN` or its hosts file; `gh auth token` prints it).
- It is acceptable only when the token is very short-lived and scoped to one repo and a few permissions. GitHub installation tokens last at most 1 hour and can be narrowed per request to repositories and permissions ([GitHub REST docs](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app), cited in `octo-sts-fit.md`).
- Better: no helper in the pod. Point git at the injecting proxy (HTTPS remote, proxy overwrites `Authorization: Basic base64(x-access-token:<tok>)` for `github.com` and `Authorization: Bearer` for `api.github.com`), and set `GH_TOKEN=placeholder` so `gh` runs. This is Anthropic's web design. The proxy asks the Satat broker for the step's token (octo-sts-fit alternative A), caches it per step.
- git over SSH is not injectable by an HTTP proxy; use HTTPS remotes only and block port 22.
- Commit signing keys: keep out of the pod; sign server-side (GitHub web-flow signatures when the App creates commits via API) (UNVERIFIED for Satat's flow).

## 3. Command-broker pattern (MCP tool or restricted RPC)

The agent holds no network path to the system. It calls `open_pr(repo, branch, title)`, `run_migration(name)`, `deploy(env)`; a trusted service validates arguments and executes with its own credentials.

- Prevents seeing the secret: **strongest** (the secret never enters the pod, never crosses a TLS boundary the pod controls, and the pod needs no network path to the target).
- Capability abuse: limited to the tool surface. Per-call authorization, argument validation, approval gates and audit are possible. Agent frameworks usually lack per-call gates by default ([arXiv 2606.28679](https://arxiv.org/abs/2606.28679v1), cited via agentpatterns, UNVERIFIED).
- Cost: every operation needs a purpose-built tool. Not general: a shell that runs `psql` or `terraform` cannot be handed a broker.
- Gotcha: MCP servers run outside Claude Code's bash sandbox and a stdio MCP server inside the pod would hold secrets in the pod; the broker must be a remote service in another namespace, authenticated by pod identity ([Claude Code sandboxing: what runs outside](https://code.claude.com/docs/en/sandboxing)). Claude Code's own MCP cloud proxy host is hardcoded and bypasses `ANTHROPIC_BASE_URL` (cc-gateway README, UNVERIFIED).
- k3s complexity: low to moderate (a Deployment, a Service, a NetworkPolicy allow, TokenReview auth, same as the planned token broker). Open source if Satat builds it (Hatchet task or small Go service).
- For side-effectful, high-blast-radius work (DB migration, cloud apply), better still: the agent **commits a migration file and opens a PR**; a separate trusted Hatchet step (outside the agent pod, after review or CI) runs it. The agent never executes it.

## 4. Credential brokers and agent-identity products

| Product | What it does | Fit for Satat | Source |
|---|---|---|---|
| Infisical Agent Vault / Agent Proxy | See section 1. Agent Proxy is the commercial variant with dynamic secrets and rotation. | Agent Vault is the closest off-the-shelf open-source proxy. Preview status. | links above |
| 1Password Credential Broker | OIDC workload token checked against admin trust policy, secrets released at runtime; private beta since 2026-06-15, GitHub Actions only so far, GA targeted late 2026, agent support on roadmap. 1Password and OpenAI Codex integration injects secrets at runtime without writing to disk. | Not ready; it hands the secret to the workload, which does not meet the goal. | [1Password](https://1password.com/blog/introducing-1password-credential-broker), [press](https://1password.com/press/2026/june/credential-broker) |
| Aembit | Secretless workload identity with proxy sidecar and Secrets Operator (syncs into Kubernetes Secrets, so not hidden). Blended identity for MCP. | Commercial SaaS; adds an external dependency. | links above |
| Teleport (Machine & Workload Identity, `tbot`, MCP access, "Beams" agent runtimes) | Short-lived identities, MCP server access control and audit, agent runtime product. | Heavy for a single-node k3s; vendor claims not verified. | [MCP access](https://goteleport.com/docs/machine-workload-identity/access-guides/mcp/), [agentic AI](https://goteleport.com/use-cases/agentic-ai/) |
| Keycard, Astrix, Okta/Auth0 for AI Agents, Arcade, Composio, Pomerium, HashiCorp Boundary, CyberArk Conjur / Secretless Broker | Not researched in this pass. Arcade and Composio are hosted OAuth-tool brokers for SaaS APIs (agent calls a tool, they hold the user token): that is pattern 3 as a service. Secretless Broker (CyberArk) is a local sidecar that opens DB/HTTP connections with injected credentials, a pre-agent precedent for pattern 1. | **UNVERIFIED**, no claims made. | |
| Cloud workload identity (IRSA / GCP WIF / Azure federated creds) | Pod exchanges a projected service-account token for short-lived cloud creds. | Gives the agent usable creds inside the pod (visible), so use only for broker components, not lane pods. E2B itself points to workload identity for per-sandbox short-lived tokens ([E2B](https://docs.e2b.dev/secrets)). | |

## 5. What fails

| Leak path | Why it fails | Mitigation |
|---|---|---|
| Env vars | Child processes, `env`, `printenv`, crash dumps, harness telemetry. Claude Code's sandbox `deny`/`mask` and `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` strip env for Bash subprocesses only ([docs](https://code.claude.com/docs/en/sandboxing)). | Keep the secret out of the pod. |
| `/proc/<pid>/environ`, `/proc/<pid>/mem` | Readable by the same UID. Scrubbing children does not help if the harness itself holds the secret under the same UID. | Run the secret holder as a different UID (or outside the pod). gVisor's /proc behaviour not checked (UNVERIFIED). |
| Files on disk, Kubernetes Secret volumes, `~/.config/gh/hosts.yml`, `~/.aws/credentials` | Any shell command reads them. | Do not mount. srt can mask files (Linux). |
| Tool output and transcripts | Anything printed enters the transcript, the LLM provider's logs and Satat's run records. | Placeholders in the pod mean transcripts contain only placeholders. |
| Git history | Agent commits a config file containing a token it saw. | Never visible; add secret scanning (push protection) on the repos as a backstop. |
| Prompt-injected exfil through allowed egress | See section 1. | Narrow per-step scope, exact-host allowlist, path/method filters, branch rules, no write to repos other than the step's. |
| DNS | Pods using cluster DNS can tunnel data via lookups; k3s NetworkPolicy cannot filter by name. The Windows srt notes already say DNS through the system resolver is not fenced. | With an HTTP CONNECT proxy the pod needs no external name resolution. Allow only the proxy address, deny DNS egress or serve a stub that answers only the proxy name (UNVERIFIED that the harnesses tolerate this). |
| Proxy bypass | Raw sockets, tools ignoring `HTTPS_PROXY`. | NetworkPolicy allows only proxy IP:port. Remember ADR 0024's start-up gap: policy lands 2 to 4 seconds late, so the entrypoint check must pass first. |
| Reflection | Upstream echoes the injected header back. | Overwrite-only rules, strip response headers, narrow hosts (srt has `stripResponseHeaders`). |

## 6. Recommended layered design for Satat

Common base (all secret classes):

- A **Satat egress proxy** in a platform namespace (not in lane pods, not a sidecar). Lane NetworkPolicy: egress only to the proxy Service and nothing else; no external DNS. The proxy terminates TLS with a Satat CA (key held only by the proxy) for a short list of **exact** hosts, overwrites auth headers, denies everything else. It identifies the pod via a per-step proxy token (worthless outside the cluster) and/or source-IP lookup, and asks the broker for the credential for that step.
- Start with an off-the-shelf engine to avoid writing TLS code: evaluate Infisical Agent Vault (MIT, preview) and iron-proxy (Apache-2.0, pre-1.0) against the needs below; a small custom Go proxy on the broker is the fallback. Neither was run. Pick by testing git smart-HTTP push, `gh`, Basic auth overwrite and Anthropic streaming through it (the Daytona/iron-proxy limitations suggest overwrite semantics will have to be custom).
- Pattern 3 (broker) for anything not HTTP-header-injectable.

Per class:

1. **GitHub.** Satat-owned GitHub App; broker mints a per-step installation token (repo-scoped, minimum permissions) per `octo-sts-fit.md` alternative A; the proxy injects it for `github.com` and `api.github.com` only. Pod holds `GH_TOKEN=placeholder`, HTTPS remotes, no credential helper. Add: proxy filters (block `api.github.com` paths outside `/repos/<step repo>/`, block gist creation and user/org endpoints), branch protection or rulesets so the token can push only to `satat/*` branches, and secret scanning push protection. Residual: the agent can still push to its own step branch and open PRs; that is the intended capability.
2. **LLM gateway (LiteLLM).** LiteLLM is in-cluster, so keep it inside the same proxy: either overwrite `Authorization` with a per-step virtual key (budget and short TTL, minted by the broker via LiteLLM's key API; verify current parameter names, UNVERIFIED), or, if the harness insists on a key in env, give it a per-step virtual key with a spend cap and expiry and accept that it is visible. The blast radius of a leaked per-step capped key is small; treat it as "scoped and disposable", not "hidden".
3. **Claude subscription OAuth token.**
   - What the spike proved: `claude -p` honours `ANTHROPIC_BASE_URL` with an env OAuth token, needs no first-run setup, and `forceLoginMethod: claudeai` is enforced (`claude-lane-spike.md`). Not proven: forwarding to the real API with a real token through a proxy.
   - Proposed: lane env `CLAUDE_CODE_OAUTH_TOKEN=<placeholder>`, `ANTHROPIC_BASE_URL=http://satat-proxy/...` (or `HTTPS_PROXY` with the Satat CA); the proxy overwrites the `Authorization` header with the real token for `api.anthropic.com`. Precedent: cc-gateway sets `CLAUDE_CODE_OAUTH_TOKEN="gateway-managed"` on clients and injects the real token upstream ([repo](https://github.com/motiful/cc-gateway), MIT, alpha; its stated purpose is identity-spoofing and it says not to violate Anthropic's terms, so use as evidence only, not as a dependency). Risks, all **UNVERIFIED**: the CLI may validate the token's shape or call other endpoints (token refresh at `platform.claude.com`, MCP at a hardcoded host) that bypass the base URL; whether Anthropic's terms allow a subscription token to be used server-side through a gateway for automated runs must be checked with Anthropic's current usage policy before relying on it.
   - Fallback if the placeholder does not work: the CLI holds the real `setup-token` value in its own env. Reduce exposure: dedicated account, scoped egress to `api.anthropic.com` only (through the proxy for audit), run the harness process under a separate UID from the shell the agent spawns, use `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` so Bash children do not inherit it, and treat the token as exposable (rotate, monitor usage). Claude Code's srt-style `credentials.envVars` mask does the same for subprocesses when the sandbox is on; whether bwrap sandboxing can run inside a gVisor pod under Pod Security `restricted` is not known (UNVERIFIED).
4. **Cloud and databases.** No credentials in lane pods, and no proxy injection for SigV4 or DB wire protocols. The agent produces artefacts (migration files, IaC changes) in a PR. A separate trusted Hatchet step in a non-agent pod, after CI or human approval, runs the migration or `apply`, using a cloud workload identity or a DB role limited to the migration. If the agent must query a database during a run, give it a read-only broker tool (MCP/RPC) that executes parameterised queries against a read replica; never a connection string. If SigV4 calls from the agent are unavoidable, the proxy would need to re-sign (only srt documents this; build or adopt).

Build order suggestion: (a) token broker (already planned), (b) proxy with GitHub injection plus lane NetworkPolicy and a bypass test (raw socket, DNS, direct IP), (c) spike on the Claude placeholder token end to end, (d) broker tools for PR creation and migrations.

## Not verified

- Whether any listed proxy (Agent Vault, iron-proxy) overwrites git smart-HTTP Basic auth cleanly and handles streaming Anthropic responses; none was run.
- Claude placeholder-token acceptance end to end; Anthropic terms for the gateway usage.
- gVisor `/proc` isolation between UIDs; srt/bwrap inside gVisor; k3s IP-spoofing protection for source-IP identity.
- Modal, OpenHands, Keycard, Astrix, Okta/Auth0, Arcade, Composio, Pomerium, Boundary, Conjur: no research done.
- Licence of Daytona/E2B secrets features for self-hosting (hosted products).

## Sources

- https://github.com/anthropic-experimental/sandbox-runtime
- https://code.claude.com/docs/en/sandboxing
- https://anthropic.com/engineering/claude-code-sandboxing
- https://code.claude.com/docs/en/security
- https://github.com/Infisical/agent-vault ; https://infisical.com/blog/agent-vault-the-open-source-credential-proxy-and-vault-for-agents
- https://hermes-agent.nousresearch.com/docs/user-guide/egress/iron-proxy
- https://vercel.com/changelog/safely-inject-credentials-in-http-headers-with-vercel-sandbox ; https://vercel.com/docs/vercel-sandbox/concepts/firewall
- https://agentpatterns.ai/security/public-placeholder-credentials-injecting-proxy/
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/index.md ; https://developers.cloudflare.com/sandbox/sdk/concepts/security/
- https://www.daytona.io/docs/en/secrets/ ; https://docs.e2b.dev/secrets
- https://docs.aembit.io/get-started/concepts/aembit-edge/
- https://1password.com/blog/introducing-1password-credential-broker
- https://goteleport.com/docs/machine-workload-identity/access-guides/mcp/
- https://github.com/motiful/cc-gateway
- https://www.promptarmor.com/resources/what-domains-should-i-add-to-my-allowlist ; https://www.brightwave.io/blog/how-we-defend-against-prompt-injection-at-brightwave ; https://www.penligent.ai/hackinglabs/claude-code-sandbox-bypass/
- https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app
