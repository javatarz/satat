package docker

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	labelManaged = "satat.managed"
	labelRun     = "satat.run"
	labelStep    = "satat.step"
	labelLane    = "satat.lane"
	labelKind    = "satat.kind"

	// Volume kinds. The step reaper removes only input volumes; caches have their own reaper.
	kindInput = "input"

	// resourcePrefix names every container, volume and network the launcher creates.
	resourcePrefix = "satat-"
	networkPrefix  = resourcePrefix + "lane-"

	inputDir   = "/etc/satat"
	outputName = "out"
	outputDir  = inputDir + "/" + outputName
	workspace  = "/workspace"
	tmpDir     = "/tmp"
	// cacheRoot holds every cache path a lane may declare.
	cacheRoot = "/cache"
)

var (
	digestRE  = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
	uidgidRE  = regexp.MustCompile(`^([1-9][0-9]*):([1-9][0-9]*)$`)
	envNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	volumeRE  = regexp.MustCompile(`^satat-[a-zA-Z0-9][a-zA-Z0-9_.-]{0,190}$`)
)

// ValidateRequest is the hardening floor of ADR 0026 and the access gate of ADR 0027. It looks at
// a finished create request, not at the lane that produced it, so a bug in the builder cannot
// widen what a step gets. The one thing it takes from the lane is cachePaths, the cache paths the
// lane declares in configuration: a volume may be mounted only there, at /etc/satat or at
// /workspace. It returns every violation, not only the first.
func ValidateRequest(cfg *container.Config, host *container.HostConfig, nets *network.NetworkingConfig, cachePaths []string) error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if cfg == nil || host == nil || nets == nil {
		return errors.New("create request is incomplete")
	}

	// Identity and image.
	if !digestRE.MatchString(cfg.Image) {
		bad("image %q is not pinned by digest", cfg.Image)
	}
	if !uidgidRE.MatchString(cfg.User) {
		bad("user %q must be a non-root numeric uid:gid", cfg.User)
	}
	if cfg.Labels[labelManaged] != "true" {
		bad("missing label %s=true", labelManaged)
	}
	if len(cfg.Volumes) != 0 {
		bad("anonymous volumes are not allowed")
	}
	if len(cfg.ExposedPorts) != 0 || len(host.PortBindings) != 0 || host.PublishAllPorts {
		bad("ports must not be exposed or published")
	}
	if cfg.AttachStdin || cfg.OpenStdin || cfg.Tty {
		bad("stdin and tty are not allowed")
	}
	for _, e := range cfg.Env {
		if strings.Contains(e, "docker.sock") || strings.HasPrefix(e, "DOCKER_HOST=") {
			bad("environment entry %q points at the Docker API", envName(e))
		}
	}

	// Privilege.
	if host.Privileged {
		bad("privileged is not allowed")
	}
	if len(host.CapAdd) != 0 {
		bad("capabilities must not be added: %v", host.CapAdd)
	}
	if !contains(host.CapDrop, "ALL") {
		bad("capabilities must drop ALL")
	}
	if !contains(host.SecurityOpt, "no-new-privileges") && !contains(host.SecurityOpt, "no-new-privileges:true") {
		bad("no-new-privileges is required")
	}
	for _, o := range host.SecurityOpt {
		if o != "no-new-privileges" && o != "no-new-privileges:true" {
			bad("security option %q is not allowed", o)
		}
	}
	if !host.ReadonlyRootfs {
		bad("root filesystem must be read-only")
	}
	if len(host.Devices) != 0 || len(host.DeviceCgroupRules) != 0 || len(host.DeviceRequests) != 0 {
		bad("devices are not allowed")
	}
	if len(host.Sysctls) != 0 {
		bad("sysctls are not allowed")
	}
	if len(host.MaskedPaths) != 0 || len(host.ReadonlyPaths) != 0 {
		bad("masked and read-only path overrides are not allowed")
	}
	if len(host.GroupAdd) != 0 {
		bad("extra groups are not allowed")
	}
	if len(host.Annotations) != 0 || len(host.StorageOpt) != 0 {
		bad("annotations and storage options are not allowed")
	}
	if host.Runtime != "" && host.Runtime != "runsc" {
		bad("runtime %q is not allowed", host.Runtime)
	}
	if host.OomScoreAdj < 0 {
		bad("a negative oom score adjustment is not allowed")
	}

	// Namespaces.
	if host.PidMode != "" {
		bad("pid mode %q is not allowed", host.PidMode)
	}
	if host.IpcMode != "" && host.IpcMode != "private" && host.IpcMode != "none" {
		bad("ipc mode %q is not allowed", host.IpcMode)
	}
	if host.UTSMode != "" || host.UsernsMode != "" || host.Cgroup != "" {
		bad("uts, userns and cgroup overrides are not allowed")
	}
	if host.CgroupnsMode != "" && host.CgroupnsMode != "private" {
		bad("cgroup namespace mode %q is not allowed", host.CgroupnsMode)
	}

	// Filesystem: no host paths, no sockets, only the mounts the launcher itself defines.
	if len(host.Binds) != 0 {
		bad("bind mounts are not allowed: %v", host.Binds)
	}
	if len(host.VolumesFrom) != 0 || len(host.Links) != 0 {
		bad("volumes-from and links are not allowed")
	}
	for _, p := range cachePaths {
		if !isCachePath(p) {
			bad("cache path %q is not a clean path under %s", p, cacheRoot)
		}
	}
	for _, m := range host.Mounts {
		switch m.Type {
		case mount.TypeVolume:
			if !strings.HasPrefix(m.Source, resourcePrefix) {
				bad("volume %q is not a Satat volume", m.Source)
			}
			if m.Target != inputDir && m.Target != workspace && !contains(cachePaths, m.Target) {
				bad("volume at %q is not allowed: only %s, %s and declared cache paths", m.Target, inputDir, workspace)
			}
			// A local-driver volume with o=bind,device=/x is a host bind mount in disguise, and a
			// subpath reaches into a directory of the volume the launcher did not choose.
			if o := m.VolumeOptions; o != nil && (o.DriverConfig != nil || o.Subpath != "") {
				bad("volume at %q must not set a driver config or a subpath", m.Target)
			}
		case mount.TypeTmpfs:
			if m.Source != "" {
				bad("tmpfs mount must not have a source")
			}
			if m.Target != tmpDir && m.Target != workspace {
				bad("tmpfs at %q is not allowed", m.Target)
			}
		default:
			bad("mount type %q at %q is not allowed", m.Type, m.Target)
		}
		if strings.Contains(m.Source, "docker.sock") || strings.Contains(m.Target, "docker.sock") {
			bad("mount %q -> %q touches the Docker socket", m.Source, m.Target)
		}
	}
	for target, opts := range host.Tmpfs {
		if target != tmpDir && target != workspace {
			bad("tmpfs at %q is not allowed", target)
		}
		if !strings.Contains(opts, "nosuid") || !strings.Contains(opts, "nodev") || !strings.Contains(opts, "size=") {
			bad("tmpfs at %q needs nosuid, nodev and a size: %q", target, opts)
		}
	}

	// Resources.
	if host.Memory <= 0 {
		bad("a memory limit is required")
	}
	if host.MemorySwap != host.Memory {
		bad("swap must equal the memory limit (no swap)")
	}
	if host.NanoCPUs <= 0 {
		bad("a cpu limit is required")
	}
	if host.PidsLimit == nil || *host.PidsLimit <= 0 {
		bad("a pids limit is required")
	}

	// Lifecycle and logging.
	if host.AutoRemove {
		bad("auto-remove is not allowed: the launcher removes the container after reading it")
	}
	if n := host.RestartPolicy.Name; n != "" && n != container.RestartPolicyDisabled {
		bad("restart policy %q is not allowed", n)
	}
	if t := host.LogConfig.Type; t != "json-file" && t != "local" {
		bad("log driver %q is not allowed", t)
	}

	// Network: exactly one Satat lane network, no host, none, container: or default bridge.
	mode := string(host.NetworkMode)
	if !strings.HasPrefix(mode, networkPrefix) {
		bad("network mode %q is not a Satat lane network", mode)
	}
	if len(nets.EndpointsConfig) != 1 || nets.EndpointsConfig[mode] == nil {
		bad("the step must attach to exactly its lane network")
	}
	if len(host.DNS) != 0 || len(host.DNSOptions) != 0 || len(host.DNSSearch) != 0 || len(host.ExtraHosts) != 0 {
		bad("dns and extra hosts overrides are not allowed")
	}

	return errors.Join(errs...)
}

