package docker

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/javatarz/satat/launcher"
)

const (
	defaultUser    = "10001:10001"
	defaultTimeout = 30 * time.Minute
	tmpBytes       = 64 << 20
)

var idRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

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
	for _, n := range l.PassEnv {
		if !envNameRE.MatchString(n) {
			return l, fmt.Errorf("lane %q: env name %q is not allowed", l.Name, n)
		}
	}
	// The floor is checked on a sample request so a lane that can never launch fails now.
	probe, err := buildRequest(l, launcher.Step{Run: "probe", Name: "probe", Lane: l.Name}, func(string) (string, bool) { return "x", true })
	if err != nil {
		return l, err
	}
	if err := ValidateRequest(probe.config, probe.host, probe.networking); err != nil {
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

// buildRequest turns a lane and a step identity into a create request. Nothing from Step.Input
// or any other runtime data reaches a flag here: only the lane and the step's identifiers do.
func buildRequest(l launcher.Lane, s launcher.Step, lookupEnv func(string) (string, bool)) (request, error) {
	if !idRE.MatchString(s.Run) || !idRE.MatchString(s.Name) {
		return request{}, fmt.Errorf("run %q and step %q must be simple identifiers", s.Run, s.Name)
	}

	env := []string{
		"HOME=" + workspace,
		"SATAT_RUN=" + s.Run,
		"SATAT_STEP=" + s.Name,
		"SATAT_INPUT=" + inputDir + "/input.json",
	}
	for _, n := range l.PassEnv {
		v, ok := lookupEnv(n)
		if !ok || v == "" {
			return request{}, fmt.Errorf("lane %q needs %s, which is not set", l.Name, n)
		}
		env = append(env, n+"="+v)
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
	nets := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{netName: {}}}
	return request{name: containerName(s.Run, s.Name), config: cfg, host: host, networking: nets}, nil
}

func uidOf(user string) string { u, _, _ := strings.Cut(user, ":"); return u }
func gidOf(user string) string { _, g, _ := strings.Cut(user, ":"); return g }
