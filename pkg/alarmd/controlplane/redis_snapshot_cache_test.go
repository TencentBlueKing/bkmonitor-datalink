package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestVerifiedSnapshotCacheReturnsIsolatedContent(t *testing.T) {
	revision, payload := neutralSnapshotPayload(t, "qg-a")
	cache := newVerifiedSnapshotCache(2, 1<<20)
	first, err := cache.loadSnapshot(context.Background(), revision, payload)
	if err != nil {
		t.Fatal(err)
	}
	first.QueryGroups[0].Identity = "mutated"
	second, err := cache.loadSnapshot(context.Background(), revision, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.QueryGroups[0].Identity; got != "qg-a" {
		t.Fatalf("cached Snapshot was polluted through caller result: %q", got)
	}
	group, err := cache.loadQueryGroup(context.Background(), revision, payload, "qg-a")
	if err != nil {
		t.Fatal(err)
	}
	group.Identity = "mutated-again"
	group, err = cache.loadQueryGroup(context.Background(), revision, payload, "qg-a")
	if err != nil || group.Identity != "qg-a" {
		t.Fatalf("cached Query Group=(%q,%v), want isolated qg-a", group.Identity, err)
	}
	if got := len(cache.entries); got != 1 {
		t.Fatalf("cache entries=%d, want 1", got)
	}
}

func TestVerifiedSnapshotCacheRevalidatesChangedPayload(t *testing.T) {
	revision, payload := neutralSnapshotPayload(t, "qg-a")
	cache := newVerifiedSnapshotCache(2, 1<<20)
	if _, err := cache.loadSnapshot(context.Background(), revision, payload); err != nil {
		t.Fatal(err)
	}
	_, changed := neutralSnapshotPayload(t, "qg-b")
	if len(changed) != len(payload) {
		t.Fatal("changed-content counterexample must preserve payload length")
	}
	if _, err := cache.loadSnapshot(context.Background(), revision, changed); err == nil {
		t.Fatal("same revision with changed content was accepted from cache")
	} else {
		var corrupt *PersistedSnapshotCorruptError
		if !errors.As(err, &corrupt) {
			t.Fatalf("changed payload error=%T %v, want persisted corruption", err, err)
		}
	}
	if _, err := cache.loadSnapshot(context.Background(), revision, `{"schema_version":`); err == nil {
		t.Fatal("malformed payload was accepted from cache")
	}
	loaded, err := cache.loadSnapshot(context.Background(), revision, payload)
	if err != nil || loaded.QueryGroups[0].Identity != "qg-a" {
		t.Fatalf("original verified payload no longer loads: %#v, %v", loaded, err)
	}
}

func TestVerifiedSnapshotCacheIsBoundedAndIsolatesCorruptSibling(t *testing.T) {
	cache := newVerifiedSnapshotCache(2, 1<<20)
	revisionA, payloadA := neutralSnapshotPayload(t, "qg-a")
	revisionB, payloadB := neutralSnapshotPayload(t, "qg-b")
	revisionC, payloadC := neutralSnapshotPayload(t, "qg-c")
	for _, item := range []struct {
		revision execution.SnapshotRevision
		payload  string
	}{{revisionA, payloadA}, {revisionB, payloadB}, {revisionA, payloadA}, {revisionC, payloadC}} {
		if _, err := cache.loadSnapshot(context.Background(), item.revision, item.payload); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(cache.entries); got != 2 {
		t.Fatalf("cache entries=%d, want bounded 2", got)
	}
	if cache.contains(revisionB) || !cache.contains(revisionA) {
		t.Fatal("A-B-A access did not evict the least recently used Snapshot B")
	}
	if _, err := cache.loadSnapshot(context.Background(), revisionA, `not-json`); err == nil {
		t.Fatal("corrupt sibling was accepted")
	}
	group, err := cache.loadQueryGroup(context.Background(), revisionC, payloadC, "qg-c")
	if err != nil || group.Identity != "qg-c" {
		t.Fatalf("healthy sibling=(%q,%v)", group.Identity, err)
	}
}

func TestVerifiedSnapshotCacheHonorsByteBound(t *testing.T) {
	revisionA, payloadA := neutralSnapshotPayload(t, "qg-a")
	probe := newVerifiedSnapshotCache(1, 1<<20)
	if _, err := probe.loadSnapshot(context.Background(), revisionA, payloadA); err != nil {
		t.Fatal(err)
	}
	entryBytes := probe.bytes
	cache := newVerifiedSnapshotCache(8, entryBytes)
	if _, err := cache.loadSnapshot(context.Background(), revisionA, payloadA); err != nil {
		t.Fatal(err)
	}
	revisionB, payloadB := neutralSnapshotPayload(t, "qg-b")
	if _, err := cache.loadSnapshot(context.Background(), revisionB, payloadB); err != nil {
		t.Fatal(err)
	}
	if len(cache.entries) != 1 || cache.bytes > entryBytes || cache.contains(revisionA) {
		t.Fatalf("byte bound entries=%d bytes=%d max=%d", len(cache.entries), cache.bytes, entryBytes)
	}
}

func TestVerifiedSnapshotCacheEvictionReleasesBackingReferences(t *testing.T) {
	cache := newVerifiedSnapshotCache(1, 1<<20)
	for _, identity := range []execution.QueryGroupIdentity{"qg-a", "qg-b"} {
		revision, payload := neutralSnapshotPayload(t, identity)
		if _, err := cache.loadSnapshot(context.Background(), revision, payload); err != nil {
			t.Fatal(err)
		}
	}
	backing := cache.entries[:cap(cache.entries)]
	for index := len(cache.entries); index < len(backing); index++ {
		if backing[index].payload != "" || backing[index].queryGroups != nil {
			t.Fatalf("evicted backing slot %d still retains Snapshot references", index)
		}
	}
}

func TestVerifiedSnapshotCacheHonorsCancellationAndConcurrentIsolation(t *testing.T) {
	revision, payload := neutralSnapshotPayload(t, "qg-a")
	cache := newVerifiedSnapshotCache(2, 1<<20)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.loadSnapshot(cancelled, revision, payload); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled load error=%v, want context.Canceled", err)
	}
	if got := len(cache.entries); got != 0 {
		t.Fatalf("cancelled load populated %d cache entries", got)
	}
	const readers = 32
	var wait sync.WaitGroup
	errorsByReader := make(chan error, readers)
	for reader := 0; reader < readers; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			loaded, err := cache.loadSnapshot(context.Background(), revision, payload)
			if err == nil {
				loaded.QueryGroups[0].Identity = "caller-local"
			}
			errorsByReader <- err
		}()
	}
	wait.Wait()
	close(errorsByReader)
	for err := range errorsByReader {
		if err != nil {
			t.Fatal(err)
		}
	}
	group, err := cache.loadQueryGroup(context.Background(), revision, payload, "qg-a")
	if err != nil || group.Identity != "qg-a" {
		t.Fatalf("concurrent callers polluted cache: %q, %v", group.Identity, err)
	}
}

func neutralSnapshotPayload(
	t *testing.T,
	identity execution.QueryGroupIdentity,
) (execution.SnapshotRevision, string) {
	t.Helper()
	groups := []QueryGroup{{Identity: identity}}
	revision, err := deriveSnapshotRevision(groups)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		SchemaVersion    string                     `json:"schema_version"`
		SnapshotRevision execution.SnapshotRevision `json:"snapshot_revision"`
		QueryGroups      []QueryGroup               `json:"query_groups"`
	}{snapshotSchemaVersion, revision, groups})
	if err != nil {
		t.Fatal(err)
	}
	return revision, string(payload)
}
