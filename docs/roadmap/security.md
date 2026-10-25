# Security

**Status:** Decided (secret store) · Backlog (parts) · **Related:** [ADR 0025](../adr/0025-github-token-broker.md),
[ADR 0032](../adr/0032-secret-store.md), #97, #143, #166 · **Source:** calls on 2026-10-16
and 2026-10-25

- Agents never hold tokens (ADR 0025: the worker clones, the pusher pushes, no broker in v0.2). Guard against prompt injection in issue text.
- Prove it with container tests: start a process in the container and check which secrets it can see.
- Staged: make it work, then right, then complete.

## Secrets: use, never see

Direction from the 2026-10-25 conversation (research in
[secret-store.md](../research/v2/secret-store.md)):

- **One store per environment, loaded once.** A human initialises it and loads the values one
  time, from any source. After that, starting or restarting Satat needs nothing from outside:
  no `op`, no shell variables, no human to unseal. 1Password is one optional way to feed the
  load, never part of the run path.
- **Each component reads only what it is entitled to.** The coding agent gets no secrets, only
  non-secret config such as git name and email. Only the pusher can push and sign. The worker
  reads a read-only token and the LLM gateway proxy holds the Claude token. Nothing mints tokens
  in v0.2.
- **The agent cannot bypass it.** No network path to the store, no credential, credentials
  bound to the holder's address or service account, and read-only policies. Defence in depth,
  so one mistake does not open the store.
- **Policy lives in the store, owned by the operator.** Not in git, because agents open PRs. A
  PR can change files; it must not be able to change who reads what.
- **Same model on Compose, k3d and Kubernetes.** The policies carry over unchanged; only the
  login method changes (AppRole on Compose, service accounts on Kubernetes).
- **Repos are trusted ones.** The operator's own or collaborators', listed in `satat.yaml`.
  Untrusted repos need kernel isolation and wait for the Kubernetes map (#166, #110).
- **Threat model: the agent.** The host and its Docker VM are trusted for now. Revisit when the
  host is not the owner's laptop (then a KMS or hardware seal).

Decided: OpenBao with one identity per component (ADR 0032). Spiked on Compose.
Decided: ADR 0025 amended. No broker, a machine-user push token read by the pusher only, a
read token read by the worker only, the repo cloned before the step starts, and an egress
allowlist with no secrets. Part of v0.2 (#99).
Next, in order:

1. The pusher moves out of the worker into its own service, with its own identity (#156, #157).
2. The worker's builder refuses the store's network and credential volumes for a lane, and a CI
   check confirms it.
3. Init and load commands (`just secrets-init`, `just secrets-load`), `doctor` checks, and
   `scripts/compose-up.sh` stops calling `op`.
4. ~~The worker clones the repo before the step starts, so the step holds no GitHub token.~~ Done (#167).
5. An LLM gateway proxy takes the Claude token and issues each step a short-lived virtual key
   (ADR 0032, #155). Until then the Compose Claude step still holds the token: the one known
   break of "use, never see".
6. Checks that run repo code move to a step with no credentials, before the pusher.
7. ~~A hostname-allowlist egress proxy on the lane network.~~ Done (#169). Left: the allowlist is
   per lane, not per step or per repo, and a host that accepts uploads is still an exfiltration route.
8. A container test: start a process in a step and list what it can see and reach.

The k3d version of the store (#153, #158) waits in the Kubernetes map (#166).
