package docker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
)

// Persistent caches (ADR 0033, stage 2) live until the cache reaper evicts them, by age and by
// total size. The step reaper (Reap) never touches them.
//
// Last use. Docker records only when a volume was created, and a local volume's labels cannot be
// changed afterwards, so the launcher records use itself. Each launch on a persistent cache key
// creates an empty marker volume (kind cache-use) labelled with the key and removes the key's
// older markers; the newest marker's CreatedAt, stamped by the daemon, is the key's last use.
// The record lives in the daemon next to the caches it describes, so it survives worker
// restarts (the worker's root is read-only and its /tmp is a tmpfs) without a state volume, a
// reset daemon loses both together, and nothing is ever mounted to read it. A key with no
// marker, for example one created before markers existed, counts as used when its newest volume
// was created.

// CachePolicy is what the cache reaper keeps.
type CachePolicy struct {
	// MaxUnused evicts a persistent cache key not used for longer than this. Zero keeps keys
	// whatever their age.
	MaxUnused time.Duration
	// MaxBytes caps the total size of all cache volumes. Over it, least recently used keys go
	// first. Zero is no cap.
	MaxBytes int64
	// StepAge removes a per-step cache volume older than this, which only a crashed step leaves
	// behind. It should be the step reaper's age. Zero leaves them to MaxUnused and MaxBytes.
	StepAge time.Duration
}

// volumeAPI is the part of the Docker API that volumes and the cache reaper use. *client.Client
// has it; tests fake it.
type volumeAPI interface {
	VolumeCreate(context.Context, client.VolumeCreateOptions) (client.VolumeCreateResult, error)
	VolumeList(context.Context, client.VolumeListOptions) (client.VolumeListResult, error)
	VolumeRemove(context.Context, string, client.VolumeRemoveOptions) (client.VolumeRemoveResult, error)
	DiskUsage(context.Context, client.DiskUsageOptions) (client.DiskUsageResult, error)
}

func cacheUseName(lane, scope string, at time.Time) string {
	return resourcePrefix + "cacheuse-" + lane + "-" + scope + "-" + strconv.FormatInt(at.UnixNano(), 10)
}

// touchCacheKey records a use of the persistent cache key (lane, scope). A failure is logged, not
// returned: the worst it does is let the reaper evict a cache early.
func touchCacheKey(ctx context.Context, api volumeAPI, log *slog.Logger, lane, scope string, now time.Time) {
	labels := map[string]string{labelManaged: "true", labelKind: kindCacheUse, labelLane: lane, labelScope: scope}
	name := cacheUseName(lane, scope, now)
	if err := createVolume(ctx, api, client.VolumeCreateOptions{Name: name, Labels: labels}); err != nil {
		log.Warn("record cache use", "lane", lane, "scope", scope, "err", err)
		return
	}
	f := make(client.Filters).Add("label", labelKind+"="+kindCacheUse, labelLane+"="+lane, labelScope+"="+scope)
	old, err := api.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		log.Warn("list cache use markers", "lane", lane, "scope", scope, "err", err)
		return
	}
	for _, v := range old.Items {
		if v.Name == name || v.Labels[labelManaged] != "true" {
			continue
		}
		if _, err := api.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
			log.Warn("remove old cache use marker", "volume", v.Name, "err", err)
		}
	}
}

// cacheKey is everything the reaper knows about one key: a persistent (lane, scope), or one
// volume a crashed step left behind.
type cacheKey struct {
	id       string
	leftover bool
	volumes  []string
	markers  []string
	created  time.Time // newest volume
	used     time.Time // newest marker
	size     int64
	inUse    bool
}

func (k *cacheKey) lastUse() time.Time {
	if !k.used.IsZero() {
		return k.used
	}
	return k.created
}

type eviction struct {
	key    *cacheKey
	reason string
}

