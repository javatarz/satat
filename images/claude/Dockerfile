# Claude lane image (ADR 0026): the unmodified claude CLI plus the Satat runner, non-root.
# Build from the repo root:  scripts/build-claude-image.sh
ARG GO_IMAGE
ARG NODE_IMAGE

FROM ${GO_IMAGE} AS runner
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY runner ./runner
COPY cmd/satat-runner ./cmd/satat-runner
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/satat-runner ./cmd/satat-runner

FROM ${NODE_IMAGE}
ARG CLAUDE_CODE_VERSION
RUN apt-get update \
  && apt-get install -y --no-install-recommends git ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && npm install -g --omit=dev "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
  && npm cache clean --force
# Managed settings are the second line of defence for #102: subscription login only, and a
# planted ANTHROPIC_API_KEY makes the session refuse to start.
RUN mkdir -p /etc/claude-code \
  && echo '{"forceLoginMethod":"claudeai","allowedProviders":["anthropic"]}' > /etc/claude-code/managed-settings.json
COPY --from=runner /out/satat-runner /usr/local/bin/satat-runner
# Numeric non-root user with no login shell. HOME is /workspace at run time (a tmpfs).
RUN groupadd --gid 10001 satat && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin satat
USER 10001:10001
ENV DISABLE_AUTOUPDATER=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
ENTRYPOINT ["/usr/local/bin/satat-runner"]
