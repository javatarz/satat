package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/javatarz/satat/config"
	"github.com/javatarz/satat/launcher"
)

// What can a step see and reach? (#170, docs/roadmap/security.md)
//
// These tests start a real step container, run a probe process in it, and read back everything
// the process can observe: its environment (and that of every process it may read), credential
// files, mounts and network. Each finding is a way the step could hold a credential or reach
// something it must not: the secret store's network, the Docker API, a host outside the egress
// allowlist. The design (ADR 0025, 0026, 0032) says the answer is "nothing", so a finding is a
// failure unless it is one of the knownGaps below, which are holes the harness has today. A
// known gap is reported as a skip with its reason; when the hole is closed the test fails until
// the entry is deleted, so the list cannot go stale.

// knownGaps are findings the harness has today, per lane of satat.yaml, with why and what closes
// them. Keep this list short and honest: it is the debt, not an exemption.
const openEgress = "internal: false gives the lane a plain bridge until the hostname-allowlist egress proxy exists (ADR 0026, roadmap/security.md next step 7)"

var knownGaps = map[string][]knownGap{
	"claude": {
		{"env CLAUDE_CODE_OAUTH_TOKEN", "the Claude step holds the long-lived token until the LLM gateway proxy issues virtual keys (ADR 0032, #155)", false},
		{"env SATAT_GITHUB_TOKEN", "the runner clones inside the step with a read token until the worker clones before the step starts (ADR 0025, roadmap/security.md next step 4)", false},
		{"route: default route (open egress)", openEgress, false},
		{"reach external-host 1.1.1.1:443", openEgress + ": any host is reachable", true},
		{"reach dns example.com", openEgress + ": any name resolves", true},
	},
	"smoke": {
		{"route: default route (open egress)", openEgress, false},
		{"reach external-host 1.1.1.1:443", openEgress + ": any host is reachable", true},
		{"reach dns example.com", openEgress + ": any name resolves", true},
	},
}

// knownGap is one accepted finding. internet marks a finding that exists only where the machine
// running the test has internet access, so its absence is not proof the hole is closed.
type knownGap struct {
	finding, reason string
	internet        bool
}

// probeScript is the "arbitrary process" the step runs. It prints one tagged line per
// observation; analyze turns them into findings. Positional arguments: $1 is an address on a
// network standing in for the secret store's `secrets` network, $2 one standing in for the
// Docker API network. Neither network is the step's own, so neither must be reachable.
const probeScript = `
for f in /proc/[0-9]*/environ; do tr '\0' '\n' < "$f" 2>/dev/null; done | sed 's/^/ENV /'
env | sed 's/^/ENV /'
for p in /var/run/docker.sock /run/docker.sock /run/secrets /var/run/secrets /root/.docker /root/.config/gh \
	/root/.git-credentials /root/.netrc "$HOME/.netrc" "$HOME/.git-credentials" "$HOME/.config/gh" "$HOME/.docker"; do
	[ -e "$p" ] && echo "FILE $p"
done
grep -rIlE "$PATTERN" /etc/satat /workspace /tmp /home /root /run /var/run /cache 2>/dev/null | sed 's/^/FILEHIT /'
sed 's/^/MOUNT /' /proc/self/mountinfo
gw=$(ip route 2>/dev/null | awk '/^default/{print $3; exit}')
echo "GATEWAY ${gw:-none}"
reach() { nc -w 2 "$2" "$3" </dev/null >/dev/null 2>&1 && echo "REACH $1 $2:$3"; }
reach secrets-network "$1" 8200
reach docker-api-network "$2" 2375
reach external-host 1.1.1.1 443
if [ -n "$gw" ]; then reach host-gateway "$gw" 2375; reach host-gateway "$gw" 2376; reach host-gateway "$gw" 8200; fi
nslookup example.com >/dev/null 2>&1 && echo "REACH dns example.com"
true
`

// credentialPattern is the value shape of the credentials Satat handles: GitHub tokens, Claude
// and Anthropic keys, OpenBao and Vault tokens, AWS keys, private keys.
const credentialPattern = `ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|gh[ousr]_[A-Za-z0-9]{20,}|sk-ant-[A-Za-z0-9_-]{20,}|(hvs|hvb|s)\.[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|BEGIN [A-Z ]*PRIVATE KEY`

