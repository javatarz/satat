#!/usr/bin/env bash
# pre-push hook: fail if a commit about to be pushed is unsigned or has a bad signature.
# Unsigned commits do not show as Verified, and fixing them after the PR is open means re-signing and force-pushing.
#
# Checks every commit reachable from the pushed ref that is not already on a remote-tracking
# branch (and not reachable from the ref being replaced on the remote), so commits that are
# already published are never re-checked.
#
# pre-commit sets PRE_COMMIT_TO_REF and PRE_COMMIT_FROM_REF. Run by hand, it checks HEAD.
#
# usage: scripts/check-signed-push.sh
set -euo pipefail

zero='^0+$'
to="${PRE_COMMIT_TO_REF:-HEAD}"
from="${PRE_COMMIT_FROM_REF:-}"

# Deleting a remote branch pushes no commits.
[[ "$to" =~ $zero ]] && exit 0

exclude=(--not --remotes)
if [ -n "$from" ] && ! [[ "$from" =~ $zero ]] && git cat-file -e "$from^{commit}" 2>/dev/null; then
  exclude+=("$from")
fi

# %G? codes (git help log, "Placeholders"):
#   G good, U good with unknown validity      -> pass
#   N no signature, B bad signature           -> fail: GitHub will not show these as Verified
#   E cannot be checked here, usually because the signer's key is not in the local keyring or
#     gpg.ssh.allowedSignersFile is not set. The commit is signed; whether GitHub verifies it
#     depends on the key being on the author's GitHub account, which only GitHub can tell, so
#     the CI check judges it. Failing here would block everyone who pushes others' commits.
#   X expired signature, Y expired key, R revoked key -> pass with a warning: the signature is
#     cryptographically good, and GitHub may still verify it, so CI makes the final call.
# Git reports N, not E, for an SSH-signed commit when gpg.ssh.allowedSignersFile is unset, which
# is most SSH signers. So an N is only unsigned if the commit object has no signature header.
has_signature() {
  local obj header re=$'\n''gpgsig(-sha256)? '
  obj="$(git cat-file commit "$1")"
  header="${obj%%$'\n\n'*}"
  [[ $'\n'"$header" =~ $re ]]
}

git rev-parse --verify --quiet "$to^{commit}" >/dev/null || { echo "signed commits: unknown ref $to" >&2; exit 1; }

unsigned=()
warned=()
while read -r sha code subject; do
  if [ "$code" = N ] && has_signature "$sha"; then code=E; fi
  case "$code" in
    N) unsigned+=("${sha:0:12} unsigned       $subject") ;;
    B) unsigned+=("${sha:0:12} bad signature  $subject") ;;
    X|Y|R) warned+=("${sha:0:12} signature code $code  $subject") ;;
  esac
# stderr dropped: git prints a verification error per commit it cannot check (the E cases).
done < <(git log --format='%H %G? %s' "$to" "${exclude[@]}" 2>/dev/null)

if [ "${#warned[@]}" -gt 0 ]; then
  echo "signed commits: these signatures are expired or revoked; GitHub may not verify them:" >&2
  printf '  %s\n' "${warned[@]}" >&2
fi

if [ "${#unsigned[@]}" -gt 0 ]; then
  cat >&2 <<EOF
signed commits: ${#unsigned[@]} commit(s) about to be pushed are not signed:
$(printf '  %s\n' "${unsigned[@]}")

Set up signing (scripts/check-signing-config.sh says what is missing), then re-sign the commits
and push again. Re-signing keeps your changes, messages and authors but rewrites the commit ids,
and signs every commit in the range as you. Only do that for your own commits; if the branch has
commits by other people, ask them to re-sign theirs.

  git rebase --exec 'git commit --amend --no-edit -S' origin/main
  # or (--force-rebase makes git rewrite commits it would otherwise keep):
  git rebase --force-rebase -S origin/main

See "Signed commits" in CONTRIBUTING.md.
EOF
  exit 1
fi
