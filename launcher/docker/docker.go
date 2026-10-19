package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"golang.org/x/sync/singleflight"

	"github.com/javatarz/satat/launcher"
)

const (
	cleanupTimeout = 30 * time.Second
	stopGrace      = 5
	logTailLines   = "5000"
	logMaxBytes    = 4 << 20
	outputMaxBytes = 64 << 20
	outputMaxFiles = 64
)

// Launcher runs steps as one-shot Docker containers. It talks to the Docker API only through the
// calls below; ADR 0027 limits who may hold that access.
type Launcher struct {
	api       *client.Client
	lanes     map[string]launcher.Lane
	lookupEnv func(string) (string, bool)
	log       *slog.Logger

	group     singleflight.Group
	mu        sync.Mutex
	cancelled map[string]bool
}

// Option adjusts a Launcher.
type Option func(*Launcher)

// WithEnv replaces os.LookupEnv as the source for lane PassEnv values.
func WithEnv(f func(string) (string, bool)) Option { return func(l *Launcher) { l.lookupEnv = f } }

// WithLogger sets the logger.
func WithLogger(lg *slog.Logger) Option { return func(l *Launcher) { l.log = lg } }

// New builds a Launcher for the given lanes. Every lane is checked against the hardening floor
// now, so a lane that could never launch is an error here.
func New(api *client.Client, lanes []launcher.Lane, opts ...Option) (*Launcher, error) {
	l := &Launcher{
		api:       api,
		lanes:     map[string]launcher.Lane{},
		lookupEnv: os.LookupEnv,
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		cancelled: map[string]bool{},
	}
	for _, o := range opts {
		o(l)
	}
	for _, lane := range lanes {
		n, err := normalizeLane(lane)
		if err != nil {
			return nil, err
		}
		if _, dup := l.lanes[n.Name]; dup {
			return nil, fmt.Errorf("lane %q is defined twice", n.Name)
		}
		l.lanes[n.Name] = n
	}
	return l, nil
}

// Setup creates each lane's network and pulls each lane's image if it is missing.
func (l *Launcher) Setup(ctx context.Context) error {
	for _, lane := range l.lanes {
		name := laneNetwork(lane.Name)
		if _, err := l.api.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); err != nil {
			if !cerrdefs.IsNotFound(err) {
				return fmt.Errorf("inspect network %s: %w", name, err)
			}
			_, err := l.api.NetworkCreate(ctx, name, client.NetworkCreateOptions{
				Driver:   "bridge",
				Internal: lane.Internal,
				Labels:   map[string]string{labelManaged: "true", labelLane: lane.Name},
			})
			if err != nil && !cerrdefs.IsConflict(err) {
				return fmt.Errorf("create network %s: %w", name, err)
			}
		}
		if _, err := l.api.ImageInspect(ctx, lane.Image); err != nil {
			if !cerrdefs.IsNotFound(err) {
				return fmt.Errorf("inspect image %s: %w", lane.Image, err)
			}
			pull, err := l.api.ImagePull(ctx, lane.Image, client.ImagePullOptions{})
			if err != nil {
				return fmt.Errorf("pull image %s: %w", lane.Image, err)
			}
			if err := pull.Wait(ctx); err != nil {
				return fmt.Errorf("pull image %s: %w", lane.Image, err)
			}
		}
	}
	return nil
}

// Launch implements launcher.Launcher.
func (l *Launcher) Launch(ctx context.Context, step launcher.Step) (launcher.Result, error) {
	lane, ok := l.lanes[step.Lane]
	if !ok {
		return launcher.Result{}, fmt.Errorf("unknown lane %q", step.Lane)
	}
	req, err := buildRequest(lane, step, l.lookupEnv)
	if err != nil {
		return launcher.Result{}, err
	}
	if err := ValidateRequest(req.config, req.host, req.networking); err != nil {
		return launcher.Result{}, fmt.Errorf("refusing to create %s: %w", req.name, err)
	}
	// Callers for one step in this process share one container. The first caller's context
	// governs the run; a joined caller sees its outcome.
	v, err, _ := l.group.Do(req.name, func() (any, error) {
		return l.run(ctx, lane, step, req)
	})
	res, _ := v.(launcher.Result)
	return res, err
}

