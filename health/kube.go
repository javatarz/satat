package health

import (
	"context"
	"fmt"
)

// KubeAPI checks that the Kubernetes API answers. The worker cannot launch a step without it.
func KubeAPI(ping func(ctx context.Context) error) Check {
	return Check{Name: "kubernetes", Run: func(ctx context.Context) error {
		if err := ping(ctx); err != nil {
			return fmt.Errorf("kubernetes api: %w", err)
		}
		return nil
	}}
}
