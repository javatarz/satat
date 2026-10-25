package docker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/javatarz/satat/launcher"
	"github.com/javatarz/satat/launcher/conformance"
)

// dockerAPI connects to the engine named by DOCKER_HOST, or skips. The tests create real
// containers, so they are skipped with -short.
func dockerAPI(t *testing.T) *client.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	api, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("no Docker client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := api.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("Docker is not reachable: %v", err)
	}
	t.Cleanup(func() { api.Close() })
	return api
}

func newLauncher(t *testing.T, api *client.Client, lanes []launcher.Lane, opts ...Option) *Launcher {
	t.Helper()
	l, err := New(api, lanes, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := l.Setup(ctx); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() {
		// Nothing a test started may outlive it.
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := l.Reap(cctx, 0); err != nil {
			t.Logf("cleanup reap: %v", err)
		}
		for _, lane := range lanes {
			_, _ = api.NetworkRemove(cctx, laneNetwork(lane.Name), client.NetworkRemoveOptions{})
		}
	})
	return l
}

func TestConformance(t *testing.T) {
	api := dockerAPI(t)
	conformance.Run(t, conformance.Config{
		Image: pinned,
		New: func(t *testing.T, lanes []launcher.Lane) launcher.Launcher {
			return newLauncher(t, api, lanes)
		},
	})
}

func shLane(name, script string) launcher.Lane {
	l := testLane()
	l.Name = name
	l.Command = []string{"sh", "-c", script}
	return l
}

func names(t *testing.T, api *client.Client) []string {
	t.Helper()
	list, err := api.ContainerList(context.Background(), client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("label", labelManaged+"=true")})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range list.Items {
		out = append(out, c.Names...)
	}
	return out
}

func TestContainerAndVolumeAreRemovedAfterEveryOutcome(t *testing.T) {
	api := dockerAPI(t)
	withCaches := func(x launcher.Lane) launcher.Lane {
		x.Caches = []launcher.Cache{{Name: "a", Path: "/cache/a"}, {Name: "b", Path: "/cache/b"}}
		return x
	}
	l := newLauncher(t, api, []launcher.Lane{
		withCaches(shLane("ok", "true")), withCaches(shLane("fail", "exit 9")),
		func() launcher.Lane { x := withCaches(shLane("slow", "sleep 60")); x.Timeout = time.Second; return x }(),
	})
	for _, lane := range []string{"ok", "fail", "slow"} {
		if _, err := l.Launch(context.Background(), launcher.Step{Run: "clean", Name: lane, Lane: lane}); err != nil {
			t.Fatalf("%s: %v", lane, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = l.Launch(ctx, launcher.Step{Run: "clean", Name: "ctx", Lane: "slow"})

	if left := names(t, api); len(left) != 0 {
		t.Errorf("containers left behind: %v", left)
	}
	vols, err := api.VolumeList(context.Background(), client.VolumeListOptions{Filters: make(client.Filters).Add("label", labelManaged+"=true")})
	if err != nil {
		t.Fatal(err)
	}
	if len(vols.Items) != 0 {
		t.Errorf("volumes left behind: %v", vols.Items)
	}
}

// A worker that died after starting a step leaves its container behind. The next attempt for the
// same step must pick it up, running or finished, and must not run it a second time.
func TestAdoptsContainerLeftByADeadWorker(t *testing.T) {
	for _, state := range []string{"running", "finished"} {
		t.Run(state, func(t *testing.T) {
			api := dockerAPI(t)
			lane := shLane("adopt", "cat /proc/sys/kernel/random/uuid; sleep 3")
			l := newLauncher(t, api, []launcher.Lane{lane})
			step := launcher.Step{Run: "crash", Name: state, Lane: "adopt", Input: []byte("{}")}

			// Simulate the dead worker: do the first half of run() by hand and walk away.
			n, _ := normalizeLane(lane)
			req, err := buildRequest(n, step, l.lookupEnv)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := api.VolumeCreate(ctx, inputVolumeOptions(step)); err != nil {
				t.Fatal(err)
			}
			created, err := api.ContainerCreate(ctx, client.ContainerCreateOptions{Name: req.name, Config: req.config, HostConfig: req.host, NetworkingConfig: req.networking})
			if err != nil {
				t.Fatal(err)
			}
			if err := l.putInput(ctx, created.ID, step.Input, lane.User); err != nil {
				t.Fatal(err)
			}
			if _, err := api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
				t.Fatal(err)
			}
			if state == "finished" {
				time.Sleep(4 * time.Second)
			}

			res, err := l.Launch(ctx, step)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Adopted || res.ExitCode != 0 || len(res.Stdout) == 0 {
				t.Errorf("want an adopted, successful result with output, got %+v", res)
			}
			if left := names(t, api); len(left) != 0 {
				t.Errorf("adopted container was not removed: %v", left)
			}
		})
	}
}

func TestRefusesToAdoptAContainerThatIsNotOurs(t *testing.T) {
	api := dockerAPI(t)
	l := newLauncher(t, api, []launcher.Lane{shLane("own", "true")})
	ctx := context.Background()
	// Same name a step would use, but created without the Satat labels.
	created, err := api.ContainerCreate(ctx, client.ContainerCreateOptions{Name: "satat-theirs-x", Config: containerConfigFor(pinned)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = api.ContainerRemove(ctx, created.ID, client.ContainerRemoveOptions{Force: true}) })

	_, err = l.Launch(ctx, launcher.Step{Run: "theirs", Name: "x", Lane: "own"})
	if err == nil {
		t.Fatal("Launch adopted a container it did not create")
	}
	if _, ierr := api.ContainerInspect(ctx, created.ID, client.ContainerInspectOptions{}); ierr != nil {
		t.Errorf("the foreign container was removed: %v", ierr)
	}
}

// A cache is a disk-backed mount of its own at its declared path, labelled as a cache.
func TestCachesAreDiskVolumesAtTheirPaths(t *testing.T) {
	api := dockerAPI(t)
	lane := shLane("cache", `grep -E ' /cache/(a|b) ' /proc/mounts; sleep 2`)
	lane.Caches = []launcher.Cache{{Name: "a", Path: "/cache/a"}, {Name: "b", Path: "/cache/b"}}
	l := newLauncher(t, api, []launcher.Lane{lane})
	ctx := context.Background()

	type out struct {
		res launcher.Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := l.Launch(ctx, launcher.Step{Run: "ct", Name: "s", Lane: "cache"})
		done <- out{res, err}
	}()
	var labels map[string]string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && labels == nil; time.Sleep(100 * time.Millisecond) {
		if v, err := api.VolumeInspect(ctx, "satat-ct-s-cache-a", client.VolumeInspectOptions{}); err == nil {
			labels = v.Volume.Labels
		}
	}
	o := <-done
	if o.err != nil {
		t.Fatal(o.err)
	}
	if labels[labelKind] != kindCache || labels[labelLane] != "cache" || labels[labelManaged] != "true" {
		t.Errorf("cache volume labels = %v", labels)
	}
	lines := strings.Split(strings.TrimSpace(string(o.res.Stdout)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two cache mounts, got %q (stderr %q)", o.res.Stdout, o.res.Stderr)
	}
	for _, ln := range lines {
		if f := strings.Fields(ln); len(f) < 3 || f[2] == "tmpfs" {
			t.Errorf("cache mount is not disk-backed: %q", ln)
		}
	}
	vols, err := api.VolumeList(ctx, client.VolumeListOptions{Filters: make(client.Filters).Add("label", labelKind+"="+kindCache)})
	if err != nil {
		t.Fatal(err)
	}
	if len(vols.Items) != 0 {
		t.Errorf("per-step cache volumes left behind: %v", vols.Items)
	}
}

// A volume with a Satat name that the launcher did not create, here a host bind in disguise, must
// not be mounted into a step, and must not be removed as if it were the step's own.
func TestRefusesToAdoptAVolumeThatIsNotOurs(t *testing.T) {
	api := dockerAPI(t)
	l := newLauncher(t, api, []launcher.Lane{shLane("ownvol", "true")})
	ctx := context.Background()
	step := launcher.Step{Run: "theirs", Name: "v", Lane: "ownvol"}
	name := inputVolume(step.Run, step.Name)
	if _, err := api.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name: name, Driver: "local", DriverOpts: map[string]string{"type": "none", "o": "bind", "device": "/tmp"},
		Labels: inputVolumeOptions(step).Labels,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: true}) })

	if _, err := l.Launch(ctx, step); err == nil {
		t.Fatal("Launch mounted a volume it did not create")
	}
	if _, err := api.VolumeInspect(ctx, name, client.VolumeInspectOptions{}); err != nil {
		t.Errorf("the foreign volume was removed: %v", err)
	}
	if left := names(t, api); len(left) != 0 {
		t.Errorf("a container was created: %v", left)
	}
}

