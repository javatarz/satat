package deploy

// The platform's images, pinned by digest (scripts/image-digest.sh); the tag is kept for readers.
// Both targets use them: compose.yaml is rendered with them, and the Helm chart's values.yaml
// defaults must equal them (deploy/helm checks).
const (
	PostgresImage    = "postgres:15.6@sha256:1ebd963e5c598f944a4e9ba27de4c95289d663dcc73731025aa53c5254094d8f"
	HatchetLiteImage = "ghcr.io/hatchet-dev/hatchet/hatchet-lite:v0.110.5@sha256:cc81f078a5285d7d2051ec4a4fa459d033e1face4ca5983d82612b00da6e3fa3"
	// GoImage and BaseImage are the worker's build and runtime bases (images/worker/Dockerfile).
	GoImage   = "golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61"
	BaseImage = "debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8"
	// OpenBaoImage is the secret store (ADR 0032). Compose only so far.
	OpenBaoImage = "ghcr.io/openbao/openbao:2.7.1@sha256:6d2b93856e3fcf7b18ad855a0b51eaba474dc8b79cf554379ea32034797d2acf"
	// OpenTofuImage is the source of the tofu binary in the secrets-apply image (images/secrets-apply).
	OpenTofuImage = "ghcr.io/opentofu/opentofu:1.13.3-minimal@sha256:f5f36f31d9df0adaf6330b120458de139159af33dcf029b5d0a7f048b20f664a"
)
