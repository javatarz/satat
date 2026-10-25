# ADR 0033: Lane toolchain: one polyglot image, build caches on disk volumes

## Status

Accepted (2026-10-25). Decides #120. Builds on [ADR 0026](0026-docker-first-runtime.md) (lanes,
hardening floor, the disk `/workspace` it anticipates) and [ADR 0027](0027-docker-api-access.md)
(the floor is the access gate). Adds package registries to the egress allowlist that
[ADR 0025](0025-github-token-broker.md)'s proxy will enforce.

## Context

In the first real runs on `javatarz/satat-sandbox` the agent wrote code and tests but could not
run them: `images/claude/Dockerfile` is `node:22-bookworm-slim` plus git, the `claude` CLI and
the runner, with no Python or other toolchain. A draft PR the agent could not test is worth much
less to review.

The issue listed three options: one image per language, a base image plus language layers, or a
repo-declared image. The owner's repos are mostly Go, Python, Java and TypeScript, and one image
per language means one lane per language for the same agent.

Dependencies are the second half. The step's network today is a plain bridge with no allowlist.
Once the egress proxy exists it allows only listed hosts, so `pip install` or `go mod download`
needs either an allowed registry or a warm cache. Caches are also where a step can leave
something for a later step, which makes them a cross-run trust question.

The current launcher matters here:

- `launcher/docker/build.go` mounts two tmpfs (`/tmp`, `/workspace`, the clone and `HOME`) and one
  volume, the per-step input volume at `/etc/satat`.
- `launcher/docker/floor.go` (`ValidateRequest`) allows tmpfs only at `/tmp` and `/workspace`, but
  accepts any volume mount whose source starts with `satat-`, at any target, and does not look at
  `VolumeOptions` (driver config, subpath).
- Volumes are created in `launcher/docker/docker.go` with the default driver and no check of their
  own. `Reap` removes every `satat.managed=true` volume older than one age limit.

## Decision

**One polyglot `claude` lane image.** It adds to the current image:

- Go, copied from the digest-pinned `golang` image the build already uses for the runner.
- Python 3 with a `python` alias and `venv`, from the Debian base's packages.
- Java: JDK 21 and Maven, copied from digest-pinned official images. Gradle comes from each
  repo's wrapper, not the image.
- Node and TypeScript from the existing Node base, with corepack enabled at build time so
  `pnpm` and `yarn` resolve without a run-time install.

Every source image is pinned by digest, as the base is today. The estimated growth is about
330 MB compressed. That is an estimate, not a measurement. Stacks outside these four get an extra
lane whose image the repo declares, pinned by digest and built outside the run, never inside a step.

**Build caches live on disk volumes, never on the `/workspace` tmpfs.** One cache path each for
Go (module and build cache), pip, npm, Maven, Gradle and corepack, set by env vars to paths
outside `/workspace`. Rollout in two stages:

1. A per-step disk volume for the caches, created and removed with the step. No cross-run risk,
   and it takes the cache's size off the memory limit.
2. Persistent read-write cache volumes keyed by (lane, repo), never shared across repos or
   accounts, labelled `satat.kind=cache`, with their own reaper policy (age and size), separate
   from the step reaper.

Stage 2 is held back for three reasons. Go, Maven and pip do not re-verify cached artifacts on
reuse, so a step that writes a bad artifact poisons later runs of that repo. A token in the step's
env can be written into a cache file and outlive the step. Concurrent Maven writers to one local
repository are unsafe, and `per_repo: 1` in `satat.yaml` (the per-repo cap of ADR 0023) is
what keeps it to one writer per key.

**The floor narrows to declared mounts.** `ValidateRequest` must refuse a volume mount with a
`DriverConfig` or a `Subpath`, and any volume target other than `/etc/satat`, `/workspace`
(for a disk-backed workspace) or a cache path the lane declares. Volume creation gets its own
check: default driver, no driver options, Satat name and labels. `node_modules`, venvs and
`target/` live in the clone, so a lane option for a disk-backed `/workspace` is needed alongside
the caches, as ADR 0026 anticipated. The step reaper must skip `satat.kind=cache` volumes.

**Egress.** When the proxy exists, each lane's allowlist adds the registries its toolchains need:
`pypi.org`, `files.pythonhosted.org`, `registry.npmjs.org`, `proxy.golang.org`,
`sum.golang.org`, `repo.maven.apache.org`, `plugins.gradle.org`. The residual risk is stated, not
solved: an agent steered by issue text can install a malicious package, and these hosts accept
uploads, so a step with a token in reach can exfiltrate it through a publish. ADR 0025 limits what
reaches the step (no GitHub token; the subscription token is still in env until its proxy exists),
and human review of the PR stays the control for what the agent installs.

**Kubernetes.** Caches map to PersistentVolumeClaims later. The worker's RBAC has no PVC create
today (ADR 0031, in review), and creating a per-repo PVC at claim time is unverified. Until both
are settled the Kubernetes launcher uses a disk-backed `emptyDir` for caches and workspace, which
is stage 1.

## Consequences

- The agent can run `go test`, `python -m unittest`, `mvn test`, `./gradlew test` and
  `npm test` in the lane, which is #120's acceptance. Builds still need the registry or a warm
  cache, so without the proxy allowlist a step's first dependency install depends on the open
  bridge.
- One bigger image to rebuild and re-pin when any of five toolchains moves. A CVE in any of them
  is a CVE in the lane.
- Disk-backed caches and workspace stop counting against `memory`, so the `workspace` limit in
  `satat.yaml` stops being the only brake on clone size. Local volumes have no size cap, so the
  cache reaper's size policy is the only one.
- The floor gains mount and volume-create checks, each with a refusal test, before stage 1 ships.
- Repos on other stacks need a second image and a lane. That is accepted until a stack is common
  enough to join the polyglot image.

## Not verified

- The real image size and pull time. 330 MB is an estimate.
- Python 3.11 (Debian bookworm, the current base) or 3.13. Recommended: move the base to a
  `trixie` Node image to get 3.13; not yet tried with the `claude` CLI.
- How much cache reuse speeds up a step, per language.
- Concurrent-writer behaviour of each cache if `per_repo` is raised above 1, or if a re-delivered
  step overlaps the original on one key.
- Disk fill: local volumes have no size cap, and what happens to the Docker VM when one fills.

Revisit when a stack outside the four is common in Satat's repos, when the egress proxy lands, or
when stage 2 caches are switched on.
