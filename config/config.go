// Package config loads satat.yaml into launcher lanes.
//
// Only lanes exist so far. The full spec, and the workflow interpreter that will read it, are
// #108. Lanes are the one part the launcher needs, so they are the one part loaded here.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/javatarz/satat/launcher"
)

// Config is the parsed satat.yaml.
type Config struct {
	Lanes []launcher.Lane
}

type file struct {
	Lanes map[string]laneFile `yaml:"lanes"`
}

type laneFile struct {
	Image     string   `yaml:"image"`
	User      string   `yaml:"user"`
	Command   []string `yaml:"command"`
	PassEnv   []string `yaml:"pass_env"`
	Memory    string   `yaml:"memory"`
	CPUs      float64  `yaml:"cpus"`
	Pids      int64    `yaml:"pids"`
	Workspace string   `yaml:"workspace"`
	Internal  bool     `yaml:"internal"`
	Runtime   string   `yaml:"runtime"`
	Timeout   string   `yaml:"timeout"`
}

// Load reads and parses the file at path.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	c, err := Parse(bytes.NewReader(b))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Parse reads satat.yaml. Unknown keys are errors, so a misspelt limit cannot silently become a
// default. Everything it returns still has to pass the launcher's own checks at docker.New.
func Parse(r io.Reader) (Config, error) {
	var f file
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return Config{}, errors.New("empty file")
		}
		return Config{}, err
	}
	if len(f.Lanes) == 0 {
		return Config{}, errors.New("no lanes defined")
	}
	var c Config
	var errs []error
	for _, name := range sortedKeys(f.Lanes) {
		l, err := f.Lanes[name].lane(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("lane %q: %w", name, err))
			continue
		}
		c.Lanes = append(c.Lanes, l)
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (lf laneFile) lane(name string) (launcher.Lane, error) {
	l := launcher.Lane{
		Name:     name,
		Image:    lf.Image,
		User:     lf.User,
		Command:  lf.Command,
		PassEnv:  lf.PassEnv,
		CPUs:     lf.CPUs,
		Internal: lf.Internal,
		Runtime:  lf.Runtime,
	}
	var errs []error
	var err error
	if l.MemoryBytes, err = parseSize(lf.Memory); err != nil {
		errs = append(errs, fmt.Errorf("memory: %w", err))
	}
	if l.WorkspaceBytes, err = parseSize(lf.Workspace); err != nil {
		errs = append(errs, fmt.Errorf("workspace: %w", err))
	}
	l.PidsLimit = lf.Pids
	if lf.Timeout != "" {
		if l.Timeout, err = time.ParseDuration(lf.Timeout); err != nil || l.Timeout <= 0 {
			errs = append(errs, fmt.Errorf("timeout %q is not a positive duration", lf.Timeout))
		}
	}
	return l, errors.Join(errs...)
}

var sizeRE = regexp.MustCompile(`^([0-9]+)\s*(KiB|MiB|GiB)$`)

// parseSize accepts whole numbers with a binary unit ("512MiB", "2GiB"). A bare number or a
// decimal unit is refused: they are the usual way a limit ends up a thousand times off.
func parseSize(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("required")
	}
	m := sizeRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("%q is not a size like 512MiB or 2GiB", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a positive size", s)
	}
	unit := map[string]int64{"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30}[m[2]]
	if n > (1<<62)/unit {
		return 0, fmt.Errorf("%q is too large", s)
	}
	return n * unit, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
