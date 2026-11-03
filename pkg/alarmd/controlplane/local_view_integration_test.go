// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The Worker's local view is the stored bytes of the objects its owned Query
// Groups' last Slots were frozen from: measured from Redis by STRLEN here,
// outside the reader, and equal on the read that fetched and on the read
// that hit the cache; a Query Group the Worker releases leaves the view at
// the next reading; one whose Slot was served from the Snapshot leaves it
// too. decision-016 sizes its stream by this number, and until now it was a
// hand-run script.
func TestTheLocalViewIsTheStoredBytesOfTheOwnedQueryGroupsLastSlots(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:local-view"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	catalog := objectCatalogTwoGroups(t, 80)
	snapshot, _, err := repository.PublishCatalog(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
		t.Fatal(err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first, second := catalog.QueryGroups[0].Identity, catalog.QueryGroups[1].Identity
	owned := []execution.QueryGroupIdentity{first, second}

	// What the store holds for each Query Group, by the store's own count.
	type stored struct{ object, contexts, plans int }
	segments := map[execution.QueryGroupIdentity]execution.ScheduleSegmentFact{}
	sizes := map[execution.QueryGroupIdentity]stored{}
	for _, identity := range owned {
		schedule, err := runtime.ReadFrozenSchedule(ctx, identity, 60)
		if err != nil {
			t.Fatal(err)
		}
		segment := schedule.Segment
		if segment.ObjectDigest == "" || len(segment.OutputContextRefs) == 0 {
			t.Fatalf("Segment of %s names no content: %+v", identity, segment)
		}
		segments[identity] = segment
		size := stored{plans: len(segment.OutputContextRefs)}
		size.object = int(client.StrLen(ctx, prefix+":qgobj:"+string(segment.ObjectDigest)).Val())
		counted := map[execution.OutputContextDigest]struct{}{}
		for _, ref := range segment.OutputContextRefs {
			if _, seen := counted[ref.Digest]; seen {
				continue
			}
			counted[ref.Digest] = struct{}{}
			size.contexts += int(client.StrLen(ctx, prefix+":outctx:"+string(ref.Digest)).Val())
		}
		if size.object == 0 || size.contexts == 0 {
			t.Fatalf("the store holds nothing for %s: %+v", identity, size)
		}
		sizes[identity] = size
	}
	fallback := func(context.Context) (controlplane.QueryGroup, error) {
		t.Fatal("a Segment naming stored content must not fall back")
		return controlplane.QueryGroup{}, nil
	}

	// Nothing owned has been frozen: an empty view over two owned Query Groups.
	if view := repository.LocalView(owned); view != (controlplane.LocalView{}) {
		t.Fatalf("view before any Slot = %+v, want empty", view)
	}
	// The first Slot of the first Query Group: the view is that Query Group.
	if _, err := repository.LoadSegmentQueryGroup(ctx, segments[first], 60, fallback); err != nil {
		t.Fatal(err)
	}
	want := controlplane.LocalView{QueryGroups: 1, ObjectBytes: sizes[first].object, OutputContextBytes: sizes[first].contexts, Plans: sizes[first].plans}
	if view := repository.LocalView(owned); view != want {
		t.Fatalf("view after the first Slot = %+v, want %+v (STRLEN of the stored object and contexts)", view, want)
	}
	// The same Slot again is served from the cache and sizes the same: a
	// view that only knew fetched bytes would fall to zero on the common path.
	if _, err := repository.LoadSegmentQueryGroup(ctx, segments[first], 60, fallback); err != nil {
		t.Fatal(err)
	}
	if view := repository.LocalView(owned); view != want {
		t.Fatalf("view after a cached Slot = %+v, want %+v unchanged", view, want)
	}
	// The second Query Group joins; the view is the sum.
	if _, err := repository.LoadSegmentQueryGroup(ctx, segments[second], 60, fallback); err != nil {
		t.Fatal(err)
	}
	both := controlplane.LocalView{QueryGroups: 2, ObjectBytes: sizes[first].object + sizes[second].object,
		OutputContextBytes: sizes[first].contexts + sizes[second].contexts, Plans: sizes[first].plans + sizes[second].plans}
	if view := repository.LocalView(owned); view != both {
		t.Fatalf("view with both = %+v, want %+v", view, both)
	}
	// Releasing the first Query Group: the view is the second alone, and the
	// first is dropped rather than kept for the day it is owned again.
	onlySecond := controlplane.LocalView{QueryGroups: 1, ObjectBytes: sizes[second].object, OutputContextBytes: sizes[second].contexts, Plans: sizes[second].plans}
	if view := repository.LocalView([]execution.QueryGroupIdentity{second}); view != onlySecond {
		t.Fatalf("view over the second alone = %+v, want %+v", view, onlySecond)
	}
	if view := repository.LocalView(owned); view != onlySecond {
		t.Fatalf("view after the first was released and owned again = %+v, want %+v: the released entry is dropped, not kept", view, onlySecond)
	}
	// A Slot of the second served from the Snapshot -- a Segment naming no
	// content -- takes it out of the view: the view counts content reads only.
	legacy := segments[second]
	legacy.ObjectDigest, legacy.OutputContextRefs = "", nil
	served := 0
	if _, err := repository.LoadSegmentQueryGroup(ctx, legacy, 60, func(ctx context.Context) (controlplane.QueryGroup, error) {
		served++
		return repository.LoadQueryGroup(ctx, legacy.Publication.SnapshotRevision, second)
	}); err != nil || served != 1 {
		t.Fatalf("legacy Segment: err=%v served=%d", err, served)
	}
	if view := repository.LocalView(owned); view != (controlplane.LocalView{}) {
		t.Fatalf("view after a Snapshot-served Slot = %+v, want empty", view)
	}
}

// MissingObjects counts what the Worker can neither serve from its cache
// nor find in the store: stored objects count zero whether cached or not,
// an unknown digest counts one, and a digest named twice counts once.
func TestMissingObjectsCountsWhatNeitherCacheNorStoreHolds(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:missing-objects"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureObjectCache(64, 1<<20); err != nil {
		t.Fatal(err)
	}
	catalog := objectCatalogTwoGroups(t, 80)
	if _, _, err := repository.PublishCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	first, err := controlplane.DeriveQueryGroupObjectDigest(catalog.QueryGroups[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := controlplane.DeriveQueryGroupObjectDigest(catalog.QueryGroups[1])
	if err != nil {
		t.Fatal(err)
	}
	// Nothing cached in a fresh process: the store answers, both present.
	cold, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if missing, err := cold.MissingObjects(ctx, []execution.ObjectDigest{first, second, first}, nil); err != nil || missing != 0 {
		t.Fatalf("stored objects missing = %d (%v), want 0", missing, err)
	}
	if missing, err := cold.MissingObjects(ctx, []execution.ObjectDigest{first, "no-such-object"}, []execution.OutputContextDigest{"no-such-context"}); err != nil || missing != 2 {
		t.Fatalf("with two unknown digests missing = %d (%v), want 2", missing, err)
	}
	// Cached: answered without the store; deleted from the store but cached
	// still counts present, because the Worker can execute from what it holds.
	if _, err := repository.LoadQueryGroupObject(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, prefix+":qgobj:"+string(first)).Err(); err != nil {
		t.Fatal(err)
	}
	if missing, err := repository.MissingObjects(ctx, []execution.ObjectDigest{first}, nil); err != nil || missing != 0 {
		t.Fatalf("cached object gone from the store missing = %d (%v), want 0", missing, err)
	}
	if missing, err := cold.MissingObjects(ctx, []execution.ObjectDigest{first}, nil); err != nil || missing != 1 {
		t.Fatalf("uncached object gone from the store missing = %d (%v), want 1", missing, err)
	}
}
