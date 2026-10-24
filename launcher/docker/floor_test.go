package docker

import (
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/javatarz/satat/launcher"
)

const pinned = "busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"

func testLane() launcher.Lane {
	return launcher.Lane{
		Name: "t", Image: pinned, MemoryBytes: 256 << 20, CPUs: 1, PidsLimit: 64, WorkspaceBytes: 32 << 20,
	}
}

func okRequest(t *testing.T) request {
	t.Helper()
	lane, err := normalizeLane(testLane())
	if err != nil {
		t.Fatal(err)
	}
	r, err := buildRequest(lane, launcher.Step{Run: "r1", Name: "s1", Lane: "t"}, func(string) (string, bool) { return "v", true })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBuiltRequestPassesTheFloor(t *testing.T) {
	r := okRequest(t)
	if err := ValidateRequest(r.config, r.host, r.networking); err != nil {
		t.Fatalf("a request built from a valid lane must pass the floor: %v", err)
	}
}

// Each case mutates a good request in one way ADR 0027 says must be refused, and expects the
// floor to name the problem.
func TestFloorRefuses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(r *request)
		want   string
	}{
		{"bind mount", func(r *request) { r.host.Binds = []string{"/:/host"} }, "bind mounts"},
		{"bind mount typed", func(r *request) {
			r.host.Mounts = append(r.host.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/etc", Target: "/host"})
		}, "mount type"},
		{"docker socket bind", func(r *request) { r.host.Binds = []string{"/var/run/docker.sock:/var/run/docker.sock"} }, "bind mounts"},
		{"docker socket as a volume mount", func(r *request) {
			r.host.Mounts = append(r.host.Mounts, mount.Mount{Type: mount.TypeVolume, Source: "satat-docker.sock", Target: "/x"})
		}, "Docker socket"},
		{"foreign volume", func(r *request) {
			r.host.Mounts = append(r.host.Mounts, mount.Mount{Type: mount.TypeVolume, Source: "someone-elses-data", Target: "/x"})
		}, "not a Satat volume"},
		{"privileged", func(r *request) { r.host.Privileged = true }, "privileged"},
		{"host network", func(r *request) { r.host.NetworkMode = "host" }, "lane network"},
		{"default bridge", func(r *request) { r.host.NetworkMode = "bridge" }, "lane network"},
		{"other container's network", func(r *request) { r.host.NetworkMode = "container:abc" }, "lane network"},
		{"second network", func(r *request) {
			r.networking.EndpointsConfig["satat-lane-other"] = &network.EndpointSettings{}
		}, "exactly its lane network"},
		{"host pid", func(r *request) { r.host.PidMode = "host" }, "pid mode"},
		{"host ipc", func(r *request) { r.host.IpcMode = "host" }, "ipc mode"},
		{"host uts", func(r *request) { r.host.UTSMode = "host" }, "uts"},
		{"added capability", func(r *request) { r.host.CapAdd = []string{"SYS_ADMIN"} }, "capabilities must not be added"},
		{"capabilities kept", func(r *request) { r.host.CapDrop = nil }, "drop ALL"},
		{"new privileges allowed", func(r *request) { r.host.SecurityOpt = nil }, "no-new-privileges"},
		{"seccomp off", func(r *request) { r.host.SecurityOpt = append(r.host.SecurityOpt, "seccomp=unconfined") }, "not allowed"},
		{"writable rootfs", func(r *request) { r.host.ReadonlyRootfs = false }, "read-only"},
		{"root user", func(r *request) { r.config.User = "0:0" }, "non-root"},
		{"root by name", func(r *request) { r.config.User = "root" }, "non-root"},
		{"no user", func(r *request) { r.config.User = "" }, "non-root"},
		{"uid only", func(r *request) { r.config.User = "10001" }, "non-root"},
		{"tag only image", func(r *request) { r.config.Image = "busybox:1.37.0" }, "pinned by digest"},
		{"device", func(r *request) { r.host.Devices = []container.DeviceMapping{{PathOnHost: "/dev/sda"}} }, "devices"},
		{"sysctl", func(r *request) { r.host.Sysctls = map[string]string{"net.ipv4.ip_forward": "1"} }, "sysctls"},
		{"unmasked paths", func(r *request) { r.host.MaskedPaths = []string{}; r.host.ReadonlyPaths = []string{"/x"} }, "masked"},
		{"other runtime", func(r *request) { r.host.Runtime = "nvidia" }, "runtime"},
		{"no memory limit", func(r *request) { r.host.Memory = 0 }, "memory limit"},
		{"swap", func(r *request) { r.host.MemorySwap = r.host.Memory * 2 }, "swap"},
		{"no cpu limit", func(r *request) { r.host.NanoCPUs = 0 }, "cpu limit"},
		{"no pids limit", func(r *request) { r.host.PidsLimit = nil }, "pids limit"},
		{"auto remove", func(r *request) { r.host.AutoRemove = true }, "auto-remove"},
		{"restart", func(r *request) { r.host.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyAlways} }, "restart policy"},
		{"published port", func(r *request) { r.host.PublishAllPorts = true }, "ports"},
		{"extra tmpfs", func(r *request) { r.host.Tmpfs["/etc"] = "rw,nosuid,nodev,size=1" }, "tmpfs at"},
		{"tmpfs without nosuid", func(r *request) { r.host.Tmpfs["/tmp"] = "rw,size=1" }, "nosuid"},
		{"anonymous volume", func(r *request) { r.config.Volumes = map[string]struct{}{"/data": {}} }, "anonymous volumes"},
		{"stdin", func(r *request) { r.config.OpenStdin = true }, "stdin"},
		{"docker host env", func(r *request) { r.config.Env = append(r.config.Env, "DOCKER_HOST=tcp://docker-proxy:2375") }, "Docker API"},
		{"unlabelled", func(r *request) { delete(r.config.Labels, labelManaged) }, "label"},
		{"log driver", func(r *request) { r.host.LogConfig.Type = "syslog" }, "log driver"},
		{"dns override", func(r *request) { r.host.ExtraHosts = []string{"x:1.2.3.4"} }, "dns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := okRequest(t)
			tc.mutate(&r)
			err := ValidateRequest(r.config, r.host, r.networking)
			if err == nil {
				t.Fatal("the floor accepted a request it must refuse")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestFloorReportsEveryViolation(t *testing.T) {
	r := okRequest(t)
	r.host.Privileged = true
	r.host.Binds = []string{"/:/host"}
	r.config.User = "0:0"
	err := ValidateRequest(r.config, r.host, r.networking)
	for _, want := range []string{"privileged", "bind mounts", "non-root"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("combined error lacks %q: %v", want, err)
		}
	}
}

func TestIncompleteRequestIsRefused(t *testing.T) {
	if err := ValidateRequest(nil, nil, nil); err == nil {
		t.Fatal("a nil request must be refused")
	}
}

func TestLaneConfigIsCheckedWhenBuilt(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(l *launcher.Lane)
		want   string
	}{
		{"unpinned image", func(l *launcher.Lane) { l.Image = "busybox:latest" }, "pinned by digest"},
		{"root user", func(l *launcher.Lane) { l.User = "0:0" }, "non-root"},
		{"unknown runtime", func(l *launcher.Lane) { l.Runtime = "kata" }, "runtime"},
		{"no memory", func(l *launcher.Lane) { l.MemoryBytes = 0 }, "positive"},
		{"workspace bigger than memory", func(l *launcher.Lane) { l.WorkspaceBytes = l.MemoryBytes }, "smaller than its memory"},
		{"lower-case env name", func(l *launcher.Lane) { l.PassEnv = []string{"token"} }, "not allowed"},
		{"bad lane name", func(l *launcher.Lane) { l.Name = "../x" }, "identifier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := testLane()
			tc.mutate(&l)
			_, err := New(nil, []launcher.Lane{l})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New() error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if _, err := New(nil, []launcher.Lane{testLane(), testLane()}); err == nil {
		t.Error("a lane defined twice must be refused")
	}
}

// Step identifiers become container names; nothing else from a step reaches a flag.
func TestStepIdentifiersAreSimple(t *testing.T) {
	lane, _ := normalizeLane(testLane())
	for _, id := range []string{"", "../x", "a b", "a/b", "-lead", strings.Repeat("a", 64), "x;rm"} {
		if _, err := buildRequest(lane, launcher.Step{Run: id, Name: "s"}, nil); err == nil {
			t.Errorf("run id %q was accepted", id)
		}
		if _, err := buildRequest(lane, launcher.Step{Run: "r", Name: id}, nil); err == nil {
			t.Errorf("step name %q was accepted", id)
		}
	}
}

func TestInputNeverReachesTheRequest(t *testing.T) {
	lane, _ := normalizeLane(testLane())
	marker := "--privileged -v /:/host SECRET-MARKER"
	r, err := buildRequest(lane, launcher.Step{Run: "r", Name: "s", Lane: "t", Input: []byte(marker)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(r.config.Env, "\n")+strings.Join(r.config.Entrypoint, " ")+strings.Join(r.config.Cmd, " ")+string(r.host.NetworkMode), "SECRET-MARKER") {
		t.Error("step input leaked into the create request")
	}
}

func TestLaneCommandReplacesTheEntrypoint(t *testing.T) {
	l := testLane()
	l.Command = []string{"sh", "-c", "id"}
	lane, err := normalizeLane(l)
	if err != nil {
		t.Fatal(err)
	}
	r, err := buildRequest(lane, launcher.Step{Run: "r", Name: "s", Lane: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.config.Entrypoint, " ") != "sh -c id" || len(r.config.Cmd) != 0 {
		t.Fatalf("entrypoint = %v, cmd = %v", r.config.Entrypoint, r.config.Cmd)
	}
	lane, _ = normalizeLane(testLane())
	r, _ = buildRequest(lane, launcher.Step{Run: "r", Name: "s", Lane: "t"}, nil)
	if len(r.config.Entrypoint) != 0 || len(r.config.Cmd) != 0 {
		t.Fatalf("no lane command must keep image defaults: %v %v", r.config.Entrypoint, r.config.Cmd)
	}
}

func TestPassEnvMustBeSet(t *testing.T) {
	l := testLane()
	l.PassEnv = []string{"CLAUDE_CODE_OAUTH_TOKEN"}
	lane, err := normalizeLane(l)
	if err != nil {
		t.Fatal(err)
	}
	_, err = buildRequest(lane, launcher.Step{Run: "r", Name: "s"}, func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("a missing secret must fail the launch naming it, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "value") {
		t.Error("the error must not carry secret values")
	}
}

// Compose treats anything labelled com.docker.compose.* as its own and would prune or recreate it.
func TestCreateRequestHasNoComposeLabels(t *testing.T) {
	r := okRequest(t)
	for k := range r.config.Labels {
		if strings.HasPrefix(k, "com.docker.compose.") {
			t.Errorf("compose label %q", k)
		}
	}
	if r.config.Labels[labelManaged] != "true" || labelManaged != "satat.managed" {
		t.Errorf("labels: %v (managed key %q)", r.config.Labels, labelManaged)
	}
}
