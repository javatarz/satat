package deploy

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ReadKey returns the value of key in the env file at path. A missing file or key reports
// ok=false. Blank lines and # comments are ignored.
func ReadKey(path, key string) (value string, ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, found := strings.Cut(line, "="); found && k == key {
			return v, true, nil
		}
	}
	return "", false, nil
}

// EnsureKey adds key=gen() to the env file unless the key is already there, so an existing value
// is never replaced. Every other line is kept byte for byte. The file is written through a temp
// file and renamed, with mode 0600.
func EnsureKey(path, key string, gen func() (string, error)) (created bool, err error) {
	if _, ok, err := ReadKey(path, key); err != nil || ok {
		return false, err
	}
	value, err := gen()
	if err != nil {
		return false, fmt.Errorf("generating %s: %w", key, err)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	content := string(existing)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += key + "=" + value + "\n"

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	// Set the mode before anything is written; the rename keeps it.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return false, err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	return true, nil
}

// RandomPassword returns 32 random bytes as unpadded base64url.
func RandomPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