func TestPassEnvReachesTheStepAndOnlyThat(t *testing.T) {
	api := dockerAPI(t)
	lane := shLane("env", `echo "token=$MY_TOKEN"; echo "host=$DOCKER_HOST"`)
	lane.PassEnv = []string{"MY_TOKEN"}
	env := map[string]string{"MY_TOKEN": "abc123", "UNRELATED_SECRET": "nope"}
	l := newLauncher(t, api, []launcher.Lane{lane}, WithEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok }))
	res, err := l.Launch(context.Background(), launcher.Step{Run: "env", Name: "e", Lane: "env"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "token=abc123\nhost=\n" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestReapRemovesOldContainersAndKeepsNewOnes(t *testing.T) {
	api := dockerAPI(t)
	lane := shLane("reap", "true")
	l := newLauncher(t, api, []launcher.Lane{lane})
	ctx := context.Background()
	n, _ := normalizeLane(lane)
	step := launcher.Step{Run: "reap", Name: "old", Lane: "reap"}
	req, _ := buildRequest(n, step, l.lookupEnv)
	if _, err := api.VolumeCreate(ctx, inputVolumeOptions(step)); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ContainerCreate(ctx, client.ContainerCreateOptions{Name: req.name, Config: req.config, HostConfig: req.host, NetworkingConfig: req.networking}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)

	if n, err := l.Reap(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("Reap(1h) = %d, %v; a fresh container must be kept", n, err)
	}
	if n, err := l.Reap(ctx, time.Second); err != nil || n != 1 {
		t.Fatalf("Reap(1s) = %d, %v; the old container must go", n, err)
	}
	if left := names(t, api); len(left) != 0 {
		t.Errorf("left after reap: %v", left)
	}
}

func containerConfigFor(image string) *container.Config {
	return &container.Config{Image: image, Cmd: []string{"true"}}
}
