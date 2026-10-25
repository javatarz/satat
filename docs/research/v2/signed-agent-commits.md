# Verified commits for agent PRs (#148)

**Status (2026-10-25): shelved.** `main` no longer requires signed commits, so agent PRs don't need Verified commits to merge, and #148 was closed. This note stays for when Verified agent commits are wanted again. Nothing below was tested against a GitHub App.

Question: how do commits on the PRs Satat's pusher opens show as Verified on GitHub, so they pass a signed-commits requirement on `main`? There is no such requirement now.

Read on 2026-10-25: GitHub's [about commit signature verification](https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification) page, plus third-party notes on `createCommitOnBranch` (the grafana, step-security and DataDog `github-api-commit` tools and a GitHub community discussion). Nothing here was run against a GitHub App yet, because the Satat App does not exist. The pusher still uses a token from `SATAT_PUSH_TOKEN`.

## Answer

**Prefer API-created commits.** A push of a locally built commit is never signed by GitHub, and an installation token has no way to attach a signing key. GitHub signs a commit for a bot when the request creates the commit through the API, is authenticated as the App, and carries no custom author, committer or signature. `createCommitOnBranch` (GraphQL) is that path.

The cost is that the pusher stops pushing the agent's commits. It replays them.

## What GitHub's docs say

The "Signature verification for bots" section says organisations and GitHub Apps can use bots to sign commits. It works "only if the request is verified and authenticated as the GitHub App or bot" and contains "no custom author information, custom committer information, and no custom signature information". It names the Commits API as an example.

The page does not mention `createCommitOnBranch`, does not name a token type, and does not say whether a commit pushed with `git` can be Verified. The `git push` case is not covered, which is consistent with the tools below needing the API.

## What changes for the pusher

From the third-party tools, not confirmed here:

- One mutation call makes one commit, on top of `expectedHeadOid`. A bundle of up to `MaxCommits` (20) becomes up to 20 calls, in order.
- The author is the authenticated App. The mutation takes no author, so the agent's own git identity (#121) does not appear on the PR commits, and the original commit ids are lost. That matches ADR 0025 (author `<slug>[bot]`), and `Satat-Run` is already in the PR body. The `Satat-Run` commit trailer can be added to each replayed message.
- The mutation takes file additions (base64) and deletions. It does not take file modes, so a mode change or an executable bit is lost and every file is `100644`. Symlinks are also not supported. The pusher should refuse a bundle that changes a mode or adds a symlink, the way it already refuses changes under `.github/workflows`.
- A request that sets a custom author falls outside the documented bot signing, so the pusher must not pass one.

## Not verified

- That a commit created by `createCommitOnBranch` with an App installation token shows `verified: true`, `reason: valid`, with the App as committer. This is the open question, and it needs a scratch repo and an App. DataDog's tool claims it holds for any token type, which the GitHub docs do not say.
- Payload size limits for a large commit, and rate-limit cost of up to 20 calls per step on top of the token calls in ADR 0025.
- Whether `git`-pushed commits could be Verified some other way. Signing in the pusher with a key held by the broker works for any commit, but it moves a long-lived signing key into the trusted worker and registers it on a bot account. Not checked: whether an App's bot account can hold a signing key at all. If it cannot, this needs a machine user.

## Fallback

If the verification test fails or the replay limits are not acceptable (for example a repo that relies on executable files in agent changes), sign in the pusher with a key held by the broker, registered as a signing key on the account that authors the commits. That is what ADR 0025 said before this note.
