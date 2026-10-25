package docker

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
)

const gib = 1 << 30

var (
	now   = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
)

// fakeVolumes is the Docker volume API over an in-memory list.
type fakeVolumes struct {
	items   []volume.Volume
	removed []string
	held    map[string]bool // VolumeRemove answers "in use"
}

func (f *fakeVolumes) VolumeCreate(_ context.Context, o client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
	for _, v := range f.items {
		if v.Name == o.Name {
			return client.VolumeCreateResult{Volume: v}, nil
		}
	}
	v := volume.Volume{Name: o.Name, Driver: "local", Labels: o.Labels, CreatedAt: now.Format(time.RFC3339)}
	f.items = append(f.items, v)
	return client.VolumeCreateResult{Volume: v}, nil
}

func (f *fakeVolumes) VolumeList(_ context.Context, o client.VolumeListOptions) (client.VolumeListResult, error) {
	var out client.VolumeListResult
	for _, v := range f.items {
		ok := true
		for want := range o.Filters["label"] {
			k, val, _ := strings.Cut(want, "=")
			ok = ok && v.Labels[k] == val
		}
		if ok {
			out.Items = append(out.Items, v)
		}
	}
	return out, nil
}

func (f *fakeVolumes) VolumeRemove(_ context.Context, name string, _ client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
	if f.held[name] {
		return client.VolumeRemoveResult{}, cerrdefs.ErrConflict.WithMessage("volume is in use")
	}
	for i, v := range f.items {
		if v.Name == name {
			f.items = append(f.items[:i], f.items[i+1:]...)
			f.removed = append(f.removed, name)
			return client.VolumeRemoveResult{}, nil
		}
	}
	return client.VolumeRemoveResult{}, cerrdefs.ErrNotFound
}

func (f *fakeVolumes) DiskUsage(context.Context, client.DiskUsageOptions) (client.DiskUsageResult, error) {
	var r client.DiskUsageResult
	r.Volumes.Items = f.items
	return r, nil
}

// cache adds a persistent cache volume for (lane, scope) created ago, of size bytes, attached
// to refs containers.
func (f *fakeVolumes) cache(lane, scope, name string, ago time.Duration, size, refs int64) {
	f.items = append(f.items, volume.Volume{
		Name: cacheKeyPrefix(lane, scope) + name, Driver: "local", CreatedAt: now.Add(-ago).Format(time.RFC3339),
		Labels:    map[string]string{labelManaged: "true", labelKind: kindCache, labelLane: lane, labelScope: scope, labelCache: name},
		UsageData: &volume.UsageData{Size: size, RefCount: refs},
	})
}

// used records a use of (lane, scope) ago.
func (f *fakeVolumes) used(lane, scope string, ago time.Duration) {
	at := now.Add(-ago)
	f.items = append(f.items, volume.Volume{
		Name: cacheUseName(lane, scope, at), Driver: "local", CreatedAt: at.Format(time.RFC3339),
		Labels: map[string]string{labelManaged: "true", labelKind: kindCacheUse, labelLane: lane, labelScope: scope},
	})
}

func (f *fakeVolumes) reap(t *testing.T, p CachePolicy) {
	t.Helper()
	var mu sync.RWMutex
	if _, err := reapCaches(context.Background(), f, &mu, quiet, now, p); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeVolumes) has(name string) bool {
	for _, v := range f.items {
		if v.Name == name {
			return true
		}
	}
	return false
}

const day = 24 * time.Hour

func TestCacheReaperEvictsUnusedKeys(t *testing.T) {
	f := &fakeVolumes{}
	f.cache("claude", "old", "go-mod", 20*day, gib, 0)
	f.cache("claude", "old", "pip", 20*day, gib, 0)
	f.used("claude", "old", 15*day)
	f.cache("claude", "fresh", "go-mod", 20*day, gib, 0)
	f.used("claude", "fresh", day)
	// No marker: its newest volume's creation is its last use.
	f.cache("claude", "nomark", "go-mod", 3*day, gib, 0)
	f.reap(t, CachePolicy{MaxUnused: 14 * day})

	for _, gone := range []string{"satat-cache-claude-old-go-mod", "satat-cache-claude-old-pip", cacheUseName("claude", "old", now.Add(-15*day))} {
		if f.has(gone) {
			t.Errorf("%s survived 15 days unused", gone)
		}
	}
	for _, kept := range []string{"satat-cache-claude-fresh-go-mod", cacheUseName("claude", "fresh", now.Add(-day)), "satat-cache-claude-nomark-go-mod"} {
		if !f.has(kept) {
			t.Errorf("%s was evicted though used recently", kept)
		}
	}
}

func TestCacheReaperEnforcesTheSizeCapLeastRecentlyUsedFirst(t *testing.T) {
	f := &fakeVolumes{}
	for i, scope := range []string{"a", "b", "c", "d"} {
		f.cache("claude", scope, "go-build", 30*day, 2*gib, 0)
		f.cache("claude", scope, "go-mod", 30*day, gib, 0)
		f.used("claude", scope, time.Duration(4-i)*day) // a is the least recently used
	}
	// 12 GiB in all; a 7 GiB cap needs two keys gone.
	f.reap(t, CachePolicy{MaxUnused: 14 * day, MaxBytes: 7 * gib})

	want := []string{"satat-cache-claude-a-go-build", "satat-cache-claude-a-go-mod", "satat-cache-claude-b-go-build", "satat-cache-claude-b-go-mod"}
	var gotCaches []string
	for _, r := range f.removed {
		if strings.HasPrefix(r, "satat-cache-") {
			gotCaches = append(gotCaches, r)
		}
	}
	if strings.Join(gotCaches, " ") != strings.Join(want, " ") {
		t.Errorf("evicted %v, want %v in that order", gotCaches, want)
	}
	if !f.has("satat-cache-claude-c-go-mod") || !f.has("satat-cache-claude-d-go-mod") {
		t.Error("evicted more than the cap needed")
	}
}

