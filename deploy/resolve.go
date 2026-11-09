package deploy

import (
	"errors"
	"fmt"
)

// FromEnv takes each named secret's value from the caller's environment with getenv, under the
// secret's own name. The kubernetes target uses it until it has a secret store. Errors name the
// secret and never carry a value.
func FromEnv(names []string, getenv func(string) (string, bool)) (Secrets, error) {
	out := make(Secrets, len(names))
	var errs []error
	for _, name := range names {
		v, ok := getenv(name)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("%s: environment variable is not set", name))
		case v == "":
			errs = append(errs, fmt.Errorf("%s: environment variable is empty", name))
		default:
			out[name] = v
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}
