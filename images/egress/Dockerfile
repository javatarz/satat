# The lane egress proxy (#169, ADR 0026): one static Go binary on an empty image, so there is no
# shell, no package manager and no CA bundle to attack. It tunnels and does no TLS.
# Build from the repo root:  scripts/build-egress-image.sh
ARG GO_IMAGE

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY egress ./egress
COPY cmd/satat-egress ./cmd/satat-egress
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/satat-egress ./cmd/satat-egress

FROM scratch
COPY --from=build /out/satat-egress /satat-egress
USER 10003:10003
EXPOSE 3128
ENTRYPOINT ["/satat-egress"]
