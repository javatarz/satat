# ADR 0033: Lane toolchain: a lean image, mise at step start, persistent caches

## Status

Accepted (2026-10-25). Decides #120. Builds on [ADR 0026](0026-docker-first-runtime.md) (lanes,
hardening floor, the disk `/workspace` it anticipates) and [ADR 0027](0027-docker-api-access.md)
(the floor is the access gate). Adds toolchain and package hosts to the egress allowlist that
[ADR 0025](0025-github-token-broker.md)'s proxy will enforce.

## Context

In the first real runs on `javatarz/satat-sandbox` the agent wrote code and tests but could not
run them: `images/claude/Dockerfile` is `node:22-bookworm-slim` plus git, the `claude` CLI and
the runner, with no Python or other toolchain. A draft PR the agent could not test is worth much
less to review.

Repos pin their own versions (`.mise.toml`, `.tool-versions`, `go.mod`, `.nvmrc`,
`.python-version`). A toolchain baked into the image serves one version per language and ignores
those pins.

The current launcher matters here:

- `launcher/docker/build.go` mounts two tmpfs (`/tmp`, and `/workspace` for the clone and `HOME`)
  and one volume, the per-step input volume at `/etc/satat`. The tmpfs options do not include
  `exec`, and Docker adds `noexec` to a tmpfs unless told otherwise (checked on colima with
  `--tmpfs`), so nothing can run from `/tmp` or `/workspace` today.
- `launcher/docker/floor.go` (`ValidateRequest`) allows tmpfs only at `/tmp` and `/workspace`, but
  accepts any volume mount whose source starts with `satat-`, at any target, and does not look at
  `VolumeOptions` (driver config, subpath).
- Volumes are created in `launcher/docker/docker.go` with the default driver and no check of their
  own. `Reap` removes every `satat.managed=true` volume older than one age limit.
- `runner/runner.go` clones with `SATAT_GITHUB_TOKEN` and strips it from `claude`'s environment.

## Decision

**One lean lane image.** The Debian Node base (the `claude` CLI needs Node), git,
ca-certificates, the runner and the `mise` binary. No Go, JDK, Maven or Python in the image.
mise is copied from the official image pinned by digest
(`COPY --from=ghcr.io/jdx/mise@sha256:<digest> /usr/local/bin/mise /usr/local/bin/mise`; the
image and path are documented by mise and the digest resolves through `scripts/image-digest.sh`).
A checksum-verified release download (`SHASUMS256.txt`, signed with minisign and GPG) is the
fallback if the image path changes.

**mise installs toolchains at step start, into a persistent cache.** Each lane step gets a disk
volume at `/cache`, keyed by (lane, repo), with `MISE_DATA_DIR=/cache/mise`,
`MISE_CACHE_DIR=/cache/mise-cache` and `TMPDIR`/`MISE_TMP_DIR` under `/cache/tmp`, because the
tmpfs mounts are `noexec`. A toolchain therefore downloads once per repo, not once per run.

- **Versions** come from the repo's `.mise.toml` or `.tool-versions`. The lane enables mise's
  idiomatic files for go (`go.mod`, `.go-version`), node (`.nvmrc`, `.node-version`), python
  (`.python-version`) and java (`.java-version`), which mise reads only when enabled. A lane-level
  `/usr/share/satat/mise-defaults.toml` in the image holds defaults for repos that pin nothing.
  The runner applies it (`MISE_GLOBAL_CONFIG_FILE`) only when `mise ls --current` finds no pinned
  tool. It is deliberately not `/etc/mise/config.toml`, which mise reads on every run and would
  install all four languages for a repo that pins one. The image also sets `GOTOOLCHAIN=local` so Go
  does not fetch a second toolchain behind mise's back.
- **The runner, not the agent, runs `mise install`** after the clone and before `claude`, then
  puts the installed tools on `claude`'s `PATH`. A failed install fails the step visibly instead of
  becoming "could not run tests" in a PR. mise runs with the same stripped environment as
  `claude`, never the clone token. The runner marks the clone's config trusted (`mise trust`),
  since there is no one to answer a prompt.
- **Build caches** (Go module and build cache, pip, npm, Maven, Gradle, corepack) live on the same
  `/cache` volume, set by env vars to paths outside `/workspace`.

**Cache volumes are persistent and required.** Without them every step downloads every
toolchain. Cache volumes are keyed by (lane, repo), never shared across repos or accounts,
labelled `satat.kind=cache`, and have their own reaper policy (age and size). The step reaper
skips them. `per_repo: 1` in `satat.yaml` (the per-repo cap of ADR 0023) gives one writer per key,
which concurrent Maven writers need.

**The floor narrows to declared mounts.** `ValidateRequest` must refuse a volume mount with a
`DriverConfig` or a `Subpath`, and any volume target other than `/etc/satat`, `/cache`, or
`/workspace` for a disk-backed workspace. Volume creation gets its own check: default driver, no
driver options, Satat name and labels. `node_modules`, venvs and `target/` live in the clone, so a
lane option for a disk-backed `/workspace` is needed alongside `/cache`, as ADR 0026 anticipated.

