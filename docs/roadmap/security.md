# Security

**Status:** Shaping (secret store) · Backlog (parts) · **Related:** [ADR 0025](../adr/0025-github-token-broker.md),
[ADR 0032](../adr/0032-secret-store.md) (Proposed), #97, #143 · **Source:** calls on 2026-10-16
and 2026-10-25

- Agents never hold tokens (ADR 0025 token broker). Guard against prompt injection in issue text.
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
  non-secret config such as git name and email. Only the pusher can push and sign. The proxy,
  broker and LiteLLM each get their own keys.
- **The agent cannot bypass it.** No network path to the store, no credential, credentials
  bound to the holder's address or service account, and read-only policies. Defence in depth,
  so one mistake does not open the store.
- **Policy lives in the store, owned by the operator.** Not in git, because agents open PRs. A
  PR can change files; it must not be able to change who reads what.
- **Same model on Compose, k3d and Kubernetes.** The policies carry over unchanged; only the
  login method changes (AppRole on Compose, service accounts on Kubernetes).
- **Threat model: the agent.** The host and its Docker VM are trusted for now. Revisit when the
  host is not the owner's laptop (then a KMS or hardware seal).

Proposed shape: OpenBao with one identity per component (ADR 0032). Spiked on Compose.
Next, in order:

1. k3d spike: the same policies with Kubernetes auth, and step pods unable to reach the store.
2. The pusher moves out of the worker into its own service, with its own identity.
3. The worker's builder refuses the store's network and credential volumes for a lane, and a CI
   check confirms it.
4. Init and load commands (`just secrets-init`, `just secrets-load`), `doctor` checks, and
   `scripts/compose-up.sh` stops calling `op`.
5. The injecting proxy (ADR 0025) takes the Claude token. Until then the Claude step still
   holds it: the one known break of "use, never see".