// planCacheEviction decides which keys go, in order: every key over the age limits, then least
// recently used keys until the total fits under MaxBytes. A key with any volume attached to a
// container, or whose usage Docker did not report, is never chosen.
func planCacheEviction(items []volume.Volume, now time.Time, p CachePolicy) []eviction {
	keys := map[string]*cacheKey{}
	get := func(id string, leftover bool) *cacheKey {
		if keys[id] == nil {
			keys[id] = &cacheKey{id: id, leftover: leftover}
		}
		return keys[id]
	}
	for _, v := range items {
		lb := v.Labels
		if lb[labelManaged] != "true" {
			continue
		}
		created, err := time.Parse(time.RFC3339, v.CreatedAt)
		if err != nil {
			created = now // unknown age: treat as new
		}
		switch lb[labelKind] {
		case kindCache:
			k := get(lb[labelLane]+"/"+lb[labelScope], false)
			if lb[labelScope] == "" {
				k = get("step/"+v.Name, true)
			}
			k.volumes = append(k.volumes, v.Name)
			if created.After(k.created) {
				k.created = created
			}
			u := v.UsageData
			if u == nil || u.RefCount != 0 {
				k.inUse = true
			}
			if u != nil && u.Size > 0 {
				k.size += u.Size
			}
		case kindCacheUse:
			k := get(lb[labelLane]+"/"+lb[labelScope], false)
			k.markers = append(k.markers, v.Name)
			if created.After(k.used) {
				k.used = created
			}
		}
	}

	var plan []eviction
	var rest []*cacheKey
	var total int64
	for _, id := range sortedIDs(keys) {
		k := keys[id]
		switch {
		case k.inUse:
		case len(k.volumes) == 0:
			// Markers whose caches are gone.
			plan = append(plan, eviction{k, "no volumes"})
			continue
		case k.leftover && p.StepAge > 0 && now.Sub(k.created) > p.StepAge:
			plan = append(plan, eviction{k, "left by a step"})
			continue
		case !k.leftover && p.MaxUnused > 0 && now.Sub(k.lastUse()) > p.MaxUnused:
			plan = append(plan, eviction{k, "unused"})
			continue
		}
		// Kept for now; an in-use key still counts towards the total.
		total += k.size
		if !k.inUse {
			rest = append(rest, k)
		}
	}
	if p.MaxBytes <= 0 {
		return plan
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].lastUse().Before(rest[j].lastUse()) })
	for _, k := range rest {
		if total <= p.MaxBytes {
			break
		}
		plan = append(plan, eviction{k, "over size cap"})
		total -= k.size
	}
	return plan
}

// reapCaches evicts what planCacheEviction chooses. Each key is removed under mu, which Launch
// holds from creating a step's volumes to creating its container, so a cache cannot go between
// being adopted and being attached; once attached, Docker itself refuses to remove it.
func reapCaches(ctx context.Context, api volumeAPI, mu *sync.RWMutex, log *slog.Logger, now time.Time, p CachePolicy) (int, error) {
	du, err := api.DiskUsage(ctx, client.DiskUsageOptions{Volumes: true, Verbose: true})
	if err != nil {
		return 0, fmt.Errorf("volume disk usage: %w", err)
	}
	var removed int
	var errs []error
	for _, e := range planCacheEviction(du.Volumes.Items, now, p) {
		n, err := evictCacheKey(ctx, api, mu, e.key)
		removed += n
		if err != nil {
			errs = append(errs, err)
			continue
		}
		log.Info("evicted cache", "key", e.key.id, "reason", e.reason, "bytes", e.key.size, "last_use", e.key.lastUse())
	}
	return removed, errors.Join(errs...)
}

func evictCacheKey(ctx context.Context, api volumeAPI, mu *sync.RWMutex, k *cacheKey) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	var removed int
	for _, name := range k.volumes {
		// No Force: Docker refuses to remove a volume a container holds, which is the last
		// guard if a step attached it after the usage was read.
		if _, err := api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{}); err != nil {
			if cerrdefs.IsNotFound(err) {
				continue
			}
			if cerrdefs.IsConflict(err) {
				// In use now: keep the key's markers so its last use stays known.
				return removed, nil
			}
			return removed, fmt.Errorf("evict cache %s: %w", name, err)
		}
		removed++
	}
	for _, name := range k.markers {
		if _, err := api.VolumeRemove(ctx, name, client.VolumeRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
			return removed, fmt.Errorf("remove cache use marker %s: %w", name, err)
		}
	}
	return removed, nil
}

func sortedIDs(m map[string]*cacheKey) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ReapCaches evicts persistent caches unused for longer than p.MaxUnused, per-step caches older
// than p.StepAge, and then least recently used caches until the total is under p.MaxBytes. It
// returns how many cache volumes it removed. It needs the disk usage endpoint (/system/df).
func (l *Launcher) ReapCaches(ctx context.Context, p CachePolicy) (int, error) {
	return reapCaches(ctx, l.api, &l.cacheMu, l.log, time.Now(), p)
}

// RunCacheReaper calls ReapCaches now and then every interval until ctx ends. Reading volume
// sizes walks every volume, so the interval should be long (an hour).
func (l *Launcher) RunCacheReaper(ctx context.Context, interval time.Duration, p CachePolicy) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if n, err := l.ReapCaches(ctx, p); err != nil {
			l.log.Warn("reap caches", "err", err)
		} else if n > 0 {
			l.log.Info("reaped cache volumes", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
