# Secrets taxonomy: what can be ephemeral in Satat

Research note, 2026-10-16. Question: for every secret type Satat has or may have, can it be minted per step with a TTL of minutes and a narrow scope, by what mechanism, and what is the fallback when only static keys exist? Builds on [ADR 0024](../../../adr/0024-kubernetes-agent-sandbox-runtime.md) and [octo-sts-fit](../octo-sts-fit.md). Vendor claims were checked against current docs on 2026-10-16; anything not re-fetched is marked UNVERIFIED.

## Headline

1. **Most of the expensive secrets can be ephemeral today.** GitHub, Anthropic API, OpenAI API, AWS/GCP/Azure, PyPI, crates.io and npm all accept a short-lived token in exchange for a signed identity token (OIDC), or can be fronted by one. The Anthropic and OpenAI results are new and change the picture: provider API keys no longer have to be static.
2. **The pod should hold one or two secrets, not a set.** Per step the pod needs (a) a GitHub installation token and (b) a LiteLLM virtual key. Everything else stays outside the sandbox (broker, launcher, CI).
3. **One root of trust makes all of it work: the pod's identity token.** A projected Kubernetes service-account token (or SPIFFE JWT-SVID) is the "bootstrap credential" that every exchange starts from. It is minted by the kubelet, audience-bound, expires in minutes to an hour, and is not a stored secret.
4. **Where ephemeral is impossible**: the Claude subscription token (1-year static), GHCR pull/push outside GitHub Actions, webhook secrets, SaaS keys that only exist as static keys, SSH deploy keys, and the *root* secrets that mint everything else (GitHub App private key, LiteLLM master key, upstream provider credentials, DB superuser). Those are "secret zero" and get sharp scope plus rotation, not ephemerality.
5. **Better name than "ephemeral tokens"**: see [Naming](#naming).

## Generic patterns

| Pattern | What it is | Standard / source | Satat use |
|---|---|---|---|
| Workload identity federation (WIF) | A workload proves who it is with a short-lived signed JWT from an IdP it already has; the target service validates it against a pre-registered trust rule and returns its own short-lived credential. No stored secret on either side. | Vendor feature; builds on OIDC. Anthropic: https://platform.claude.com/docs/en/manage-claude/workload-identity-federation . OpenAI: https://developers.openai.com/api/docs/guides/workload-identity-federation . GCP: https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-kubernetes . Azure: https://learn.microsoft.com/en-us/azure/aks/workload-identity-overview | The default for Anthropic, OpenAI, clouds, PyPI, crates.io |
| OAuth 2.0 token exchange (RFC 8693) | A standard STS grant: present a `subject_token`, get a new token for a narrower `audience`/`scope`. OpenAI's endpoint literally uses `grant_type=urn:ietf:params:oauth:grant-type:token-exchange`. Anthropic uses the RFC 7523 `jwt-bearer` grant instead (a sibling). | https://www.rfc-editor.org/rfc/rfc8693 | The shape of Satat's own broker: pod JWT in, scoped token out |
| Projected service-account token | Kubernetes mints an audience-bound JWT into the pod filesystem, rotated by the kubelet. The root credential for federation on k8s. | https://www.kubernetes.dev/resources/keps/1393/ ; OpenAI k8s guide shows `serviceAccountToken` with `expirationSeconds` | Bootstrap identity for every exchange |
| Dynamic secrets with leases (Vault/OpenBao) | A secrets manager creates a unique credential per request (DB user, cloud keys, k8s token) with a lease; expiry or revoke deletes it. | https://openbao.org/docs/concepts/lease/ ; https://openbao.org/docs/secrets/databases/ ; https://developer.hashicorp.com/vault/docs/secrets/aws | Postgres per-run users, if worth running OpenBao |
| JIT issuance via own broker | A small service holding the one root secret mints narrowly scoped, short-lived tokens on request after authenticating the caller. Octo-sts and the proposed Satat broker are this. | octo-sts-fit alternative A | GitHub installation tokens, LiteLLM keys |
| SPIFFE/SPIRE | Attested workload identity issuing X.509-SVIDs and JWT-SVIDs (short-lived, `exp` mandatory). An identity layer that feeds federation, not a secret store. | https://spiffe.io/docs/latest/spiffe-specs/jwt-svid/ ; OpenAI accepts JWT-SVIDs and X.509 | Overkill for one k3s node; the k8s projected token already does the job |
| Vendor-native expiring tokens | OAuth access+refresh tokens or `expiresIn` on a token API. Not federation: something still holds a refresh credential. | Slack, Sentry, Linear (below) | Fallback tier |

### Naming

"Ephemeral tokens" is understood but loose. Industry terms, roughly in order of how well they fit what Satat wants:

- **Short-lived credentials** (AWS, Google, Anthropic, OpenAI docs all use "short-lived"). Best plain-English term.
- **Keyless** / **secretless** authentication, when no stored secret exists on the workload (Sigstore, Chainguard, GitHub's "no long-lived keys"). Describes the federation path, not the fallback path.
- **Workload identity federation** for the mechanism; **token exchange** for the protocol step.
- **Just-in-time (JIT) credentials / JIT access** for minting per request; **dynamic secrets** is the Vault/OpenBao term; **zero standing privilege** is the broader security-architecture framing.
- Avoid "ephemeral" alone for the lease-based DB/cloud case; Vault docs say "dynamic secrets", and "ephemeral" in Terraform means something else (ephemeral resources/values), which will confuse people in a repo that also holds Terraform.

Suggested wording for Satat docs: **"short-lived, per-step credentials"**, with the mechanism named where it matters (federated, brokered, leased).

Satat also needs a second category named: **"root secrets"** (a.k.a. "secret zero"): the few static secrets that authenticate the minting itself. The owner's goal is best stated as "no static secret ever reaches a sandbox, and root secrets are few, narrow and rotated."

## The matrix

Legend for Ephemeral: **Yes** = minted per step, TTL of minutes to 1 hour; **Partly** = short-lived but the minter holds a long-lived root; **No** = static only.

| # | Secret | Ephemeral? | Mechanism | Achievable TTL | Achievable scope | Fallback (static) |
|---|---|---|---|---|---|---|
| 1 | GitHub (App installation token) | **Yes** | Own broker over TokenReview, or octo-sts, minting via `POST /app/installations/{id}/access_tokens` | Fixed at 1 hour; cannot be shortened at mint (can revoke via `DELETE /installation/token` after the step) | Per repo (`repositories`/`repository_ids`) and per permission set, never above the App's grants | Fine-grained PAT, 90 d to 1 yr, one human. Rejected in octo-sts-fit |
| 2 | Claude subscription OAuth token | **No** | `claude setup-token` | 1 year, static (https://code.claude.com/docs/en/authentication) | Inference only ("can only make model requests"); whole subscription, no per-run scope or budget | Rotate every <= 90 days by hand; dedicated seat; lives only in the launcher/gateway, not the sandbox. See notes below |
| 3a | Anthropic API | **Yes** (Workload Identity Federation) | k8s JWT to `POST /v1/oauth/token` (RFC 7523) bound to a service account | `token_lifetime_seconds` 60 to 86400, default 3600; capped at 2x remaining IdP-JWT life | OAuth scope (default `workspace:developer`), per workspace and per service account | API key with expiry (3 h to 30 d presets, or custom) and workspace/service-account binding |
| 3b | OpenAI API | **Yes** (WIF, token exchange endpoint) | k8s JWT or SPIFFE JWT-SVID or X.509 to `auth.openai.com` token exchange | At most 1 hour; never outlives the subject token; no refresh | Mapped project service account, narrowed further by mapping `permissions` | Project service-account key, rotate |
| 3c | Other LLM providers (Gemini, Bedrock, Azure OpenAI, OpenRouter, Mistral, ...) | Varies | Google/AWS/Azure: cloud WIF (row 6). OpenRouter, Mistral and most others: UNVERIFIED, assume static key | Cloud: 1 h typical | Cloud IAM; static keys are usually project-wide | Static key with spend limit set at the provider, rotated quarterly |
| 4 | LiteLLM virtual key | **Yes** | `POST /key/generate` with the master key (broker or launcher). Also JWT-to-key mapping so the pod presents an IdP JWT instead of a key: https://docs.litellm.ai/docs/proxy/jwt_key_mapping | `duration` (e.g. `"30min"`); `upperbound_key_generate_params.duration` caps it | `models`, `max_budget`, `budget_duration`, `rpm`/`tpm`, per-model budgets, `team_id` | n/a. The master key is a root secret (see below). Delete the key at step end as well as relying on expiry |
| 5 | Postgres / DB creds (Hatchet DB, app DB) | **Yes, with extra component** | OpenBao or Vault database engine: unique user per request with `default_ttl`/`max_ttl`. Or per-run schema/role created by the launcher | Minutes to hours; lease revoke drops the role | SQL grants in `creation_statements`; per-role | Static role per service, password in a k8s Secret, rotated (OpenBao "static roles" automate this) |
| 6 | Cloud (AWS, GCP, Azure) for Terraform and deploy | **Yes** | AWS `AssumeRoleWithWebIdentity` via IAM OIDC provider; GCP WIF with pool/provider and optional SA impersonation; Azure federated identity credential. A self-managed k3s needs a **publicly reachable issuer and JWKS** for AWS/Azure; GCP and OpenAI also accept an uploaded JWKS, and Anthropic accepts a pasted JWKS "for air-gapped clusters". Cwii write-up of the issuer requirement: http://cwii.dev/self-hosted-oidc.html | AWS and GCP: 1 h default, up to 12 h (AWS role max; UNVERIFIED for exact current numbers). Azure: Entra token 24 h per MS docs (not shortenable by the workload) | IAM role/subject conditions; per-repo or per-lane subject; session policy can narrow further | Static IAM key: avoid; if forced, one user with a narrow policy, rotated every 90 d |
| 7 | Container registry (GHCR) | **No outside Actions** | Only a classic PAT or the Actions `GITHUB_TOKEN` work. App installation tokens do not authenticate to GHCR, even though `packages` appears as a permission (REST Packages API only): https://github.com/oras-project/oras/discussions/2063 ; https://docs.github.com/packages/working-with-a-github-packages-registry/working-with-the-container-registry | `GITHUB_TOKEN` in CI push: per job, ephemeral | Push: workflow's own repo. Pull cross-repo: package-level "Manage Actions access" | **Make Satat's images public** (no pull secret at all); or classic PAT with only `read:packages` in a `registries.yaml`/pull secret, 90 d rotation, owned by a bot account. Or mirror into a cloud registry reached by WIF (ECR/GAR; UNVERIFIED for k3s kubelet credential-provider fit) |
| 8 | Hatchet API token | **Partly** | Token API has `expiresIn`: https://github.com/hatchet-dev/hatchet/pull/670 (dashboard 100 y / 1 y / 3 mo; CLI default 90 d). No federation. Could mint per deploy from the admin CLI | Any duration chosen at creation; per-step minting is possible via the admin API but pointless | Tenant-scoped, no finer scopes found (UNVERIFIED) | Launcher/worker token, 90 d, rotated by `hatchet-admin token create` in the deploy pipeline; never in sandboxes (sandboxes do not talk to Hatchet per ADR 0024) |
| 9 | ntfy tokens | **Partly** | `ntfy token add --expires=<dur> --label=...` and per-user ACLs: https://github.com/binwiederhier/ntfy/blob/7b54850c/cmd/token.go | Any duration chosen at creation (e.g. `2d`) | Tokens "have full access, and can perform any task a user can do", so scope = the user's ACLs. Make one write-only user per topic | Write-only user on one topic, 90 d token, rotated by deploy pipeline. Low blast radius |
| 10 | SaaS keys: Linear | **Partly** | OAuth app with `client_credentials`: "Request a new client credentials token at the start of each run" (Linear's own CI guidance), 30-day token, no refresh. Or `actor=app` OAuth with 24 h access tokens + rotating refresh tokens: https://linear.app/developers/oauth-2-0-authentication | 30 d (client creds) or 24 h (refresh flow); you can mint per run but cannot shorten | OAuth scopes; app actor sees all public teams | OAuth client id/secret held by the broker (root secret), 30-day tokens minted per run; personal API keys avoided |
| 10 | SaaS keys: Slack | **Partly** | Token rotation (opt-in, irreversible): 12 h access tokens, single-use refresh tokens: https://docs.slack.dev/authentication/using-token-rotation/ | 12 h | Granular bot scopes per app | Non-rotating bot token, scoped to the minimum channels/scopes; rotate by reinstall |
| 10 | SaaS keys: Sentry | **Partly** | Public integrations: 8 h tokens with refresh (https://docs.sentry.io/integrations/integration-platform/public-integration/); org tokens and internal integrations are static (https://docs.sentry.io/account/auth-tokens/) | 8 h | Scoped per integration; org-wide projects | Internal integration token, minimum scopes, one per use, rotate quarterly |
| 10 | SaaS keys: others | Mostly **No** | UNVERIFIED per vendor. Heuristic: OAuth2 with refresh = Partly; "API key" in a settings page = No | n/a | Usually coarse | Per-vendor key, least scope, expiry if the vendor supports it, calendar rotation (90 d), owner = Satat operator, stored only in the broker |
| 11 | npm / PyPI / crates.io publish | **Yes** | Trusted publishing via OIDC. npm: GitHub Actions, GitLab, CircleCI **cloud runners only**, npm CLI >= 11.5.1 (https://docs.npmjs.com/trusted-publishers/). PyPI: 15-minute project-scoped token (https://docs.pypi.org/trusted-publishers/). crates.io: 30-minute token, first publish needs a static token (https://crates.io/docs/trusted-publishing) | 15 to 30 min | One package/crate, one workflow file (+ optional environment) | Self-hosted k3s runners cannot use npm trusted publishing today. Publish from GitHub Actions (cloud runner), not from a sandbox. Else a package-scoped, 2FA-bypass granular token with expiry, rotated at each release |
| 12 | SSH / deploy keys | **No** (keys), **Yes** (certs) | Avoid SSH: use HTTPS with installation tokens for git. For hosts: SSH certificates from a CA (OpenBao SSH secrets engine, `step-ca`, Teleport) with minute-scale validity (UNVERIFIED, not re-fetched) | Cert: minutes | Principals and `force-command` | GitHub deploy key is static, repo-scoped, read-only by default; use only where HTTPS tokens cannot be used; rotate yearly; held by the broker |
| 13 | Webhook secrets (GitHub, Hatchet event sources, etc.) | **No** | HMAC shared secret, by design. The sender signs; there is no token to mint | n/a | One secret per sender/endpoint | Unique random 32-byte secret per webhook, stored only in the receiver, rotate yearly with a dual-secret overlap window (accept old and new) |
| 14 | The root secrets | **No** | See next section | n/a | n/a | n/a |

## Root secrets (secret zero)

These authenticate the minting and cannot themselves be minted per step:

| Root secret | Why static | Narrowing and rotation |
|---|---|---|
| GitHub App private key | Signs the App JWT that mints installation tokens; GitHub supports multiple active keys, so rotation is overlap-based | App with minimum permissions (contents, PRs, issues, metadata) installed on named repos only; key held only by the broker; rotate yearly or on any suspicion; use KMS signing if a KMS exists (octo-sts supports gcp/aws/akv KMS) |
| LiteLLM master key | Required by `/key/generate` | Held by the broker only; rotate on a calendar; the sandbox cannot reach admin routes with a virtual key |
| Upstream provider credential behind LiteLLM | LiteLLM must call Anthropic/OpenAI | Prefer WIF (Anthropic/OpenAI both support it; whether LiteLLM can use WIF credentials for upstream calls is UNVERIFIED, so check before relying on it). Fallback: service-account key with expiry, provider-side spend cap, rotate <= 90 d |
| Claude subscription token | 1-year static OAuth token | See row 2 |
| Postgres superuser / OpenBao root | Bootstrap | Used only by the broker/OpenBao; rotate root via the engine |
| Cloud: none, if WIF is used | The k3s signing key is the root; keep it out of backups and public (only the public JWKS is published) | Rotating the cluster signing key requires republishing the JWKS (OpenAI and GCP docs both warn) |
| Hatchet admin credentials, ntfy admin | Operator access | Operator-only, never in sandboxes |

Storage of root secrets is the still-open secrets-on-Kubernetes ADR. This note does not decide it, but the taxonomy shows the set is small: about 5 to 8 values, all consumed by the broker/launcher/CI, none by sandboxes.

## Notes on specific rows

**Row 2, Claude subscription.** `claude setup-token` produces a one-year token; Claude Code docs say it "can only make model requests" and is for CI and scripts (https://code.claude.com/docs/en/authentication). Claude Code also reads `CLAUDE_CODE_OAUTH_REFRESH_TOKEN` and `CLAUDE_CODE_OAUTH_SCOPES` for renewable auth (mentioned in https://github.com/anthropics/claude-code/issues/51784; not documented as a supported pattern, UNVERIFIED). No per-run scope or budget exists on a subscription, so the sandbox should never hold it. The workable pattern is to keep it in the launcher or a gateway and have LiteLLM or a sidecar proxy attach it, exposing only a per-step LiteLLM key to the pod. Whether subscription tokens may be used for scheduled automation under Anthropic's terms is unclear from public sources (a community question is open in https://github.com/anthropics/claude-code/issues/27125 ); UNVERIFIED, check current terms before depending on it. Because Anthropic WIF exists (row 3a), an API-billed path can be fully keyless, so the subscription token is the only part of the LLM path that cannot be.

**Row 1 versus federation.** GitHub itself is the one place WIF is not offered to third parties: GitHub is an OIDC *issuer* (for Actions), but there is no "present any OIDC token, receive a GitHub token" endpoint. That is why an App plus a broker (or octo-sts) is needed. Installation tokens cannot be made shorter than 1 hour, so scope, not TTL, is the control; revoke at step end.

**Rows 3a/3b and k3s.** Both vendors validate the cluster JWT against a registered issuer. For a private cluster they accept an uploaded or pasted JWKS (OpenAI: "For self-hosted Kubernetes clusters, OpenAI supports only local JWKS mode"; Anthropic: JWKS "you can paste, for air-gapped clusters"). That avoids the public-issuer problem that blocked octo-sts on k3s. Costs: the uploaded JWKS must be refreshed when the k3s signing key rotates; the `iss` must match exactly (k3s default is `https://kubernetes.default.svc.<cluster-domain>`, per octo-sts-fit); Anthropic treats tokens with a `jti` as single-use (`jti_reused`), so a projected token must be fresh per exchange. The projected-volume-through-SandboxTemplate question from octo-sts-fit applies here too and is still unverified.

**Granularity trap.** WIF trust rules match on the JWT `sub`, which for Kubernetes is `system:serviceaccount:<ns>:<sa>`. Under ADR 0024 that is per lane, not per step. For per-step scope, either (a) put the step identity into a broker-minted token (broker reads the pod's `SandboxClaim`, as octo-sts-fit alternative A does) and mint the downstream credential there, or (b) accept lane-level scope for the provider side and enforce per-step limits in LiteLLM (budget, `duration`, model list). Option (b) is simpler: the provider sees the gateway's identity, and LiteLLM is the per-step policy point.

**Row 6, issuer.** AWS and Azure need the issuer's discovery document and JWKS reachable over public HTTPS (cwii write-up above). A static copy on a bucket or Pages is enough, at the cost of republishing on key rotation. GCP accepts an uploaded JWKS, so no public issuer is needed there.

**Row 11, publishing.** The sandbox should not publish packages. Release publishing belongs in GitHub Actions where trusted publishing works and the token is minted per job. If an agent must trigger a release, it opens a PR or pushes a tag and the Actions workflow publishes.

## Recommended shape for Satat

Per step, the sandbox receives at most:

1. A GitHub installation token for the step's repo, minted by the broker (TTL 1 h, revoked at step end).
2. A LiteLLM virtual key, minted by the broker (`duration` roughly the step timeout, `models` per lane, `max_budget` per step).

Nothing else crosses into the pod. Provider keys, the subscription token, Hatchet and ntfy tokens, SaaS keys, DB credentials and cloud credentials stay with the launcher/broker/CI. Deploy and publish run in GitHub Actions with OIDC (cloud, PyPI, crates.io, npm, GHCR via `GITHUB_TOKEN`).

Rotation cadence for what stays static (suggested, not decided): root keys yearly or on suspicion; vendor API keys and PATs 90 days; Hatchet and ntfy tokens 90 days minted by the deploy pipeline; webhook secrets yearly with overlap. The rotator is the deploy pipeline where the vendor has an API (Hatchet, ntfy, LiteLLM, GitHub App keys), and a calendar-driven human task where it does not (Slack reinstall, GHCR PAT, subscription token).

## Not verified

- Exact maximum TTL for AWS/GCP sessions and AWS role chaining limits (background knowledge).
- Whether LiteLLM can authenticate upstream to Anthropic or OpenAI with WIF-minted tokens instead of static keys.
- Whether the Claude Code CLI accepts a federated Anthropic token (e.g. `ANTHROPIC_AUTH_TOKEN`) with the same behavior as an API key; the SDKs do, the CLI harness was not tested.
- Whether Anthropic's terms allow the subscription token for unattended automation.
- Hatchet token scoping finer than tenant; Hatchet token revocation behavior.
- SSH CA tooling details; ECR/GAR kubelet credential-provider fit on k3s.
- OpenRouter, Mistral and other providers' short-lived credential support.
- Whether `SandboxTemplate` passes a projected `serviceAccountToken` volume (carried over from octo-sts-fit).

## Sources

- Anthropic WIF: https://platform.claude.com/docs/en/manage-claude/workload-identity-federation ; https://platform.claude.com/docs/en/manage-claude/authentication ; https://platform.claude.com/docs/en/manage-claude/wif-admin-api
- OpenAI WIF: https://developers.openai.com/api/docs/guides/workload-identity-federation ; https://developers.openai.com/api/reference/workload-identity-federation ; https://developers.openai.com/api/docs/guides/workload-identity-federation/kubernetes
- Claude Code auth: https://code.claude.com/docs/en/authentication ; https://github.com/anthropics/claude-code/issues/51784 ; https://github.com/anthropics/claude-code/issues/27125
- LiteLLM: https://docs.litellm.ai/docs/proxy/virtual_keys ; https://docs.litellm.ai/docs/proxy/users ; https://docs.litellm.ai/docs/proxy/jwt_key_mapping
- OpenBao/Vault: https://openbao.org/docs/concepts/lease/ ; https://openbao.org/docs/secrets/databases/ ; https://openbao.org/docs/secrets/kubernetes/ ; https://developer.hashicorp.com/vault/docs/secrets/aws
- GHCR: https://docs.github.com/packages/working-with-a-github-packages-registry/working-with-the-container-registry ; https://github.com/oras-project/oras/discussions/2063 ; https://stackoverflow.com/questions/76821352
- Registries: https://docs.npmjs.com/trusted-publishers/ ; https://github.blog/changelog/2025-07-31-npm-trusted-publishing-with-oidc-is-generally-available/ ; https://docs.pypi.org/trusted-publishers/ ; https://crates.io/docs/trusted-publishing
- Hatchet: https://github.com/hatchet-dev/hatchet/pull/670 ; https://github.com/hatchet-dev/hatchet/blob/main/cmd/hatchet-admin/cli/token.go
- ntfy: https://github.com/binwiederhier/ntfy/blob/7b54850c/cmd/token.go
- SaaS: https://linear.app/developers/oauth-2-0-authentication ; https://docs.slack.dev/authentication/using-token-rotation/ ; https://docs.slack.dev/authentication/tokens/ ; https://docs.sentry.io/integrations/integration-platform/public-integration/ ; https://docs.sentry.io/account/auth-tokens/
- Clouds: https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-kubernetes ; https://learn.microsoft.com/en-us/azure/aks/workload-identity-overview ; https://learn.microsoft.com/en-us/azure/azure-arc/kubernetes/workload-identity ; http://cwii.dev/self-hosted-oidc.html
- Standards: https://www.rfc-editor.org/rfc/rfc8693 ; https://spiffe.io/docs/latest/spiffe-specs/jwt-svid/ ; https://spiffe.io/docs/latest/spiffe-specs/spiffe_workload_api/
