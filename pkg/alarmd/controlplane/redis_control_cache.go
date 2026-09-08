package controlplane

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The activation header is the control-plane version signal. Every Lua script
// that writes catalog:activation or a Schedule timeline also SETs the header
// to a value carrying the advanced record revision inside the same script, so
// two reads observing the same header observe the same activation and timeline
// bytes. The cache never replaces the live header read: every read still
// starts with one small GET, and a header that is absent disables caching.
const (
	controlTimelineCacheMaxEntries = 4096
	// Match the existing snapshot cache convention rather than adding another
	// operator-tuned memory limit. Timelines above this size load uncached.
	controlTimelineCacheMaxBytes = 32 << 20
)

type controlVersion struct {
	header string
	known  bool
}

type cachedActivation struct {
	entry      *parsedActivation
	payloadLen int64
}

type cachedTimeline struct {
	queryGroup execution.QueryGroupIdentity
	payload    []byte
}

// controlReadCache holds the parsed activation and the raw timelines observed
// under one header value. A lookup under another header misses; a store under
// another header replaces everything, so a version change evicts all entries.
type controlReadCache struct {
	mu         sync.Mutex
	version    string
	activation *cachedActivation
	timelines  map[execution.QueryGroupIdentity]*list.Element
	order      *list.List
	bytes      int
	maxEntries int
	maxBytes   int
}

func newControlReadCache(maxEntries, maxBytes int) *controlReadCache {
	return &controlReadCache{
		timelines:  make(map[execution.QueryGroupIdentity]*list.Element),
		order:      list.New(),
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
	}
}

func (cache *controlReadCache) resetLocked(version string) {
	cache.version = version
	cache.activation = nil
	cache.timelines = make(map[execution.QueryGroupIdentity]*list.Element)
	cache.order.Init()
	cache.bytes = 0
}

func (cache *controlReadCache) enterLocked(version string) {
	if cache.version != version {
		cache.resetLocked(version)
	}
}

func (cache *controlReadCache) lookupActivation(version string, payloadLen int64) (*parsedActivation, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.version != version || cache.activation == nil || cache.activation.payloadLen != payloadLen {
		return nil, false
	}
	return cache.activation.entry, true
}

func (cache *controlReadCache) storeActivation(version string, entry *parsedActivation, payloadLen int64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.enterLocked(version)
	cache.activation = &cachedActivation{entry: entry, payloadLen: payloadLen}
}

func (cache *controlReadCache) dropActivation() {
	cache.mu.Lock()
	cache.activation = nil
	cache.mu.Unlock()
}

func (cache *controlReadCache) lookupTimeline(version string, queryGroup execution.QueryGroupIdentity) ([]byte, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.version != version {
		return nil, false
	}
	element, ok := cache.timelines[queryGroup]
	if !ok {
		return nil, false
	}
	cache.order.MoveToFront(element)
	return element.Value.(*cachedTimeline).payload, true
}

func (cache *controlReadCache) storeTimeline(version string, queryGroup execution.QueryGroupIdentity, payload []byte) {
	if cache.maxEntries <= 0 || cache.maxBytes <= 0 || len(payload) > cache.maxBytes {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.enterLocked(version)
	if element, ok := cache.timelines[queryGroup]; ok {
		cache.bytes -= len(element.Value.(*cachedTimeline).payload)
		cache.order.Remove(element)
		delete(cache.timelines, queryGroup)
	}
	cache.timelines[queryGroup] = cache.order.PushFront(&cachedTimeline{queryGroup: queryGroup, payload: payload})
	cache.bytes += len(payload)
	for cache.order.Len() > cache.maxEntries || cache.bytes > cache.maxBytes {
		last := cache.order.Back()
		evicted := last.Value.(*cachedTimeline)
		cache.bytes -= len(evicted.payload)
		cache.order.Remove(last)
		delete(cache.timelines, evicted.queryGroup)
	}
}

func (cache *controlReadCache) timelineCount() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.order.Len()
}

// ControlReadCacheObjectStats counts one cached control object. Hits served
// the object after a small version probe without reading its body; misses read
// the body because nothing was cached for the observed version; refreshes read
// the body because a cached copy no longer matched the observed version, epoch
// or length.
type ControlReadCacheObjectStats struct {
	Hits      uint64
	Misses    uint64
	Refreshes uint64
}

// ControlReadCacheStats is the low-cardinality view intended for a counter
// such as control_cache_total{object, result}. The snapshot object counts only
// cross-call revision cache outcomes, not reuse inside one RunOne scope.
type ControlReadCacheStats struct {
	Snapshot   ControlReadCacheObjectStats
	Activation ControlReadCacheObjectStats
	Timeline   ControlReadCacheObjectStats
}

type controlReadObjectCounters struct {
	hits      atomic.Uint64
	misses    atomic.Uint64
	refreshes atomic.Uint64
}

func (counters *controlReadObjectCounters) snapshot() ControlReadCacheObjectStats {
	return ControlReadCacheObjectStats{
		Hits: counters.hits.Load(), Misses: counters.misses.Load(), Refreshes: counters.refreshes.Load(),
	}
}