var (
	credentialValueRE = regexp.MustCompile(credentialPattern)
	// A variable whose name says it carries a credential, or points at the store or the Docker API.
	credentialNameRE = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|API_?KEY|PRIVATE_?KEY)|^(DOCKER_HOST|VAULT_.*|BAO_.*|OPENBAO_.*|AWS_.*|GH_.*|GITHUB_.*)$`)
)

// allowedMountTargets are the only places a step's filesystem is assembled from, besides the
// kernel's own /proc, /dev and /sys and the lane's declared /cache paths.
var allowedMountTargets = map[string]bool{
	"/": true, "/etc/satat": true, "/workspace": true, "/tmp": true,
	"/etc/hosts": true, "/etc/hostname": true, "/etc/resolv.conf": true,
	"/sbin/docker-init": true, // the init process the launcher asks for (Init: true)
}

// analyze reads the probe's output and returns the sorted, de-duplicated findings. sentinels are
// values the test planted in the worker's environment: seeing one in the step is a leak whatever
// the variable is called. It never returns a credential's value.
func analyze(out string, sentinels []string) []string {
	found := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		tag, rest, _ := strings.Cut(strings.TrimRight(line, "\r"), " ")
		switch tag {
		case "ENV":
			name, value, ok := strings.Cut(rest, "=")
			if !ok || value == "" {
				continue
			}
			if credentialNameRE.MatchString(name) || credentialValueRE.MatchString(value) || containsAny(value, sentinels) {
				found["env "+name] = true
			}
		case "FILE":
			found["file "+rest] = true
		case "FILEHIT":
			found["file with a credential: "+rest] = true
		case "MOUNT":
			if f := mountFinding(rest); f != "" {
				found[f] = true
			}
		case "GATEWAY":
			if rest != "none" {
				found["route: default route (open egress)"] = true
			}
		case "REACH":
			found["reach "+rest] = true
		}
	}
	var list []string
	for f := range found {
		list = append(list, f)
	}
	sort.Strings(list)
	return list
}

// mountFinding checks one /proc/self/mountinfo line (without its MOUNT tag).
func mountFinding(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return ""
	}
	target := fields[4]
	if strings.Contains(line, "docker.sock") {
		return "mount " + target + " (Docker socket)"
	}
	switch {
	case allowedMountTargets[target],
		target == "/proc" || strings.HasPrefix(target, "/proc/"),
		target == "/dev" || strings.HasPrefix(target, "/dev/"),
		target == "/sys" || strings.HasPrefix(target, "/sys/"),
		strings.HasPrefix(target, cacheRoot+"/"):
		return ""
	}
	return "mount " + target + " (not a mount the launcher defines)"
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Unit tests of the classifier need no Docker, so they run under -short too. They prove the
// probe would notice a leak, which is what stops the Docker test passing by looking at nothing.
func TestAnalyzeFlagsEveryKindOfLeak(t *testing.T) {
	ghToken := "ghp_" + strings.Repeat("a", 36)
	out := strings.Join([]string{
		"ENV PATH=/usr/bin",
		"ENV SATAT_GIT_NAME=Satat",
		"ENV CLAUDE_CODE_OAUTH_TOKEN=whatever",
		"ENV INNOCENT_NAME=" + ghToken,
		"ENV RENAMED=planted-value-1234",
		"ENV EMPTY_TOKEN=",
		"ENV DOCKER_HOST=tcp://docker-proxy:2375",
		"FILE /var/run/docker.sock",
		"FILEHIT /workspace/.netrc",
		"MOUNT 1 2 0:1 / /workspace rw - tmpfs tmpfs rw",
		"MOUNT 1 2 0:1 / /proc/bus ro - proc proc rw",
		"MOUNT 1 2 0:1 / /cache/go-mod rw - ext4 /dev/vda rw",
		"MOUNT 1 2 0:1 / /openbao-creds rw - ext4 /dev/vda rw",
		"MOUNT 1 2 0:1 /docker.sock /x rw - ext4 /dev/vda rw",
		"GATEWAY 172.18.0.1",
		"REACH secrets-network 172.19.0.2:8200",
		"garbage line",
	}, "\n")
	want := []string{
		"env CLAUDE_CODE_OAUTH_TOKEN", "env DOCKER_HOST", "env INNOCENT_NAME", "env RENAMED",
		"file /var/run/docker.sock", "file with a credential: /workspace/.netrc",
		"mount /openbao-creds (not a mount the launcher defines)", "mount /x (Docker socket)",
		"reach secrets-network 172.19.0.2:8200", "route: default route (open egress)",
	}
	got := analyze(out, []string{"planted-value-1234"})
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings\n got: %q\nwant: %q", got, want)
	}
	if clean := analyze("ENV PATH=/usr/bin\nENV HOME=/workspace\nGATEWAY none\nMOUNT 1 2 0:1 / /etc/satat rw - ext4 v rw", nil); len(clean) != 0 {
		t.Errorf("a clean step produced findings: %q", clean)
	}
}

// A step in a lane with no pass_env and no route out sees no credential and reaches nothing,
// even though the worker's environment is full of them. This is the design target.
func TestStepSeesAndReachesNothing(t *testing.T) {
	api := dockerAPI(t)
	h := newHarness(t, api)
	lane := testLane()
	lane.Name = "expose-baseline"
	lane.Internal = true
	if got := h.probe(t, lane); len(got) != 0 {
		t.Errorf("an unprivileged internal step can see or reach: %q", got)
	}
}

// The same probe against the lanes in satat.yaml: their pass_env, network and runtime, with the
// image swapped for busybox (the claude image is built locally and is not in CI) and the caches
// and limits trimmed. Holes that exist today are knownGaps; anything else is a regression.
func TestConfiguredLanesExposure(t *testing.T) {
	api := dockerAPI(t)
	cfg, err := config.Load(filepath.Join("..", "..", "satat.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, api)
	for _, lane := range cfg.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			probe := testLane()
			probe.Name = "expose-" + lane.Name
			probe.PassEnv, probe.OptionalEnv = lane.PassEnv, lane.OptionalEnv
			probe.Internal, probe.Runtime = lane.Internal, lane.Runtime
			got := h.probe(t, probe)
			gaps := knownGaps[lane.Name]
			known := map[string]bool{}
			for _, g := range gaps {
				known[g.finding] = true
			}
			have := map[string]bool{}
			for _, f := range got {
				have[f] = true
				if !known[f] {
					t.Errorf("lane %q: new exposure: %s", lane.Name, f)
				}
			}
			var reasons []string
			for _, g := range gaps {
				if !have[g.finding] {
					if g.internet {
						continue
					}
					t.Errorf("lane %q: known gap %q is closed: delete it from knownGaps", lane.Name, g.finding)
					continue
				}
				reasons = append(reasons, fmt.Sprintf("%s: %s", g.finding, g.reason))
			}
			if !t.Failed() && len(reasons) > 0 {
				t.Skipf("known gaps, not yet closed:\n  %s", strings.Join(reasons, "\n  "))
			}
		})
	}
}

// harness owns the decoys and the fake credentials of one test.
type harness struct {
	api       *client.Client
	secretsIP string
	dockerIP  string
	env       map[string]string
	sentinels []string
}

func newHarness(t *testing.T, api *client.Client) *harness {
	t.Helper()
	h := &harness{api: api, env: map[string]string{
		"SATAT_GIT_NAME":  "Satat Test",
		"SATAT_GIT_EMAIL": "satat@example.invalid",
	}}
	sweepDecoys(api)
	h.secretsIP = decoy(t, api, "secrets", 8200)
	h.dockerIP = decoy(t, api, "docker-api", 2375)

	// Every credential the worker holds or will hold, as a fake the test made up. Built at run
	// time so no token-shaped string sits in the source.
	for name, shape := range map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-",
		"SATAT_GITHUB_TOKEN":      "github_pat_",
		"SATAT_PUSH_TOKEN":        "github_pat_",
		"BAO_TOKEN":               "hvs.",
		"OPENBAO_SECRET_ID":       "",
	} {
		v := shape + randHex(24)
		h.env[name] = v
		h.sentinels = append(h.sentinels, v)
	}
	return h
}

// probe runs the probe script as the step of a lane (the worker's environment is h.env) and
// returns its findings.
func (h *harness) probe(t *testing.T, lane launcher.Lane) []string {
	t.Helper()
	script := strings.ReplaceAll(probeScript, "$PATTERN", "'"+credentialPattern+"'")
	lane.Command = []string{"sh", "-c", script, "probe", h.secretsIP, h.dockerIP}
	lane.Timeout = time.Minute
	l := newLauncher(t, h.api, []launcher.Lane{lane}, WithEnv(func(n string) (string, bool) {
		v, ok := h.env[n]
		return v, ok
	}))
	res, err := l.Launch(context.Background(), launcher.Step{Run: "expose", Name: "probe", Lane: lane.Name})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("probe exited %d: %s", res.ExitCode, res.Stderr)
	}
	out := string(res.Stdout)
	if !strings.Contains(out, "GATEWAY ") || !strings.Contains(out, "MOUNT ") {
		t.Fatalf("probe did not run to the end; the test would prove nothing:\n%s", redact(out, h.sentinels))
	}
	return analyze(out, h.sentinels)
}

func redact(s string, secrets []string) string {
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, "<redacted>")
	}
	return s
}

// decoy starts a listener on its own network and returns its address: a stand-in for a service
// that lives on a network the step must not be on (the secret store's, the Docker API's). It is
// not labelled satat.managed, so the launcher's reaper leaves it alone; the test removes it.
func decoy(t *testing.T, api *client.Client, name string, port int) string {
	t.Helper()
	ctx := context.Background()
	labels := map[string]string{"satat.test": "exposure"}
	netName := "satat-test-" + name + "-" + randHex(4)
	if _, err := api.NetworkCreate(ctx, netName, client.NetworkCreateOptions{Driver: "bridge", Labels: labels}); err != nil {
		t.Fatalf("create decoy network: %v", err)
	}
	t.Cleanup(func() { _, _ = api.NetworkRemove(context.Background(), netName, client.NetworkRemoveOptions{}) })

	c, err := api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  pinned,
			Cmd:    []string{"nc", "-lk", "-p", fmt.Sprint(port), "-e", "echo", "ok"},
			Labels: labels,
		},
		HostConfig:       &container.HostConfig{NetworkMode: container.NetworkMode(netName)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{netName: {}}},
	})
	if err != nil {
		t.Fatalf("create decoy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = api.ContainerRemove(context.Background(), c.ID, client.ContainerRemoveOptions{Force: true})
	})
	if _, err := api.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("start decoy: %v", err)
	}
	info, err := api.ContainerInspect(ctx, c.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ep := info.Container.NetworkSettings.Networks[netName]
	if ep == nil || !ep.IPAddress.IsValid() {
		t.Fatalf("decoy has no address on %s", netName)
	}
	ip := ep.IPAddress.String()

	// Control: a container on the decoy's own network does reach it, so a step that cannot is
	// kept out by the network and not by a dead listener.
	ctl, err := api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  pinned,
			Cmd:    []string{"sh", "-c", fmt.Sprintf("for i in 1 2 3 4 5; do nc -w 2 %s %d </dev/null && exit 0; sleep 1; done; exit 1", ip, port)},
			Labels: labels,
		},
		HostConfig:       &container.HostConfig{NetworkMode: container.NetworkMode(netName)},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{netName: {}}},
	})
	if err != nil {
		t.Fatalf("create decoy control: %v", err)
	}
	defer func() { _, _ = api.ContainerRemove(ctx, ctl.ID, client.ContainerRemoveOptions{Force: true}) }()
	if _, err := api.ContainerStart(ctx, ctl.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("start decoy control: %v", err)
	}
	w := api.ContainerWait(ctx, ctl.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case r := <-w.Result:
		if r.StatusCode != 0 {
			t.Fatalf("the decoy %s:%d is not reachable from its own network: the probe would prove nothing", ip, port)
		}
	case err := <-w.Error:
		t.Fatal(err)
	}
	return ip
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// sweepDecoys removes decoys a killed earlier run left behind, found by their label.
func sweepDecoys(api *client.Client) {
	ctx := context.Background()
	f := make(client.Filters).Add("label", "satat.test=exposure")
	if cs, err := api.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f}); err == nil {
		for _, c := range cs.Items {
			_, _ = api.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true})
		}
	}
	if ns, err := api.NetworkList(ctx, client.NetworkListOptions{Filters: f}); err == nil {
		for _, n := range ns.Items {
			_, _ = api.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{})
		}
	}
}
