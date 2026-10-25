#!/usr/bin/env bash
# Check that git will sign the commits made in this repo (signing is encouraged, not required).
#
# A pre-commit hook runs before the commit exists, so this checks configuration only:
# commit.gpgsign must be true and a signing key must be configured for gpg.format.
# scripts/check-signed-push.sh checks the commits themselves before they are pushed.
#
# usage: scripts/check-signing-config.sh [--brief]
#   --brief  print only the one-line reason (for scripts/doctor.sh) and no fix instructions
set -euo pipefail

brief=0
[ "${1:-}" = "--brief" ] && brief=1

problem() {
  if [ "$brief" -eq 1 ]; then
    echo "$1"
    exit 1
  fi
  cat >&2 <<EOF
commit signing: $1

Signing is not required, but signed commits show as Verified. For example, with an SSH key:

  git config commit.gpgsign true
  git config gpg.format ssh
  git config user.signingkey ~/.ssh/id_ed25519.pub

or with a GPG key:

  git config commit.gpgsign true
  git config user.signingkey <key id>

Add the same key to GitHub as a signing key (Settings > SSH and GPG keys) so your commits show as
Verified. Use --global to sign in every repo. See "Signed commits" in CONTRIBUTING.md.
EOF
  exit 1
}

gpgsign="$(git config --bool --get commit.gpgsign 2>/dev/null || true)"
[ "$gpgsign" = "true" ] || problem "commit.gpgsign is not true"

format="$(git config --get gpg.format 2>/dev/null || echo openpgp)"
key="$(git config --get user.signingkey 2>/dev/null || true)"

case "$format" in
  openpgp)
    if [ -z "$key" ]; then
      # With no user.signingkey, gpg picks a secret key matching the committer's email.
      email="$(git config --get user.email 2>/dev/null || true)"
      gpg_program="$(git config --get gpg.openpgp.program 2>/dev/null \
        || git config --get gpg.program 2>/dev/null || echo gpg)"
      if [ -z "$email" ] || ! command -v "$gpg_program" >/dev/null 2>&1 \
          || ! "$gpg_program" --list-secret-keys "<$email>" >/dev/null 2>&1; then
        problem "gpg.format is openpgp but user.signingkey is unset and gpg has no secret key for your user.email"
      fi
    fi
    ;;
  ssh)
    if [ -z "$key" ] && [ -z "$(git config --get gpg.ssh.defaultKeyCommand 2>/dev/null || true)" ]; then
      problem "gpg.format is ssh but user.signingkey is unset"
    fi
    ;;
  x509)
    [ -n "$key" ] || problem "gpg.format is x509 but user.signingkey is unset"
    ;;
  *)
    problem "gpg.format is '$format', expected openpgp, ssh or x509"
    ;;
esac
