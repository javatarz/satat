# ADR 0025: Satat token broker for per-step GitHub tokens

## Status

Proposed (2026-10-16). Based on #105 ([findings](../research/v2/octo-sts-fit.md)). Supersedes
the identity part of [ADR 0006](0006-github-app-identity.md) in spirit (a GitHub App, not a PAT)
for v2; ADR 0020's PAT stays in force for the v0.1 Canvas stack.

## Context

Step pods on Kubernetes (ADR 0024) need a GitHub token that is short-lived and scoped to the
repository of that one step. ADR 0024 forbids `env` on claims, annotations are readable by
anyone who can `get pods`, and lane pods mount no service-account token.

## Decision

Satat runs a small broker (Go) that holds a Satat-owned GitHub App's private key and mints
installation tokens with `POST /app/installations/{id}/access_tokens`, narrowed by
`repositories` and `permissions`. A step pod presents a projected service-account token with
audience `satat-broker`. The broker validates it with TokenReview, maps the pod to its claim,
run and step, and mints a token for that step's repository only. Tokens last one hour.

octo-sts is not adopted. Its subject is the lane's service account, so it cannot scope per
step, the k3s issuer is cluster-internal and self-signed (octo-sts cannot trust a custom CA),
and it ships no Kubernetes manifests. It stays the fallback if per-lane tokens suffice.

## Consequences

- One more Satat component, a few hundred lines, plus RBAC for `tokenreviews` and read on
  pods and claims.
- The App private key lives only in the broker. Where it is stored depends on the pending
  secrets-on-Kubernetes ADR.
- The launcher must write the repo and step into the claim annotation, and the broker must be
  able to read them.

## Not verified

- `SandboxTemplate` accepting a projected `serviceAccountToken` volume with automount off.
- The pod to claim lookup through owner references.
- Broker and rate-limit behaviour under load.

Revisit if the template cannot carry a projected token, or if per-lane scope proves enough.
