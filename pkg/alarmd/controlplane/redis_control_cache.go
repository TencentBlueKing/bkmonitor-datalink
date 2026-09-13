package controlplane

import (
	"container/list"
	"context"
	"errors"
	"strconv"
	"strings"
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
// The cache bounds are not constants any more: they are a share of the
// container the process was given, derived in package config and installed by
// ConfigureControlTimelineCache at assembly. The values below are the product
// reference container's share, so a repository that is never configured still
// caches; TestControlTimelineCacheDefaultMatchesReferenceContainer pins them to
// the same derivation the bundle applies.
const (
	controlTimelineCacheDefaultMaxEntries = 131072
	controlTimelineCacheDefaultMaxBytes   = 128 << 20
)

// Accounting a cached timeline. An entry holds the decoded object and nothing
// else: the persisted bytes are kept by no reader. Every read entry point
// discards them, and the three compare-and-set paths that do use them read
// them live, so the cache neither stores a second copy of the payload nor owes
// anyone the guarantee that a stored copy still matches its decoded object.
//
// The decoded object was expected to dwarf its JSON; measured against
// production-shaped timelines it does not. The JSON repeats a field name for
// every value, while the decoded object repeats a struct field and the string
// bytes, so the two land within the same order of magnitude and an entry can
// be charged a multiple of the payload it was decoded from.
//
// The multiple is not one flat number, because the cost has a sawtooth in it.
// json.Unmarshal grows the two Plans slices by doubling and rounds each growth
// to an allocator size class, so how much spare capacity a decoded timeline
// retains depends on where its Plan count lands between two steps - and the
// two slices, having different element sizes, step at different counts. The
// result is a ratio that varies with shape far more than with size, in sharp
// teeth rather than a trend. Sweeping every Plan count from 4 to 250 under
// -race:
//
//	14 Plans   0.98      18 Plans   1.24      28 Plans   1.00
//	 9 Plans   1.20      19 Plans   1.21     160 Plans   1.18
//
// Without -race every reading is about 0.08 lower, peaking at 1.09.
//
// So an entry is charged 3/2 of its payload: comfortably above the tallest
// tooth measured in either mode, because the teeth are narrow enough that
// sampling forty shapes does not prove where the tallest one is, and because
// the growth behaviour being sampled belongs to the standard library rather
// than to this package. Over-charging costs budget - at the production shape
// the charge is 215 KiB against 143 KiB held, and the byte budget still holds
// more than twice the Query Groups a Worker owns - while under-charging would
// put the process over a bound it believes it is under.
//
// An earlier 9/8 came from a single sample of one shape and sat below several
// teeth. It was not an upper bound at all, and neither was the 5/4 that
// looked sufficient before the sweep went past the shapes an evenly spaced one
// visits.
//
// The race detector's readings bind here even though no production binary
// carries it: an accounting constant that fails its own measurement in one
// build mode is not a bound.
//
// The payload length is known at the one moment it is needed, immediately
// after decoding, which is why nothing has to keep the bytes to size what came
// out of them.
const (
	parsedTimelineBytesNumerator   = 3
	parsedTimelineBytesDenominator = 2
	// The list element, the map entry and the cachedTimeline header. Noise
	// against a real timeline, and the reason the entry bound cannot be reached
	// before the byte bound.
	cachedTimelineOverheadBytes = 256
)

func cachedTimelineBytes(payloadLen int) int {
	return payloadLen*parsedTimelineBytesNumerator/parsedTimelineBytesDenominator + cachedTimelineOverheadBytes
}

type controlVersion struct {
	header string
	known  bool
}

type cachedActivation struct {
	entry      *parsedActivation
	payloadLen int64
}

// cachedTimeline holds the decoded object every reader would otherwise
// rebuild. bytes is what the entry was charged, kept on the entry so eviction
// subtracts exactly what the store added.
type cachedTimeline struct {
	queryGroup execution.QueryGroupIdentity
	timeline   persistedScheduleTimeline
	bytes      int
}

// controlReadCache holds the parsed activation and the decoded timelines
// observed under one header value. A lookup under another header misses; a
// store under another header replaces everything, so a version change evicts
// all entries.
//
// That whole-cache reset is coarse: one publication changes a handful of Query
// Groups but retires every cached timeline, and the next tick decodes all of
// them again. Making invalidation per object is a separate change and is not
// attempted here; the reset costs nothing while the header is stable, which is
// what production shows between publications.
type controlReadCache struct {
	mu         sync.Mutex
	version    string
	activation *cachedActivation
	timelines  map[execution.QueryGroupIdentity]*list.Element
	order      *list.List
	bytes      int
	evictions  uint64
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

// advance moves the cache to a new header keeping every timeline except the
// ones the activation changed. The activation entry always goes: the header
// changed because the activation record did. It reports whether the cache
// was actually moved, so a caller that raced another can tell, and the
// record revisions of the timelines it dropped and kept, for the audit.
func (cache *controlReadCache) advance(
	version string, changed []execution.QueryGroupIdentity,
) (moved bool, dropped, kept map[execution.QueryGroupIdentity]uint64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.version == version {
		return false, nil, nil
	}
	if cache.version == "" {
		cache.resetLocked(version)
		return true, nil, nil
	}
	cache.version = version
	cache.activation = nil
	dropped = make(map[execution.QueryGroupIdentity]uint64, len(changed))
	for _, queryGroup := range changed {
		element, ok := cache.timelines[queryGroup]
		if !ok {
			continue
		}
		entry := element.Value.(*cachedTimeline)
		dropped[queryGroup] = entry.timeline.RecordRevision
		cache.bytes -= entry.bytes
		cache.order.Remove(element)
		delete(cache.timelines, queryGroup)
	}
	kept = make(map[execution.QueryGroupIdentity]uint64, len(cache.timelines))
	for queryGroup, element := range cache.timelines {
		kept[queryGroup] = element.Value.(*cachedTimeline).timeline.RecordRevision
	}
	return true, dropped, kept
}

// deltaAuditEvery is how often a header change crossed by delta is audited
// against the timelines themselves: every sixteenth crossing of a process.
// The audit costs one read of every cached timeline, so it costs 1/16 of
// what every header change cost before deltas. Retire it once deployments
// have reported zero missed timelines across a month of header changes;
// over-named ones are a wasted read and do not keep it alive.
const deltaAuditEvery = 16

// auditDelta checks one applied delta against the timelines it spoke for. A
// delta names what its activation wrote; a Worker needs to know what changed.
// The two are the same only while that activation's script is the only
// writer of timelines, and that is a claim about the whole system, not about
// the script. So a sample of crossings reads every cached timeline back: a
// kept timeline whose persisted record revision moved is one the delta
// missed, and a dropped one whose revision did not move is one it over-named.
// The audit only counts; it never changes what the cache holds.
func (repository *RedisCatalogRepository) auditDelta(ctx context.Context, dropped, kept map[execution.QueryGroupIdentity]uint64) {
	counters := &repository.controlReads.audit
	counters.samples.Add(1)
	var missed, overNamed uint64
	for queryGroup, revision := range kept {
		timeline, _, err := repository.readScheduleTimeline(ctx, queryGroup)
		switch {
		case errors.Is(err, ErrScheduleUnavailable):
			missed++
		case err != nil:
			continue
		case timeline.RecordRevision != revision:
			missed++
		}
	}
	for queryGroup, revision := range dropped {
		timeline, _, err := repository.readScheduleTimeline(ctx, queryGroup)
		if err == nil && timeline.RecordRevision == revision {
			overNamed++
		}
	}
	counters.missed.Add(missed)
	counters.overNamed.Add(overNamed)
	if missed == 0 && overNamed == 0 {
		counters.agreed.Add(1)
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

// lookupTimeline serves the decoded timeline. The object is shared with every
// other caller under this version and is read-only to all of them: the
// compare-and-set paths never come here, they read and decode live.
func (cache *controlReadCache) lookupTimeline(
	version string, queryGroup execution.QueryGroupIdentity,
) (persistedScheduleTimeline, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.version != version {
		return persistedScheduleTimeline{}, false
	}
	element, ok := cache.timelines[queryGroup]
	if !ok {
		return persistedScheduleTimeline{}, false
	}
	cache.order.MoveToFront(element)
	return element.Value.(*cachedTimeline).timeline, true
}

// storeTimeline charges the entry from the payload the object was decoded
// from. payloadLen is the sizing input, not something the entry keeps.
func (cache *controlReadCache) storeTimeline(
	version string,
	queryGroup execution.QueryGroupIdentity,
	timeline persistedScheduleTimeline,
	payloadLen int,
) {
	entryBytes := cachedTimelineBytes(payloadLen)
	if cache.maxEntries <= 0 || cache.maxBytes <= 0 || entryBytes > cache.maxBytes {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.enterLocked(version)
	if element, ok := cache.timelines[queryGroup]; ok {
		cache.bytes -= element.Value.(*cachedTimeline).bytes
		cache.order.Remove(element)
		delete(cache.timelines, queryGroup)
	}
	cache.timelines[queryGroup] = cache.order.PushFront(
		&cachedTimeline{queryGroup: queryGroup, timeline: timeline, bytes: entryBytes},
	)
	cache.bytes += entryBytes
	for cache.order.Len() > cache.maxEntries || cache.bytes > cache.maxBytes {
		last := cache.order.Back()
		evicted := last.Value.(*cachedTimeline)
		cache.bytes -= evicted.bytes
		cache.order.Remove(last)
		delete(cache.timelines, evicted.queryGroup)
		cache.evictions++
	}
}

func (cache *controlReadCache) timelineCount() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.order.Len()
}

// configureTimelineBounds installs the budget derived from the container this
// process was given. It is called once at assembly, before any read.
func (cache *controlReadCache) configureTimelineBounds(maxEntries, maxBytes int) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.maxEntries, cache.maxBytes = maxEntries, maxBytes
	for cache.order.Len() > 0 && (cache.order.Len() > cache.maxEntries || cache.bytes > cache.maxBytes) {
		last := cache.order.Back()
		evicted := last.Value.(*cachedTimeline)
		cache.bytes -= evicted.bytes
		cache.order.Remove(last)
		delete(cache.timelines, evicted.queryGroup)
		cache.evictions++
	}
}

func (cache *controlReadCache) timelineOccupancy() ControlTimelineCacheOccupancy {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return ControlTimelineCacheOccupancy{
		Entries: cache.order.Len(), Bytes: cache.bytes,
		MaxEntries: cache.maxEntries, MaxBytes: cache.maxBytes, Evictions: cache.evictions,
	}
}

// ControlReadCacheObjectStats counts one cached control object. Hits served
// the object after a small version probe without reading its body; misses read
// the body because nothing was cached for the observed version; refreshes read
// the body because a cached copy no longer matched the observed version, epoch
// or length. Shared counts the misses and refreshes that took the body from a
// complete read another caller of this process already had in flight instead
// of reading it again, so bodies actually read are misses plus refreshes minus
// shared. Only the snapshot object shares reads today.
type ControlReadCacheObjectStats struct {
	Hits      uint64
	Misses    uint64
	Refreshes uint64
	Shared    uint64
}

// ControlReadCacheStats is the low-cardinality view intended for a counter
// such as control_cache_total{object, result}. The snapshot object counts only
// cross-call revision cache outcomes, not reuse inside one RunOne scope.
type ControlReadCacheStats struct {
	Snapshot   ControlReadCacheObjectStats
	Activation ControlReadCacheObjectStats
	Timeline   ControlReadCacheObjectStats
	Version    ControlReadCacheObjectStats
	// Delta counts header changes: Hits crossed one keeping the timelines the
	// activation did not change, Misses dropped every cached timeline.
	Delta      ControlReadCacheObjectStats
	DeltaAudit ControlDeltaAuditStats
	// Index counts catalog index entries reused (Hits) and read from the
	// object catalog (Misses); IndexAudit reconciles the index against the
	// snapshot body with the same vocabulary as DeltaAudit, and its Missed is
	// the one number that must stay at zero before a reader may depend on
	// the index.
	Index      ControlReadCacheObjectStats
	IndexAudit ControlDeltaAuditStats
	// TimelineOccupancy answers whether the derived budget actually holds the
	// Query Groups this Worker owns. Without it a miss rate cannot be told
	// apart from a version change, and the budget's formula stays unfalsifiable.
	TimelineOccupancy ControlTimelineCacheOccupancy
}

// ControlTimelineCacheOccupancy is what the timeline cache holds against what
// it was allowed to hold. Entries read against the Worker's owned Query Group
// count says whether the working set fits; Evictions rising while the version
// header is stable says it does not.
type ControlTimelineCacheOccupancy struct {
	Entries    int
	Bytes      int
	MaxEntries int
	MaxBytes   int
	Evictions  uint64
}

type controlReadObjectCounters struct {
	hits      atomic.Uint64
	misses    atomic.Uint64
	refreshes atomic.Uint64
	shared    atomic.Uint64
}

func (counters *controlReadObjectCounters) snapshot() ControlReadCacheObjectStats {
	return ControlReadCacheObjectStats{
		Hits: counters.hits.Load(), Misses: counters.misses.Load(), Refreshes: counters.refreshes.Load(),
		Shared: counters.shared.Load(),
	}
}

type controlReadCounters struct {
	snapshot   controlReadObjectCounters
	activation controlReadObjectCounters
	timeline   controlReadObjectCounters
	version    controlReadObjectCounters
	// delta counts header changes by how the cache crossed them: hits kept
	// the timelines the activation's delta did not name, misses dropped every
	// timeline because no usable delta was found or it said full.
	delta controlReadObjectCounters
	// audit counts the sampled reconciliations of a delta against what the
	// timelines actually did; see auditDelta.
	audit deltaAuditCounters
	// index counts catalog index entries reused (hits) and read from the
	// object catalog (misses) while bringing the index up to a publication;
	// indexAudit reconciles the index against the snapshot body, see
	// auditCatalogIndex.
	index      controlReadObjectCounters
	indexAudit deltaAuditCounters
}

type deltaAuditCounters struct {
	samples   atomic.Uint64
	agreed    atomic.Uint64
	overNamed atomic.Uint64
	missed    atomic.Uint64
}

// ControlDeltaAuditStats are the sampled reconciliations of activation deltas.
// Samples is how many header changes were audited; Agreed how many of those
// found the delta exact. OverNamed counts timelines a delta named that had
// not changed (a wasted read, harmless) and Missed counts timelines a delta
// did not name that had changed (a Worker running on stale content, harmful).
// The two directions are never added together: their remedies are opposite.
type ControlDeltaAuditStats struct {
	Samples   uint64
	Agreed    uint64
	OverNamed uint64
	Missed    uint64
}

func (repository *RedisCatalogRepository) ControlReadCacheStats() ControlReadCacheStats {
	if repository == nil {
		return ControlReadCacheStats{}
	}
	return ControlReadCacheStats{
		Snapshot:   repository.controlReads.snapshot.snapshot(),
		Activation: repository.controlReads.activation.snapshot(),
		Timeline:   repository.controlReads.timeline.snapshot(),
		Version:    repository.controlReads.version.snapshot(),
		Delta:      repository.controlReads.delta.snapshot(),
		DeltaAudit: ControlDeltaAuditStats{
			Samples: repository.controlReads.audit.samples.Load(), Agreed: repository.controlReads.audit.agreed.Load(),
			OverNamed: repository.controlReads.audit.overNamed.Load(), Missed: repository.controlReads.audit.missed.Load(),
		},
		Index: repository.controlReads.index.snapshot(),
		IndexAudit: ControlDeltaAuditStats{
			Samples: repository.controlReads.indexAudit.samples.Load(), Agreed: repository.controlReads.indexAudit.agreed.Load(),
			OverNamed: repository.controlReads.indexAudit.overNamed.Load(), Missed: repository.controlReads.indexAudit.missed.Load(),
		},
		TimelineOccupancy: repository.controlCache.timelineOccupancy(),
	}
}

// ConfigureControlTimelineCache installs the timeline cache budget derived
// from this container. The derivation lives in package config, which this
// package does not import: the assembly that already knows the container is
// what hands the numbers down.
func (repository *RedisCatalogRepository) ConfigureControlTimelineCache(maxEntries, maxBytes int) error {
	if repository == nil || repository.controlCache == nil || maxEntries <= 0 || maxBytes <= 0 {
		return errors.New("alarmd controlplane: invalid control timeline cache budget")
	}
	repository.controlCache.configureTimelineBounds(maxEntries, maxBytes)
	return nil
}

// fetchControlVersion performs the one small live read that every activation
// and timeline read starts with. An absent header leaves caching disabled for
// that read; the caller then follows the uncached path. Callers go through
// readControlVersion, which reuses the value inside one operation.
func (repository *RedisCatalogRepository) fetchControlVersion(ctx context.Context) (controlVersion, error) {
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
// adoptControlVersion moves the control read cache to a header it has not
// seen, before anything is stored under it. The activation's delta says which
// timelines that activation wrote; those are dropped and the rest are kept,
// so a header change costs one small read instead of every owned timeline.
// Without a usable delta the cache is reset, which is what every header
// change cost before. Concurrent readers of the same new header adopt it
// once: the first to take the lock moves the cache, the others find it moved.
func (repository *RedisCatalogRepository) adoptControlVersion(ctx context.Context, version controlVersion) {
	if !version.known || !repository.controlCache.supersedes(version.header) {
		return
	}
	repository.adoptMu.Lock()
	defer repository.adoptMu.Unlock()
	if !repository.controlCache.supersedes(version.header) {
		return
	}
	counters := &repository.controlReads.delta
	revision, ok := activationHeaderRevision(version.header)
	if ok {
		delta, present, err := repository.loadActivationDelta(ctx, revision)
		if err == nil && present && !delta.Full {
			moved, dropped, kept := repository.controlCache.advance(version.header, delta.QueryGroups)
			if moved {
				if crossings := counters.hits.Add(1); crossings%deltaAuditEvery == 0 && (len(dropped) > 0 || len(kept) > 0) {
					repository.auditDelta(ctx, dropped, kept)
				}
			}
			return
		}
	}
	repository.controlCache.mu.Lock()
	repository.controlCache.enterLocked(version.header)
	repository.controlCache.mu.Unlock()
	counters.misses.Add(1)
}

// activationHeaderRevision reads the record revision off the front of an
// activation header, which activationHeader writes as "<revision>|...".
func activationHeaderRevision(header string) (uint64, bool) {
	end := strings.IndexByte(header, '|')
	if end <= 0 {
		return 0, false
	}
	revision, err := strconv.ParseUint(header[:end], 10, 64)
	if err != nil || revision == 0 {
		return 0, false
	}
	return revision, true
}

func (repository *RedisCatalogRepository) loadParsedActivationAt(ctx context.Context, version controlVersion) (*parsedActivation, error) {
	counters := &repository.controlReads.activation
	// Whether this read refreshes an entry or misses is decided before the
	// cache crosses the header: crossing it drops the activation entry, and
	// that drop is the refresh, not a miss.
	hadActivation := repository.controlCache.hasActivation()
	repository.adoptControlVersion(ctx, version)
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
		if hadActivation {
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

// loadScheduleTimelineAt serves the timeline observed under version. A hit
// returns the object decoded and validated when the payload was first read;
// nothing is decoded or validated again.
//
// Caching the bytes alone saved only the Redis read, which is the cheap half:
// in a production profile the Redis GET was 0.26% of the process while
// decoding and re-validating the same bytes was 58%. One Slot execution loads
// the same timeline about 25 times - ReadFrozenSchedule, its successor, the
// retirement read and the freeze all load it independently - so every constant
// on this path is paid 25 times per execution.
//
// The returned timeline is shared with every other caller under this version
// and must be treated as read-only. Nothing on this path writes to it: every
// entry point here reads Segments and returns copies of what it finds. The
// compare-and-set paths, which do write, do not come through here at all -
// they use loadScheduleTimelineForUpdate, which reads live.
func (repository *RedisCatalogRepository) loadScheduleTimelineAt(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	version controlVersion,
) (persistedScheduleTimeline, error) {
	if repository == nil || repository.client == nil || queryGroup == "" {
		return persistedScheduleTimeline{}, errors.New("alarmd controlplane: Query Group schedule is required")
	}
	counters := &repository.controlReads.timeline
	superseded := version.known && repository.controlCache.supersedes(version.header)
	repository.adoptControlVersion(ctx, version)
	if version.known {
		if timeline, ok := repository.controlCache.lookupTimeline(version.header, queryGroup); ok {
			counters.hits.Add(1)
			return timeline, nil
		}
	}
	timeline, payload, err := repository.readScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return persistedScheduleTimeline{}, err
	}
	if version.known {
		if superseded {
			counters.refreshes.Add(1)
		} else {
			counters.misses.Add(1)
		}
		repository.controlCache.storeTimeline(version.header, queryGroup, timeline, len(payload))
	} else {
		counters.misses.Add(1)
	}
	return timeline, nil
}

// readScheduleTimeline reads and decodes one timeline live. The bytes come
// back with it because a compare-and-set needs the exact persisted value it is
// replacing, and re-marshalling the decoded object would not reproduce it.
func (repository *RedisCatalogRepository) readScheduleTimeline(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (persistedScheduleTimeline, []byte, error) {
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
	return timeline, payload, nil
}

// supersedes reports whether storing under version replaces content cached
// under an older header, which distinguishes a refresh from a cold miss.
func (cache *controlReadCache) supersedes(version string) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.version != "" && cache.version != version
}
