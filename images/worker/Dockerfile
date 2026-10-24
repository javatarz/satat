# The Satat worker (ADR 0027): the one container with Docker API access (through the socket proxy)
# and the GitHub write token. Build through Compose; the digests come from compose.yaml build args.
ARG GO_IMAGE
ARG BASE_IMAGE

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Everything the module's Go packages need, so a new import cannot break the build (see
# Dockerfile.dockerignore for what is let through).
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/satat-worker ./cmd/satat-worker
# The step pod's wrapper: static, so it runs from any lane image.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/satat-entry ./cmd/satat-entry
# The pusher refuses to publish unscanned changes, so the scanner ships with it.
RUN CGO_ENABLED=0 GOBIN=/out go install -trimpath github.com/zricethezav/gitleaks/v8@v8.30.1

FROM ${BASE_IMAGE}
RUN apt-get update \
  && apt-get install -y --no-install-recommends git ca-certificates \
  && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/satat-worker /out/satat-entry /out/gitleaks /usr/local/bin/
RUN groupadd --gid 10002 satat && useradd --uid 10002 --gid 10002 --no-create-home --shell /usr/sbin/nologin satat
USER 10002:10002
ENTRYPOINT ["/usr/local/bin/satat-worker"]
