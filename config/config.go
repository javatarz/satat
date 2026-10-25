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
	Lanes  []launcher.Lane
	Limits Limits
	// Accounts maps a lane name to the credential account it spends, for the per-account cap.
	// A lane without an entry is its own account.
	Accounts map[string]string
	// LaneMaxRuns is each lane's own cap (lane.max_runs), by lane name.
	LaneMaxRuns map[string]int
}

// Limits are the deploy-time caps the workflow enforces through Hatchet (ADR 0023). Global is the
// whole platform; PerRepo and PerAccount are the defaults for each repo and each account.
type Limits struct {
	Global     int
	PerRepo    int
	PerAccount int
}

type file struct {
	Limits limitsFile          `yaml:"limits"`
	Lanes  map[string]laneFile `yaml:"lanes"`
}

type limitsFile struct {
	Global     int `yaml:"global"`
	PerRepo    int `yaml:"per_repo"`
	PerAccount int `yaml:"per_account"`
}

type laneFile struct {
	Image       string     `yaml:"image"`
	User        string     `yaml:"user"`
	Command     []string   `yaml:"command"`
	PassEnv     []string   `yaml:"pass_env"`
	OptionalEnv []string   `yaml:"optional_env"`
	Memory      string     `yaml:"memory"`
	CPUs        float64    `yaml:"cpus"`
	Pids        int64      `yaml:"pids"`
	Workspace   string     `yaml:"workspace"`
	Internal    bool       `yaml:"internal"`
	Runtime     string     `yaml:"runtime"`
	Timeout     string     `yaml:"timeout"`
	Account     string     `yaml:"account"`
	MaxRuns     int        `yaml:"max_runs"`
	Caches      cachesFile `yaml:"caches"`
}

// cachesFile is a lane's cache list: cache name to the path the step sees it at (ADR 0033).
type cachesFile struct {
	Paths map[string]string `yaml:"paths"`
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
	c := Config{
		Limits:      Limits{Global: f.Limits.Global, PerRepo: f.Limits.PerRepo, PerAccount: f.Limits.PerAccount},
		Accounts:    map[string]string{},
		LaneMaxRuns: map[string]int{},
	}
	var errs []error
	if c.Limits.Global <= 0 || c.Limits.PerRepo <= 0 || c.Limits.PerAccount <= 0 {
		errs = append(errs, errors.New("limits: global, per_repo and per_account are required and must be positive"))
	}
	for _, name := range sortedKeys(f.Lanes) {
		lf := f.Lanes[name]
		l, err := lf.lane(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("lane %q: %w", name, err))
			continue
		}
		if lf.MaxRuns < 0 {
			errs = append(errs, fmt.Errorf("lane %q: max_runs must not be negative", name))
		}
		c.Lanes = append(c.Lanes, l)
		c.Accounts[name] = lf.Account
		if c.Accounts[name] == "" {
			c.Accounts[name] = name
		}
		c.LaneMaxRuns[name] = lf.MaxRuns
		if lf.MaxRuns == 0 {
			c.LaneMaxRuns[name] = 1
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (lf laneFile) lane(name string) (launcher.Lane, error) {
	l := launcher.Lane{
		Name:        name,
		Image:       lf.Image,
		User:        lf.User,
		Command:     lf.Command,
		PassEnv:     lf.PassEnv,
		OptionalEnv: lf.OptionalEnv,
		CPUs:        lf.CPUs,
		Internal:    lf.Internal,
		Runtime:     lf.Runtime,
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
	// Names and paths are checked by the launcher, with the rest of the lane, at docker.New.
	for _, n := range sortedKeys(lf.Caches.Paths) {
		l.Caches = append(l.Caches, launcher.Cache{Name: n, Path: lf.Caches.Paths[n]})
	}
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
