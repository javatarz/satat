package docker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/javatarz/satat/egress"
	"github.com/javatarz/satat/launcher"
)

// A lane with an Egress runs its steps on an internal network and puts one proxy container on
// that network and on egressOutNetwork, which has a route out (#169, ADR 0025, ADR 0026). The
// step reaches the outside only by asking the proxy, at egressAlias:egressPort.
const (
	labelEgress = "satat.egress"

	egressAlias      = "egress"
	egressPort       = "3128"
	egressOutNetwork = resourcePrefix + "egress-out"
	egressUser       = "10003:10003"
	egressMemory     = 128 << 20
	egressPids       = 256
	egressEnvAllow   = "SATAT_EGRESS_ALLOW"
)

func egressContainer(lane string) string { return resourcePrefix + "egress-" + lane }

// proxyURL is where a step finds its lane's proxy. The name is the alias the proxy has on the
// lane's network only; it resolves nowhere else.
func proxyURL() string { return "http://" + egressAlias + ":" + egressPort }

// proxyEnv is the proxy settings a step gets. Loopback is never proxied.
func proxyEnv() []string {
	u := proxyURL()
	const no = "localhost,127.0.0.1,::1"
	return []string{
		"HTTP_PROXY=" + u, "HTTPS_PROXY=" + u, "ALL_PROXY=" + u,
		"http_proxy=" + u, "https_proxy=" + u, "all_proxy=" + u,
		"NO_PROXY=" + no, "no_proxy=" + no,
	}
}

// checkEgress refuses an egress that cannot work or would not be closed: it needs an internal
// lane (on a plain bridge the step would simply bypass the proxy), a pinned image and a
// non-empty allowlist of host names.
func checkEgress(l launcher.Lane) error {
	e := l.Egress
	if e == nil {
		return nil
	}
	var errs []error
	if !l.Internal {
		errs = append(errs, errors.New("egress needs internal: true, or the step could bypass the proxy"))
	}
	if !digestRE.MatchString(e.Image) {
		errs = append(errs, fmt.Errorf("egress image %q is not pinned by digest", e.Image))
	}
	if len(e.Allow) == 0 {
		errs = append(errs, errors.New("egress.allow is empty: leave egress out for a lane with no network"))
	}
	if _, err := egress.ParseAllowlist(e.Allow); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// egressRequest is the create request for a lane's proxy. The allowlist is the lane's, from
// configuration; nothing from a step reaches it.
func egressRequest(l launcher.Lane) (name string, cfg *container.Config, host *container.HostConfig, nets *network.NetworkingConfig) {
	allow := slices.Clone(l.Egress.Allow)
	sort.Strings(allow)
	lane := laneNetwork(l.Name)
	pids := int64(egressPids)
	cfg = &container.Config{
		User:   egressUser,
		Image:  l.Egress.Image,
		Env:    []string{egressEnvAllow + "=" + strings.Join(allow, ",")},
		Labels: map[string]string{labelEgress: l.Name},
	}
	host = &container.HostConfig{
		NetworkMode:    container.NetworkMode(lane),
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
		RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyDisabled},
		LogConfig:      container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "2"}},
		Resources:      container.Resources{Memory: egressMemory, MemorySwap: egressMemory, NanoCPUs: 1e9, PidsLimit: &pids},
	}
	nets = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
		lane:             {Aliases: []string{egressAlias}},
		egressOutNetwork: {},
	}}
	return egressContainer(l.Name), cfg, host, nets
}

// ValidateEgressRequest is the floor for a proxy container: unprivileged like a step, with
// nothing mounted, nothing published, and on exactly its lane's network and the outbound one.
func ValidateEgressRequest(cfg *container.Config, host *container.HostConfig, nets *network.NetworkingConfig) error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if cfg == nil || host == nil || nets == nil {
		return errors.New("create request is incomplete")
	}
	if !digestRE.MatchString(cfg.Image) {
		bad("image %q is not pinned by digest", cfg.Image)
	}
	if !uidgidRE.MatchString(cfg.User) {
		bad("user %q must be a non-root numeric uid:gid", cfg.User)
	}
	if cfg.Labels[labelEgress] == "" {
		bad("missing label %s", labelEgress)
	}
	if len(cfg.Volumes) != 0 || len(host.Binds) != 0 || len(host.Mounts) != 0 || len(host.VolumesFrom) != 0 {
		bad("the proxy mounts nothing")
	}
	if len(cfg.ExposedPorts) != 0 || len(host.PortBindings) != 0 || host.PublishAllPorts {
		bad("the proxy must not publish ports")
	}
	if host.Privileged || len(host.CapAdd) != 0 || !contains(host.CapDrop, "ALL") {
		bad("the proxy must be unprivileged and drop ALL capabilities")
	}
	if !contains(host.SecurityOpt, "no-new-privileges") || len(host.SecurityOpt) != 1 {
		bad("no-new-privileges is required and nothing else is allowed")
	}
	if !host.ReadonlyRootfs {
		bad("root filesystem must be read-only")
	}
	if host.PidMode != "" || host.IpcMode != "" || host.UTSMode != "" || host.UsernsMode != "" || len(host.Devices) != 0 || len(host.Sysctls) != 0 {
		bad("no namespace, device or sysctl overrides are allowed")
	}
	if host.Memory <= 0 || host.MemorySwap != host.Memory || host.NanoCPUs <= 0 || host.PidsLimit == nil || *host.PidsLimit <= 0 {
		bad("memory (no swap), cpu and pids limits are required")
	}
	if host.AutoRemove {
		bad("auto-remove is not allowed")
	}
	mode := string(host.NetworkMode)
	if !strings.HasPrefix(mode, networkPrefix) {
		bad("network mode %q is not a Satat lane network", mode)
	}
	if len(nets.EndpointsConfig) != 2 || nets.EndpointsConfig[mode] == nil || nets.EndpointsConfig[egressOutNetwork] == nil {
		bad("the proxy must attach to exactly its lane network and %s", egressOutNetwork)
	}
	if len(host.DNS) != 0 || len(host.ExtraHosts) != 0 {
		bad("dns and extra hosts overrides are not allowed")
	}
	return errors.Join(errs...)
}