// ValidateVolume is the floor for creating a volume. A step can only mount a volume the launcher
// created, so this is where a volume's driver is pinned: the default local driver with no
// options, because driver options are how a named volume becomes a bind mount of a host path.
func ValidateVolume(o client.VolumeCreateOptions) error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if !volumeRE.MatchString(o.Name) {
		bad("volume name %q is not a Satat volume name", o.Name)
	}
	if o.Driver != "" && o.Driver != "local" {
		bad("volume driver %q is not allowed", o.Driver)
	}
	if len(o.DriverOpts) != 0 {
		bad("volume driver options are not allowed")
	}
	if o.ClusterVolumeSpec != nil {
		bad("cluster volumes are not allowed")
	}
	if o.Labels[labelManaged] != "true" {
		bad("missing label %s=true", labelManaged)
	}
	if !knownKind(o.Labels[labelKind]) {
		bad("volume kind %q is not known", o.Labels[labelKind])
	}
	return errors.Join(errs...)
}

func knownKind(k string) bool { return k == kindInput }

// isCachePath reports whether p is a clean absolute path strictly under cacheRoot.
func isCachePath(p string) bool {
	return path.IsAbs(p) && path.Clean(p) == p && strings.HasPrefix(p, cacheRoot+"/")
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func envName(entry string) string {
	name, _, _ := strings.Cut(entry, "=")
	return name
}
