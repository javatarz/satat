# The repo the agent works on, committed so a change can be diffed and Satat can take it as a
# snapshot. The image matches Satat's check-python lane.
FROM python:3.13.16-slim@sha256:cf50a4f6ac4f026458b08d5222023d700ac806410dff5e9ca8728fa44f2b850f
# Claude Code for the baseline agent, at the version in scripts/build-claude-image.sh. Harbor's
# claude-code agent skips its own install (about 3 minutes a trial) when this version is present,
# and installs the right one if they drift. Satat never runs in this sandbox, so it is unaffected.
ARG CLAUDE_CODE_VERSION=2.1.301
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates curl procps nodejs npm \
  && npm install -g --omit=dev "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
  && npm cache clean --force \
  && rm -rf /var/lib/apt/lists/*
# Harbor keeps the sandbox alive with `sh -c "sleep infinity"`, which ignores SIGTERM, so every
# stop waited Docker's 10-second grace period: twice a trial, with the separate verifier. Nothing
# in the sandbox needs a clean shutdown (Harbor copies /app out first), so stop it at once.
STOPSIGNAL SIGKILL
WORKDIR /app
COPY repo/ /app/
RUN git init -q -b main \
  && git -c user.name=Satat -c user.email=satat@localhost add -A \
  && git -c user.name=Satat -c user.email=satat@localhost commit -q -m "Base"
