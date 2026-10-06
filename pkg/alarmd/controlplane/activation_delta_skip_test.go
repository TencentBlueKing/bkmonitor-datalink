// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Worker that does not read the control header while two activations go by
// finds a header two revisions past the one its cache holds. The delta it can
// read is the second activation's, which says nothing about what the first
// one changed. Keeping timelines on the strength of that delta served the
// first activation's edit from the cache, at the old content, with nothing
// to expire it: a timeline cache has no time bound, and the sampled audit
// only counts. So a Worker that skipped a revision drops everything, reads
// the edited timeline fresh, and says so in its own counter.
func TestWorkerThatSkippedARevisionDropsEveryTimeline(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:activation-delta-skip"
	leader, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	at := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconciler(leader, compiler, semantics, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	both := catalogWithAllSchedules(t, twoGroupCatalog(t, 80, true, true), 60, 0)
	edited := catalogWithAllSchedules(t, twoGroupCatalog(t, 82, true, true), 60, 0)
	onlyA := catalogWithAllSchedules(t, twoGroupCatalog(t, 82, true, false), 60, 0)
	a := onlyA.QueryGroups[0].Identity
	var b execution.QueryGroupIdentity
	for _, group := range both.QueryGroups {
		if group.Identity != a {
			b = group.Identity
		}
	}
	publishAndActivate := func(catalog controlplane.Catalog) controlplane.ActivationState {
		t.Helper()
		snapshot, _, err := leader.PublishCatalog(ctx, catalog)
		if err != nil {
			t.Fatal(err)
		}
		state, err := reconciler.Ensure(ctx, snapshot.Publication)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}

	worker, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(worker, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	initial := publishAndActivate(both)
	if _, err := worker.LoadActivation(ctx); err != nil {
		t.Fatal(err)
	}
	for _, group := range []execution.QueryGroupIdentity{a, b} {
		if _, err := runtime.ReadFrozenSchedule(ctx, group, 120); err != nil {
			t.Fatal(err)
		}
	}
	before, err := runtime.ReadFrozenSchedule(ctx, a, 120)
	if err != nil {
		t.Fatal(err)
	}

	// Revision n+1 edits A. The Worker reads nothing while it goes by.
	at = time.Unix(180, 0)
	editedState := publishAndActivate(edited)
	if editedState.RecordRevision != initial.RecordRevision+1 {
		t.Fatalf("edited activation revision = %d, want %d", editedState.RecordRevision, initial.RecordRevision+1)
	}
	// Revision n+2 retires B; its delta names B alone.
	at = time.Unix(240, 0)
	retiredState := publishAndActivate(onlyA)
	if retiredState.RecordRevision != initial.RecordRevision+2 {
		t.Fatalf("retiring activation revision = %d, want %d", retiredState.RecordRevision, initial.RecordRevision+2)
	}

	hook := newControlReadCountingHook()
	client.AddHook(hook)
	stale, err := runtime.ReadFrozenSchedule(ctx, a, 240)
	if err != nil {
		t.Fatal(err)
	}
	if got := hook.bodyReads("timeline"); got != 1 {
		t.Fatalf("reading the timeline the skipped revision edited read %d bodies, want 1: it was served from a cache the delta could not speak for", got)
	}
	freshRepository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	freshRuntime, err := controlplane.NewRedisCatalogRuntime(freshRepository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := freshRuntime.ReadFrozenSchedule(ctx, a, 240)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Segment.Start == before.Segment.Start || stale.Segment.ObjectDigest == before.Segment.ObjectDigest {
		t.Fatalf("the Worker still serves the segment from before the edit (start %d, object %s)", stale.Segment.Start, stale.Segment.ObjectDigest)
	}
	if stale.Segment.Start != fresh.Segment.Start || stale.Segment.ObjectDigest != fresh.Segment.ObjectDigest ||
		stale.Segment.ScheduleRevision != fresh.Segment.ScheduleRevision {
		t.Fatalf("the Worker that skipped a revision reads segment start=%d object=%s revision=%s, a fresh reader start=%d object=%s revision=%s",
			stale.Segment.Start, stale.Segment.ObjectDigest, stale.Segment.ScheduleRevision,
			fresh.Segment.Start, fresh.Segment.ObjectDigest, fresh.Segment.ScheduleRevision)
	}
	stats := worker.ControlReadCacheStats()
	if stats.DeltaSkips != 1 || stats.Delta != (controlplane.ControlReadCacheObjectStats{}) {
		t.Fatalf("delta stats = %+v skips = %d, want the one crossing counted as a skipped revision and neither as a hit nor a miss", stats.Delta, stats.DeltaSkips)
	}
}