func TestCacheReaperNeverEvictsAttachedCaches(t *testing.T) {
	f := &fakeVolumes{}
	// Oldest and over every limit, but one of its volumes is attached to a container.
	f.cache("claude", "busy", "go-mod", 60*day, 5*gib, 0)
	f.cache("claude", "busy", "pip", 60*day, gib, 1)
	f.used("claude", "busy", 60*day)
	f.cache("claude", "idle", "go-mod", 10*day, gib, 0)
	f.used("claude", "idle", 2*day)
	// Size unknown: treated as in use too.
	f.items = append(f.items, volume.Volume{
		Name: "satat-cache-claude-unknown-go-mod", CreatedAt: now.Add(-60 * day).Format(time.RFC3339),
		Labels: map[string]string{labelManaged: "true", labelKind: kindCache, labelLane: "claude", labelScope: "unknown", labelCache: "go-mod"},
	})
	f.reap(t, CachePolicy{MaxUnused: 14 * day, MaxBytes: 3 * gib})

	if !f.has("satat-cache-claude-busy-go-mod") || !f.has("satat-cache-claude-busy-pip") {
		t.Error("a key with an attached volume lost a volume")
	}
	if !f.has("satat-cache-claude-unknown-go-mod") {
		t.Error("a volume with unknown usage was evicted")
	}
	// The attached key keeps the total over the cap, so the idle key goes instead.
	if f.has("satat-cache-claude-idle-go-mod") {
		t.Error("the size cap did not fall back to the next unattached key")
	}
}

// A step that attaches a cache after the usage was read makes Docker refuse the removal. The
// reaper then keeps the key's marker and does not report an error.
func TestCacheReaperKeepsAKeyDockerRefusesToRemove(t *testing.T) {
	f := &fakeVolumes{held: map[string]bool{"satat-cache-claude-race-go-mod": true}}
	f.cache("claude", "race", "go-mod", 30*day, gib, 0)
	f.used("claude", "race", 30*day)
	f.reap(t, CachePolicy{MaxUnused: 14 * day})
	if !f.has(cacheUseName("claude", "race", now.Add(-30*day))) {
		t.Error("the marker of a key still in use was removed")
	}
}

func TestCacheReaperRemovesStepLeftoversAndOrphanMarkers(t *testing.T) {
	f := &fakeVolumes{}
	step := func(name string, ago time.Duration, refs int64) {
		f.items = append(f.items, volume.Volume{
			Name: name, CreatedAt: now.Add(-ago).Format(time.RFC3339),
			Labels:    map[string]string{labelManaged: "true", labelKind: kindCache, labelLane: "claude", labelRun: "r", labelStep: "s", labelCache: "go-mod"},
			UsageData: &volume.UsageData{RefCount: refs},
		})
	}
	step("satat-old-s-cache-go-mod", 3*time.Hour, 0)
	step("satat-new-s-cache-go-mod", 10*time.Minute, 0)
	step("satat-busy-s-cache-go-mod", 3*time.Hour, 1)
	f.used("claude", "gone", day) // its caches were removed by hand
	f.reap(t, CachePolicy{MaxUnused: 14 * day, StepAge: 2 * time.Hour})

	if f.has("satat-old-s-cache-go-mod") || f.has(cacheUseName("claude", "gone", now.Add(-day))) {
		t.Errorf("leftovers kept: %v", f.items)
	}
	if !f.has("satat-new-s-cache-go-mod") || !f.has("satat-busy-s-cache-go-mod") {
		t.Errorf("a running step's cache was removed: %v", f.removed)
	}
}

func TestCacheReaperLeavesOtherVolumesAlone(t *testing.T) {
	f := &fakeVolumes{}
	f.items = append(f.items,
		volume.Volume{Name: "satat-r-s-in", CreatedAt: now.Add(-60 * day).Format(time.RFC3339), Labels: map[string]string{labelManaged: "true", labelKind: kindInput}, UsageData: &volume.UsageData{Size: 100 * gib}},
		volume.Volume{Name: "postgres-data", CreatedAt: now.Add(-60 * day).Format(time.RFC3339), Labels: map[string]string{labelKind: kindCache}, UsageData: &volume.UsageData{Size: 100 * gib}},
	)
	f.reap(t, CachePolicy{MaxUnused: day, MaxBytes: 1, StepAge: time.Hour})
	if len(f.removed) != 0 {
		t.Errorf("removed %v, which are not Satat caches", f.removed)
	}
}

// Touching a key leaves exactly one marker, the newest, and touches no other key.
func TestCacheUseKeepsOneMarkerPerKey(t *testing.T) {
	f := &fakeVolumes{}
	f.used("claude", "a", 3*day)
	f.used("claude", "a", 2*day)
	f.used("claude", "b", 2*day)
	touchCacheKey(context.Background(), f, quiet, "claude", "a", now)

	var a, b []string
	for _, v := range f.items {
		switch v.Labels[labelScope] {
		case "a":
			a = append(a, v.Name)
		case "b":
			b = append(b, v.Name)
		}
	}
	if len(a) != 1 || a[0] != cacheUseName("claude", "a", now) {
		t.Errorf("markers for a = %v, want only the newest", a)
	}
	if len(b) != 1 {
		t.Errorf("touching a changed b: %v", b)
	}
}
