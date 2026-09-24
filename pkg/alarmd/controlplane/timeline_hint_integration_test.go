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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A timeline read that carries the Worker's revision hint is answered from
// the cache when the cached body is at that revision, with no header probe;
// by one read of the body when it is not cached; and by the header path when
// the body turns out to be at another revision - a stale hint costs a probe
// and never returns a Segment from the wrong timeline.
func TestAHintedTimelineReadSkipsTheHeaderUntilTheHintGoesStale(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:hint", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	oldCatalog := validCatalog(t, 80)
	oldSnapshot, _, err := repository.PublishCatalog(ctx, oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := oldCatalog.QueryGroups[0].Identity
	oldOpen := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, nil)
	oldActivation := activationState(t, 1, oldSnapshot, oldOpen, nil)
	if err := repository.CompareAndSetInitialScheduleActivation(ctx, controlplane.ActivationExpectation{}, oldActivation,
		[]execution.InitialScheduleActivationFact{{Segment: oldOpen.Segment}}); err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	hinted := controlplane.WithTimelineRevisionHint(ctx, 1)

	// First hinted read: nothing cached, one body read, no header.
	before := repository.ControlReadCacheStats()
	if _, err := runtime.ReadFrozenSchedule(hinted, queryGroup, 120); err != nil {
		t.Fatal(err)
	}
	after := repository.ControlReadCacheStats()
	if after.HintedTimeline.Misses != before.HintedTimeline.Misses+1 || after.HintedTimeline.Hits != before.HintedTimeline.Hits {
		t.Fatalf("first hinted read: hinted %+v -> %+v, want one miss", before.HintedTimeline, after.HintedTimeline)
	}
	if after.Version.Hits+after.Version.Misses+after.Version.Refreshes != before.Version.Hits+before.Version.Misses+before.Version.Refreshes {
		t.Fatalf("a hinted read probed the header: version reads %+v -> %+v", before.Version, after.Version)
	}
	// Second hinted read: the cached body is at the hinted revision, no
	// Redis command at all.
	before = after
	if _, err := runtime.ReadFrozenSchedule(hinted, queryGroup, 120); err != nil {
		t.Fatal(err)
	}
	after = repository.ControlReadCacheStats()
	if after.HintedTimeline.Hits != before.HintedTimeline.Hits+1 || after.HintedTimeline.Misses != before.HintedTimeline.Misses {
		t.Fatalf("second hinted read: hinted %+v -> %+v, want one hit", before.HintedTimeline, after.HintedTimeline)
	}
	if after.Version.Hits+after.Version.Misses+after.Version.Refreshes != before.Version.Hits+before.Version.Misses+before.Version.Refreshes {
		t.Fatalf("a hinted cache hit probed the header: version reads %+v -> %+v", before.Version, after.Version)
	}

	// The timeline moves to revision 2 by a cutover; the Worker still hints
	// 1. The read must not answer with the old Segment: it falls to the
	// header path and returns the timeline as it is.
	newCatalog := catalogWithSchedule(t, oldCatalog, 120, 30)
	newSnapshot, _, err := repository.PublishCatalog(ctx, newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	boundary := execution.EvaluationTime(180)
	oldClosed := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, &boundary)
	newOpen := frozenSchedule(t, newSnapshot.Publication, newCatalog.QueryGroups[0], boundary, nil)
	newActivation := activationState(t, 2, newSnapshot, newOpen, nil)
	if err := repository.CompareAndSetScheduleCutover(ctx, controlplane.ActivationExpectation{
		RecordRevision: oldActivation.RecordRevision, Current: oldActivation.Current,
	}, newActivation, []execution.ScheduleCutoverFact{{OldSegment: oldClosed.Segment, NewSegment: newOpen.Segment}}); err != nil {
		t.Fatal(err)
	}
	before = repository.ControlReadCacheStats()
	loaded, err := runtime.ReadFrozenSchedule(hinted, queryGroup, boundary)
	if err != nil || loaded.Segment.Start != boundary {
		t.Fatalf("stale hint read = (%+v, %v), want the open Segment from the timeline as it is now", loaded.Segment, err)
	}
	after = repository.ControlReadCacheStats()
	if after.HintedTimeline.Refreshes != before.HintedTimeline.Refreshes+1 {
		t.Fatalf("a stale hint was not counted as one: hinted %+v -> %+v", before.HintedTimeline, after.HintedTimeline)
	}
	// With the right hint the moved timeline is served hinted again.
	before = after
	if _, err := runtime.ReadFrozenSchedule(controlplane.WithTimelineRevisionHint(ctx, 2), queryGroup, boundary); err != nil {
		t.Fatal(err)
	}
	after = repository.ControlReadCacheStats()
	if after.HintedTimeline.Hits+after.HintedTimeline.Misses != before.HintedTimeline.Hits+before.HintedTimeline.Misses+1 {
		t.Fatalf("the right hint after a move was not served hinted: %+v -> %+v", before.HintedTimeline, after.HintedTimeline)
	}
}

