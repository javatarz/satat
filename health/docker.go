package health

import (
	"context"
	"fmt"
)

// DockerPing checks that the Docker API answers. The worker cannot launch a step without it.
func DockerPing(ping func(ctx context.Context) error) Check {
	return Check{Name: "docker", Run: func(ctx context.Context) error {
		if err := ping(ctx); err != nil {
			return fmt.Errorf("docker ping: %w", err)
		}
		return nil
	}}
}
