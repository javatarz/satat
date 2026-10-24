// Package launcher runs one step as one isolated, one-shot workload (ADR 0026).
//
// Workflows and drivers see only this interface. The Docker backend lives in launcher/docker;
// a Kubernetes backend (ADR 0024) is a second implementation that must pass launcher/conformance.
package launcher

import (
	"context"
	"time"
)

// Lane is the fixed shape a step runs in: image, user, limits, network and the secrets it may
// receive. It is the equivalent of a SandboxTemplate. Lanes come from configuration only; no
// workflow input, issue text or agent output can change one (ADR 0027).
type Lane struct {
	Name string
	// Image must be pinned by digest (repo[:tag]@sha256:...).
	Image string
	// User is a numeric uid:gid, never root. Defaults to 10001:10001.
	User string
	// Command replaces the image's entrypoint (and its default arguments) on every backend. Empty
	// keeps the image defaults.
	Command []string
	// PassEnv names environment variables copied from the launcher's own environment into the
	// step (for example CLAUDE_CODE_OAUTH_TOKEN). A name that is unset fails the launch.
	PassEnv []string

	MemoryBytes    int64
	CPUs           float64
	PidsLimit      int64
	WorkspaceBytes int64 // size of the tmpfs at /workspace; counts against MemoryBytes

	// Internal puts the lane on a network with no route out except to containers attached to it.
	Internal bool
	// Runtime is "" (engine default) or "runsc" (gVisor) where installed.
	Runtime string

	// Timeout bounds one step. Defaults to 30 minutes.
	Timeout time.Duration
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
