# Secret stores and brokers for Satat v0.2 (Kubernetes)

Research note, 2026-10-16. Question: which tool holds the secrets that must stay long-lived, on one
small k3s node (16 GiB ARM VM, platform about 2.4 GiB idle), and what changes on a managed cluster.
Constraint (ADR 0011, platform-requirements NN): secrets never in git, Terraform state or cloud-init.
Open item: where secrets live on Kubernetes. Owner wants short-lived, narrowly scoped secrets.

Method: GitHub API (release dates, SPDX license, last push) and vendor docs fetched 2026-10-16.
Anything not read from a source is marked UNVERIFIED. Exa hit its free rate limit, so general web
search was used for Akeyless and Doppler (secondary sources, weak).

## Which secrets are actually long-lived

Short-lived secrets are better produced by the system that owns them, not by a store:

| Secret | Long-lived? | Better pattern |
|---|---|---|
| GitHub installation token for steps | No | Mint per step from the App key (octo-sts, ADR 0025, or a store plugin below) |
| LLM key reaching step pods | No | LiteLLM virtual key per run; upstream provider keys stay in LiteLLM only |
| GitHub App private key | Yes | Needed by whatever mints tokens; one consumer |
| Upstream LLM provider keys | Yes | One consumer (LiteLLM) |
| Postgres passwords (Hatchet, LiteLLM) | Yes, by nature | Consumers are platform pods only; step pods never touch Postgres |
| ntfy credentials, Canvas key | Yes | One consumer each |

Consequence: the count of long-lived secrets is small (about 6) and every one is consumed by platform
pods, not step pods. This weakens the case for a dynamic-secrets engine on one node.

## Comparison

Release data from GitHub API on 2026-10-16.

