# The secret store's reconciler (ADR 0032): OpenTofu and the module in deploy/secrets/tofu, with its
# providers, so a run needs no network. Build through Compose; the digests come from compose.yaml
# build args.
ARG TOFU_IMAGE
ARG BASE_IMAGE

# The OpenTofu image is a binary source only: as a base, its ONBUILD step fails the build.
FROM ${TOFU_IMAGE} AS tofu

# The OpenBao image: already pulled for the stack, and it has the CA certificates init needs.
FROM ${BASE_IMAGE}
COPY --from=tofu /usr/local/bin/tofu /usr/local/bin/tofu
WORKDIR /work
COPY deploy/secrets/tofu/main.tf deploy/secrets/tofu/.terraform.lock.hcl ./
USER 0:0
RUN tofu init -input=false -no-color -lockfile=readonly
ENTRYPOINT ["sh", "-c"]
