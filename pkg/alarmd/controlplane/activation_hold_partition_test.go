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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// One Query Group that a publication brings back before its retirement has
// drained used to fail the whole activation, and every other Query Group of
// the publication waited on it. The activation now goes ahead without it:
// the publication becomes current, the others are cut over, the undrained
// one stays in Draining with no Segment and no activated Plan, and the
// counts add up (publication = activated + held). Once it drains, the next
// reconcile of the same publication brings it back; no new publication is
// needed, and a reconcile that finds nothing to bring back changes nothing.
func TestScheduleActivationHoldsTheUndrainedQueryGroupAndActivatesTheRest(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:hold-partition", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var holds []observability.ActivationHoldFacts
	repository.ConfigureObserver(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.Stage == observability.StageActivationHold && observation.ActivationHold != nil {
			holds = append(holds, *observation.ActivationHold)
		}
	}))
	lastHold := func() observability.ActivationHoldFacts {
		t.Helper()
		if len(holds) == 0 {
			t.Fatal("no activation hold facts were reported")
		}
		return holds[len(holds)-1]
	}
	initial := catalogWithAllSchedules(t, twoGroupCatalog(t, 80, true, true), 60, 0)
	onlyA := catalogWithAllSchedules(t, twoGroupCatalog(t, 80, true, false), 60, 0)
	returned := catalogWithAllSchedules(t, twoGroupCatalog(t, 82, true, true), 60, 0)
	a := onlyA.QueryGroups[0].Identity
	var b execution.QueryGroupIdentity
	plansOf := map[execution.QueryGroupIdentity]int{}
	for _, group := range returned.QueryGroups {
		plansOf[group.Identity] = len(group.Plans)
		if group.Identity != a {
			b = group.Identity
		}
	}
	if b == "" || len(returned.QueryGroups) != 2 {
		t.Fatalf("fixture did not yield two Query Groups: %+v", returned.QueryGroups)
	}
	stillRunning := func(group execution.QueryGroupIdentity) execution.ProgressLoadResult {
		return execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: group}, NextSlot: 60,
		}}
	}
	drained := func(group execution.QueryGroupIdentity) execution.ProgressLoadResult {
		return execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: group}, NextSlot: 90, LastFullSlot: 60,
			LastCompletionKind: execution.CompletionFull,
		}}
	}
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		a: stillRunning(a), b: stillRunning(b),
	}}
	compiler, semantics := runtimePlanCompiler(t)
	at := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		repository, compiler, semantics, progress, func() time.Time { return at },
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(catalog controlplane.Catalog) controlplane.SnapshotPublicationRef {
		t.Helper()
		snapshot, _, err := repository.PublishCatalog(ctx, catalog)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot.Publication
	}
	activeSet := func(state controlplane.ActivationState) map[execution.QueryGroupIdentity]struct{} {
		t.Helper()
		groups, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
		if err != nil {
			t.Fatal(err)
		}
		set := make(map[execution.QueryGroupIdentity]struct{}, len(groups))
		for _, group := range groups {
			set[group] = struct{}{}
		}
		return set
	}

	if _, err := reconciler.Ensure(ctx, publish(initial)); err != nil {
		t.Fatal(err)
	}
	at = time.Unix(90, 0)
	retired, err := reconciler.Ensure(ctx, publish(onlyA))
	if err != nil || len(retired.Draining) != 1 || retired.Draining[0].QueryGroup != b || retired.Draining[0].RetiredBoundary != 90 {
		t.Fatalf("retirement = (%+v, %v), want b draining from 90", retired.Draining, err)
	}

	// B comes back before it drained: the publication activates with A cut
	// over to its new content and B held out.
	at = time.Unix(180, 0)
	publication := publish(returned)
	held, err := reconciler.Ensure(ctx, publication)
	if err != nil || held.Current != publication || len(held.Draining) != 1 || held.Draining[0].QueryGroup != b ||
		held.Draining[0].RetiredBoundary != 90 {
		t.Fatalf("activation with b undrained = (%+v, %v), want the publication current with b still draining", held, err)
	}
	if set := activeSet(held); len(set) != 1 || !has(set, a) {
		t.Fatalf("active set with b held = %v, want only a", set)
	}
	if len(held.Plans) != plansOf[a] {
		t.Fatalf("activated Plans with b held = %d, want a's %d and none of b's", len(held.Plans), plansOf[a])
	}
	hold := lastHold()
	if hold.Reappeared != 1 || hold.Held != 1 || hold.MaxAgeSeconds != 90 || len(hold.Samples) != 1 || hold.Samples[0] != string(b) {
		t.Fatalf("hold facts = %+v, want b held for 90 seconds", hold)
	}
	if len(returned.QueryGroups) != len(activeSet(held))+hold.Held {
		t.Fatalf("publication %d != activated %d + held %d", len(returned.QueryGroups), len(activeSet(held)), hold.Held)
	}
	if schedule, err := runtime.ReadFrozenSchedule(ctx, a, 180); err != nil || schedule.Segment.Start != 180 ||
		schedule.Segment.Publication.SnapshotRevision != publication.SnapshotRevision {
		t.Fatalf("a's Schedule after the held activation = (%+v, %v), want it cut over to the publication at 180", schedule, err)
	}
	if _, retiredNow, err := runtime.ReadScheduleRetirement(ctx, b); err != nil || !retiredNow {
		t.Fatalf("b's retirement while held = (%t, %v), want still retired", retiredNow, err)
	}

	// B drains. The next reconcile of the same publication brings it back.
	progress.byGroup[b] = drained(b)
	returnedState, err := reconciler.Ensure(ctx, publication)
	if err != nil || returnedState.RecordRevision != held.RecordRevision+1 || returnedState.Current != publication ||
		len(returnedState.Draining) != 0 {
		t.Fatalf("reconcile after b drained = (%+v, %v), want b reactivated on the same publication", returnedState, err)
	}
	if set := activeSet(returnedState); len(set) != 2 || !has(set, a) || !has(set, b) {
		t.Fatalf("active set after b returned = %v, want a and b", set)
	}
	if len(returnedState.Plans) != plansOf[a]+plansOf[b] {
		t.Fatalf("activated Plans after b returned = %d, want %d", len(returnedState.Plans), plansOf[a]+plansOf[b])
	}
	// A's records are carried unchanged; b's are new, restart through
	// WARMING and belong to the publication's epoch.
	carried := make(map[execution.PlanIdentity]controlplane.PlanActivationRecord, len(held.Plans))
	for _, record := range held.Plans {
		carried[record.Fact.Plan] = record
	}
	returnedPlans := 0
	for _, record := range returnedState.Plans {
		if previous, ok := carried[record.Fact.Plan]; ok {
			if !reflect.DeepEqual(previous, record) {
				t.Fatalf("a carried Plan record changed when b returned: %+v -> %+v", previous, record)
			}
			continue
		}
		returnedPlans++
		if !record.Fact.Selected.ForceWarming ||
			record.Fact.Selected.StateApplyEpoch != execution.StateApplyEpoch(publication.PublicationEpoch) {
			t.Fatalf("b's returned Plan record = %+v, want ForceWarming at the publication's epoch", record.Fact.Selected)
		}
	}
	if returnedPlans != plansOf[b] {
		t.Fatalf("%d Plans returned with b, want %d", returnedPlans, plansOf[b])
	}
	if hold := lastHold(); hold.Held != 0 || hold.Reappeared != 1 {
		t.Fatalf("hold facts after b returned = %+v, want b reappeared and nothing held", hold)
	}
	if schedule, err := runtime.ReadFrozenSchedule(ctx, b, 180); err != nil || schedule.Segment.Start != 180 ||
		schedule.Segment.Publication.SnapshotRevision != publication.SnapshotRevision {
		t.Fatalf("b's Schedule after returning = (%+v, %v), want a Segment opened at 180 for the publication", schedule, err)
	}
	if _, retiredNow, err := runtime.ReadScheduleRetirement(ctx, b); err != nil || retiredNow {
		t.Fatalf("b's retirement after returning = (%t, %v), want cleared", retiredNow, err)
	}

	// Nothing is held any more: a further reconcile changes nothing.
	settled, err := reconciler.Ensure(ctx, publication)
	if err != nil || settled.RecordRevision != returnedState.RecordRevision {
		t.Fatalf("reconcile with nothing held = (%+v, %v), want the activation unchanged", settled, err)
	}
}

func has(set map[execution.QueryGroupIdentity]struct{}, group execution.QueryGroupIdentity) bool {
	_, ok := set[group]
	return ok
}
