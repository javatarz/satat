// Package compose is the deploy target for Docker Compose on one machine (ADR 0026).
package compose

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"text/template"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/deploy"
)

//go:embed compose.yaml.tmpl
var composeTmpl string

var tmpl = template.Must(template.New("compose.yaml").Parse(composeTmpl))

// Runner runs an external command. Up, Down and Status use it (added with those methods).
type Runner interface {
	Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

// Target deploys with Docker Compose in Dir.
type Target struct {
	Dir string
	Run Runner
	Out io.Writer
}

var _ deploy.Target = (*Target)(nil)

// Render returns compose.yaml. Only the worker's secret environment varies with cfg.
func (t *Target) Render(cfg config.Config) (map[string][]byte, error) {
	var buf bytes.Buffer
	data := struct{ WorkerEnv []string }{WorkerEnv: cfg.SecretNames()}
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render compose.yaml: %w", err)
	}
	return map[string][]byte{"compose.yaml": buf.Bytes()}, nil
}

func (t *Target) Up(context.Context, config.Config, deploy.Secrets) error {
	return errors.ErrUnsupported
}

func (t *Target) Down(context.Context, bool) error { return errors.ErrUnsupported }

func (t *Target) Status(context.Context) (deploy.Status, error) {
	return deploy.Status{}, errors.ErrUnsupported
}
