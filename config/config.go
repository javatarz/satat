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
	// CacheReaper bounds the persistent caches of every lane together.
	CacheReaper CacheReaper
	// Poller says which repos the worker polls for ready-for-agent issues (#135).
	Poller Poller
	// Deploy says where Satat is deployed and where each secret comes from.
	Deploy Deploy
}

// Deploy is the optional deploy section. Secrets is nil when the section is absent, so a caller
// that needs the map can tell "not configured" from "configured empty".
type Deploy struct {
	Target string
	// Secrets maps a secret name to where its value comes from: SecretFromEnv, or an op://
	// reference. Values are references, never secrets.
	Secrets map[string]string
}

// SecretFromEnv says a secret's value is already in the caller's environment.
const SecretFromEnv = "env"

// WorkerSecrets are the secrets the worker itself uses, beyond what lanes pass to steps: the push
// token, and the read token it checks issues and clones repos with (no step holds either).
var WorkerSecrets = []string{"SATAT_GITHUB_TOKEN", "SATAT_PUSH_TOKEN"}

// SecretNames is every secret the platform needs, sorted and unique: each lane's pass_env plus
// the worker's own.
func (c Config) SecretNames() []string {
	seen := map[string]bool{}
	for _, l := range c.Lanes {
		for _, n := range l.PassEnv {
			seen[n] = true
		}
	}
	for _, n := range WorkerSecrets {
		seen[n] = true
	}
	return sortedKeys(seen)
}

// Poller is the worker's issue poll. With no repos it is off.
type Poller struct {
	// Cron is a five-field cron expression.
	Cron  string
	Repos []PollRepo
}

// PollRepo is one repo to poll: issues labelled ready-for-agent are queued onto Lane, and the
// agent starts from Base.
type PollRepo struct {
	Name string
	Base string
	Lane string
}

// DefaultPollCron is every five minutes.
const DefaultPollCron = "*/5 * * * *"

// CacheReaper is the cache reaper's policy (ADR 0033): a persistent cache unused for longer than
// MaxUnused is evicted, and over MaxBytes in all, the least recently used go first.
type CacheReaper struct {
	MaxUnused time.Duration
	MaxBytes  int64
}

const (
	defaultCacheMaxUnused = 14 * 24 * time.Hour
	defaultCacheMaxBytes  = 20 << 30
)

// Limits are the deploy-time caps the workflow enforces through Hatchet (ADR 0023). Global is the
// whole platform; PerRepo and PerAccount are the defaults for each repo and each account.
type Limits struct {
	Global     int
	PerRepo    int
	PerAccount int
}

type file struct {
	Limits      limitsFile          `yaml:"limits"`
	CacheReaper cacheReaperFile     `yaml:"cache_reaper"`
	Poller      pollerFile          `yaml:"poller"`
	Lanes       map[string]laneFile `yaml:"lanes"`
	Deploy      *deployFile         `yaml:"deploy"`
}

type pollerFile struct {
	Cron  string         `yaml:"cron"`
	Repos []pollRepoFile `yaml:"repos"`
}

type pollRepoFile struct {
	Name string `yaml:"name"`
	Base string `yaml:"base"`
	Lane string `yaml:"lane"`
}

type cacheReaperFile struct {
	MaxUnused string `yaml:"max_unused"`
	MaxSize   string `yaml:"max_size"`
}