type controlReadCounters struct {
	snapshot   controlReadObjectCounters
	activation controlReadObjectCounters
	timeline   controlReadObjectCounters
}

func (repository *RedisCatalogRepository) ControlReadCacheStats() ControlReadCacheStats {
	if repository == nil {
		return ControlReadCacheStats{}
	}
	return ControlReadCacheStats{
		Snapshot:   repository.controlReads.snapshot.snapshot(),
		Activation: repository.controlReads.activation.snapshot(),
		Timeline:   repository.controlReads.timeline.snapshot(),
	}
}

// readControlVersion performs the one small live read that every activation
// and timeline read starts with. An absent header leaves caching disabled for
// that read; the caller then follows the uncached path.
func (repository *RedisCatalogRepository) readControlVersion(ctx context.Context) (controlVersion, error) {
	header, err := repository.client.Get(ctx, repository.activationHeaderKey()).Result()
	if errors.Is(err, redis.Nil) {
		return controlVersion{}, nil
	}
	if err != nil {
		return controlVersion{}, activationDependencyIO(err)
	}
	return controlVersion{header: header, known: header != ""}, nil
}

func (repository *RedisCatalogRepository) clearActivationCaches() {
	repository.activationCache.clear()
	repository.controlCache.dropActivation()
}

// loadParsedActivationAt serves the activation observed under version. The
// activation body is read only when the cache has no copy for that version or
// its persisted length changed. A missing activation surfaces
// ErrActivationUnavailable before any cached copy is consulted.
func (repository *RedisCatalogRepository) loadParsedActivationAt(ctx context.Context, version controlVersion) (*parsedActivation, error) {
	counters := &repository.controlReads.activation
	if version.known {
		size, err := repository.client.StrLen(ctx, repository.activationKey()).Result()
		if err != nil {
			repository.clearActivationCaches()
			return nil, activationDependencyIO(err)
		}
		if size == 0 {
			repository.clearActivationCaches()
			return nil, ErrActivationUnavailable
		}
		if entry, ok := repository.controlCache.lookupActivation(version.header, size); ok {
			counters.hits.Add(1)
			return entry, nil
		}
	}
	payload, err := repository.client.Get(ctx, repository.activationKey()).Result()
	if errors.Is(err, redis.Nil) {
		repository.clearActivationCaches()
		return nil, ErrActivationUnavailable
	}
	if err != nil {
		repository.clearActivationCaches()
		return nil, activationDependencyIO(err)
	}
	entry, err := repository.activationCache.load(payload)
	if err != nil {
		return nil, err
	}
	if version.known {
		if repository.controlCache.hasActivation() {
			counters.refreshes.Add(1)
		} else {
			counters.misses.Add(1)
		}
		repository.controlCache.storeActivation(version.header, entry, int64(len(payload)))
	} else {
		counters.misses.Add(1)
	}
	return entry, nil
}

func (cache *controlReadCache) hasActivation() bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.activation != nil
}

// loadScheduleTimelineAt serves the timeline observed under version. On a hit
// the cached bytes are decoded and validated exactly as a live payload would
// be; only the Redis read is skipped.
func (repository *RedisCatalogRepository) loadScheduleTimelineAt(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	version controlVersion,
) (persistedScheduleTimeline, []byte, error) {
	if repository == nil || repository.client == nil || queryGroup == "" {
		return persistedScheduleTimeline{}, nil, errors.New("alarmd controlplane: Query Group schedule is required")
	}
	counters := &repository.controlReads.timeline
	if version.known {
		if payload, ok := repository.controlCache.lookupTimeline(version.header, queryGroup); ok {
			timeline, err := decodeScheduleTimeline(queryGroup, payload)
			if err != nil {
				return persistedScheduleTimeline{}, nil, err
			}
			counters.hits.Add(1)
			return timeline, payload, nil
		}
	}
	payload, err := repository.client.Get(ctx, repository.scheduleTimelineKey(queryGroup)).Bytes()
	if errors.Is(err, redis.Nil) {
		return persistedScheduleTimeline{}, nil, ErrScheduleUnavailable
	}
	if err != nil {
		return persistedScheduleTimeline{}, nil, activationDependencyIO(err)
	}
	timeline, err := decodeScheduleTimeline(queryGroup, payload)
	if err != nil {
		return persistedScheduleTimeline{}, nil, err
	}
	if version.known {
		if repository.controlCache.supersedes(version.header) {
			counters.refreshes.Add(1)
		} else {
			counters.misses.Add(1)
		}
		repository.controlCache.storeTimeline(version.header, queryGroup, payload)
	} else {
		counters.misses.Add(1)
	}
	return timeline, payload, nil
}

// supersedes reports whether storing under version replaces content cached
// under an older header, which distinguishes a refresh from a cold miss.
func (cache *controlReadCache) supersedes(version string) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.version != "" && cache.version != version
}
