# shellcheck shell=bash
# Source from the root of a proof clone (see scripts/digest-proof-setup.sh) before any proof
# command: it puts the recording fake gh and git first on PATH, so nothing reaches github.com.
# The invalid GH_TOKEN makes a real gh reached by mistake fail to authenticate.
export FAKE_GH_DIR="$PWD/docs/scratch/fake-gh"
mkdir -p "$FAKE_GH_DIR"
export PATH="$PWD/scripts/testdata/fake-bin:$PATH"
export DIGEST_GH="$PWD/scripts/testdata/fake-bin/gh"
export GH_TOKEN=fake-proof-token-not-valid
# The speaker map the proofs use: it assigns the fixture's owners, and leaves Dana Silva out.
export DIGEST_SPEAKERS="$PWD/.claude/skills/meeting-digest/fixtures/speakers.json"
