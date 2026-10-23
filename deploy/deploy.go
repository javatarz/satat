// Package deploy defines how Satat is brought up on a place to run it. A Target renders the
// artifacts for that place from satat.yaml and drives them; Compose on a laptop is the first.
package deploy

import (
	"context"

	"github.com/javatarz/satat/config"
)

// Secrets maps a secret name to its resolved value. Values live in memory and in the child
// process environment only; they are never written to disk.
type Secrets map[string]string

// ServiceStatus is the state of one service of the platform.
type ServiceStatus struct {
	Name    string
	State   string
	Healthy bool
	Detail  string
}

// Status is the state of the whole platform.
type Status struct {
	Services []ServiceStatus
}

// Target is one place Satat can run.
type Target interface {
	// Render returns the checked-in artifacts for cfg, keyed by file name.
	Render(cfg config.Config) (map[string][]byte, error)
	// Up brings the platform up, idempotently.
	Up(ctx context.Context, cfg config.Config, secrets Secrets) error
	// Down stops the platform; removeData also deletes its volumes.
	Down(ctx context.Context, removeData bool) error
	// Status reports each service.
	Status(ctx context.Context) (Status, error)
}
