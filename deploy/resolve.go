package deploy

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"

	"github.com/javatarz/satat/config"
)

// OpError is the failure of `op read`. It carries the exit status only, never the command's
// output, because that output could contain the secret.
type OpError struct{ Status string }

func (e *OpError) Error() string { return "op read failed: " + e.Status }

// Resolve turns secret references into values. A ref equal to config.SecretFromEnv is looked up
// in the shell with getenv, under the secret's own name; any other ref is passed to read (an
// op:// reference). Errors name the secret and never carry a value or the reader's message.
func Resolve(ctx context.Context, refs map[string]string,
	read func(ctx context.Context, ref string) (string, error),
	getenv func(string) (string, bool),
) (Secrets, error) {
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make(Secrets, len(refs))
	var errs []error
	for _, name := range names {
		ref := refs[name]
		var value string
		if ref == config.SecretFromEnv {
			v, ok := getenv(name)
			if !ok {
				errs = append(errs, fmt.Errorf("%s: environment variable is not set", name))
				continue
			}
			value = v
		} else {
			v, err := read(ctx, ref)
			if err != nil {
				// Drop err's message: a reader's error text might contain the value.
				var opErr *OpError
				if errors.As(err, &opErr) {
					errs = append(errs, fmt.Errorf("%s: %s", name, opErr.Error()))
				} else {
					errs = append(errs, fmt.Errorf("%s: reading the value failed", name))
				}
				continue
			}
			value = v
		}
		if value == "" {
			errs = append(errs, fmt.Errorf("%s: resolved to an empty value", name))
			continue
		}
		out[name] = value
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// OpRead reads one value with the 1Password CLI. Stderr is discarded and never reported.
func OpRead(ctx context.Context, ref string) (string, error) {
	out, err := exec.CommandContext(ctx, "op", "read", "--no-newline", ref).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", &OpError{Status: exitErr.ProcessState.String()}
		}
		// Not started (for example op is not installed): the message holds no value.
		return "", fmt.Errorf("running op: %w", err)
	}
	return string(out), nil
}
