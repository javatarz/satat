// Package launcher runs one step as one isolated, one-shot workload (ADR 0026).
//
// Workflows and drivers see only this interface. The Docker backend lives in launcher/docker;
// a Kubernetes backend (ADR 0024) is a second implementation that must pass launcher/conformance.
package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"time"
)

// Lane is the fixed shape a step runs in: image, user, limits, network and the secrets it may
// receive. It is the equivalent of a SandboxTemplate. Lanes come from configuration only; no
// workflow input, issue text or agent output can change one (ADR 0027).
type Lane struct {
	Name string
	// Image must be pinned by digest (repo[:tag]@sha256:...) or by its bare image ID (sha256:...).
	Image string
	// User is a numeric uid:gid, never root. Defaults to 10001:10001.
	User string
	// Command replaces the image's command. Empty keeps the image default.
	Command []string
	// PassEnv names environment variables copied from the launcher's own environment into the
	// step (for example CLAUDE_CODE_OAUTH_TOKEN). A name that is unset fails the launch.
	PassEnv []string
	// OptionalEnv is like PassEnv, but a name that is unset or empty is skipped.
	OptionalEnv []string

	MemoryBytes    int64
	CPUs           float64
	PidsLimit      int64
	WorkspaceBytes int64 // size of the tmpfs at /workspace; counts against MemoryBytes

	// Caches are disk-backed directories for build tools' downloads and outputs (ADR 0033). Each
	// step gets its own, empty and removed with the step, unless PersistCaches is set.
	Caches []Cache
	// PersistCaches keeps the caches between steps, one set per (lane, Step.CacheScope), never
	// shared across repos. A step can leave a poisoned artifact or a token in a cache for the
	// next step of the same repo, which is why it is opt-in (ADR 0033, stage 2).
	PersistCaches bool

	// Internal puts the lane on a network with no route out except to containers attached to it.
	Internal bool
	// Egress, when set, gives an Internal lane a way out: a hostname-allowlist proxy attached to
	// the lane's network and to one with a route out (#169). The step gets HTTP_PROXY and
	// HTTPS_PROXY pointing at it and has no other route. Nil means no way out at all.
	Egress *Egress
	// Runtime is "" (engine default) or "runsc" (gVisor) where installed.
	Runtime string

	// Timeout bounds one step. Defaults to 30 minutes.
	Timeout time.Duration
}

// Egress is a lane's proxy: what it runs and the hosts it lets a step reach. It holds no secret
// and does no TLS interception (ADR 0025); it decides on the host name and port alone.
type Egress struct {
	// Image is the proxy image, pinned by digest (images/egress, scripts/build-egress-image.sh).
	Image string
	// Allow lists the hosts a step may reach: "host", "*.suffix" for every sub-domain, each with
	// an optional ":port" (default 443 and 80). Never an address.
	Allow []string
}

// Cache is one cache directory. It is a name and a path, not a backend object: Docker backs it
// with a volume, Kubernetes with an emptyDir or a claim. It does not count against MemoryBytes.
type Cache struct {
	// Name is lower-case letters and digits, optionally joined by single hyphens.
	Name string
	// Path is where the step sees the cache: a clean absolute path under /cache.
	Path string
}

// Step is one unit of work. Its identity names the container, so a re-delivered step finds the
// container the first attempt started instead of starting a second one.
type Step struct {
	Run  string
	Name string
	Lane string
	// Input is non-secret data placed at /etc/satat/input.json. A step writes its results under
	// the directory named by $SATAT_OUTPUT.
	Input []byte
	// Checkout is a tar archive of the repo, cloned by the caller before the step starts so the
	// step needs no credential to get it. It is placed at /etc/satat/repo, owned by the step's
	// user. Entries other than directories, regular files and symlinks, and any path that leaves
	// the archive's root, fail the launch. Nil places nothing. It is read once, and only when the
	// workload is created: a re-delivered step that finds its earlier workload does not read it.
	Checkout io.Reader
	// CacheScope selects the persistent caches a step gets in a lane with PersistCaches: one
	// scope per repo, from CacheScope(repo). It is lower-case letters and digits only, and is
	// required in such a lane and ignored in others.
	CacheScope string
}

// CacheScope is the cache scope for a repo ("owner/name"): a hash, so the repo name never
// reaches a backend flag, and case-folded, because GitHub names are case-insensitive.
func CacheScope(repo string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(repo)))
	return hex.EncodeToString(sum[:16])
}

// Result is what a finished step left behind. A non-zero ExitCode is a result, not an error.
type Result struct {
	ExitCode  int
	OOMKilled bool
	TimedOut  bool
	Cancelled bool
	// Adopted is true when this call found a container an earlier attempt had started.
	Adopted bool

	Stdout, Stderr []byte
	LogsTruncated  bool

	// Files are the regular files the step left in $SATAT_OUTPUT, keyed by path relative to it.
	// They are read after the step exits and before it is removed. Output past the backend's
	// size limit is dropped and FilesTruncated is set.
	Files          map[string][]byte
	FilesTruncated bool
}

// Launcher starts, watches, cancels and cleans up steps.
type Launcher interface {
	// Launch runs the step to completion and removes its workload on every exit path. It is
	// idempotent: concurrent or repeated calls for one (Run, Name) share one workload.
	Launch(ctx context.Context, step Step) (Result, error)
	// Cancel stops a running step. Launch then returns Cancelled. A step that is not running
	// is not an error.
	Cancel(ctx context.Context, run, name string) error
}
