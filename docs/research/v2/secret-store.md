# Secret store: load once, read per identity, same on Compose and Kubernetes

Research for #143, 2026-10-25. Feeds [ADR 0032](../../adr/0032-secret-store.md). Builds on
[secrets-stores.md](notes/secrets-stores.md) (the tool survey, 2026-10-16) and
[secrets-agent-isolation.md](notes/secrets-agent-isolation.md) (keeping secrets from the agent).
Those notes are not repeated here. Facts that can change are as of October 2026.

The first draft of this file recommended plain Kubernetes Secrets. The owner then added
per-identity access that the agent cannot bypass, with the policy held in the store, and asked
for it to work on Docker Compose now. Kubernetes Secrets cannot do that on Compose, so the
recommendation changed to OpenBao. The Kubernetes findings below still apply to where the
OpenBao seal key lives on k3d and EKS.

## The bar

The owner's words, from #143 and the follow-up on 2026-10-25: load the secrets in once by hand,
after which "the store on that machine should be self sufficient"; setting up again per
environment is fine; no 1Password dependency long term. Then: a store that "works locally and
transfers to a k3d and k8s setup later", "initialised once per environment and then securely
accessible only to agents that are authorised to pick up certain secrets", with control over
"what agent has access to which secrets in a way that the agent cannot bypass".

Answers to the follow-up questions (2026-10-25):

| Question | Answer |
|---|---|
| Who gets access | Each component gets only what it is entitled to. The coding agent gets non-secret config (git name and email) to commit. Only the pusher can push. A signing key, if any, is the pusher's |
| See or use | Use, never see. A secret never enters the agent's container |
| Where the policy lives | In the store, changed only by the operator. Not in git, because agents open PRs |
| Local runtime | Docker Compose now (ADR 0026), k3d and Kubernetes later, same model |
| Extra service | Acceptable if it earns its place |
| Threat model | The agent, including a prompt-injected one. The laptop and its Docker VM are trusted |
| Pusher | Moves out of the worker into its own service, with its own identity |
| Injecting proxy | Out of scope. It is a consumer of the store, owned by ADR 0025 |

As tests:

1. **Load once** per environment, from any source.
2. **Self-sufficient:** starting, restarting or redeploying needs nothing from outside: no `op`,
   no shell variables, no human to unseal.
3. **Encrypted at rest.**
4. **Per-identity read:** each component reads only the secrets granted to it.
5. **Not bypassable by the agent:** the agent cannot read a secret, reach the store, borrow
   another component's credential, or change the policy, including by editing files it can
   commit.
6. **Policy in the store,** changed only with an operator credential.
7. **Same model on Compose, k3d and Kubernetes,** even if the login method differs.
8. **No 1Password in the run path.**

Today nothing passes 2, 4 or 6. `scripts/compose-up.sh` reads three values with `op` on every
start and puts all of them in the worker's environment, and the worker passes two of them into
the Claude step container (`pass_env` in `satat.yaml`).

## What Satat stores, and who reads it

