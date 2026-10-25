# Secret store: load once, self-sufficient per cluster

Research for #143, 2026-10-25. Feeds [ADR 0032](../../adr/0032-secret-store.md). Builds on
[secrets-stores.md](notes/secrets-stores.md) (the tool survey, 2026-10-16) and
[secrets-agent-isolation.md](notes/secrets-agent-isolation.md) (keeping secrets from the agent).
Those notes are not repeated here. Facts that can change are as of October 2026.

## The bar

The owner's words: "a single way to run this on our cluster. I'm OK to load the secrets in once
manually (either from 1Pass or loading in something like an env file into the encrypted store).
After that, the store on that machine should be self sufficient. Across different environments
(local vs aws), a setup again is OK." And the repo should not depend on 1Password long term.

Read as tests a store must pass:

1. **Load once.** A human puts the values in one time per environment. Any source works.
2. **Self-sufficient.** After the load, starting, restarting or redeploying Satat needs nothing
   from outside the cluster: no `op`, no shell variables, no human to unseal.
3. **Encrypted at rest** on that machine.
4. **Same way on k3d and on AWS**, even if each needs its own load.
5. **No 1Password in the repo's run path.** `op` may feed the load; nothing else calls it.

Today neither path passes test 2. `scripts/compose-up.sh` reads three values with `op` on every
start. On PR #130 (ADR 0028 and 0031) `satat deploy up` resolves `deploy.secrets` (`op://` or
`env`) on every run, and `satat.yaml` names the owner's 1Password vault, which the
[provider-neutrality roadmap](../../roadmap/provider-neutrality.md#secrets-and-credentials)
already flags.

## What Satat stores

From [secrets-stores.md](notes/secrets-stores.md#which-secrets-are-actually-long-lived) and
`satat.yaml`. Two kinds:

| Kind | Secrets today | Who makes them |
|---|---|---|
| Loaded (a human supplies them) | `CLAUDE_CODE_OAUTH_TOKEN`, `SATAT_GITHUB_TOKEN`, `SATAT_PUSH_TOKEN`; later the GitHub App key (ADR 0025) and the LLM gateway key | The one-time load |
| Generated (Satat makes them) | Postgres password, Hatchet client token | `deploy up`, once, already in-cluster on #130 |

The store only has to solve the first kind. The second already meets the bar on #130.

## Kubernetes Secrets with encryption at rest

### k3s and k3d

- `--secrets-encryption` at first server start makes k3s generate a key, write
  `/var/lib/rancher/k3s/server/cred/encryption-config.json` and pass it to the API server.
  Providers: `aescbc` (default) and `secretbox` (April 2025 releases and later)
  ([docs](https://docs.k3s.io/security/secrets-encryption)).
- It can be turned on for an existing cluster with `k3s secrets-encrypt enable` plus restarts,
  from the March 2026 releases (v1.34.6+k3s1); #130 pins v1.34.12-k3s1
  ([CLI docs](https://docs.k3s.io/cli/secrets-encrypt)).
- Rotation on one server: `k3s secrets-encrypt rotate-keys`, then wait for
  `reencrypt_finished` (same page). The docs warn a wrong procedure can corrupt the cluster.
- **The key lives on the same disk as the data.** It protects copies of the datastore (a backup,
  a dump, a disk image). It does not protect against root on the node. k3s has no KMS plugin in
  its docs.
- k3d passes the flags with `--k3s-arg "--secrets-encryption@server:0"`
  ([k3d create](https://k3d.io/stable/usage/commands/k3d_cluster_create/)). `scripts/k3d-up.sh`
  on #130 does not pass them today.

**Spike on this laptop** (k3d v5.9.0, `rancher/k3s:v1.34.12-k3s1`, colima; one placeholder
Secret, deleted afterwards):

| Step | Result |
|---|---|
| Create with `--secrets-encryption --secrets-encryption-provider=secretbox` | 11 s. `secrets-encrypt status`: Enabled, key `secretboxkey`, XSalsa20-Poly1305 |
| Where state lives | `/var/lib/rancher/k3s` is an anonymous Docker volume on the server container. It holds both `server/cred/encryption-config.json` (mode 600) and the SQLite datastore `server/db/state.db` |
| Plain text in the datastore | 0 matches for the placeholder value; `k8s:enc:secretbox` prefix present |
| `k3d cluster stop` then `start` | Secret reads back; still encrypted |
| `k3s secrets-encrypt rotate-keys` | "keys rotated, reencryption finished" in about a second; new key `secretboxkey-<timestamp>`; Secret reads back |
| `k3d cluster delete` | Server container and its `/var/lib/rancher/k3s` volume are gone, so the key and every Secret go with them |

So on k3d: survives stop/start (and, by the same volume, a colima restart, not tested here).
`k3d cluster delete` loses the store, and a load is needed again. That is the "setup again" the
owner accepts, but it should be a known step, and `doctor` should catch an empty store.

### EKS

- Since 2025-03-05, EKS envelope-encrypts **all** Kubernetes API data by default on Kubernetes
  1.28 and later, with KMS provider v2 and an AWS-owned key, at no charge. A customer managed
  KMS key is optional (normal KMS charges)
  ([announcement](https://aws.amazon.com/about-aws/whats-new/2025/03/amazon-eks-envelope-encrypts-kubernetes-api-data-default/),
  [user guide](https://docs.aws.amazon.com/eks/latest/userguide/envelope-encryption.html)).
- The key is in KMS, not on a node. Nothing to configure, nothing to rotate by hand with the
  AWS-owned key.
- Self-sufficient: the Secret lives in the control plane; pods restart and nodes are replaced
  without a load. Deleting the cluster loses it, as on k3d.

### Getting to the containers

- A Secret mounted as a volume is held by kubelet in tmpfs and removed when the pod goes
  ([Secrets](https://kubernetes.io/docs/concepts/configuration/secret/),
  [volumes](https://kubernetes.io/docs/concepts/storage/volumes/#secret)). Not on node disk,
  unless swap is on with an old kernel
  ([Linux security](https://kubernetes.io/docs/concepts/security/linux-security/)).
- Anyone who can create a pod in a namespace can read that namespace's Secrets through the pod
  ([good practices](https://kubernetes.io/docs/concepts/security/secrets-good-practices/)). The
  ADR 0024 / 0031 RBAC already denies the worker pod creation and Secret reads.
- A pod can only mount Secrets from its own namespace, so lane namespaces need their own copy.
  #130 already creates one per lane with only that lane's `pass_env` keys.

## Alternatives against the bar

| Option | Load once | Self-sufficient | Encrypted at rest | Same on k3d and AWS | No 1Password | Moving parts | Verdict |
|---|---|---|---|---|---|---|---|
| **Kubernetes Secrets + encryption at rest** | Yes, `kubectl apply` on stdin | Yes | k3s secretbox (key on same disk); EKS KMS v2 by default | Yes, same API; only the cluster flag differs | Yes | None new | **Recommended** |
| OpenBao in-cluster, Shamir seal | Yes | **No**: a human unseals after every restart | Yes | Yes | Yes | Server, storage, injector or operator | Fails test 2 |
| OpenBao, static-key seal | Yes | Yes | Yes, but the unseal key is a file or env var beside it. Docs recommend it only when another secrets manager already holds that key ([static seal](https://openbao.org/docs/configuration/seal/static/)) | Yes | Yes | As above | Same trust as k3s encryption, more to run |
| OpenBao, AWS KMS auto-unseal | Yes | Yes on AWS | Yes, key in KMS | **No**: k3d has no KMS, so local needs static or Shamir | Yes | As above plus IAM | Worth it only with dynamic secrets ([secrets-stores.md](notes/secrets-stores.md#recommendation)) |
| Sealed Secrets | Yes (seal, commit, apply) | Yes, while the controller key exists | Ciphertext in git; plain Secret in the cluster | Yes, but re-seal per cluster | Yes | Controller; key renews every 30 days and needs backing up ([README](https://github.com/bitnami-labs/sealed-secrets)) | Ciphertext in a public repo; no gain without GitOps |
| SOPS + age | Yes | Needs the age key at deploy time | Ciphertext in git | Yes | Yes | A key to deliver | Same git issue; moves secret zero to the age key |
| External Secrets Operator + AWS Secrets Manager | Load into Secrets Manager once | Yes on AWS (pod identity, no static key) | Yes, KMS | **No** on k3d: needs a static AWS key, which breaks test 2 locally | Yes | Operator, IAM, about $0.40 per secret a month ([pricing](https://aws.amazon.com/secrets-manager/pricing/)) | Good later AWS-only upgrade; not "one way" |
| ESO, Kubernetes provider | n/a | Yes | Same as Secrets | Yes | Yes | Operator, a token Secret | Only copies Secrets between namespaces; `deploy up` already does that |
| 1Password at every start (today) | No, every start | **No** | Not stored | Yes | **No** | `op` on the host | What the owner wants to stop |

ESO's whole model is "the source is outside the cluster". That is fine on AWS, where Secrets
Manager is the store and the cluster pulls from it with pod identity. It conflicts with
"self-sufficient" only on k3d, where there is no workload identity to the source.

## Compose today, and what "one way" means

ADR 0026 put v0.2 on Docker Compose on the laptop. #130 adds k3d behind the same
`deploy.target` switch. Docker has no encrypted secret store outside Swarm, so Compose cannot
use the Kubernetes answer:

- Compose `secrets:` take a `file` or `environment` source and bind-mount it at
  `/run/secrets/<name>` ([docs](https://docs.docker.com/compose/how-tos/use-secrets/)). Not
  encrypted; the file sits on the host.
- Swarm secrets are encrypted, but only `docker stack deploy` services get them. The worker
  creates plain containers through the API (ADR 0027), which cannot take Swarm secrets.
- A container's env is kept by dockerd in the Docker VM (noted in ADR 0028). On colima that disk
  is not encrypted by Docker.

| Option | One way? | Compose stays? | Self-sufficient locally | Cost |
|---|---|---|---|---|
| **A. k3d is the local runtime.** Compose stays only as the Docker launcher's test bed (or is retired). | Yes: one store, one load, one `deploy up` on every cluster | No, not for running Satat | Yes | k3d's RAM and the 4 min cold `deploy up` measured on #130; reverses ADR 0026's "Docker first" for running |
| **B. Same command, two stores.** `secrets load` writes a Kubernetes Secret on Kubernetes and a root-only file in a Docker named volume on Compose. | Same UX, two stores | Yes | Yes, but plain text on the Docker VM disk | Two code paths, two `doctor` checks; Compose store is weaker |
| **C. Compose keeps reading a source at start** (`op`, or `env` from the shell). | No | Yes | No | Fails test 2 and test 5 on Compose |

The owner's sentence says "on our cluster", which reads as A. But ADR 0026 chose Docker first
for good reasons, and A also changes ADR 0028's default target. **This is the decision for the
owner**; the ADR presents it and does not take it silently. If B is chosen, the load command and
its input format stay the same, so moving to A later changes no habits.

## Keeping secrets from the agent

The store decides where values rest. It does not keep them from the agent. From
[secrets-agent-isolation.md](notes/secrets-agent-isolation.md#answer-in-brief): anything inside
the step pod, in env or in a file, is readable by the agent and can reach the transcript. Only a
secret that never enters the pod is safe; that is the injecting proxy and the token broker
(ADR 0025, [secrets-decisions.md](secrets-decisions.md)).

What the store design must still hold:

- The store Secret lives in namespace `satat`. Only the operator (with `kubectl`) reads it.
  The worker has no Secret RBAC (ADR 0031) and lane pods have no service-account token.
- A lane gets only its `pass_env` keys, mounted as files with `items` (ADR 0031). The worker gets
  only its own (`SATAT_PUSH_TOKEN`).
- `satat-entry` scrubs loaded values from step output (ADR 0031). Unchanged.
- When the proxy lands, the Claude token moves from the lane Secret to a proxy Secret, and the
  lane gets a placeholder. The store does not change, only which namespace gets the copy.

## The one-time load

Proposed shape (names are suggestions):

```sh
# any source that prints NAME=value lines works
satat secrets load < secrets.env                 # a file you delete afterwards
op inject -i secrets.env.tpl | satat secrets load   # 1Password, nothing on disk
pass show satat/env | satat secrets load            # or any other manager
just secrets-load                                   # wraps the above, reads stdin
```

- **Input:** dotenv on stdin only, never argv or a path in the repo. `secrets.*` is already in
  `.gitignore`. A committed `secrets.example.env` lists the names with empty values, so a fork
  knows what to fill without 1Password.
- **Validation:** the required names come from `satat.yaml` (every lane's `pass_env` plus the
  worker's own). Refuse a missing name, an empty value or an unknown name, naming the variable,
  never printing a value. All or nothing: nothing is written if any check fails.
- **Write:** one Secret `satat-secrets` in namespace `satat`, server-side apply with `data` (so a
  dropped key is pruned, as #130 found), values on stdin. This is the store.
- **Fan-out:** `deploy up` reads `satat-secrets` and applies the per-lane and worker Secrets from
  it, as #130 does today from `op`. `deploy up` never calls `op` or reads the shell.
- **Rotation:** `satat secrets set NAME` reads one value from stdin and patches that key; or
  rerun `load` with the full set. Then `deploy up` (or the load itself) restarts the worker,
  because #130 notes a changed worker Secret does not roll it. Lane copies take effect on the
  next step. A checksum annotation on the worker pod template would make the restart automatic.
- **Encryption key rotation** is a cluster task: `k3s secrets-encrypt rotate-keys` on k3d; on EKS
  nothing with the AWS-owned key.
- **`doctor`:** a `check_secrets` reads the key names of `satat-secrets` (never values, for
  example `kubectl get secret satat-secrets -o go-template='{{range $k, $v := .data}}{{$k}} {{end}}'`)
  and compares them with the required names: OK, or FAIL "missing: NAME, NAME (run: just
  secrets-load)". On k3d it also runs `k3s secrets-encrypt status` in the server container and
  fails if encryption is off. `check_op` becomes a WARN-only hint, or goes, once `deploy up` no
  longer needs `op`.

## Where this agrees and differs with #130 (ADR 0031)

Agrees:

- Kubernetes Secrets as the mechanism; values on stdin, never argv or disk.
- Server-side apply with `data`, field manager `satat`, so dropped keys are pruned.
- One Secret per lane, mounted read-only with `items` for that lane's names only.
- Worker has no Secret RBAC; Postgres password and Hatchet token generated in-cluster once.

Differs:

- **Source of values.** ADR 0031 resolves `deploy.secrets` (`op://` or `env`) on every
  `deploy up`. This proposal loads once into `satat/satat-secrets`, and `deploy up` reads from
  there. `deploy.secrets` shrinks to the list of names (or is derived from `pass_env` and the
  worker), with no source and no vault name in `satat.yaml`.
- **Encryption at rest.** `scripts/k3d-up.sh` should pass `--secrets-encryption` and
  `--secrets-encryption-provider=secretbox`. An existing cluster can be switched with
  `k3s secrets-encrypt enable` or recreated (then loaded again).
- **Rotation.** ADR 0031 leaves the worker restart to a manual `kubectl rollout restart`. This
  proposal makes it part of `load` / `set` or `deploy up`.

## Not verified

- A colima restart with the k3d cluster stopped (the volume should survive; not run).
- `k3s secrets-encrypt enable` on an existing k3d cluster (documented, not run).
- EKS: nothing was run on AWS. Statements come from AWS docs.
- The Compose option B volume store; not built.
- `op inject` with a template file; not run (no real secrets were used in this research).

## Sources

- k3s: https://docs.k3s.io/security/secrets-encryption, https://docs.k3s.io/cli/secrets-encrypt
- k3d: https://k3d.io/stable/usage/commands/k3d_cluster_create/
- EKS: https://aws.amazon.com/about-aws/whats-new/2025/03/amazon-eks-envelope-encrypts-kubernetes-api-data-default/, https://docs.aws.amazon.com/eks/latest/userguide/envelope-encryption.html
- Kubernetes: https://kubernetes.io/docs/concepts/configuration/secret/, https://kubernetes.io/docs/concepts/storage/volumes/#secret, https://kubernetes.io/docs/concepts/security/linux-security/, https://kubernetes.io/docs/concepts/security/secrets-good-practices/
- OpenBao static seal: https://openbao.org/docs/configuration/seal/static/
- Sealed Secrets: https://github.com/bitnami-labs/sealed-secrets
- ESO Kubernetes provider: https://external-secrets.io/latest/provider/kubernetes/
- AWS Secrets Manager pricing: https://aws.amazon.com/secrets-manager/pricing/
- Docker Compose secrets: https://docs.docker.com/compose/how-tos/use-secrets/
