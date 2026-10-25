# ADR 0032: Secrets live in OpenBao, read per component identity

## Status

Accepted (2026-10-25). Research in [secret-store.md](../research/v2/secret-store.md), #143.
The Kubernetes half is checked on k3d in #153.
Settles the "where secrets live" question left open in `AGENTS.md`, for Compose now and
Kubernetes later. Amends the secret source in ADR 0028 and ADR 0031 (open PRs #129 and #130)
and ADR 0027's "the pusher runs inside the worker". Leaves ADR 0011 (never in git, Terraform
state or cloud-init) and ADR 0025 (broker, proxy, pusher) unchanged.

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
- **One identity and one policy per component** (pusher, token broker, egress proxy, LiteLLM,
  worker). A policy grants `read` on named entries and nothing else. The coding agent has no
  identity. Git name and email are step inputs, not secrets.
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
- **The pusher becomes its own service** with its own identity, holding the push token and
  signing key. The worker keeps the Docker API and loses push access.
- **`doctor` checks** that OpenBao is unsealed and every required entry name exists.

## Consequences

- `scripts/compose-up.sh` and `check_op` go; starting Satat no longer needs `op`.
- One more service (about 54 MiB idle) and one more to upgrade. The OpenBao Go client goes into
  the pusher, broker, proxy and worker. LiteLLM likely needs OpenBao Agent beside it.
- Until the injecting proxy exists, the worker's policy includes the Claude token and the
  launcher passes it into the Claude step, as today. This is the one known break of "use,
  never see". When the proxy lands, the grant moves to the proxy's policy.
- On Compose the worker holds the Docker API, so it can read any volume, the credentials
  included. That is root on the Docker VM and is accepted under ADR 0027. On Kubernetes the
  worker has no such reach.
- Component credentials on Compose are long-lived per environment. Their address binding makes
  a copy useless elsewhere. Rotate by issuing a new one.
- Losing the operator password, `docker compose down -v` or `k3d cluster delete` means
  initialising and loading again. `doctor` catches an empty or sealed store.
- OpenBao 2.6+ needs a token to generate a new root token, so the recovery key alone does not
  recover access. That is why the operator login exists.
- External Secrets Operator with AWS Secrets Manager stays open as a later backend for several
  clusters sharing one source. Kubernetes Secrets, Sealed Secrets and SOPS are not used as the
  store.

## Not verified

- k3d with Kubernetes auth and a NetworkPolicy keeping step pods from OpenBao (#153).
- `kubectl port-forward` reaching the pod as loopback, for the operator login.
- A colima restart; OpenBao Agent for LiteLLM; Raft snapshot and restore; anything on EKS.

Revisit when a second cluster must share secrets, when the host stops being trusted (then a
KMS or hardware seal), or when a secret needs a TTL shorter than a manual rotation.
