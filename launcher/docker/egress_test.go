package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/javatarz/satat/launcher"
)

// curlImage is a small image with curl and a shell, for the step's side of the egress tests.
const curlImage = "curlimages/curl:8.10.1@sha256:d9b4541e214bcd85196d6e92e2753ac6d0ea699f0af5741f8c6cccbfcf00ef4b"

var (
	egressImageOnce sync.Once
	egressImageRef  string
	egressImageErr  error
)

// egressImage builds the proxy image from this checkout and returns its digest-pinned reference.
// It cross-compiles the proxy on the host and wraps it in an empty image, the shape of
// images/egress/Dockerfile, so the test needs no registry and no pull. The tag is keyed by the
// binary, so a second run reuses the image and an edit gets a new one.
func egressImage(t *testing.T, api *client.Client) string {
	t.Helper()
	egressImageOnce.Do(func() { egressImageRef, egressImageErr = buildEgressImage(api) })
	if egressImageErr != nil {
		t.Fatalf("build the egress proxy image: %v", egressImageErr)
	}
	return egressImageRef
}

func buildEgressImage(api *client.Client) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	info, err := api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", err
	}
	arch := map[string]string{"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}[info.Info.Architecture]
	if arch == "" {
		arch = "amd64"
	}
	dir, err := os.MkdirTemp("", "satat-egress-build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "satat-egress")
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", bin, "./cmd/satat-egress")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", &buildError{err, string(out)}
	}
	b, err := os.ReadFile(bin)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	tag := "satat-egress-test:" + hex.EncodeToString(sum[:6])

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dockerfile := "FROM scratch\nCOPY satat-egress /satat-egress\nUSER " + egressUser + "\nENTRYPOINT [\"/satat-egress\"]\n"
	for _, f := range []struct {
		name string
		data []byte
		mode int64
	}{{"Dockerfile", []byte(dockerfile), 0o644}, {"satat-egress", b, 0o755}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data))}); err != nil {
			return "", err
		}
		if _, err := tw.Write(f.data); err != nil {
			return "", err
		}
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	res, err := api.ImageBuild(ctx, &buf, client.ImageBuildOptions{Tags: []string{tag}, Remove: true})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if strings.Contains(string(out), `"error"`) {
		return "", &buildError{nil, string(out)}
	}
	insp, err := api.ImageInspect(ctx, tag)
	if err != nil {
		return "", err
	}
	return "satat-egress-test@" + insp.ID, nil
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string {
	if e.err == nil {
		return e.out
	}
	return e.err.Error() + ": " + e.out
}

// removeEgress takes a test's proxy and its lane network away with it.
func removeEgress(t *testing.T, l *Launcher, lane string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := l.RemoveEgress(ctx, lane); err != nil {
		t.Logf("remove egress proxy: %v", err)
	}
}

func hasInternet(t *testing.T, api *client.Client) bool {
	t.Helper()
	probe := testLane()
	probe.Name = "egress-net-control"
	probe.Image = curlImage
	probe.Command = []string{"sh", "-c", "curl -fsS -m 10 -o /dev/null https://example.com"}
	probe.Timeout = time.Minute
	// An open lane, deliberately: this asks whether the machine running the test can reach the
	// internet at all, so a failure of the proxied request can be told from a dead network.
	l := newLauncher(t, api, []launcher.Lane{probe})
	res, err := l.Launch(context.Background(), launcher.Step{Run: "egress", Name: "control", Lane: probe.Name})
	return err == nil && res.ExitCode == 0
}

// egressProbe is the step: each line prints "<case> <ok|blocked>" so a failure names the case.
// "ok" means the request got an answer from the far end; "blocked" means curl or the proxy
// stopped it. The cases run against the same lane the claude lane is built on.
const egressProbe = `
t() { name=$1; shift; if "$@" >/dev/null 2>&1; then echo "$name ok"; else echo "$name blocked"; fi; }
c='curl -fsS -m 8 -o /dev/null'
t allowed-https           $c https://example.com
t allowed-http            $c http://example.com
t other-host-https        $c https://github.com
t other-host-http         $c http://neverssl.com
t raw-ip-https            $c https://1.1.1.1
t raw-ip-http             $c http://1.1.1.1
t metadata-ip             $c http://169.254.169.254/latest/meta-data/
t loopback-ip             $c http://127.0.0.1:2375/version
t decoy-ip-via-proxy      $c "http://$DECOY/"
t allowed-wrong-port      $c https://example.com:8443
t direct-bypass-host      $c --noproxy '*' https://example.com
t direct-bypass-ip        $c --noproxy '*' https://1.1.1.1
t direct-bypass-tcp       nc -w 3 1.1.1.1 443
t direct-bypass-dns       nslookup example.com
t direct-bypass-env-unset env -u HTTPS_PROXY -u https_proxy -u ALL_PROXY -u all_proxy curl -fsS -m 8 -o /dev/null https://example.com
t proxy-reachable         nc -w 3 egress 3128
t proxy-is-not-a-web-server $c http://egress:3128/
true
`

