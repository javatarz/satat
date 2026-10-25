# ADR 0025: GitHub credentials: Satat App, broker, and a pod that never holds a token

## Status

Accepted (2026-10-16). The open items under "Not verified" remain to be checked. Based on #105 ([findings](../research/v2/octo-sts-fit.md)) and the secrets
research ([taxonomy](../research/v2/notes/secrets-taxonomy.md),
[agent isolation](../research/v2/notes/secrets-agent-isolation.md),
[leak prevention](../research/v2/notes/secrets-leak-prevention.md)). Rewritten after the first
version, which had the pod receive a token from the broker. Supersedes the identity part of
[ADR 0006](0006-github-app-identity.md) in spirit (a GitHub App, not a PAT) for v2; ADR 0020's
PAT stays in force for the v0.1 Canvas stack. Expected to become the GitHub section of a wider
secrets ADR.

## Context

Step pods on Kubernetes (ADR 0024) need to read repos and propose changes. Anything an agent
can read (env, files, credential-helper output, tool output) reaches its transcript, so the
goal is a pod that never holds a GitHub token. Claims cannot set `env`, annotations are
readable by anyone who can `get pods`, and lane pods mount no service-account token.

GitHub gives no way to shorten an installation token's one-hour lifetime, and no permission
setting separates "push a branch" from "merge" (both need `contents: write`).

## Decision

**A Satat-owned GitHub App.** Permissions: contents, pull requests, issues, metadata. No
administration, workflows or secrets. Commits and PRs come from `<slug>[bot]`, which GitHub
badges as a bot. Commit author is `<slug>[bot]` with the App's noreply email, plus a
`Satat-Run: <id>` trailer. The pusher creates the commits through the API
(`createCommitOnBranch`) as the App, so GitHub signs them and they show as Verified; it replays
the agent's commits one by one and sets no author. See
[signed agent commits](../research/v2/signed-agent-commits.md) (#148). If that does not verify,
the fallback is to sign in the pusher with a key held by the broker.

**A token broker (Go)** holds the App private key and mints installation tokens with
`POST /app/installations/{id}/access_tokens`, narrowed by `repositories` and `permissions`.
The pod never calls it. Only trusted callers do: the egress proxy and the pusher. octo-sts is
not adopted: its subject is the lane's service account (no per-step scope), the k3s issuer is
cluster-internal and self-signed, and it ships no Kubernetes manifests.

**Reads.** At step start the proxy asks the broker for a read-only token (`contents: read`,
that repo only) and injects it into requests to `github.com` and `api.github.com`. The pod has
HTTPS remotes, a placeholder `GH_TOKEN` and no credential helper.

**Writes: a trusted pusher by default.** The agent commits locally and never pushes. When the
step ends, the worker takes a `git bundle` or patch, runs a gitleaks scan on added lines,
checks the branch name (`satat/<run>`) and refuses changes under `.github/workflows`, then
mints a write token (`contents: write`, `pull_requests: write`, that repo only), pushes,
opens a **draft** PR, and revokes the token with `DELETE /installation/token`. A lane that must
push mid-step uses the proxy instead: it mints the write token on the first push, injects it,
and revokes it at step end. A token therefore exists for seconds, not an hour.

**Merge protection by ruleset.** The App can technically merge because merging needs
`contents: write`, so GitHub rulesets enforce it. On the default branch: pull request with at
least one human approval, required status checks, updates restricted, approval of the most
recent push by someone other than the pusher, and **no bypass entry for the App**. On
`satat/*`: the App may create and push, but not delete or force-push. The App lacks
`administration`, so it cannot edit rulesets. Forks were considered (a permission-level
block) and rejected as more to manage.

**Onboarding gate.** A repo cannot be registered until a check reads back the App
installation (permissions no wider than above) and the rulesets (no weaker than above). The
check re-runs on a schedule, and a repo that no longer passes is paused.

## Consequences

- One more Satat component (broker) and a proxy or pusher as its only callers. The broker needs
  no TokenReview: the launcher already knows each step's repo, so callers ask for "this repo,
  these permissions, this step".
- The proxy must tie a connection to a step (pod IP lookup or per-step proxy credential).
  Not designed yet.
- The agent cannot push mid-step by default, so multi-step workflows hand a branch between
  steps through the pusher.
- The App private key lives only in the broker. Where it is stored depends on the pending
  platform-secrets decision.
- Rulesets become part of onboarding. A repo owner who cannot or will not set them cannot use
  Satat.
- Neither the pusher nor the proxy stops misuse of the capability inside its scope, such as a
  bad but well-formed change on a `satat/*` branch. Human review of the PR is that control.

## Not verified

- A proxy that does TLS interception and header overwrite for `git push`, `gh` and the GitHub
  API (spike on Infisical Agent Vault, iron-proxy, or a custom Go proxy).
- That GitHub rejects the merge API call for the App under the ruleset, with the App absent
  from the bypass list. Test on a scratch repo.
- That API-created commits (`createCommitOnBranch`) with an App installation token show as
  Verified, and that replaying a bundle through them keeps what Satat needs (#148). Needs a
  scratch repo and an App.
- Rate limits: two App token calls plus a revoke per step at Satat's volume.

Revisit if the proxy cannot carry `git push`, or if rulesets cannot be enforced on the
repositories Satat must serve.
