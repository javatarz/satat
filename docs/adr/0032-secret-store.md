# ADR 0032: Secrets load once into encrypted Kubernetes Secrets

## Status

Proposed (2026-10-25). Research in [secret-store.md](../research/v2/secret-store.md), #143.
Settles the "where secrets live on Kubernetes" question left open in `AGENTS.md`. Amends the
secret source in ADR 0028 and ADR 0031 (both on open PRs #129 and #130). Leaves ADR 0011's rule
(never in git, Terraform state or cloud-init) and ADR 0025 (token broker) unchanged.

## Context

The owner wants one way to run Satat on a cluster, with secrets loaded into an encrypted store
once per environment by hand, after which the store is self-sufficient. Setting up again on a
new environment (k3d, AWS) is fine. The repo should not depend on 1Password.

Today every start needs `op`: `scripts/compose-up.sh` on `main`, and `deploy up` resolving
`deploy.secrets` on #130, whose `satat.yaml` names the owner's vault. Kubernetes Secrets are
encrypted at rest by default on EKS (1.28+, KMS v2) and with one flag on k3s. On k3d a spike
showed the key and data share one Docker volume: they survive stop and start, and are lost on
`k3d cluster delete`.

## Decision

- **The store is a Kubernetes Secret,** `satat-secrets` in namespace `satat`, encrypted at rest
  by the cluster. No other secret manager runs in or beside the cluster.
- **Encryption at rest is required.** k3d and k3s start the server with `--secrets-encryption
  --secrets-encryption-provider=secretbox` (`scripts/k3d-up.sh`). EKS uses its default envelope
  encryption; a customer managed KMS key is optional.
- **One load per cluster.** `satat secrets load` (and `just secrets-load`) reads `NAME=value`
  lines on stdin, checks them against the names `satat.yaml` requires (every lane `pass_env`,
  plus the worker's), refuses missing, empty or unknown names without printing values, and
  server-side applies the Secret with `data`. Any source can feed it: a file, `op inject`,
  `pass`, a shell. 1Password is one optional source, documented, never called by Satat.
- **`deploy up` reads only the store.** It fans the values out into the per-lane and worker
  Secrets as ADR 0031 does, but from `satat-secrets`, never from `op` or the shell.
  `deploy.secrets` keeps names only, or is derived; `satat.yaml` names no vault.
- **Generated secrets stay generated.** The Postgres password and Hatchet token are created
  in-cluster once, as on #130.
- **Rotation.** `satat secrets set NAME` patches one key from stdin; a full `load` replaces the
  set. Either restarts the worker. Lane copies apply from the next step. Key rotation is
  `k3s secrets-encrypt rotate-keys` on k3s/k3d and nothing on EKS with the AWS-owned key.
- **`doctor` checks the store,** by key names only: missing names fail with "run: just
  secrets-load". On k3d it also fails if `k3s secrets-encrypt status` is not Enabled.

### Decision for the owner: what runs locally

Docker has no encrypted store outside Swarm, and Swarm secrets do not reach containers the worker
creates. So "one way" depends on what runs on the laptop:

- **A. k3d locally (recommended).** One store, one load, one `deploy up` everywhere. Compose is
  kept only as the Docker launcher's test bed, or retired. Reverses ADR 0026's "Docker first"
  for running Satat, and makes `kubernetes` the default `deploy.target`.
- **B. Compose stays, same command.** `secrets load` on the compose target writes a root-only
  file in a Docker named volume mounted into the worker only. Same UX; plain text on the Docker
  VM disk; a second code path.

This ADR is accepted with one of A or B named here.

## Consequences

- `deploy up` and `just run` stop needing `op` on the host. `scripts/compose-up.sh` and
  `check_op` in `doctor.sh` go once the chosen target uses the store; until then they stay.
- `k3d cluster delete` or a new EKS cluster means loading again. `doctor` catches an empty store.
- On k3s the encryption key is on the same disk as the data. It protects backups and copies of
  the datastore, not root on the node. EKS keeps the key in KMS.
- The store is not what keeps secrets from the agent. A lane still gets its `pass_env` values as
  files, readable by the agent. The injecting proxy and token broker (ADR 0025) remain the fix;
  they change which namespace gets a copy, not the store.
- Satat does not use External Secrets Operator, Sealed Secrets, SOPS or OpenBao now. Revisit
  for AWS Secrets Manager with ESO and pod identity if several clusters must share one source,
  and for OpenBao if dynamic database credentials are needed.

## Not verified

- Anything on EKS; from AWS docs only.
- Enabling encryption on an existing k3d cluster, and a colima restart with the cluster stopped.
- Option B's volume store; not built.

Revisit this ADR when a second cluster must share secrets, when the laptop stops being a target,
or when a secret needs a TTL shorter than a manual rotation.