func (l *Launcher) run(ctx context.Context, lane launcher.Lane, step launcher.Step, req request) (launcher.Result, error) {
	var res launcher.Result
	volume := inputVolume(step.Run, step.Name)

	// Remove the container and its volume on every exit path, even when ctx is already done.
	var id string
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		l.cleanup(cctx, req.name, id, volume)
	}()

	if _, err := l.api.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name:   volume,
		Labels: map[string]string{labelManaged: "true", labelRun: step.Run, labelStep: step.Name},
	}); err != nil {
		return res, fmt.Errorf("create input volume: %w", err)
	}

	created, err := l.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: req.name, Config: req.config, HostConfig: req.host, NetworkingConfig: req.networking,
	})
	switch {
	case err == nil:
		id = created.ID
	case cerrdefs.IsConflict(err):
		// An earlier attempt created it: adopt, but only if it is ours.
		found, ierr := l.api.ContainerInspect(ctx, req.name, client.ContainerInspectOptions{})
		if ierr != nil {
			return res, fmt.Errorf("adopt %s: %w", req.name, ierr)
		}
		lb := found.Container.Config.Labels
		if lb[labelManaged] != "true" || lb[labelRun] != step.Run || lb[labelStep] != step.Name {
			return res, fmt.Errorf("container %s exists and is not a Satat step container", req.name)
		}
		id = found.Container.ID
		res.Adopted = true
		l.log.Info("adopted existing container", "container", req.name, "state", found.Container.State.Status)
	default:
		return res, fmt.Errorf("create %s: %w", req.name, err)
	}

	insp, err := l.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return res, fmt.Errorf("inspect %s: %w", req.name, err)
	}
	if insp.Container.State.Status == container.StateCreated {
		if err := l.putInput(ctx, id, step.Input, lane.User); err != nil {
			return res, err
		}
		if _, err := l.api.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
			return res, fmt.Errorf("start %s: %w", req.name, err)
		}
	}

	if err := l.wait(ctx, id, req.name, lane.Timeout, &res); err != nil {
		return res, err
	}

	fin, err := l.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return res, fmt.Errorf("inspect %s after exit: %w", req.name, err)
	}
	res.ExitCode = fin.Container.State.ExitCode
	res.OOMKilled = fin.Container.State.OOMKilled
	if err := l.logs(ctx, id, &res); err != nil {
		return res, err
	}
	if err := l.collect(ctx, id, &res); err != nil {
		return res, err
	}

	l.mu.Lock()
	res.Cancelled = l.cancelled[req.name]
	delete(l.cancelled, req.name)
	l.mu.Unlock()
	return res, nil
}

// wait blocks until the container stops. A step that outlives its lane timeout, or whose caller
// gives up, is stopped here; the caller's cleanup then removes it.
func (l *Launcher) wait(ctx context.Context, id, name string, timeout time.Duration, res *launcher.Result) error {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	w := l.api.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case <-w.Result:
		return nil
	case err := <-w.Error:
		return fmt.Errorf("wait %s: %w", name, err)
	case <-tctx.Done():
		l.stop(context.WithoutCancel(ctx), id)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res.TimedOut = true
		return nil
	}
}

func (l *Launcher) stop(ctx context.Context, id string) {
	cctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	grace := stopGrace
	if _, err := l.api.ContainerStop(cctx, id, client.ContainerStopOptions{Timeout: &grace}); err != nil && !cerrdefs.IsNotFound(err) {
		l.log.Warn("stop container", "id", id, "err", err)
	}
}

func (l *Launcher) putInput(ctx context.Context, id string, input []byte, user string) error {
	uid, _ := strconv.Atoi(uidOf(user))
	gid, _ := strconv.Atoi(gidOf(user))
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// The output directory is created owned by the step's user: a new volume is root-owned and
	// the step is not root.
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: outputName + "/", Mode: 0o700, Uid: uid, Gid: gid, ModTime: time.Unix(0, 0)}); err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "input.json", Mode: 0o444, Size: int64(len(input)), ModTime: time.Unix(0, 0)}); err != nil {
		return err
	}
	if _, err := tw.Write(input); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if _, err := l.api.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: inputDir, Content: &buf}); err != nil {
		return fmt.Errorf("copy input: %w", err)
	}
	return nil
}

func (l *Launcher) logs(ctx context.Context, id string, res *launcher.Result) error {
	rc, err := l.api.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: logTailLines})
	if err != nil {
		return fmt.Errorf("read logs: %w", err)
	}
	defer rc.Close()
	var stdout, stderr capped
	if _, err := stdcopy.StdCopy(&stdout, &stderr, rc); err != nil {
		return fmt.Errorf("read logs: %w", err)
	}
	res.Stdout, res.Stderr = stdout.buf.Bytes(), stderr.buf.Bytes()
	res.LogsTruncated = stdout.truncated || stderr.truncated
	return nil
}

