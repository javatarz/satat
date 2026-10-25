package docker

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/javatarz/satat/launcher"
)

const (
	defaultUser    = "10001:10001"
	defaultTimeout = 30 * time.Minute
	tmpBytes       = 64 << 20
)

var (
	idRE        = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
	cacheNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	scopeRE     = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
)

const maxCaches = 16

// checkCaches refuses cache names that are not identifiers and paths that are not clean, under
// /cache, distinct and not nested in one another.
func checkCaches(l launcher.Lane) error {
	if len(l.Caches) > maxCaches {
		return fmt.Errorf("at most %d caches", maxCaches)
	}
	var errs []error
	names := map[string]bool{}
	for i, c := range l.Caches {
		if !cacheNameRE.MatchString(c.Name) || len(c.Name) > 32 {
			errs = append(errs, fmt.Errorf("cache name %q is not an identifier", c.Name))
		}
		if names[c.Name] {
			errs = append(errs, fmt.Errorf("cache %q is declared twice", c.Name))
		}
		names[c.Name] = true
		if !isCachePath(c.Path) {
			errs = append(errs, fmt.Errorf("cache %q: path %q must be a clean absolute path under %s", c.Name, c.Path, cacheRoot))
		}
		for _, o := range l.Caches[:i] {
			if o.Path == c.Path || strings.HasPrefix(c.Path, o.Path+"/") || strings.HasPrefix(o.Path, c.Path+"/") {
				errs = append(errs, fmt.Errorf("caches %q and %q overlap at %s", o.Name, c.Name, c.Path))
			}
		}
	}
	return errors.Join(errs...)
}

// normalizeLane fills defaults and rejects a lane that could not pass the floor, so a bad lane
// fails when the launcher is built and not on the first step.
func normalizeLane(l launcher.Lane) (launcher.Lane, error) {
	if !idRE.MatchString(l.Name) {
		return l, fmt.Errorf("lane name %q is not a valid identifier", l.Name)
	}
	if l.User == "" {
		l.User = defaultUser
	}
	if l.Timeout == 0 {
		l.Timeout = defaultTimeout
	}
	if l.MemoryBytes <= 0 || l.CPUs <= 0 || l.PidsLimit <= 0 || l.WorkspaceBytes <= 0 {
		return l, fmt.Errorf("lane %q needs positive memory, cpus, pids and workspace limits", l.Name)
	}
	if l.WorkspaceBytes+tmpBytes >= l.MemoryBytes {
		return l, fmt.Errorf("lane %q: workspace and /tmp (%d bytes) must be smaller than its memory limit", l.Name, l.WorkspaceBytes+tmpBytes)
	}
	for _, n := range append(append([]string{}, l.PassEnv...), l.OptionalEnv...) {
		if !envNameRE.MatchString(n) {
			return l, fmt.Errorf("lane %q: env name %q is not allowed", l.Name, n)
		}
	}
	if err := checkCaches(l); err != nil {
		return l, fmt.Errorf("lane %q: %w", l.Name, err)
	}
	if err := checkEgress(l); err != nil {
		return l, fmt.Errorf("lane %q: %w", l.Name, err)
	}
	// The floor is checked on a sample request so a lane that can never launch fails now.
	probe, err := buildRequest(l, launcher.Step{Run: "probe", Name: "probe", Lane: l.Name, CacheScope: "probe"}, func(string) (string, bool) { return "x", true })
	if err != nil {
		return l, err
	}
	if err := ValidateRequest(probe.config, probe.host, probe.networking, cachePaths(l)); err != nil {
		return l, fmt.Errorf("lane %q: %w", l.Name, err)
	}
	return l, nil
}

type request struct {
	name       string
	config     *container.Config
	host       *container.HostConfig
	networking *network.NetworkingConfig
}

func containerName(run, step string) string { return resourcePrefix + run + "-" + step }
func inputVolume(run, step string) string   { return containerName(run, step) + "-in" }
func laneNetwork(lane string) string        { return networkPrefix + lane }

func inputVolumeOptions(s launcher.Step) client.VolumeCreateOptions {
	return client.VolumeCreateOptions{
		Name:   inputVolume(s.Run, s.Name),
		Labels: map[string]string{labelManaged: "true", labelKind: kindInput, labelRun: s.Run, labelStep: s.Name},
	}
}

// cachePaths are the paths the lane declares for caches: the only places, besides /etc/satat and
// /workspace, where the floor lets a volume be mounted.
func cachePaths(l launcher.Lane) []string {
	var out []string
	for _, c := range l.Caches {
		out = append(out, c.Path)
	}
	return out
}

// cacheVolume is the volume behind one of a lane's caches for one step.
type cacheVolume struct {
	opts    client.VolumeCreateOptions
	path    string
	persist bool
}

// cacheKeyPrefix names a persistent cache key's volumes: satat-cache-<lane>-<scope>-.
func cacheKeyPrefix(lane, scope string) string {
	return resourcePrefix + "cache-" + lane + "-" + scope + "-"
}

