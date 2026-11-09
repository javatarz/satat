#!/usr/bin/env bash
# Set up an offline proof clone for the meeting-digest publish proofs.
#
# Makes docs/scratch/fake-origin.git, a bare copy of this checkout whose main is the current
# HEAD, standing in for github.com. Then clones https://github.com/javatarz/satat.git into
# docs/scratch/proof with `url.<fake origin>.insteadOf` redirecting it there, and sets the same
# redirect plus an explicit remote.origin.pushurl in the clone, so no fetch or push reaches
# github.com. The clone's raw origin URL stays the GitHub one, as in the owner's checkout.
#
# Refuses a dirty tree: the proof clone holds only what is committed.
#
# usage:
#   scripts/digest-proof-setup.sh
set -euo pipefail

readonly github_url="https://github.com/javatarz/satat.git"
readonly fake_origin_dir="docs/scratch/fake-origin.git"
readonly proof_dir="docs/scratch/proof"

die() {
  echo "error: $*" >&2
  exit 1
}

[[ $# -eq 0 ]] || die "usage: $0"
cd "$(dirname "${BASH_SOURCE[0]}")/.."
[[ -z "$(git status --porcelain --untracked-files=all)" ]] || die "the tree is dirty; commit first"

rm -rf "$proof_dir" "$fake_origin_dir"
git clone -q --bare . "$fake_origin_dir"
git -C "$fake_origin_dir" update-ref refs/heads/main "$(git rev-parse HEAD)"
git -C "$fake_origin_dir" symbolic-ref HEAD refs/heads/main

fake_origin="$(cd "$fake_origin_dir" && pwd -P)"
readonly fake_origin
git -c "url.$fake_origin.insteadOf=$github_url" clone -q "$github_url" "$proof_dir"
git -C "$proof_dir" config "url.$fake_origin.insteadOf" "$github_url"
git -C "$proof_dir" config remote.origin.pushurl "$fake_origin"

echo "proof: $proof_dir"
