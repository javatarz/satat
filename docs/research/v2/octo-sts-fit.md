# octo-sts fit with k3s (#105)

Part of #99. Question: can octo-sts, or an alternative, mint per-step GitHub tokens for pods on k3s, and what does self-hosting involve? Source-read and docs-read only; nothing was run. Feeds build step 7 (token broker) and ADR 0025.

Read on 2026-10-16: `octo-sts/app` v0.12.0 (2026-10-09, Apache-2.0, last push 2026-10-15), its README, `docs/architecture.md`, `pkg/provider`, `pkg/octosts/trust_policy.go`, `pkg/envconfig`, `cmd/app/main.go`; k3s `pkg/daemons/control/server.go`; GitHub's installation-token REST docs.

## Answer

**octo-sts can federate a k3s pod, but only with extra plumbing, and it is too coarse for "per step".** An alternative that fits better is a small Satat-owned broker that mints GitHub App installation tokens directly. octo-sts is the fallback if per-lane tokens turn out to be enough.

## How octo-sts works

A workload presents an OIDC token. octo-sts discovers the issuer's keys, verifies the token, loads a trust policy from `.github/chainguard/<identity>.sts.yaml` **in the target repo** (or an org policy in the `.github` repo), checks issuer, subject, audience and optional claims against it, then asks GitHub for an installation token narrowed to the policy's permissions and repositories. Tokens are 1 hour (GitHub's installation-token limit).

```yaml
issuer: https://<k3s issuer>
subject: system:serviceaccount:satat-lane-a:runner
permissions:
  contents: write
  pull_requests: write
```

The audience defaults to the deployment's `STS_DOMAIN`; a policy can override it with `audience` or `audience_pattern`.

## Does it fit k3s?

| Requirement | Finding |
|---|---|
| OIDC issuer from k3s service accounts | k3s sets `--service-account-issuer=https://kubernetes.default.svc.<cluster-domain>` (default `cluster.local`) and an `api-audiences` of the same URL plus `k3s`. That issuer exists only inside the cluster and is served over the cluster's self-signed CA. |
| octo-sts can verify it | octo-sts runs `oidc.NewProvider` on the issuer named in the token, with the system trust store and no way to add a CA (`pkg/provider/provider.go`). Issuers must be `https` (plain `http` only for localhost) and the path may not contain odd characters. So the default issuer works only if octo-sts runs in the same cluster **and** the cluster CA is added to its trust store (for example `SSL_CERT_FILE` on a custom image or mount), **and** unauthenticated callers may read the discovery and JWKS endpoints (Vault and OpenBao guides bind `system:service-account-issuer-discovery` to `system:unauthenticated`). |
| Alternative: public issuer | Set `--service-account-issuer` to an https URL you host (static `/.well-known/openid-configuration` and JWKS on a bucket or Pages) and `--service-account-jwks-uri` to match. This is what lets the hosted `octo-sts.dev` work, but then every cluster rebuild or key rotation means republishing JWKS. A k3d issue ([k3d-io/k3d#1187](https://github.com/k3d-io/k3d/issues/1187)) reports cluster creation failing when these flags are passed through `--k3s-args`; unresolved there. |
| Subject granularity | The subject is `system:serviceaccount:<namespace>:<serviceaccount>`. ADR 0024 runs one namespace and template per lane, so the subject identifies the **lane**, not the step or the run. The `kubernetes.io` claim carries the pod name and UID, but trust policies match string and boolean claims, not nested objects (read from `CheckToken`; not tested), so a policy cannot pin a token to one pod. |
| Per-step scope | The caller chooses the `scope` (repo) and `identity` in the request. Any pod in the lane that holds a lane token can ask for any repo whose policy trusts the lane. The launcher cannot narrow it per step. A compromised step could take a token for another repo the lane is trusted on. |
| Where policy lives | In each target repo (`.github/chainguard/*.sts.yaml`), so onboarding a repo means committing a policy to it, and write access to that repo's default branch is write access to the trust. Fits "Satat owns config in git", but adds a per-repo setup step. |
| ADR 0024 compatibility | Lane pods have `automountServiceAccountToken: false` and no token. A **projected** `serviceAccountToken` volume with an explicit audience is separate from automount and allowed under Pod Security `restricted`, but whether `SandboxTemplate` passes through extra volumes is **not verified**. Network policy would also need to allow the pod to reach octo-sts. |

## Self-hosting octo-sts

- **Images:** `ghcr.io/octo-sts/app` and `ghcr.io/octo-sts/webhook`, cosign-signed. Resolve digests with `scripts/image-digest.sh`.
- **Own GitHub App.** The hosted App asks for a large permission superset (organization admin and members write among them) because a GitHub App cannot be narrowed after install. A self-hosted deployment can register its own App with only what Satat needs. That is the main security argument for self-hosting.
- **Key handling:** `APP_SECRET_CERTIFICATE_FILE` or `_ENV_VAR` (PEM) or a KMS key (`KMS_PROVIDER` gcp, aws, akv). On k3s with no KMS, the PEM is a Kubernetes Secret, which needs the still-open secrets-on-Kubernetes ADR.
- **Config:** `PORT`, `STS_DOMAIN` (also the default audience), `GITHUB_APP_IDS`, optional `OCTOSTS_ALLOWED_ISSUERS`, `ORG_POLICY_REPO`, `OCTOSTS_POLICY_DIR`.
- **Webhook component:** a second binary validates trust-policy changes in PRs (the `Trust Policy Validation` check). Optional for exchange, but its config must match the app's.
- **Terraform module** under `modules/app` targets GCP (Cloud Run, Firestore, KMS). No Helm chart or Kubernetes manifests ship. We would write our own Deployment and Service.
- **Dependency weight:** built on Chainguard's `terraform-infra-common`, `go-grpc-kit` and cloudevents libraries, and the exchange speaks their gRPC `SecurityTokenService`. The metrics and tracing setup is on by default (`METRICS=true`), and `EVENT_INGRESS_URI` unset only logs a warning.
- **Footprint:** not measured. It is a single Go process with in-memory caches (install, trust policy, org issuer). Multi-App sticky routing is not needed for one App.
- **Behaviours to know:** a missing org allowlist file fails open; policy changes can take up to an hour to apply through caches; GitHub permission changes to the shared App arrive quarterly (irrelevant if we own the App).

## Alternatives

### A. Own broker minting installation tokens (recommended)

The GitHub REST API `POST /app/installations/{id}/access_tokens` takes `repositories` (up to 500), `repository_ids` and `permissions`, and returns a token valid one hour that cannot exceed the App's grants. That is the whole "token minting" half of octo-sts, with narrowing per request.

A Go service holds the App private key and:

1. Accepts a pod's projected service-account token (audience `satat-broker`).
2. Validates it with the Kubernetes TokenReview API (no public issuer, no CA trick, no discovery endpoint exposure).
3. Reads the pod name from the review result, finds the pod's `SandboxClaim`, and looks up the run and step it belongs to (claim annotations are written by the launcher).
4. Mints a token for exactly that step's repo and the permissions its lane allows.

Satat already owns the launcher and the step-to-repo mapping, which octo-sts cannot see. Cost: roughly a few hundred lines of Go using `google/go-github` and `bradleyfalzon/ghinstallation`, plus the broker's RBAC (`tokenreviews` create, `pods` and `sandboxclaims` get). The private key is held by the broker only, never by a lane pod.

### B. Launcher mints and injects the token

The launcher mints the token itself, as in A, and passes it into the pod. There is no clean channel: annotations are readable by anyone who can `get pods` and ADR 0024 forbids `env` on the claim, and worker RBAC has no `secrets`. This would need a design change to ADR 0024's worker RBAC, so it is a poor fit.

### C. Fine-grained PAT

What ADR 0020 uses today for Agent Canvas. Long-lived, tied to one human, one token for all repos. Rejected for v2 (NN12 per-org credential accounts and "no long-lived keys").

### D. Hosted `octo-sts.dev`

Works only with a public issuer (see above) and uses the shared App with its broad permission superset. Not attractive for an always-on agent.

## Recommendation

1. Build the token broker (alternative A) as build step 7. It gives true per-step scope and avoids exposing or re-hosting an OIDC issuer. The later secrets research changed how it delivers tokens: the pod never receives one. The egress proxy or a trusted pusher calls the broker and uses the token outside the pod (see [ADR 0025](../../adr/0025-github-token-broker.md)). Ignore the pod-presents-a-projected-token flow described above in alternative A.
2. Keep octo-sts as the fallback if per-lane tokens are acceptable, for example a read-only clone token for a lane. It would then need its own Deployment, a CA or public-issuer solution, and a policy in every target repo.
3. Register a **Satat-owned GitHub App** (this brings back the idea in superseded ADR 0006) with the minimum permissions: contents, pull requests, issues, metadata. Store its private key where the future secrets ADR says.

## Not verified

- Whether `SandboxTemplate` accepts a projected `serviceAccountToken` volume, and whether the lane's `automountServiceAccountToken: false` leaves it intact. No longer needed under ADR 0025, since the pod holds no token.
- Whether `claim_pattern` can match the nested `kubernetes.io` claim (read from source, not run).
- The pod-name-to-claim lookup: the pod carries an owner reference to the `Sandbox`, and the `Sandbox` to the claim (inferred from the #103 spike, not tested).
- octo-sts memory and CPU, and running it on arm64 (images are multi-arch only if the registry says so; not checked).
- Rate limits: installation tokens share 5,000 to 15,000 requests per hour per installation. At a handful of concurrent steps this is not a concern, but it is untested.

## Sources

- https://github.com/octo-sts/app (README, `docs/architecture.md`, `pkg/provider/provider.go`, `pkg/octosts/trust_policy.go`, `pkg/envconfig/envconfig.go`, `cmd/app/main.go`, `modules/app`)
- https://github.com/k3s-io/k3s/blob/master/pkg/daemons/control/server.go (`service-account-issuer`, `api-audiences`)
- https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app
- https://github.com/k3d-io/k3d/issues/1187
- https://developer.hashicorp.com/vault/docs/auth/jwt/oidc-providers/kubernetes
- https://www.kubernetes.dev/resources/keps/1393/
