package docker

import (
	"context"
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
	l := newLauncher(t, api, []launcher.Lane{
		shLane("ok", "true"), shLane("fail", "exit 9"),
		func() launcher.Lane { x := shLane("slow", "sleep 60"); x.Timeout = time.Second; return x }(),
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
			if _, err := api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: inputVolume(step.Run, step.Name), Labels: map[string]string{labelManaged: "true"}}); err != nil {
				t.Fatal(err)
			}
			created, err := api.ContainerCreate(ctx, client.ContainerCreateOptions{Name: req.name, Config: req.config, HostConfig: req.host, NetworkingConfig: req.networking})
			if err != nil {
				t.Fatal(err)
			}
			if err := l.putInput(ctx, created.ID, step.Input); err != nil {
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
	req, _ := buildRequest(n, launcher.Step{Run: "reap", Name: "old", Lane: "reap"}, l.lookupEnv)
	if _, err := api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: inputVolume("reap", "old"), Labels: map[string]string{labelManaged: "true"}}); err != nil {
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
