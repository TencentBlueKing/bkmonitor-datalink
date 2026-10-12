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
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group's execution identity is the Segment its Slots are frozen
// from: the publication, query and schedule revisions a Slot's contract
// carries, and the Plans activated in it. It is answered from the timeline
// the Slot path already cached at the lease's revision and from nothing else:
// with the timeline in the store and not in the cache there is no answer, a
// revision the cache does not hold has none, and asking reads nothing and
// counts no cache hit.
func TestTheExecutionIdentityIsTheCachedSegmentASlotIsFrozenFrom(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:identity", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	catalog := validCatalog(t, 80)
	snapshot, _, err := repository.PublishCatalog(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	group := catalog.QueryGroups[0]
	open := frozenSchedule(t, snapshot.Publication, group, 60, nil)
	if err := repository.CompareAndSetInitialScheduleActivation(ctx, controlplane.ActivationExpectation{},
		activationState(t, 1, snapshot, open, nil), []execution.InitialScheduleActivationFact{{Segment: open.Segment}}); err != nil {
		t.Fatal(err)
	}
	at := execution.EvaluationTime(120)

	// In the store, not in the cache: no answer, and no read to get one.
	before := repository.ControlReadCacheStats()
	if identity, ok := repository.CachedExecutionIdentity(group.Identity, 1, at); ok {
		t.Fatalf("a cold cache answered %+v", identity)
	}
	if after := repository.ControlReadCacheStats(); after != before {
		t.Fatalf("asking a cold cache read or counted: %+v -> %+v", before, after)
	}

	// The Slot path reads the timeline at the lease's revision, as it does to
	// freeze a Slot.
	compiler, stateSemantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := runtime.ReadFrozenSchedule(controlplane.WithTimelineRevisionHint(ctx, 1), group.Identity, at)
	if err != nil {
		t.Fatal(err)
	}

	before = repository.ControlReadCacheStats()
	identity, ok := repository.CachedExecutionIdentity(group.Identity, 1, at)
	if !ok {
		t.Fatal("the timeline the Slot path cached gave no identity")
	}
	if after := repository.ControlReadCacheStats(); after != before {
		t.Fatalf("an identity answer read or counted: %+v -> %+v", before, after)
	}
	segment := frozen.Segment
	if identity.SnapshotRevision != segment.Publication.SnapshotRevision || identity.QueryRevision != segment.QueryRevision ||
		identity.ScheduleRevision != segment.ScheduleRevision {
		t.Fatalf("identity = %+v, want the frozen Segment's publication %s, query %s, schedule %s",
			identity, segment.Publication.SnapshotRevision, segment.QueryRevision, segment.ScheduleRevision)
	}
	want := make([]execution.PlanIdentity, 0, len(group.Plans))
	for _, plan := range group.Plans {
		want = append(want, plan.Identity)
	}
	if len(want) == 0 || !sameIdentities(identity.Plans, want) {
		t.Fatalf("identity plans = %v, want the Segment's activated Plans %v", identity.Plans, want)
	}
	// And when each of them is due, as the Segment froze it: what a reader of
	// a window needs to know which of them it should have seen.
	if len(identity.Schedules) != len(open.Plans) || !reflect.DeepEqual(identity.Schedules, open.Plans) {
		t.Fatalf("identity schedules = %+v, want the Segment's frozen Plan schedules %+v", identity.Schedules, open.Plans)
	}

	// A lease at another revision, and a time no Segment holds, have none.
	if _, ok := repository.CachedExecutionIdentity(group.Identity, 2, at); ok {
		t.Fatal("a revision the cache does not hold answered")
	}
	if _, ok := repository.CachedExecutionIdentity(group.Identity, 1, segment.Start-1); ok {
		t.Fatal("a time before the Segment answered")
	}
}

func sameIdentities(got, want []execution.PlanIdentity) bool {
	if len(got) != len(want) {
		return false
	}
	for _, plan := range want {
		if !slices.Contains(got, plan) {
			return false
		}
	}
	return true
}