// A step on an egress lane reaches the allowlisted host through the proxy and nothing else:
// not another host, not an address, not a private or metadata address, not by skipping the
// proxy. The lane has no other route out (internal network), and the proxy is the only thing
// on it that does. #169.
func TestEgressProxyLetsAStepReachOnlyTheAllowlist(t *testing.T) {
	api := dockerAPI(t)
	online := hasInternet(t, api)

	lane := testLane()
	lane.Name = "egress-allow"
	lane.Image = curlImage
	lane.Internal = true
	lane.Egress = &launcher.Egress{Image: egressImage(t, api), Allow: []string{"example.com"}}
	lane.Timeout = 3 * time.Minute
	decoy := decoy(t, api, "egress-decoy", 2375)
	lane.Command = []string{"sh", "-c", "DECOY=" + decoy + ":2375; " + egressProbe}

	l := newLauncher(t, api, []launcher.Lane{lane})
	t.Cleanup(func() { removeEgress(t, l, lane.Name) })
	res, err := l.Launch(context.Background(), launcher.Step{Run: "egress", Name: "probe", Lane: lane.Name})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("probe exited %d: %s", res.ExitCode, res.Stderr)
	}
	t.Logf("probe results (online=%v):\n%s", online, res.Stdout)
	got := map[string]string{}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			got[f[0]] = f[1]
		}
	}
	// Everything but the allowed host is blocked, with or without internet access.
	for _, name := range []string{
		"other-host-https", "other-host-http", "raw-ip-https", "raw-ip-http", "metadata-ip", "loopback-ip",
		"decoy-ip-via-proxy", "allowed-wrong-port", "direct-bypass-host", "direct-bypass-ip", "direct-bypass-tcp",
		"direct-bypass-dns", "direct-bypass-env-unset", "proxy-is-not-a-web-server",
	} {
		if got[name] != "blocked" {
			t.Errorf("%s: %q, want blocked\n%s", name, got[name], res.Stdout)
		}
	}
	// The control: the proxy itself is reachable, so the refusals above are its answers and not a
	// dead network.
	if got["proxy-reachable"] != "ok" {
		t.Errorf("the step cannot reach its proxy: %q\n%s", got["proxy-reachable"], res.Stdout)
	}
	if !online {
		t.Log("no internet access on this machine: the allowed-host cases were not checked")
		return
	}
	for _, name := range []string{"allowed-https", "allowed-http"} {
		if got[name] != "ok" {
			t.Errorf("%s: %q, want ok\n%s", name, got[name], res.Stdout)
		}
	}
}

// The proxy is the only thing that changes between an internal lane and an egress lane, and what
// it is allowed is the lane's configuration and nothing a step can change.
func TestEgressProxyContainerIsHardenedAndLabelled(t *testing.T) {
	api := dockerAPI(t)
	lane := testLane()
	lane.Name = "egress-harden"
	lane.Internal = true
	lane.Egress = &launcher.Egress{Image: egressImage(t, api), Allow: []string{"b.example.com", "a.example.com:8443"}}
	l := newLauncher(t, api, []launcher.Lane{lane})
	t.Cleanup(func() { removeEgress(t, l, lane.Name) })

	ctx := context.Background()
	insp, err := api.ContainerInspect(ctx, egressContainer(lane.Name), client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := insp.Container
	if !c.State.Running {
		t.Fatalf("the proxy is not running: %+v", c.State)
	}
	if c.Config.Labels[labelEgress] != lane.Name || c.Config.Labels[labelManaged] != "" {
		t.Errorf("labels = %v: the proxy is Satat's, but not a step the step reaper may remove", c.Config.Labels)
	}
	if want := egressEnvAllow + "=a.example.com:8443,b.example.com"; !slices.Contains(c.Config.Env, want) {
		t.Errorf("env = %q, want %q in it", c.Config.Env, want)
	}
	h := c.HostConfig
	if !h.ReadonlyRootfs || len(h.CapDrop) != 1 || h.CapDrop[0] != "ALL" || h.Privileged || len(h.PortBindings) != 0 || len(c.Mounts) != 0 {
		t.Errorf("the proxy is not hardened: readonly=%v capdrop=%v privileged=%v ports=%v mounts=%v", h.ReadonlyRootfs, h.CapDrop, h.Privileged, h.PortBindings, c.Mounts)
	}
	if c.Config.User != egressUser {
		t.Errorf("user = %q", c.Config.User)
	}
	nets := c.NetworkSettings.Networks
	if nets[laneNetwork(lane.Name)] == nil || nets[egressOutNetwork] == nil || len(nets) != 2 {
		t.Errorf("networks = %v", nets)
	}

	// A launch with the proxy gone puts it back; a changed allowlist replaces it.
	if err := l.RemoveEgress(ctx, lane.Name); err != nil {
		t.Fatal(err)
	}
	if err := l.ensureEgress(ctx, l.lanes[lane.Name]); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	changed := l.lanes[lane.Name]
	changed.Egress = &launcher.Egress{Image: changed.Egress.Image, Allow: []string{"c.example.com"}}
	if err := l.ensureEgress(ctx, changed); err != nil {
		t.Fatalf("replace: %v", err)
	}
	insp, err = api.ContainerInspect(ctx, egressContainer(lane.Name), client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := insp.Container.Config.Env; !slices.Contains(got, egressEnvAllow+"=c.example.com") || !insp.Container.State.Running {
		t.Errorf("after a config change: env %q running %v", got, insp.Container.State.Running)
	}
}

// A lane network left open by an earlier configuration is never reused for an egress lane.
func TestSetupReplacesAnOpenLaneNetworkWithAnInternalOne(t *testing.T) {
	api := dockerAPI(t)
	open := testLane()
	open.Name = "egress-flip"
	l := newLauncher(t, api, []launcher.Lane{open})
	got, err := api.NetworkInspect(context.Background(), laneNetwork(open.Name), client.NetworkInspectOptions{})
	if err != nil || got.Network.Internal {
		t.Fatalf("setup: internal=%v err=%v", got.Network.Internal, err)
	}
	_ = l

	closed := open
	closed.Internal = true
	l2 := newLauncher(t, api, []launcher.Lane{closed})
	_ = l2
	got, err = api.NetworkInspect(context.Background(), laneNetwork(open.Name), client.NetworkInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Network.Internal {
		t.Error("the lane network is still an open bridge after the lane became internal")
	}
}