| Tool | License / paywall | Self-host cost on one node | Dynamic secrets | Rotation | Pod auth | Audit | Secret zero on one VM | Fit with "never in TF state / cloud-init" |
|---|---|---|---|---|---|---|---|---|
| HashiCorp Vault | BSL 1.1 (IBM) from 1.15; internal use allowed, no competing hosted offering ([LICENSE](https://raw.githubusercontent.com/hashicorp/vault/main/LICENSE)). Latest v2.2.0, 2026-10-15. Enterprise-only: schedule-based root rotation, rotation logging, Oracle ([DB docs](https://developer.hashicorp.com/vault/docs/secrets/databases)) | Go binary + raft; docs "strongly discourage" single node for prod ([raft](https://developer.hashicorp.com/vault/docs/internals/integrated-storage)). RAM UNVERIFIED (expect 100-300 MiB) | DB (dynamic roles, 1h default / 24h max TTL), AWS/GCP/Azure, PKI, SSH, K8s tokens. GitHub App: community plugin only (below) | Static roles rotate by `rotation_period` (24h default) | Kubernetes auth (SA JWT via TokenReview, bound SA names and namespaces) ([docs](https://developer.hashicorp.com/vault/docs/auth/kubernetes)) | Audit devices (file/syslog/socket) UNVERIFIED in this pass | Shamir by default; auto-unseal via cloud KMS or transit (UNVERIFIED for OSS specifics) | Good if unsealed by cloud KMS via instance role; initial root token and unseal shares must be captured out of band |
| OpenBao | MPL-2.0, Linux Foundation / OpenSSF sandbox. v2.7.1, 2026-10-01; 8.4k stars, active ([repo API](https://api.github.com/repos/openbao/openbao)) | Same shape as Vault. RAM UNVERIFIED | Same DB engine (PostgreSQL: root rotation, dynamic, static roles) ([docs](https://openbao.org/docs/secrets/databases/)); plugins for AWS, GCP, Azure, LDAP etc. in [openbao-plugins](https://github.com/openbao/openbao-plugins) (LDAP v0.1.0 on 2026-10-02) | Static role rotation; `rotate-root` | Kubernetes auth, same login flow ([docs](https://openbao.org/docs/auth/kubernetes/)); BSO operator keeps Vault CRD names | Audit devices UNVERIFIED | Shamir default; auto-unseal: AWS/GCP/Azure KMS, OCI, PKCS#11, transit, static key ([seal docs](https://openbao.org/docs/configuration/seal/)). Since v2.7.0 many KMS seals are plugin-only | Same as Vault; static-key seal puts a key in a file, which is itself a secret to deliver |
| Infisical | MIT except `ee/`; dynamic secrets need Advanced plan, and self-host needs a license ([dynamic secrets](https://infisical.com/docs/documentation/platform/dynamic-secrets/overview), [repo](https://github.com/infisical/infisical)). v0.166.8, 2026-10-15 | Heavy: Infisical + Postgres + Redis (hard dependency). Chart limits 1000Mi + 512Mi + 256Mi; minimum node 2 CPU / 4 GB ([Helm](https://infisical.com/docs/self-hosting/deployment-options/kubernetes-helm), [hardware](https://infisical.com/docs/self-hosting/configuration/requirements)) | 32 providers incl. PostgreSQL, AWS IAM, GitHub, Kubernetes, LDAP (paid) | Rotation via jobs in Redis | Machine identities incl. Kubernetes auth (UNVERIFIED in this pass); operator CRDs `InfisicalStaticSecret`, `InfisicalDynamicSecret` (v1alpha1 only), push secret ([operator](https://infisical.com/docs/integrations/platforms/kubernetes/overview)) | Audit log, 90-day retention on paid plans | Needs `ENCRYPTION_KEY`, `AUTH_SECRET`, DB URI in a Kubernetes Secret first; losing the key loses all data | Bootstrap secrets must be injected out of band |
| Akeyless | Proprietary SaaS; only the Gateway runs on your side (secondary source). Free tier about 5 clients, 5 dynamic, 5 rotated, 3-day audit (conflicting sources, check [pricing](https://www.akeyless.io/pricing/)) | Gateway pod, size UNVERIFIED | DB, cloud, etc. (UNVERIFIED) | Yes | K8s auth (UNVERIFIED) | Yes | Vendor holds root key (DFC) UNVERIFIED | OK, but a SaaS dependency and free-tier ceiling |
| Doppler | Proprietary SaaS. Free for 3 users; rotation on paid tiers; dynamic secrets unclear (sources conflict) ([toolradar](https://toolradar.com/tools/doppler), [review](https://devopsboys.com/blog/doppler-secrets-manager-review-2026)) | Operator only | Not confirmed | Paid | Service token in a Kubernetes Secret; operator does not restart pods (needs Reloader) per one review | 3-day on free (secondary) | One long-lived token per cluster | Token is secret zero |
| 1Password (Connect / service accounts / operator) | Proprietary SaaS plus self-hosted Connect. Operator v1.12.0, 2026-03-24 ([releases](https://api.github.com/repos/1Password/onepassword-operator/releases/latest)). Up to 100 service accounts, scoped to vaults ([docs](https://www.1password.dev/service-accounts/)) | Connect server is two small containers, RAM UNVERIFIED | None | Manual / item versions | Static token or Connect token in a Secret | Usage reports | One long-lived token | Token is secret zero; no short-lived minting |
| External Secrets Operator (ESO) | Apache-2.0, active (chart 2.13.0, 2026-10-16) ([repo](https://api.github.com/repos/external-secrets/external-secrets)). Sync tool, not a store | Controller pods (3 deployments by default), RAM UNVERIFIED | Generators: `GithubAccessToken` mints App installation tokens from a key in a K8s Secret, refresh below 60 min ([docs](https://external-secrets.io/latest/api/generator/github/)); other generators UNVERIFIED | Via `refreshInterval` | Uses backend's own auth (cloud workload identity, Vault K8s auth) | Backend's | Depends on backend; with cloud SM and IRSA/workload identity, none | Best on managed clusters |
| Secrets Store CSI driver | Apache-2.0 (UNVERIFIED), v1.6.1, 2026-09-09 ([releases](https://api.github.com/repos/kubernetes-sigs/secrets-store-csi-driver/releases/latest)) | DaemonSet + per-provider pod | None itself (provider-dependent) | Rotation reconciler optional | Pod SA token to provider | Provider's | Depends on provider | Mounts as files without a K8s Secret; `SecretProviderClass` per pod. Vault CSI with VSO is Enterprise ([VSO](https://developer.hashicorp.com/vault/docs/deploy/kubernetes/vso)) |
| Sealed Secrets | Apache-2.0 (UNVERIFIED), v0.40.0, 2026-09-10 ([releases](https://api.github.com/repos/bitnami-labs/sealed-secrets/releases/latest)) | One controller, small | None | Manual re-seal | None (cluster-side decrypt) | K8s audit only | Controller private key lives in the cluster; back it up | Encrypted ciphertext in git: stretches "never in git" and survives node rebuild only if key backed up |
| SOPS + age | MPL-2.0 (UNVERIFIED), v3.13.3, 2026-07-23 ([releases](https://api.github.com/repos/getsops/sops/releases/latest)) | No runtime component (decrypt in CI or via Flux/Argo) | None | Manual | n/a | Git history | age private key must be delivered to CI or cluster | Same git caveat as Sealed Secrets; works well for non-secret-ish config |
| Kubernetes Secrets + etcd encryption | Built in. k3s: opt-in `--secrets-encryption` at first start, `aescbc` default, `secretbox` since April 2025 releases; key in `/var/lib/rancher/k3s/server/cred/encryption-config.json`; `k3s secrets-encrypt` rotates; no KMS plugin mentioned ([docs](https://docs.k3s.io/security/secrets-encryption)) | Zero | None | Manual (CI re-applies, then rollout restart) | RBAC + SA | K8s API audit (off by default in k3s, UNVERIFIED) | None | Best: CI writes Secrets over kubectl (OIDC, WireGuard); nothing in state or cloud-init. Key sits on the same disk as the data, so it only protects etcd dumps and backups |
| SPIFFE/SPIRE | Apache-2.0 (UNVERIFIED), v1.15.4, 2026-10-15 ([releases](https://api.github.com/repos/spiffe/spire/releases/latest)) | Server + agent DaemonSet, plus datastore | Short-lived X.509/JWT SVIDs (identity, not secrets) | Automatic | Workload attestation (k8s) | Logs | Trust bundle bootstrap | Solves identity, not storage. Overkill for one node; useful only if a backend accepts SVIDs |
| cert-manager | Apache-2.0 (UNVERIFIED), v1.21.2, 2026-09-11 ([releases](https://api.github.com/repos/cert-manager/cert-manager/releases/latest)) | Three small deployments | Certificates (ACME, CA, Vault PKI) | Automatic renewal | RBAC | K8s events | ACME account key or CA key | Right tool for TLS only. Not a secret store |

## Specific questions

### GitHub App installation tokens from Vault/OpenBao

- Vault: no first-party engine. The community plugin `martinbaillie/vault-plugin-secrets-github` (Apache-2.0, 321 stars) signs a 10-minute app JWT and returns an installation token that GitHub expires after one hour. Callers narrow by `repositories`, `repository_ids` and `permissions`; Vault policy (`allowed_parameters`) constrains them; tokens revoke through Vault leases ([README](https://github.com/martinbaillie/vault-plugin-secrets-github)). Latest release v2.3.2, 2025-11-13, last push the same day: about 11 months quiet. Must be registered in the plugin catalog (SHA256).
- OpenBao: nothing first-party in [openbao-plugins](https://github.com/openbao/openbao-plugins) (visible releases were LDAP; list not fully read, UNVERIFIED). One fork exists, `rknightion/openbao-plugin-secrets-github` (Apache-2.0, 2 stars, pushed 2026-10-15), ported to the OpenBao SDK ([search](https://api.github.com/search/repositories?q=openbao+github+app+token+secrets+plugin)). Single-maintainer, unaudited.
- Alternatives that need no store: ESO `GithubAccessToken` generator, or octo-sts (ADR 0025). Note the App private key is still a long-lived secret in all of them.
- Infisical has a GitHub dynamic secret provider (paid plan).

### DB password rotation on restart / lease

- Vault and OpenBao dynamic roles issue a new user per `/creds` read with `default_ttl` (1h) and `max_ttl` (24h); credentials are revoked at lease expiry. The root DB user is rotated by `rotate-root` so only the store knows it. Static roles map to one existing user and rotate on `rotation_period` (24h default) or `rotation_schedule`.
- "Rotation on restart" is not a store feature: the consumer must fetch at start and renew or restart. Vault Secrets Operator does this on Kubernetes: `VaultDynamicSecret` renews at `renewalPercent` (default 67), and when the secret changes it patches `restartedAt` on targets listed in `rolloutRestartTargets`; `instantUpdates` reacts to static-role rotation events ([API](https://developer.hashicorp.com/vault/docs/deploy/kubernetes/vso/api-reference)). OpenBao has a BSO fork with Vault-compatible CRDs (maturity UNVERIFIED).
- Fit for Satat: Hatchet and LiteLLM are single deployments with a connection pool. Dynamic users mean pool churn and restarts every TTL, and the store's root DB credential becomes a new crown jewel. Static-role rotation with a rolling restart is simpler; a plain CI job doing the same against a Kubernetes Secret gives about the same result on one node. UNVERIFIED: how Hatchet and LiteLLM behave on credential change mid-run.

### Infisical

Dynamic secrets (32 providers) and self-hosting with them require the paid Advanced plan; the operator's `InfisicalDynamicSecret` is still v1alpha1 with no v1beta1 replacement. Needs Postgres and Redis (Redis is a hard dependency holding the job queue). Roughly 1.7 GiB of memory limits on the chart, which is a large share of a node whose platform uses 2.4 GiB. Poor fit.

## Secret zero on one VM with Terraform

Every self-hosted store (Vault, OpenBao, Infisical) needs a root of trust delivered outside Terraform state and cloud-init. Realistic routes:

1. Cloud KMS auto-unseal using the VM's instance role. No key material to deliver, but ties v0.2 to one cloud and still needs the init recovery keys captured once by a human.
2. Static-key or transit seal. The key file is a secret that has to arrive by some channel (GitHub Actions over WireGuard, as in ADR 0011).
3. Shamir with manual unseal after every reboot: unacceptable for an unattended agent platform.

The simplest approach in Kubernetes is the one ADR 0011 already uses: the deploy pipeline holds the secrets (GitHub secrets, cloud OIDC) and writes them to the cluster. That adds no new root of trust.

## Recommendation

Now (single-node k3s, k3d on laptop):

1. Plain Kubernetes Secrets, with k3s `--secrets-encryption --secrets-encryption-provider secretbox` on the server. The deploy pipeline creates them with `kubectl` over the existing OIDC/WireGuard path. Nothing in Terraform state, cloud-init or git. Same mechanism on k3d (no encryption needed there).
2. One namespace for platform Secrets; step lanes get no Secret, no SA token and no store access (matches ADR 0024 RBAC). Step credentials are minted per run: LiteLLM virtual key and a GitHub token from octo-sts (ADR 0025) or the ESO generator.
3. Rotation = pipeline re-applies the Secret then does a rollout restart (or Stakater Reloader, UNVERIFIED fit). Document a rotation period per secret.
4. Skip Vault/OpenBao/Infisical for now: extra moving part, secret-zero problem, HA discouraged on one node, and the dynamic engines mostly serve consumers Satat does not have. Skip Sealed Secrets/SOPS because committing ciphertext conflicts with the letter of "never in git" and the benefit (GitOps) is not in use.

Triggers to revisit: Postgres credentials need short TTLs by policy, more than one cluster, or multiple tenants. Then pick OpenBao (MPL-2.0, avoids the BSL question, same engines) with cloud-KMS auto-unseal and its secrets operator, on a separate small node rather than the step node.

Managed cluster later:

- Move long-lived secrets to the cloud secret manager (AWS Secrets Manager, GCP Secret Manager, Azure Key Vault) and sync with ESO using workload identity (IRSA, GKE Workload Identity, Azure WI). No static credential to bootstrap, ESO only needs a role. Cloud DB IAM auth or ESO password generators replace dynamic DB users if wanted.
- Enable provider-side etcd/KMS envelope encryption (managed clusters offer it).
- Keep octo-sts or the ESO GitHub generator for installation tokens.
- cert-manager when ingress TLS arrives. SPIRE only if a backend needs workload identity federation.

## UNVERIFIED items

RAM figures for Vault, OpenBao, ESO, Akeyless and Connect; audit device details for Vault and OpenBao; Infisical Kubernetes machine-identity auth; licenses of Sealed Secrets, SOPS, SPIRE, cert-manager, CSI driver (taken from general knowledge, not read); Akeyless and Doppler plan details (secondary sources only); whether OpenBao has any other GitHub secrets plugin; Hatchet and LiteLLM behavior on credential change.
