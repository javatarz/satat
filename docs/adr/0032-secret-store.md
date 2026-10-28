# ADR 0032: Secrets live in OpenBao, read per component identity

## Status

Accepted (2026-10-25). Research in [secret-store.md](../research/v2/secret-store.md), #143.
The Kubernetes half is checked on k3d in #153.
Amended 2026-10-25: one store on every environment, and the Claude token held by an LLM gateway
proxy (see Decision). Settles the "where secrets live" question left open in `AGENTS.md`, for Compose now and
Kubernetes later. Amends the secret source in ADR 0028 and ADR 0031 (open PRs #129 and #130)
and ADR 0027's "the pusher runs inside the worker". Leaves ADR 0011 (never in git, Terraform
state or cloud-init) unchanged. Amends ADR 0025 (2026-10-25): no broker, and the egress proxy holds no secret
and needs no identity.
Updated 2026-10-28: the gateway proxy is checked on Compose in
[claude-gateway-proxy-spike.md](../research/v2/claude-gateway-proxy-spike.md), #155. It works;
whether Anthropic's terms allow the proxy to hold the subscription token is open (see Not
verified).

## Context

The owner wants secrets loaded once per environment into an encrypted store that is then
self-sufficient, with no 1Password in the run path. Each component may read only the secrets
it is entitled to: the coding agent gets none (only non-secret git name and email), and only
the pusher can push. The agent must not be able to bypass this, so the policy lives in the
store and only the operator can change it; a PR cannot. It must work on Docker Compose now
(ADR 0026) and carry over to k3d and Kubernetes. The threat is the agent; the host is trusted.

Kubernetes Secrets with RBAC meet this on Kubernetes but have no Compose equivalent. Compose
and Swarm secrets put the grant in a file in git. A spike on Compose showed OpenBao meets every
test: per-identity reads, denial of every widening attempt, address-bound credentials, an
operator-only policy, and unsealing itself after a restart.

## Decision

- **The store is OpenBao,** one node, Raft storage, a KV v2 mount `satat/` with one entry per
  secret. It is the only place loaded secrets rest. Image pinned by digest.
- **One identity and one policy per component** (pusher, worker and the LLM gateway proxy). A policy grants `read` on named entries and nothing else. The coding agent has no
  identity. Git name and email are step inputs, not secrets.
- **One store everywhere.** Compose, k3d and cloud Kubernetes all run OpenBao with the same
  policies and entry names. Only the login method differs. No second store (Kubernetes Secrets,
  a cloud secret manager) holds loaded secrets in any environment.
- **Login:** AppRole on Compose, with the credential written once into a volume mounted only
  into that service and bound to its fixed address on an internal `secrets` network.
  Kubernetes auth on k3d and Kubernetes, bound to the service account. Policies are the same on
  both.
- **The agent has no path to the store.** Lanes never join the `secrets` network and never get
  a credential volume. The worker's create-request builder refuses both, and a CI check
  confirms that only named services join `secrets` or mount a credential volume.
- **The operator owns the policy.** An operator login whose tokens work only from loopback
  inside the OpenBao container (`docker exec`, `kubectl exec`) writes values, policies and
  roles, and cannot read values back. The root token is revoked after init. The recovery key
  and operator password are shown once, for the operator's own password manager.
- **Self-sufficient:** the static-key seal, with the key in its own volume on Compose and in a
  Kubernetes Secret on k3d (k3s `secretbox` encryption on) and EKS (envelope encryption on by
  default; the AWS KMS seal is open there).
- **Initialise once:** `just secrets-init` (seal key, init, mounts, policies, roles,
  credentials) and `just secrets-load` (`NAME=value` on stdin, validated, values never
  printed). Any source can feed the load; 1Password is optional and never called by Satat.
- **The Claude token is held by an LLM gateway proxy, not the lane.** The proxy reads
  `claude-oauth-token` under its own identity. Each step gets a short-lived, budget-capped,
  per-lane virtual key (about the step timeout, deleted at step end) and points
  `ANTHROPIC_BASE_URL` at the proxy. The lane holds that key, never the long-lived token. The
  proxy is LiteLLM on its `/anthropic` pass-through route, which forwards Claude Code's request
  unchanged and swaps only the credential. Each key is limited to `/anthropic/v1/messages` and
  minted and deleted by a LiteLLM non-admin user whose own budget caps the total; the lane's managed
  settings allow `customEndpoint` and pin the base URL. The same on Compose and Kubernetes.
- **GitHub credentials are static entries.** `github-push-token` (a machine user's fine-grained
  token) is readable by the pusher only; `github-read-token` by the worker only. Nothing mints
  tokens in v0.2 (ADR 0025).
- **The pusher becomes its own service** with its own identity, holding the push token and
  signing key. The worker keeps the Docker API and loses push access.
- **`doctor` checks** that OpenBao is unsealed and every required entry name exists.

## Consequences

- `scripts/compose-up.sh` and `check_op` go; starting Satat no longer needs `op`.
- One more service (about 54 MiB idle) and one more to upgrade. The OpenBao Go client goes into
  the pusher and worker. LiteLLM gets its token from OpenBao Agent, which runs as its entrypoint
  and restarts it when the token changes (LiteLLM's own Vault support is a paid feature). A
  rotation takes about 20 s and cuts requests in flight.
- Until the gateway proxy is built, the worker's policy includes the Claude token
  and the launcher passes it into the Claude step on Compose, as today: the one known break of
  "use, never see". On Kubernetes no interim delivery path is built; Claude steps there wait for
  the proxy. When it lands, the grant moves to the proxy's policy and the worker loses it.
- On Compose the worker holds the Docker API, so it can read any volume, the credentials
  included. That is root on the Docker VM and is accepted under ADR 0027. On Kubernetes the
  worker has no such reach.
- Component credentials on Compose are long-lived per environment. Their address binding makes
  a copy useless elsewhere. Rotate by issuing a new one.
- Losing the operator password, `docker compose down -v` or `k3d cluster delete` means
  initialising and loading again. `doctor` catches an empty or sealed store.
- OpenBao 2.6+ needs a token to generate a new root token, so the recovery key alone does not
  recover access. That is why the operator login exists.
- Kubernetes Secrets, Sealed Secrets, SOPS and External Secrets Operator are not used as the
  store or to deliver values to lanes. Another system may feed `secrets-load` as a source.

## Not verified

- k3d with Kubernetes auth and a NetworkPolicy keeping step pods from OpenBao (#153). If it
  fails in a way that cannot be fixed, the store choice is reopened for all environments
  together, not split.
- Whether Anthropic's terms allow a proxy to hold the subscription token. The Claude Code legal
  page says developers "may not collect, store, or intermediate Claude.ai credentials or session
  tokens", and the Usage Policy from 2026-11-12 restricts services "that route requests through
  consumer subscriptions". If the owner rules it out, the proxy holds a Console API key instead
  (billed at API rates), or the token goes back to the lane with the proxy only forwarding it.
  Either is an amendment to this ADR.
- The gateway proxy on k3d, and its budget overshoot under concurrent calls (the budget is
  checked before each call against spend written about 12 s later).
- `kubectl port-forward` reaching the pod as loopback, for the operator login.
- A colima restart; Raft snapshot and restore; anything on EKS.

Revisit when a second cluster must share secrets, when the host stops being trusted (then a
KMS or hardware seal), or when a secret needs a TTL shorter than a manual rotation.