type deployFile struct {
	Target  string            `yaml:"target"`
	Secrets map[string]string `yaml:"secrets"`
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
	// Persist keeps the caches between steps, one set per (lane, repo). Off by default.
	Persist bool              `yaml:"persist"`
	Paths   map[string]string `yaml:"paths"`
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
		Deploy:      Deploy{Target: "compose"},
	}
	var errs []error
	if c.Limits.Global <= 0 || c.Limits.PerRepo <= 0 || c.Limits.PerAccount <= 0 {
		errs = append(errs, errors.New("limits: global, per_repo and per_account are required and must be positive"))
	}
	var err error
	if c.CacheReaper, err = f.CacheReaper.parse(); err != nil {
		errs = append(errs, fmt.Errorf("cache_reaper: %w", err))
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
	var perr error
	if c.Poller, perr = f.Poller.parse(c.Accounts); perr != nil {
		errs = append(errs, fmt.Errorf("poller: %w", perr))
	}
	if f.Deploy != nil {
		c.Deploy.Target = f.Deploy.Target
		c.Deploy.Secrets = f.Deploy.Secrets
		errs = append(errs, c.checkDeploy()...)
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

var (
	pollRepoRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
	pollBranchRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

// parse checks the repos against the lanes (the keys of lanes) and fills the defaults: cron every
// five minutes, base main, lane claude.
func (pf pollerFile) parse(lanes map[string]string) (Poller, error) {
	p := Poller{Cron: pf.Cron}
	if p.Cron == "" {
		p.Cron = DefaultPollCron
	}
	var errs []error
	if len(strings.Fields(p.Cron)) != 5 {
		errs = append(errs, fmt.Errorf("cron %q is not a five-field cron expression", p.Cron))
	}
	seen := map[string]bool{}
	for i, rf := range pf.Repos {
		r := PollRepo{Name: rf.Name, Base: rf.Base, Lane: rf.Lane}
		if r.Base == "" {
			r.Base = "main"
		}
		if r.Lane == "" {
			r.Lane = "claude"
		}
		switch {
		case !pollRepoRE.MatchString(r.Name) || strings.Contains(r.Name, ".."):
			errs = append(errs, fmt.Errorf("repos[%d]: name %q is not owner/name", i, r.Name))
			continue
		case seen[r.Name]:
			errs = append(errs, fmt.Errorf("repos[%d]: %s is listed twice", i, r.Name))
			continue
		case !pollBranchRE.MatchString(r.Base) || strings.Contains(r.Base, ".."):
			errs = append(errs, fmt.Errorf("repos[%d]: base %q is not a plain branch name", i, r.Base))
			continue
		}
		if _, ok := lanes[r.Lane]; !ok {
			errs = append(errs, fmt.Errorf("repos[%d]: unknown lane %q", i, r.Lane))
			continue
		}
		seen[r.Name] = true
		p.Repos = append(p.Repos, r)
	}
	return p, errors.Join(errs...)
}

// parse fills the defaults, 14 days and 20 GiB, for what the file leaves out.
func (cf cacheReaperFile) parse() (CacheReaper, error) {
	r := CacheReaper{MaxUnused: defaultCacheMaxUnused, MaxBytes: defaultCacheMaxBytes}
	var errs []error
	if cf.MaxUnused != "" {
		d, err := time.ParseDuration(cf.MaxUnused)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("max_unused %q is not a positive duration", cf.MaxUnused))
		}
		r.MaxUnused = d
	}
	if cf.MaxSize != "" {
		n, err := parseSize(cf.MaxSize)
		if err != nil {
			errs = append(errs, fmt.Errorf("max_size: %w", err))
		}
		r.MaxBytes = n
	}
	return r, errors.Join(errs...)
}

// checkDeploy validates a present deploy section. Messages name variables and lanes, never the
// configured values.
func (c Config) checkDeploy() []error {
	var errs []error
	if c.Deploy.Target != "compose" {
		errs = append(errs, fmt.Errorf("deploy: target must be %q", "compose"))
	}
	for _, name := range sortedKeys(c.Deploy.Secrets) {
		if ref := c.Deploy.Secrets[name]; ref != SecretFromEnv && !strings.HasPrefix(ref, "op://") {
			errs = append(errs, fmt.Errorf("deploy: secrets.%s must be %q or an op:// reference", name, SecretFromEnv))
		}
	}
	used := map[string]bool{}
	for _, l := range c.Lanes {
		for _, n := range l.PassEnv {
			used[n] = true
			if _, ok := c.Deploy.Secrets[n]; !ok {
				errs = append(errs, fmt.Errorf("deploy: secrets has no entry for %s, which lane %q passes", n, l.Name))
			}
		}
	}
	for _, n := range WorkerSecrets {
		used[n] = true
		if _, ok := c.Deploy.Secrets[n]; !ok {
			errs = append(errs, fmt.Errorf("deploy: secrets has no entry for %s, which the worker uses", n))
		}
	}
	for _, name := range sortedKeys(c.Deploy.Secrets) {
		if !used[name] {
			errs = append(errs, fmt.Errorf("deploy: secrets.%s is not used by any lane or the worker", name))
		}
	}
	return errs
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
	l.PersistCaches = lf.Caches.Persist
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