// cacheVolumes names the step's cache volumes. The builder mounts them and Launch creates them
// from this one list, so the two cannot disagree. A per-step cache is named after the step and
// goes with it. A persistent one is named after its lane and the step's cache scope, the only
// runtime value that reaches a volume name, and it can only pick a name under that fixed prefix.
func cacheVolumes(l launcher.Lane, s launcher.Step) []cacheVolume {
	var out []cacheVolume
	for _, c := range l.Caches {
		cv := cacheVolume{path: c.Path, persist: l.PersistCaches, opts: client.VolumeCreateOptions{
			Name: containerName(s.Run, s.Name) + "-cache-" + c.Name,
			Labels: map[string]string{
				labelManaged: "true", labelKind: kindCache, labelLane: l.Name,
				labelRun: s.Run, labelStep: s.Name, labelCache: c.Name,
			},
		}}
		if l.PersistCaches {
			cv.opts = client.VolumeCreateOptions{
				Name: cacheKeyPrefix(l.Name, s.CacheScope) + c.Name,
				Labels: map[string]string{
					labelManaged: "true", labelKind: kindCache, labelLane: l.Name,
					labelScope: s.CacheScope, labelCache: c.Name,
				},
			}
		}
		out = append(out, cv)
	}
	return out
}

// buildRequest turns a lane and a step identity into a create request. Nothing from Step.Input
// or any other runtime data reaches a flag here: only the lane and the step's identifiers do.
func buildRequest(l launcher.Lane, s launcher.Step, lookupEnv func(string) (string, bool)) (request, error) {
	if !idRE.MatchString(s.Run) || !idRE.MatchString(s.Name) {
		return request{}, fmt.Errorf("run %q and step %q must be simple identifiers", s.Run, s.Name)
	}
	if s.CacheScope != "" && !scopeRE.MatchString(s.CacheScope) {
		return request{}, fmt.Errorf("cache scope %q must be 1 to 32 lower-case letters and digits", s.CacheScope)
	}
	if l.PersistCaches && len(l.Caches) > 0 && s.CacheScope == "" {
		return request{}, fmt.Errorf("lane %q keeps caches per repo, so a step needs a cache scope", l.Name)
	}

	env := []string{
		"HOME=" + workspace,
		"SATAT_RUN=" + s.Run,
		"SATAT_STEP=" + s.Name,
		"SATAT_INPUT=" + inputDir + "/input.json",
		"SATAT_OUTPUT=" + outputDir,
	}
	if l.Egress != nil {
		env = append(env, proxyEnv()...)
	}
	for _, n := range l.PassEnv {
		v, ok := lookupEnv(n)
		if !ok || v == "" {
			return request{}, fmt.Errorf("lane %q needs %s, which is not set", l.Name, n)
		}
		env = append(env, n+"="+v)
	}
	for _, n := range l.OptionalEnv {
		if v, ok := lookupEnv(n); ok && v != "" {
			env = append(env, n+"="+v)
		}
	}

	netName := laneNetwork(l.Name)
	pids := l.PidsLimit
	init := true
	mem := l.MemoryBytes

	cfg := &container.Config{
		User:       l.User,
		Env:        env,
		Cmd:        l.Command,
		Image:      l.Image,
		WorkingDir: workspace,
		Labels: map[string]string{
			labelManaged: "true",
			labelRun:     s.Run,
			labelStep:    s.Name,
			labelLane:    l.Name,
		},
	}
	host := &container.HostConfig{
		NetworkMode:    container.NetworkMode(netName),
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
		Init:           &init,
		Runtime:        l.Runtime,
		RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyDisabled},
		LogConfig:      container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "2"}},
		Tmpfs: map[string]string{
			tmpDir:    fmt.Sprintf("rw,nosuid,nodev,size=%d,mode=1777", tmpBytes),
			workspace: fmt.Sprintf("rw,nosuid,nodev,size=%d,mode=0755,uid=%s,gid=%s", l.WorkspaceBytes, uidOf(l.User), gidOf(l.User)),
		},
		Mounts: []mount.Mount{{
			Type:          mount.TypeVolume,
			Source:        inputVolume(s.Run, s.Name),
			Target:        inputDir,
			VolumeOptions: &mount.VolumeOptions{NoCopy: true},
		}},
		Resources: container.Resources{
			Memory:     mem,
			MemorySwap: mem,
			NanoCPUs:   int64(l.CPUs * 1e9),
			PidsLimit:  &pids,
		},
	}
	// Cache volumes are created without NoCopy: Docker then copies the image's directory at the
	// path, and with it the step user's ownership, into the new empty volume.
	for _, cv := range cacheVolumes(l, s) {
		host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeVolume, Source: cv.opts.Name, Target: cv.path})
	}
	nets := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{netName: {}}}
	return request{name: containerName(s.Run, s.Name), config: cfg, host: host, networking: nets}, nil
}

func uidOf(user string) string { u, _, _ := strings.Cut(user, ":"); return u }
func gidOf(user string) string { _, g, _ := strings.Cut(user, ":"); return g }
