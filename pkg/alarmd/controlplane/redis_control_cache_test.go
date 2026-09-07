package controlplane

import (
	"bytes"
	"testing"
)

func TestControlReadCacheTimelinesAreBoundedByEntriesAndBytes(t *testing.T) {
	cache := newControlReadCache(2, 1<<20)
	cache.storeTimeline("v1", "qg-a", []byte("aaaa"))
	cache.storeTimeline("v1", "qg-b", []byte("bbbb"))
	if _, ok := cache.lookupTimeline("v1", "qg-a"); !ok {
		t.Fatal("recently used qg-a evicted early")
	}
	cache.storeTimeline("v1", "qg-c", []byte("cccc"))
	if cache.timelineCount() != 2 {
		t.Fatalf("timeline entries=%d, want bounded 2", cache.timelineCount())
	}
	if _, ok := cache.lookupTimeline("v1", "qg-b"); ok {
		t.Fatal("least recently used qg-b survived eviction")
	}
	if payload, ok := cache.lookupTimeline("v1", "qg-a"); !ok || !bytes.Equal(payload, []byte("aaaa")) {
		t.Fatal("qg-a lost after eviction of its sibling")
	}
	if cache.bytes != 8 {
		t.Fatalf("cached bytes=%d, want 8", cache.bytes)
	}

	small := newControlReadCache(8, 6)
	small.storeTimeline("v1", "qg-a", []byte("aaaa"))
	small.storeTimeline("v1", "qg-b", []byte("bbbb"))
	if small.timelineCount() != 1 || small.bytes != 4 {
		t.Fatalf("byte bound entries=%d bytes=%d, want 1/4", small.timelineCount(), small.bytes)
	}
	if _, ok := small.lookupTimeline("v1", "qg-b"); !ok {
		t.Fatal("newest timeline evicted instead of oldest")
	}
	small.storeTimeline("v1", "qg-c", []byte("ccccccccc"))
	if _, ok := small.lookupTimeline("v1", "qg-c"); ok {
		t.Fatal("timeline above the byte bound was retained")
	}
	small.storeTimeline("v1", "qg-b", []byte("bb"))
	if small.timelineCount() != 1 || small.bytes != 2 {
		t.Fatalf("replacing an entry double counted: entries=%d bytes=%d", small.timelineCount(), small.bytes)
	}
}

func TestControlReadCacheVersionChangeEvictsEverything(t *testing.T) {
	cache := newControlReadCache(8, 1<<20)
	entry := &parsedActivation{}
	cache.storeActivation("v1", entry, 10)
	cache.storeTimeline("v1", "qg-a", []byte("aaaa"))
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
	cache.storeTimeline("v2", "qg-b", []byte("bbbb"))
	if _, ok := cache.lookupTimeline("v2", "qg-a"); ok {
		t.Fatal("timeline from the previous version survived the version change")
	}
	if _, ok := cache.lookupActivation("v2", 10); ok {
		t.Fatal("activation from the previous version survived the version change")
	}
	if cache.timelineCount() != 1 || cache.bytes != 4 {
		t.Fatalf("version change left entries=%d bytes=%d", cache.timelineCount(), cache.bytes)
	}
	cache.dropActivation()
	cache.storeActivation("v2", entry, 5)
	if _, ok := cache.lookupTimeline("v2", "qg-b"); !ok {
		t.Fatal("storing the activation under the same version evicted timelines")
	}
	fresh := newControlReadCache(8, 1<<20)
	if fresh.supersedes("v1") {
		t.Fatal("an empty cache never supersedes")
	}
}
