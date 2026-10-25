# Claude lane image (ADR 0026): the unmodified claude CLI plus the Satat runner, non-root, with a
# polyglot toolchain (Go, JDK 21 + Maven, Python, Node) so the agent can run a repo's own tests (#120).
# Build from the repo root:  scripts/build-claude-image.sh
ARG GO_IMAGE
ARG NODE_IMAGE
ARG JDK_IMAGE
ARG MAVEN_IMAGE

FROM ${GO_IMAGE} AS runner
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY runner ./runner
COPY cmd/satat-runner ./cmd/satat-runner
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/satat-runner ./cmd/satat-runner

# Source stage for /usr/share/maven. Copied rather than apt-installed: Debian bookworm ships an old
# Maven (3.8.x) that drags in its own default JDK, and a digest-pinned upstream tree matches how Go
# and the JDK are pinned. Maven is pure Java, so it runs on the copied JDK.
FROM ${MAVEN_IMAGE} AS maven
FROM ${JDK_IMAGE} AS jdk

FROM ${NODE_IMAGE}
ARG CLAUDE_CODE_VERSION
RUN apt-get update \
  && apt-get install -y --no-install-recommends git ca-certificates \
    python3 python3-venv python3-pip python-is-python3 \
  && rm -rf /var/lib/apt/lists/* \
  && npm install -g --omit=dev "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
  && npm cache clean --force
# Go from the pinned golang image (the runner build stage). GOTOOLCHAIN=local (set below) stops go
# from trying to download a newer toolchain, which the network-less lane could not do anyway.
COPY --from=runner /usr/local/go /usr/local/go
# JDK 21 (Temurin jammy build: its glibc is older than bookworm's, so it runs here) and Maven.
COPY --from=jdk /opt/java/openjdk /opt/java/openjdk
COPY --from=maven /usr/share/maven /usr/share/maven
RUN ln -s /usr/share/maven/bin/mvn /usr/local/bin/mvn
# corepack ships with Node; enabling it puts yarn/pnpm shims on PATH. The shims download the pinned
# package manager on first use into COREPACK_HOME, so never prompt for it.
RUN corepack enable
# Managed settings are the second line of defence for #102: subscription login only, and a
# planted ANTHROPIC_API_KEY makes the session refuse to start.
RUN mkdir -p /etc/claude-code \
  && echo '{"forceLoginMethod":"claudeai","allowedProviders":["anthropic"]}' > /etc/claude-code/managed-settings.json
COPY --from=runner /out/satat-runner /usr/local/bin/satat-runner
# Numeric non-root user with no login shell. HOME is /workspace at run time (a tmpfs).
RUN groupadd --gid 10001 satat && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin satat
# Package-manager caches. The root filesystem is read-only at run time, so these paths are NOT
# writable until the launcher mounts a disk volume on /cache. This image must therefore be merged
# together with the launcher cache-mount slice of #120: on its own, go/pip/npm/mvn/corepack fail
# with read-only or permission errors. Ownership here only seeds fresh named volumes.
RUN mkdir -p /cache/go-mod /cache/go-build /cache/pip /cache/npm /cache/maven /cache/gradle /cache/corepack \
  && chown -R 10001:10001 /cache
USER 10001:10001
ENV JAVA_HOME=/opt/java/openjdk \
  PATH=/usr/local/go/bin:/opt/java/openjdk/bin:$PATH \
  GOTOOLCHAIN=local \
  GOMODCACHE=/cache/go-mod \
  GOCACHE=/cache/go-build \
  PIP_CACHE_DIR=/cache/pip \
  npm_config_cache=/cache/npm \
  MAVEN_ARGS=-Dmaven.repo.local=/cache/maven \
  GRADLE_USER_HOME=/cache/gradle \
  COREPACK_HOME=/cache/corepack \
  COREPACK_ENABLE_DOWNLOAD_PROMPT=0
ENV DISABLE_AUTOUPDATER=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
ENTRYPOINT ["/usr/local/bin/satat-runner"]