| Secret | Reader | Where it ends up |
|---|---|---|
| GitHub push token (later: App write token from the broker) | Pusher | Pusher only |
| Commit signing key (ADR 0025) | Pusher | Pusher only |
| GitHub App private key (ADR 0025) | Token broker | Broker only |
| Claude subscription token | Egress proxy (ADR 0025, decisions log 2) | Proxy only. Until the proxy exists, the launcher passes it into the Claude step (prototype shortcut, see below) |
| GitHub read token | Egress proxy, and the worker's `ready-for-agent` label check | Until the broker exists, the worker |
| LLM gateway upstream key | LiteLLM | LiteLLM only |
| Git name and email | Coding agent | Not a secret. Passed as step input, stays out of the store |
| Postgres password, Hatchet client token | Postgres, Hatchet, worker | Generated in-cluster once (#130). Can move into the store later; not needed for the bar |

The coding agent has no row with a secret in it. That is the whole point of "use, never see".

## Why not Kubernetes Secrets or Compose secrets alone

| Option | Per-identity read | Agent cannot change policy | Compose | Kubernetes | Verdict |
|---|---|---|---|---|---|
| Kubernetes Secrets + RBAC (`resourceNames` per service account) | Yes | Only if the RBAC objects are not applied from git by a pipeline the agent can steer | **No**: nothing equivalent | Yes; k3s secretbox or EKS KMS for rest | Good on Kubernetes, no Compose story |
| Compose `secrets:` per service | Yes, by which service lists it | **No**: the grant is in `compose.yaml`, in git | Yes; plain files on the host | No | Fails test 6 and 3 |
| Docker Swarm secrets | Per service | Grant in the stack file, in git | Only `docker stack` services; containers the worker creates cannot use them (ADR 0027) | No | Does not fit the launcher |
| **OpenBao** (MPL-2.0 Vault fork), static-key seal | Yes: policy per identity | Yes: policies are store state, written with an operator credential | Yes: AppRole login | Yes: Kubernetes auth (service account) | **Recommended** |
| Infisical self-hosted | Yes: machine identities | Yes | Needs Postgres and Redis; docs size an instance at 2 CPU and 4 GB per container ([requirements](https://infisical.com/docs/self-hosting/configuration/requirements)) | Yes | Too heavy for the laptop |
| HashiCorp Vault | As OpenBao | As OpenBao | As OpenBao | As OpenBao | BSL licence; OpenBao is the same model under MPL-2.0 |
| ESO + AWS Secrets Manager | Yes, by IAM | Yes | No workload identity on the laptop | Yes on EKS | AWS-only. A possible later backend, not "one way" |
| Sealed Secrets, SOPS | No (they decrypt into Kubernetes Secrets or env) | No | Partial | Yes | Solve "ciphertext in git", not access |

## The design

### One store, one identity per component

OpenBao runs as one small service (single node, integrated Raft storage). Secrets live in a
KV v2 mount `satat/`, one entry per secret by name. Each reading component has one identity and
one policy that lists the secret names it may read, with `read` and nothing else:

```hcl
# policy "pusher"
path "satat/data/github-push-token"  { capabilities = ["read"] }
path "satat/data/commit-signing-key" { capabilities = ["read"] }
```

"Which component gets which secret" is the set of these policies. It is store state, not a file
in the repo, so a PR cannot change it.

### How a component proves who it is

| Runtime | Login | Bound to |
|---|---|---|
| Compose | AppRole: `role_id` and `secret_id` written once by the init into a named volume mounted only into that service | `secret_id_bound_cidrs` and `token_bound_cidrs` set to the service's fixed address on the `secrets` network |
| k3d, Kubernetes | Kubernetes auth: the pod's service account token, checked with the TokenReview API ([docs](https://openbao.org/docs/auth/kubernetes/)) | `bound_service_account_names` and `bound_service_account_namespaces` |

The policies are the same on both. Only the role definitions change, so moving to k3d keeps
every "who reads what" decision and swaps the login method. The Compose credential is long-lived
per environment, which is the price of having no workload identity; the address binding means a
copy of it is useless from anywhere else.

### What stops the agent

Layered, so no single mistake opens the store:

1. **No route.** OpenBao is only on an internal `secrets` network. Step containers are only on
   their lane network. The worker builds every create request from lane config (ADR 0027), so
   the builder must refuse the `secrets` network for a lane, and a CI check (like
   `scripts/check-compose-socket.sh`) checks that only named services join it.
2. **No credential.** Step containers get no volume but their own per-step one (ADR 0026), so
   they never see a `role_id` or `secret_id`.
3. **A stolen credential does not work elsewhere.** Address-bound login and tokens. The lane
   floor drops all capabilities, so a step cannot change its own address or spoof ARP.
4. **A token cannot widen itself.** Component policies grant `read` on named paths only: no
   list, no write, no policy, no role, no token creation.
5. **Policy changes need the operator.** The operator identity (userpass) has
   `token_bound_cidrs=127.0.0.1/32`, so its tokens work only from loopback inside the OpenBao
   container: `docker exec` on Compose, `kubectl exec` (or port-forward, not verified) on
   Kubernetes. Reaching that needs the Docker API or cluster admin, which the agent never has
   (ADR 0027, ADR 0024). The operator can write values and policies but cannot read values back.
6. **Git is not the policy.** An agent PR that edits `compose.yaml` or `satat.yaml` to join a
   lane to `secrets` or mount a credential volume still has to pass the builder refusal, the CI
   check and human review (ADR 0025 rulesets). It cannot change who may read what.

### Seal and restart

OpenBao uses the built-in static-key seal ([docs](https://openbao.org/docs/configuration/seal/static/)):
a 32-byte key in a file, read at start, so it unseals itself after any restart. The docs advise
it only when something else already protects the key. Here that is the Docker VM on Compose and
the cluster's own Secret encryption on Kubernetes, which matches the owner's threat model (the
host is trusted). The seal key lives in its own volume, mounted read-only into OpenBao only.

| Runtime | Seal key lives in | Rest encryption of the key itself |
|---|---|---|
| Compose | Named volume, OpenBao only | None beyond the Docker VM disk |
| k3d / k3s | A Kubernetes Secret mounted into the OpenBao pod | k3s `--secrets-encryption --secrets-encryption-provider=secretbox` (spike below) |
| EKS | A Kubernetes Secret, or the AWS KMS seal | EKS envelope encryption by default (below), or KMS holds the key |

### Initialise once per environment

Proposed shape (names are suggestions):

```sh
just secrets-init                                # first start: seal key, init, mounts, auth, policies, roles
op inject -i secrets.env.tpl | just secrets-load # or a file, pass, any NAME=value source on stdin
just secrets-grant pusher github-push-token      # change a policy (operator, via docker exec)
```

- `secrets-init` generates the seal key, runs `bao operator init` once, enables `satat/`,
  AppRole and userpass, writes the component policies and roles, issues each component's
  AppRole credential into its volume, creates the operator login, and revokes the root token.
  It prints the recovery key and the operator password once, for the operator's own password
  manager. Nothing is left in a container but the component credentials.
- `secrets-load` reads `NAME=value` lines on stdin only (never argv or a repo path), checks the
  names against the set the policies reference, refuses missing, empty or unknown names without
  printing values, and writes each entry as the operator.
- A committed `secrets.example.env` lists the names with empty values.
- **Rotation of a value:** load one name again; the next read gets it. Services that cache must
  re-read on a timer or on restart.
- **`doctor`:** checks OpenBao is unsealed, and as the operator lists entry names (metadata,
  never values) against the required set.
- Policies and roles are created by the init script, not by OpenBao's declarative `initialize`
  stanza ([self-init](https://openbao.org/docs/configuration/self-init/)), because that would put
  the policy in a config file in git.

### How components read

Satat's own Go services (pusher, broker, proxy, worker) use the OpenBao Go client
(`github.com/openbao/openbao/api/v2`, with `api/auth/approle` and `api/auth/kubernetes`): log in,
read the named entries, keep the values in memory, renew or log in again. Third-party services
(LiteLLM) need a value as a file or env var; the usual answer is OpenBao Agent rendering a
template beside them with its own identity. Not tried here.

### The Claude token (amended 2026-10-25)

Target: an LLM gateway proxy holds the token and gives each step a short-lived virtual key
(ADR 0032, `secrets-decisions.md` section 3). LiteLLM is the first candidate; any proxy with
the same properties will do. Whether it can use the subscription OAuth token upstream is
unproven (card GATEWAY_SPIKE).

Until then on Compose, the Claude lane cannot work without the token in its
container. The launcher (worker identity) would read `claude-oauth-token` and pass it in, as it
does today. That breaks "use, never see" for one secret, already accepted as a prototype
shortcut in ADR 0026. The store design does not change when the proxy lands: the grant moves
from the worker's policy to the proxy's.

## Spike on Compose

On this laptop: colima, Docker 29.5.2, `ghcr.io/openbao/openbao:2.7.1` (pinned by digest), a
separate Compose project `satat-secret-spike`, placeholder values only. Services: `openbao`,
`pusher` and `proxy` (OpenBao CLI images standing in for the components, fixed addresses on
the internal `secrets` network, credentials in their own volumes), and `agent` (lane network
only, `cap_drop: ALL`, read-only root, non-root, as the lane floor). All containers ran
non-root with no capabilities. Scripts were kept in `docs/scratch/secret-spike/` (gitignored).

Server config:

```hcl
api_addr     = "http://openbao:8200"
cluster_addr = "http://openbao:8201"
storage "raft"  { path = "/openbao/file"  node_id = "satat-1" }
listener "tcp"  { address = "0.0.0.0:8200"  tls_disable = true }
seal "static"   { current_key_id = "spike-1"  current_key = "file:///openbao/seal/unseal.key" }
```

Results. A denial counted only if the error was the expected one (`permission denied`, the
CIDR message, or no route), not any failure.

| Check | Result |
|---|---|
| Init once (seal key, init, mounts, policies, roles, credentials, load, root revoked) | Done in one script run, a few seconds |
| Pusher reads `github-push-token`, `commit-signing-key` | Allowed |
| Pusher reads `claude-oauth-token` | `permission denied` |
| Pusher lists the store, writes a value, edits its policy, creates a token with the proxy policy, issues itself a new `secret_id`, reads the proxy's `role_id` | All `permission denied` |
| Proxy reads `claude-oauth-token`; reads `github-push-token` | Allowed; `permission denied` |
| Pusher's credential copied into the proxy and used from there | `source address "172.30.0.30" unauthorized by CIDR restrict` |
| Agent reaches OpenBao by name; by IP | `bad address`; timed out |
| Operator from loopback inside OpenBao: change a policy, rotate a value | Allowed |
| Operator from loopback reads a value back | `permission denied` |
| Operator password used from the pusher's container | Login works, every use of the token is `permission denied` (address-bound) |
| `docker compose down` (volumes kept) then `up` | Unsealed by itself (`Seal Type static`, `Sealed false`); all 19 checks pass again with no human |
| Placeholder values in the Raft data files | 0 matches |
| OpenBao memory at idle | 54 MiB |

Found on the way:

- OpenBao 2.7 has no `file` storage backend ("unknown storage type file"); integrated Raft is
  the single-node choice.
- Since 2.6.0, `bao operator generate-root` uses an authenticated endpoint
  (`sys/generate-root-token`), and the old unauthenticated one is off by default since 2.5.3
  (listener `disable_unauthed_generate_root_endpoints`). So the recovery key alone no longer
  makes a new root token. That is why the design keeps a narrow operator login rather than a
  break-glass root path. Losing the operator password means initialising the environment again,
  which the owner accepts.
- `docker compose exec -T` reads the caller's stdin, so a load script must read its input first.

## Kubernetes findings that still apply

These came from the first draft. They now describe where the seal key and the store's data
sit on Kubernetes, not the store itself.

### k3s and k3d

- `--secrets-encryption` at first server start makes k3s generate a key, write
  `/var/lib/rancher/k3s/server/cred/encryption-config.json` and pass it to the API server.
  Providers: `aescbc` (default) and `secretbox` (April 2025 releases and later)
  ([docs](https://docs.k3s.io/security/secrets-encryption)).
- It can be turned on for an existing cluster with `k3s secrets-encrypt enable` plus restarts,
  from the March 2026 releases (v1.34.6+k3s1); #130 pins v1.34.12-k3s1
  ([CLI docs](https://docs.k3s.io/cli/secrets-encrypt)).
- The key lives on the same disk as the data. It protects copies of the datastore, not root on
  the node. k3s has no KMS plugin in its docs.
- k3d passes the flags with `--k3s-arg "--secrets-encryption@server:0"`
  ([k3d create](https://k3d.io/stable/usage/commands/k3d_cluster_create/)). `scripts/k3d-up.sh`
  on #130 does not pass them today.

Spike (k3d v5.9.0, `rancher/k3s:v1.34.12-k3s1`, colima; one placeholder Secret):

| Step | Result |
|---|---|
| Create with `--secrets-encryption --secrets-encryption-provider=secretbox` | 11 s. `secrets-encrypt status`: Enabled, XSalsa20-Poly1305 |
| Where state lives | `/var/lib/rancher/k3s` is an anonymous Docker volume on the server container, holding both the encryption config and the SQLite datastore |
| Plain text in the datastore | 0 matches; `k8s:enc:secretbox` prefix present |
| `k3d cluster stop` then `start` | Secret reads back, still encrypted |
| `k3s secrets-encrypt rotate-keys` | Done in about a second; Secret reads back |
| `k3d cluster delete` | Key and every Secret gone |

So on k3d the seal key Secret, and OpenBao's Raft volume, survive stop and start and are lost on
`k3d cluster delete`. That is the "setup again" the owner accepts; `doctor` should catch it.

### EKS

- Since 2025-03-05, EKS envelope-encrypts all Kubernetes API data by default on 1.28 and later,
  with KMS v2 and an AWS-owned key, at no charge
  ([announcement](https://aws.amazon.com/about-aws/whats-new/2025/03/amazon-eks-envelope-encrypts-kubernetes-api-data-default/),
  [user guide](https://docs.aws.amazon.com/eks/latest/userguide/envelope-encryption.html)).
- On EKS the OpenBao AWS KMS seal is also open, which keeps the seal key off the cluster.

## Not verified

- **k3d with Kubernetes auth.** Same policies, roles bound to service accounts, a step pod with
  no service-account token and a NetworkPolicy keeping it from OpenBao. Spike card #153; #130
  brings k3d.
- `kubectl port-forward` arriving as loopback inside the pod, so the operator login works
  through it. `kubectl exec` is the fallback.
- A colima restart (as opposed to `compose down` / `up`). Not run, to avoid stopping the
  running Satat stack. Named volumes live in the VM disk, so it should survive.
- OpenBao Agent rendering a file for the gateway proxy, and the proxy using the OAuth token
  upstream (card GATEWAY_SPIKE).
- Raft snapshot and restore (`bao operator raft snapshot`), if re-initialising per environment
  ever stops being acceptable.
- Anything on EKS; from AWS docs only.
- The real pusher, broker and proxy using the Go client. The spike used the CLI.

## Sources

- OpenBao: [static seal](https://openbao.org/docs/configuration/seal/static/),
  [self-init](https://openbao.org/docs/configuration/self-init/),
  [AppRole](https://openbao.org/docs/auth/approle/),
  [Kubernetes auth](https://openbao.org/docs/auth/kubernetes/),
  listener parameters (`website/content/docs/configuration/listener/tcp.mdx` on `release/2.7.x`),
  `operator generate-root` (`website/content/docs/commands/operator/generate-root.mdx`),
  [releases](https://github.com/openbao/openbao/releases) (2.7.1, 2026-10-01)
- Infisical: [self-hosting](https://infisical.com/docs/self-hosting/overview),
  [requirements](https://infisical.com/docs/self-hosting/configuration/requirements)
- k3s: https://docs.k3s.io/security/secrets-encryption, https://docs.k3s.io/cli/secrets-encrypt
- k3d: https://k3d.io/stable/usage/commands/k3d_cluster_create/
- EKS: https://aws.amazon.com/about-aws/whats-new/2025/03/amazon-eks-envelope-encrypts-kubernetes-api-data-default/, https://docs.aws.amazon.com/eks/latest/userguide/envelope-encryption.html
- Kubernetes: https://kubernetes.io/docs/concepts/security/secrets-good-practices/
- Docker Compose secrets: https://docs.docker.com/compose/how-tos/use-secrets/