// collect reads the output directory out of the stopped container. Only regular files are kept,
// at most outputMaxFiles and outputMaxBytes in all; anything else in the archive is ignored.
func (l *Launcher) collect(ctx context.Context, id string, res *launcher.Result) error {
	got, err := l.api.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: outputDir})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read output: %w", err)
	}
	defer got.Content.Close()
	res.Files = map[string][]byte{}
	var total int64
	tr := tar.NewReader(got.Content)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read output: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		rel := path.Clean(strings.TrimPrefix(h.Name, outputName+"/"))
		if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
			continue
		}
		if len(res.Files) >= outputMaxFiles || total+h.Size > outputMaxBytes {
			res.FilesTruncated = true
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return fmt.Errorf("read output %s: %w", rel, err)
		}
		total += int64(len(b))
		res.Files[rel] = b
	}
}

// capped keeps the first logMaxBytes written to it and drops the rest.
type capped struct {
	buf       bytes.Buffer
	truncated bool
}

func (c *capped) Write(p []byte) (int, error) {
	room := logMaxBytes - c.buf.Len()
	if room < len(p) {
		c.truncated = true
		p = p[:max(room, 0)]
	}
	c.buf.Write(p)
	return len(p), nil
}

// cleanup removes the container and the input volume. Errors other than "already gone" are logged;
// the reaper catches what this misses.
func (l *Launcher) cleanup(ctx context.Context, name, id, volume string) {
	// Only a container this call created or adopted is removed, and only by its ID: a name that
	// belongs to something else is never touched.
	if id != "" {
		if _, err := l.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !cerrdefs.IsNotFound(err) {
			l.log.Warn("remove container", "container", name, "err", err)
		}
	}
	if _, err := l.api.VolumeRemove(ctx, volume, client.VolumeRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		l.log.Warn("remove volume", "volume", volume, "err", err)
	}
}

// Cancel implements launcher.Launcher.
func (l *Launcher) Cancel(ctx context.Context, run, name string) error {
	cname := containerName(run, name)
	insp, err := l.api.ContainerInspect(ctx, cname, client.ContainerInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("cancel %s: %w", cname, err)
	}
	if lb := insp.Container.Config.Labels; lb[labelManaged] != "true" {
		return fmt.Errorf("container %s is not a Satat step container", cname)
	}
	if !insp.Container.State.Running {
		return nil
	}
	l.mu.Lock()
	l.cancelled[cname] = true
	l.mu.Unlock()
	l.stop(ctx, insp.Container.ID)
	return nil
}

// Reap removes Satat containers and volumes created more than maxAge ago, whatever their state.
// maxAge must exceed the longest lane timeout, so a step a restarted worker could still adopt is
// left alone. It removes by label, one object at a time, never with a prune call.
func (l *Launcher) Reap(ctx context.Context, maxAge time.Duration) (int, error) {
	cutoff := time.Now().Add(-maxAge)
	list, err := l.api.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("label", labelManaged+"=true")})
	if err != nil {
		return 0, fmt.Errorf("list containers: %w", err)
	}
	var removed int
	var errs []error
	for _, c := range list.Items {
		if time.Unix(c.Created, 0).After(cutoff) {
			continue
		}
		if _, err := l.api.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !cerrdefs.IsNotFound(err) {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	vols, err := l.api.VolumeList(ctx, client.VolumeListOptions{Filters: make(client.Filters).Add("label", labelManaged+"=true")})
	if err != nil {
		return removed, errors.Join(append(errs, fmt.Errorf("list volumes: %w", err))...)
	}
	for _, v := range vols.Items {
		created, perr := time.Parse(time.RFC3339, v.CreatedAt)
		if perr != nil || created.After(cutoff) {
			continue
		}
		if _, err := l.api.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) && !cerrdefs.IsConflict(err) {
			errs = append(errs, err)
		}
	}
	return removed, errors.Join(errs...)
}

// RunReaper calls Reap now and then every interval until ctx ends.
func (l *Launcher) RunReaper(ctx context.Context, interval, maxAge time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if n, err := l.Reap(ctx, maxAge); err != nil {
			l.log.Warn("reap", "err", err)
		} else if n > 0 {
			l.log.Info("reaped containers", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

var _ launcher.Launcher = (*Launcher)(nil)