// ensureNetwork creates the network, or checks the one that exists. A network that exists with
// the wrong internal setting is never used: the lane's closure is the network's, so a lane moved
// to internal: true would otherwise keep its old open bridge. It is replaced if nothing is
// attached, and the launch fails if something is.
func (l *Launcher) ensureNetwork(ctx context.Context, name string, internal bool, labels map[string]string) error {
	for range 2 {
		got, err := l.api.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
		if err == nil {
			if got.Network.Internal == internal {
				return nil
			}
			if len(got.Network.Containers) != 0 || got.Network.Labels[labelManaged] != "true" {
				return fmt.Errorf("network %s exists with internal=%v, want %v, and cannot be replaced (it is in use or not Satat's): remove it", name, got.Network.Internal, internal)
			}
			if _, err := l.api.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
				return fmt.Errorf("replace network %s: %w", name, err)
			}
			l.log.Info("replaced network with the wrong internal setting", "network", name, "internal", internal)
		} else if !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("inspect network %s: %w", name, err)
		}
		_, err = l.api.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge", Internal: internal, Labels: labels})
		if err == nil {
			return nil
		}
		if !cerrdefs.IsConflict(err) {
			return fmt.Errorf("create network %s: %w", name, err)
		}
	}
	return fmt.Errorf("network %s: lost the race to create it twice", name)
}

// ensureEgress makes sure the lane's proxy exists with the lane's current allowlist, and runs.
// A proxy whose image or allowlist differs from the lane's (the config changed) is replaced. It
// only ever touches a container carrying its own label.
func (l *Launcher) ensureEgress(ctx context.Context, lane launcher.Lane) error {
	if lane.Egress == nil {
		return nil
	}
	_, err, _ := l.group.Do("egress:"+lane.Name, func() (any, error) { return nil, l.ensureEgressOnce(ctx, lane) })
	return err
}

func (l *Launcher) ensureEgressOnce(ctx context.Context, lane launcher.Lane) error {
	name, cfg, host, nets := egressRequest(lane)
	if err := ValidateEgressRequest(cfg, host, nets); err != nil {
		return fmt.Errorf("refusing to create %s: %w", name, err)
	}
	if err := l.ensureNetwork(ctx, laneNetwork(lane.Name), true, map[string]string{labelManaged: "true", labelLane: lane.Name}); err != nil {
		return err
	}
	if err := l.ensureNetwork(ctx, egressOutNetwork, false, map[string]string{labelManaged: "true"}); err != nil {
		return err
	}
	for range 2 {
		found, err := l.api.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
		switch {
		case err == nil:
			c := found.Container
			if c.Config.Labels[labelEgress] != lane.Name {
				return fmt.Errorf("container %s exists and is not a Satat egress proxy", name)
			}
			if c.Config.Image == cfg.Image && slices.Contains(c.Config.Env, cfg.Env[0]) && onNetworks(c, string(host.NetworkMode)) {
				if c.State.Running {
					return nil
				}
				if _, err := l.api.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
					return fmt.Errorf("start %s: %w", name, err)
				}
				return nil
			}
			l.log.Info("replacing egress proxy: its image or allowlist changed", "container", name)
			if _, err := l.api.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
				return fmt.Errorf("remove %s: %w", name, err)
			}
		case !cerrdefs.IsNotFound(err):
			return fmt.Errorf("inspect %s: %w", name, err)
		}
		created, err := l.api.ContainerCreate(ctx, client.ContainerCreateOptions{Name: name, Config: cfg, HostConfig: host, NetworkingConfig: nets})
		if cerrdefs.IsConflict(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
		if _, err := l.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
			return fmt.Errorf("start %s: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("egress proxy %s: lost the race to create it twice", name)
}

// onNetworks reports whether the container is on its lane network and the outbound one.
func onNetworks(c container.InspectResponse, lane string) bool {
	nets := c.NetworkSettings.Networks
	return nets[lane] != nil && nets[egressOutNetwork] != nil && len(nets) == 2
}

// RemoveEgress removes the proxy of the named lane, if there is one. Proxies are long-lived like
// the lane networks: the step reaper leaves them, and Setup or the next launch recreates them.
func (l *Launcher) RemoveEgress(ctx context.Context, lane string) error {
	name := egressContainer(lane)
	found, err := l.api.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	if found.Container.Config.Labels[labelEgress] != lane {
		return fmt.Errorf("container %s is not a Satat egress proxy", name)
	}
	_, err = l.api.ContainerRemove(ctx, found.Container.ID, client.ContainerRemoveOptions{Force: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}