**Egress.** When the proxy exists, the lane's allowlist must carry toolchain hosts as well as
package registries:

| Purpose | Hosts | Source |
|---|---|---|
| mise version lists and Java catalog | `mise-versions.jdx.dev`, `mise-java.jdx.dev` | mise docs |
| Go | `dl.google.com` | mise docs (`go.download_mirror` default) |
| Node | `nodejs.org` | mise docs (`node.mirror_url` default) |
| Python (python-build-standalone), Temurin JDK, other GitHub releases | `github.com`, `release-assets.githubusercontent.com`, `api.github.com` (attestations) | Temurin URL read from the mise-java catalog and its redirect; Python's GitHub host from the catalog's file names |
| Package registries | `pypi.org`, `files.pythonhosted.org`, `registry.npmjs.org`, `proxy.golang.org`, `sum.golang.org`, `repo.maven.apache.org`, `plugins.gradle.org`, `services.gradle.org` | toolchain defaults |

Allowing `github.com` for release downloads overlaps with ADR 0025's token-injected GitHub
traffic, so the proxy must inject a token only for git and API calls to the step's repo, not for
release downloads.

**Kubernetes.** `/cache` maps to a PersistentVolumeClaim per (lane, repo) later. The worker's RBAC
has no PVC create today (ADR 0031, in review), and creating a per-repo PVC at claim time is
unverified. Until both are settled the Kubernetes launcher uses a disk-backed `emptyDir`, which
works but downloads toolchains on every step.

**Alternatives superseded.** A baked polyglot image (Go, Python, JDK, Maven, Node; the first
version of this ADR) serves one version per language and costs about 330 MB (estimated). Per-language
images multiply lanes and still ignore repo pins. A repo-declared image, digest-pinned and built
outside the run, stays valid as an escape hatch for stacks mise cannot install.

## Consequences

- Repo-pinned versions work, and the image shrinks to roughly today's size plus mise.
- The first step on a repo is slow: it downloads every toolchain the repo needs. Later steps reuse
  `/cache`.
- Steps run binaries fetched at run time, which is a wider supply-chain surface than a baked
  image. Mitigations: a pinned mise version; mise's own checks (Go and Node checked against
  published SHA-256 files, Node's checksum file GPG-verified, Python checked by GitHub attestation,
  Java checked against the catalog's checksum when it has one); `mise.lock` with
  `mise install --locked` for repos that commit one; and the egress allowlist. Repos without a
  lockfile get whatever version their pin resolves to on the day.
- The agent can write to `/cache`, so a step can tamper with a toolchain or cache that a later run
  of the same repo executes. Keying by (lane, repo) keeps that inside one repo. A separate install
  step that mounts `/cache` read-only into the agent is a possible later fix, not decided here.
- A repo's `.mise.toml` can define tasks, env templates and sourced files that run code when mise
  loads it. The step already runs agent-chosen code from that repo, so this adds no new boundary:
  the sandbox floor of ADR 0026 is the boundary, and trusting the clone's config is accepted.
- The registries and GitHub release hosts accept uploads, so an agent steered by issue text can
  install a malicious package or exfiltrate through a publish. ADR 0025 limits what reaches the
  step (no GitHub token; the subscription token is still in env until its proxy exists), and human
  review of the PR stays the control.
- The floor gains mount and volume-create checks, each with a refusal test, and the reaper gains a
  cache policy. Local volumes have no size cap, so that policy is the only brake on disk use.
- The cache reaper reads volume sizes and use counts from `GET /system/df`, so the Docker socket
  proxy enables `SYSTEM`. The owner approved this after checking that the daemon exposes no write
  route under `/system`; ADR 0027 records the evidence and the tripwire test that guards it.
- The reaper and its last-use marker volumes are a Docker-backend detail. They do not carry to
  Kubernetes, where a namespace ResourceQuota and PVC annotations would cover size, count and
  last use. The `caches` lane config, `persist` and the hashed repo scope are backend-neutral.
  A shared in-cluster mirror per ecosystem may suit cluster scale better than per-repo volumes;
  that is a design opinion, not verified.

## Not verified

- First-run time per toolchain, and the speed-up from a warm `/cache`. Record real figures.
- The full host list. `dl.google.com`, `nodejs.org` and the two `jdx.dev` hosts are from mise's
  docs. The GitHub hosts come from one Temurin catalog URL and its redirect, and from
  python-build-standalone file names. `services.gradle.org` and `api.github.com` are expected but
  not observed. Confirm by running `mise install` behind the proxy and logging hosts.
- That the lean image plus mise on Debian bookworm installs all four toolchains as uid 10001 with
  a read-only root.
- `mise trust` and `--locked` behaviour when run non-interactively by the runner.
- Concurrent-writer behaviour of each cache if `per_repo` is raised above 1, or if a re-delivered
  step overlaps the original on one key.
- Disk fill: local volumes have no size cap, and what happens to the Docker VM when one fills.

Revisit when the egress proxy lands, when a common stack is one mise cannot install, or if a
tampered cache is ever found.
