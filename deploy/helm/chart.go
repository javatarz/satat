// Package helm embeds the Satat Helm chart, which the Kubernetes deploy target installs. The chart
// holds the platform (Postgres, hatchet-lite, the worker and its ServiceAccount and config); the
// lane objects and every Secret are applied by the target itself.
package helm

import (
	"embed"
	"io/fs"
	"os"
)

// Chart is the chart directory, satat/, with its templates.
//
//go:embed all:satat
var Chart embed.FS

// WriteTo writes the chart into dir, which becomes the chart's root (dir/Chart.yaml). Files that
// exist in dir are an error, so dir should be new.
func WriteTo(dir string) error {
	sub, err := fs.Sub(Chart, "satat")
	if err != nil {
		return err
	}
	return os.CopyFS(dir, sub)
}
