package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// cachedTimelineFor is a timeline the cache can hold and a test can tell apart
// from its neighbours. The cache never inspects it; only the byte accounting,
// which is driven by the payload length, and identity matter here.
func cachedTimelineFor(queryGroup execution.QueryGroupIdentity, revision uint64) persistedScheduleTimeline {
	return persistedScheduleTimeline{
		SchemaVersion: scheduleTimelineSchemaVersion, RecordRevision: revision, QueryGroup: queryGroup,
	}
}

func TestControlReadCacheTimelinesAreBoundedByEntriesAndBytes(t *testing.T) {
	four := cachedTimelineBytes(4)
	cache := newControlReadCache(2, 16*four)
	cache.storeTimeline("v1", "qg-a", cachedTimelineFor("qg-a", 1), 4)
	cache.storeTimeline("v1", "qg-b", cachedTimelineFor("qg-b", 2), 4)
	if _, ok := cache.lookupTimeline("v1", "qg-a"); !ok {
		t.Fatal("recently used qg-a evicted early")
	}
	cache.storeTimeline("v1", "qg-c", cachedTimelineFor("qg-c", 3), 4)
	if cache.timelineCount() != 2 {
		t.Fatalf("timeline entries=%d, want bounded 2", cache.timelineCount())
	}
	if _, ok := cache.lookupTimeline("v1", "qg-b"); ok {
		t.Fatal("least recently used qg-b survived eviction")
	}
	if timeline, ok := cache.lookupTimeline("v1", "qg-a"); !ok || timeline.RecordRevision != 1 {
		t.Fatal("qg-a lost after eviction of its sibling")
	}
	if cache.bytes != 2*four {
		t.Fatalf("cached bytes=%d, want %d", cache.bytes, 2*four)
	}
	if cache.evictions != 1 {
		t.Fatalf("evictions=%d, want 1", cache.evictions)
	}

	// An entry is charged the object decoded from its payload, so a bound of
	// one entry admits exactly one.
	small := newControlReadCache(8, four+1)
	small.storeTimeline("v1", "qg-a", cachedTimelineFor("qg-a", 1), 4)
	small.storeTimeline("v1", "qg-b", cachedTimelineFor("qg-b", 2), 4)
	if small.timelineCount() != 1 || small.bytes != four {
		t.Fatalf("byte bound entries=%d bytes=%d, want 1/%d", small.timelineCount(), small.bytes, four)
	}
	if _, ok := small.lookupTimeline("v1", "qg-b"); !ok {
		t.Fatal("newest timeline evicted instead of oldest")
	}
	small.storeTimeline("v1", "qg-c", cachedTimelineFor("qg-c", 3), 4096)
	if _, ok := small.lookupTimeline("v1", "qg-c"); ok {
		t.Fatal("timeline above the byte bound was retained")
	}
	two := cachedTimelineBytes(2)
	small.storeTimeline("v1", "qg-b", cachedTimelineFor("qg-b", 4), 2)
	if small.timelineCount() != 1 || small.bytes != two {
		t.Fatalf("replacing an entry double counted: entries=%d bytes=%d", small.timelineCount(), small.bytes)
	}
}

func TestControlReadCacheVersionChangeEvictsEverything(t *testing.T) {
	four := cachedTimelineBytes(4)
	cache := newControlReadCache(8, 16*four)
	entry := &parsedActivation{}
	cache.storeActivation("v1", entry, 10)
	cache.storeTimeline("v1", "qg-a", cachedTimelineFor("qg-a", 1), 4)
	if got, ok := cache.lookupActivation("v1", 10); !ok || got != entry {
		t.Fatal("activation lookup under its version missed")
	}
	if _, ok := cache.lookupActivation("v1", 11); ok {
		t.Fatal("activation with another persisted length was served")
	}
	if _, ok := cache.lookupActivation("v2", 10); ok {
		t.Fatal("activation under another version was served")
	}
	if _, ok := cache.lookupTimeline("v2", "qg-a"); ok {
		t.Fatal("timeline under another version was served")
	}
	if cache.supersedes("v2") != true || cache.supersedes("v1") != false {
		t.Fatal("supersedes must report only a different non-empty cached version")
	}
	cache.storeTimeline("v2", "qg-b", cachedTimelineFor("qg-b", 2), 4)
	if _, ok := cache.lookupTimeline("v2", "qg-a"); ok {
		t.Fatal("timeline from the previous version survived the version change")
	}
	if _, ok := cache.lookupActivation("v2", 10); ok {
		t.Fatal("activation from the previous version survived the version change")
	}
	if cache.timelineCount() != 1 || cache.bytes != four {
		t.Fatalf("version change left entries=%d bytes=%d", cache.timelineCount(), cache.bytes)
	}
	cache.dropActivation()
	cache.storeActivation("v2", entry, 5)
	if _, ok := cache.lookupTimeline("v2", "qg-b"); !ok {
		t.Fatal("storing the activation under the same version evicted timelines")
	}
	fresh := newControlReadCache(8, 16*four)
	if fresh.supersedes("v1") {
		t.Fatal("an empty cache never supersedes")
	}
}

// TestControlReadCacheReportsItsBudgetAndOccupancy covers the surface the
// metrics read: a budget that cannot be seen next to what it holds cannot be
// checked against production, which is how the replaced constant survived.
func TestControlReadCacheReportsItsBudgetAndOccupancy(t *testing.T) {
	four := cachedTimelineBytes(4)
	cache := newControlReadCache(8, 16*four)
	cache.storeTimeline("v1", "qg-a", cachedTimelineFor("qg-a", 1), 4)
	occupancy := cache.timelineOccupancy()
	if occupancy.Entries != 1 || occupancy.Bytes != four ||
		occupancy.MaxEntries != 8 || occupancy.MaxBytes != 16*four || occupancy.Evictions != 0 {
		t.Fatalf("occupancy = %+v", occupancy)
	}
	cache.configureTimelineBounds(1, four+1)
	cache.storeTimeline("v1", "qg-b", cachedTimelineFor("qg-b", 2), 4)
	occupancy = cache.timelineOccupancy()
	if occupancy.Entries != 1 || occupancy.MaxEntries != 1 || occupancy.MaxBytes != four+1 ||
		occupancy.Evictions != 1 {
		t.Fatalf("occupancy after reconfiguration = %+v", occupancy)
	}
	// Shrinking the budget below what is held evicts down to it immediately
	// rather than leaving the process over its own bound until the next store.
	cache.configureTimelineBounds(1, 1)
	if occupancy = cache.timelineOccupancy(); occupancy.Entries != 0 || occupancy.Bytes != 0 {
		t.Fatalf("shrinking the budget left %+v", occupancy)
	}
}