// A hinted activation request is answered from the Query Group's timeline
// with the same facts the header path answers, reading no header; after a
// cutover the closed Segment's Slot is historical on both paths.
func TestAHintedActivationRequestIsAnsweredFromTheTimelineWithoutTheHeader(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:hint-activation", time.Hour)
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
	plan := group.Plans[0]
	schedule := frozenSchedule(t, snapshot.Publication, group, 60, nil)
	state := activationState(t, 1, snapshot, schedule, nil)
	if err := repository.CompareAndSetInitialScheduleActivation(ctx, controlplane.ActivationExpectation{}, state,
		[]execution.InitialScheduleActivationFact{{Segment: schedule.Segment}}); err != nil {
		t.Fatal(err)
	}
	contract := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: group.Identity, EvaluationTime: 60},
		SnapshotRevision: snapshot.Publication.SnapshotRevision, QueryRevision: group.QueryPlan.QueryRevision,
		ScheduleRevision: group.ScheduleRevision, ScheduleSegmentStart: 60, DuePlanSetDigest: "due-plans-v1",
	}
	missing := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "9999"}
	request := execution.PlanActivationRequest{Contract: contract, Plans: []execution.PlanKey{{PlanIdentity: plan.Identity}, {PlanIdentity: missing}}}
	byHeader, err := repository.LoadActivations(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if byHeader.Facts[0].Selection == execution.ActivationNone {
		t.Fatal("the fixture's Plan is not active on the header path, so the two paths cannot be told apart")
	}
	// Counted on the Redis wire: the repository's own version counters tick
	// only inside a control version scope, and a request without one reads
	// the header live and silently.
	hook := newControlReadCountingHook()
	client.AddHook(hook)
	hook.reset()
	if _, err := repository.LoadActivations(ctx, request); err != nil {
		t.Fatal(err)
	}
	if hook.count("get", "header") == 0 {
		t.Fatal("the header path did not read the header on the wire, so the hinted path cannot be told from it")
	}
	hook.reset()
	hinted := controlplane.WithTimelineRevisionHint(ctx, 1)
	byTimeline, err := repository.LoadActivations(hinted, request)
	if err != nil {
		t.Fatal(err)
	}
	// The two paths are two implementations of one answer; the whole result
	// is compared so that a change to one of them shows here.
	if !reflect.DeepEqual(byTimeline, byHeader) || byTimeline.Facts[1].Selection != execution.ActivationNone {
		t.Fatalf("hinted activations = %+v, want the header path's %+v", byTimeline, byHeader)
	}
	if headers, activations := hook.count("get", "header"), hook.bodyReads("activation"); headers != 0 || activations != 0 {
		t.Fatalf("a hinted activation request read the header %d times and the activation body %d times, want neither", headers, activations)
	}
	// A contract naming another Segment than the timeline holds is refused on
	// the hinted path as on the header path.
	wrong := request
	wrong.Contract.ScheduleSegmentStart = 61
	if _, err := repository.LoadActivations(hinted, wrong); err == nil {
		t.Fatal("a contract that does not reference its persisted Segment was answered on the hinted path")
	}

	// After the cutover the Slot at 60 is in a closed Segment: historical,
	// every Plan ActivationNone, with the right hint (2) and without.
	newCatalog := catalogWithSchedule(t, catalog, 120, 30)
	newSnapshot, _, err := repository.PublishCatalog(ctx, newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	boundary := execution.EvaluationTime(180)
	oldClosed := frozenSchedule(t, snapshot.Publication, group, 60, &boundary)
	newOpen := frozenSchedule(t, newSnapshot.Publication, newCatalog.QueryGroups[0], boundary, nil)
	newActivation := activationState(t, 2, newSnapshot, newOpen, nil)
	if err := repository.CompareAndSetScheduleCutover(ctx, controlplane.ActivationExpectation{RecordRevision: state.RecordRevision, Current: state.Current},
		newActivation, []execution.ScheduleCutoverFact{{OldSegment: oldClosed.Segment, NewSegment: newOpen.Segment}}); err != nil {
		t.Fatal(err)
	}
	historical, err := repository.LoadActivations(controlplane.WithTimelineRevisionHint(ctx, 2), request)
	if err != nil || historical.Facts[0].Selection != execution.ActivationNone {
		t.Fatalf("hinted activations for a closed Segment = (%+v, %v), want ActivationNone", historical.Facts, err)
	}
	byHeaderAfter, err := repository.LoadActivations(ctx, request)
	if err != nil || byHeaderAfter.Facts[0].Selection != execution.ActivationNone {
		t.Fatalf("header activations for a closed Segment = (%+v, %v), want ActivationNone", byHeaderAfter.Facts, err)
	}
}

// A retired timeline is what a draining Query Group runs its backlog from:
// DrainingContent serves its last Segment's content until the timeline is
// gone, and says nothing for a Query Group that has none. A Worker
// executing from the view reads the retired backlog from this; a draining
// Query Group the view carried without content was refused as no_content
// on every round and never drained.
func TestDrainingContentServesTheRetiredTimelinesLastSegment(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:draining-content", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	compiler, semantics := runtimePlanCompiler(t)
	at := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		repository, compiler, semantics, progress, func() time.Time { return at },
	)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(sourceID, tableID string, boundary int64) execution.QueryGroupIdentity {
		t.Helper()
		catalog := catalogWithStrategyQueryTable(t, sourceID, tableID)
		snapshot, _, err := repository.PublishCatalog(ctx, catalog)
		if err != nil {
			t.Fatal(err)
		}
		at = time.Unix(boundary, 0)
		if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
			t.Fatalf("activation at %d: %v", boundary, err)
		}
		return catalog.QueryGroups[0].Identity
	}
	cpu := publish("1001", "system.cpu", 60)
	mem := publish("1002", "system.mem", 90)
	if cpu == mem {
		t.Fatal("the fixture needs the retired and the live Query Group to differ")
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	retiredAt, retired, err := runtime.ReadScheduleRetirement(ctx, cpu)
	if err != nil || !retired || retiredAt != 90 {
		t.Fatalf("retirement of the replaced Query Group = (%d, %t, %v), want retired at 90", retiredAt, retired, err)
	}
	segment, err := runtime.ReadFrozenSchedule(ctx, cpu, 60)
	if err != nil || segment.Segment.ObjectDigest == "" {
		t.Fatalf("retired Segment = (%+v, %v), want one that names its content", segment.Segment, err)
	}

	digest, refs, draining, err := repository.DrainingContent(ctx, cpu)
	if err != nil || !draining || digest != segment.Segment.ObjectDigest {
		t.Fatalf("DrainingContent(retired) = (%q, %v, %t, %v), want the retired Segment's content %q", digest, refs, draining, err, segment.Segment.ObjectDigest)
	}
	if len(refs) != len(segment.Segment.OutputContextRefs) {
		t.Fatalf("DrainingContent(retired) refs = %v, want the Segment's %v", refs, segment.Segment.OutputContextRefs)
	}
	digest, refs, draining, err = repository.DrainingContent(ctx, "never-activated")
	if err != nil || draining || digest != "" || refs != nil {
		t.Fatalf("DrainingContent(absent) = (%q, %v, %t, %v), want nothing", digest, refs, draining, err)
	}
}
