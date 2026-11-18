// Package secrets reads a component's secrets from the secret store (ADR 0032): it logs in, reads
// the named entries of the satat/ mount into memory, and returns them. Errors name the entry, never
// a value.
//
// A component reads its secrets once, at start. Nothing renews the store token, so to rotate a
// value, load the new one and restart the component.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/openbao/openbao/api/v2"
)

// Mount is the KV v2 mount that holds every entry.
const Mount = "satat"

// AppRole logs in with the role_id and secret_id files in Dir, which secrets-init writes into the
// component's credential volume.
type AppRole struct{ Dir string }

// Login implements api.AuthMethod.
func (a AppRole) Login(ctx context.Context, c *api.Client) (*api.Secret, error) {
	role, err := os.ReadFile(filepath.Join(a.Dir, "role_id"))
	if err != nil {
		return nil, err
	}
	secret, err := os.ReadFile(filepath.Join(a.Dir, "secret_id"))
	if err != nil {
		return nil, err
	}
	return c.Logical().WriteWithContext(ctx, "auth/approle/login", map[string]any{
		"role_id":   strings.TrimSpace(string(role)),
		"secret_id": strings.TrimSpace(string(secret)),
	})
}

// Read logs in to the store at addr with login and returns the value of each entry, by entry name.
// It fails on the first entry it cannot read.
func Read(ctx context.Context, addr string, login api.AuthMethod, entries []string) (map[string]string, error) {
	cfg := api.DefaultConfig()
	cfg.Address = addr
	c, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := c.Auth().Login(ctx, login); err != nil {
		return nil, fmt.Errorf("logging in to the secret store at %s: %w", addr, err)
	}
	kv := c.KVv2(Mount)
	out := make(map[string]string, len(entries))
	for _, name := range entries {
		s, err := kv.Get(ctx, name)
		var re *api.ResponseError
		switch {
		case errors.As(err, &re) && re.StatusCode == http.StatusForbidden:
			return nil, fmt.Errorf("the secret store denies entry %s (grant it: just secrets-grant <component> %s)", name, name)
		case errors.Is(err, api.ErrSecretNotFound):
			return nil, fmt.Errorf("entry %s is not in the secret store (run: just secrets-load)", name)
		case err != nil:
			return nil, fmt.Errorf("reading entry %s: %w", name, err)
		}
		v, _ := s.Data["value"].(string)
		if v == "" {
			return nil, fmt.Errorf("entry %s has no value (run: just secrets-load)", name)
		}
		out[name] = v
	}
	return out, nil
}
