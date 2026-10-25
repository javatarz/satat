# Claude lane image (ADR 0026): the unmodified claude CLI plus the Satat runner, non-root. Language
# toolchains are NOT baked in: mise installs them at run time from the repo's .mise.toml (or the lane
# defaults in /usr/share/satat/mise-defaults.toml) into the /cache volume, so the image stays lean (#120).
# Build from the repo root:  scripts/build-claude-image.sh
ARG GO_IMAGE
ARG NODE_IMAGE
ARG MISE_IMAGE

FROM ${GO_IMAGE} AS runner
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY runner ./runner
COPY cmd/satat-runner ./cmd/satat-runner
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/satat-runner ./cmd/satat-runner

# Digest-pinned official mise image, used only as the source of the mise binary.
FROM ${MISE_IMAGE} AS mise

FROM ${NODE_IMAGE}
ARG CLAUDE_CODE_VERSION
RUN apt-get update \
  && apt-get install -y --no-install-recommends git ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && npm install -g --omit=dev "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
  && npm cache clean --force
COPY --from=mise /usr/local/bin/mise /usr/local/bin/mise
# Lane defaults, applied by the runner only when a repo pins nothing (SATAT_MISE_DEFAULTS). Keep this
# OUT of /etc/mise: mise reads /etc/mise/config.toml on every run, which would install all four
# languages for a repo that pins only one.
COPY images/claude/mise-config.toml /usr/share/satat/mise-defaults.toml
# Managed settings sit above every other settings level and the agent cannot edit them: root-owned,
# on the read-only root filesystem. They keep the #102 rules (subscription login only, and a planted
# ANTHROPIC_API_KEY makes the session refuse to start) and stop the agent skipping the repo's git
# hooks (#174): deny rules plus a root-owned PreToolUse hook that reads the whole Bash command.
RUN install -d -m 0755 -o root -g root /etc/claude-code /usr/local/lib/satat /usr/local/lib/satat/hooks
COPY --chown=root:root --chmod=0644 images/claude/managed-settings.json /etc/claude-code/managed-settings.json
COPY --chown=root:root --chmod=0755 images/claude/hooks/guard-git-hooks.js /usr/local/lib/satat/hooks/guard-git-hooks.js
COPY --from=runner /out/satat-runner /usr/local/bin/satat-runner
# Running a repo's tests leaves bytecode and dependency dirs behind, and an agent that runs
# `git add -A` commits them (the first sandbox-greet run did: __pycache__/*.pyc). Exclude the common
# ones for every repo; the repo's own .gitignore still applies.
COPY images/claude/gitignore /etc/satat/gitignore
RUN git config --system core.excludesFile /etc/satat/gitignore
# Numeric non-root user with no login shell. HOME is /workspace at run time (a tmpfs).
RUN groupadd --gid 10001 satat && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin satat
# Caches and scratch. The root filesystem is read-only at run time, so these paths are NOT writable
# until the launcher mounts a disk volume on /cache. This image must therefore be merged together
# with (after) the launcher cache-mount slice of #120: on its own, mise, go, pip, npm and mvn fail
# with read-only or permission errors. Ownership here only seeds fresh named volumes.
# /cache/tmp is the scratch dir: the /tmp and /workspace tmpfs mounts are noexec, and tools that
# must execute what they write (go run/test, JVM native libs) need an exec-capable TMPDIR.
RUN mkdir -p /cache/mise /cache/mise-cache /cache/mise-state \
    /cache/go-mod /cache/go-build /cache/pip /cache/npm /cache/maven /cache/gradle /cache/corepack /cache/tmp \
  && chown -R 10001:10001 /cache
USER 10001:10001
# Shims first so `go`, `python`, `java`, `node` resolve to the mise-managed (repo-pinned) versions.
ENV MISE_DATA_DIR=/cache/mise \
  MISE_CACHE_DIR=/cache/mise-cache \
  MISE_STATE_DIR=/cache/mise-state \
  MISE_CONFIG_DIR=/etc/mise \
  SATAT_MISE_DEFAULTS=/usr/share/satat/mise-defaults.toml \
  MISE_YES=1 \
  MISE_TRUSTED_CONFIG_PATHS=/workspace \
  PATH=/cache/mise/shims:$PATH \
  GOTOOLCHAIN=local \
  GOMODCACHE=/cache/go-mod \
  GOCACHE=/cache/go-build \
  PIP_CACHE_DIR=/cache/pip \
  npm_config_cache=/cache/npm \
  MAVEN_ARGS=-Dmaven.repo.local=/cache/maven \
  GRADLE_USER_HOME=/cache/gradle \
  COREPACK_HOME=/cache/corepack \
  COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
  TMPDIR=/cache/tmp \
  GOTMPDIR=/cache/tmp \
  JAVA_TOOL_OPTIONS=-Djava.io.tmpdir=/cache/tmp \
  PYTHONDONTWRITEBYTECODE=1
ENV DISABLE_AUTOUPDATER=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
ENTRYPOINT ["/usr/local/bin/satat-runner"]
