// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// versionedGaps refuses, as the gap store does, a second statement of one
// marker at one version, and counts the refusals.
type versionedGaps struct {
	*windowedPorts
	written map[string]execution.MutationDigest
	refused int
}

func (gaps *versionedGaps) ApplyGap(ctx context.Context, request execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	if gaps.written == nil {
		gaps.written = map[string]execution.MutationDigest{}
	}
	result := execution.GapGuardApplyResult{}
	var applied []execution.PlanGapMutation
	for _, item := range request.Items {
		key := fmt.Sprintf("%v/%v", item.Identity, item.ApplyVersion)
		if earlier, found := gaps.written[key]; found && earlier != item.MutationDigest {
			gaps.refused++
			result.Items = append(result.Items, execution.GapGuardApplyItemResult{Identity: item.Identity, Status: execution.GapGuardConflict})
			continue
		}
		gaps.written[key] = item.MutationDigest
		applied = append(applied, item)
		result.Items = append(result.Items, execution.GapGuardApplyItemResult{Identity: item.Identity, Status: execution.GapGuardApplied})
	}
	if len(applied) != 0 {
		if _, err := gaps.windowedPorts.ApplyGap(ctx, execution.GapGuardApplyRequest{Contract: request.Contract,
			Items: applied, Retention: request.Retention}); err != nil {
			return execution.GapGuardApplyResult{}, err
		}
	}
	return result, nil
}

// 4a: a Plan new to its activation, judged first-seen, that the Slot's budget
// cuts after its second window writes one statement at the Slot's version -
// the budget's open, in the tail - and not the marker that records it ran
// besides. The marker had been written in the head, and the budget's open at
// the same version was then refused as another statement on every attempt,
// so the Slot was retried for as long as it was due.
func TestAFirstSeenPlanCutByTheBudgetAfterTwoWindowsWritesOneStatementAtItsVersion(t *testing.T) {
	fixture, _ := newPrepassFixture(t, 6)
	gaps := &versionedGaps{windowedPorts: fixture.ports}
	stream := fixture.stream
	stream.coordinator.ports.GapGuard = gaps
	for position := range 5 {
		fixture.mergeNew(t, position)
	}
	if _, first := stream.windows.apply.firstSeen[fixture.due.Key()]; !first || stream.windows.count != 2 {
		t.Fatalf("first-seen=%v windows=%d, want a first-seen Plan with two windows landed", first, stream.windows.count)
	}
	if len(fixture.ports.guards) != 0 {
		t.Fatalf("the head wrote %+v; a first-seen Plan's marker waits for the tail", fixture.ports.guards)
	}

	if err := stream.completeWindowsBeyondSlotBudget(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The tail ends at the Progress this fixture does not commit; what it
	// wrote before is what is read.
	_, _ = stream.coordinator.finishWindowedSlot(context.Background(), stream)

	if gaps.refused != 0 {
		t.Fatalf("%d statements met another of their version: the Slot would be retried for ever", gaps.refused)
	}
	guards := fixture.ports.guards
	if len(guards) != 1 || len(guards[0].Scopes) != 1 || guards[0].Scopes[0].Kind == execution.GapClear ||
		guards[0].Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonExecutionBudgetExhausted) {
		t.Fatalf("statements %+v, want the budget's one open and no satisfied marker", guards)
	}
}

// A first-seen Plan that writes nothing else this Slot has its marker written
// in the tail, after every window's events.
func TestAFirstSeenPlanWritesItsMarkerInTheTail(t *testing.T) {
	const series = 6
	fixture, _ := newPrepassFixture(t, series)
	for position := range series {
		fixture.mergeNew(t, position)
	}
	stream := fixture.stream
	_, _ = stream.coordinator.finishWindowedSlot(context.Background(), stream)
	guards := fixture.ports.guards
	if len(guards) != 1 || len(guards[0].Scopes) != 1 || guards[0].Scopes[0].Kind != execution.GapClear ||
		guards[0].Scopes[0].Scope != (execution.GapScope{}) {
		t.Fatalf("statements %+v, want the one satisfied marker of a first-seen Plan", guards)
	}
	if gap, event := fixture.traceIndex(t, "gap"), fixture.traceIndex(t, fmt.Sprintf("event:event-%s", prepassSeries(series-1))); gap < event {
		t.Fatalf("the first-seen marker went out before the last window's events: %v", fixture.ports.trace)
	}
}

// The case the tail placement leaves: a first-seen Plan whose Slot landed
// windows and never reached its tail - its retries ran out, or Progress moved
// past it - left its State at that Slot's version and no marker. The next
// Slot reads that State as written before it, so the Plan is not first-seen
// there: forced to warm up with no marker, it is a changed Plan, writes
// nothing, and the protection path writes its WARMING marker under
// PLAN_REACTIVATED - warming restarted on a generation that ran. It warms
// up; it is not taken for a Plan that ran and evaluated without one.
func TestAFirstSeenPlanWhoseSlotNeverReachedItsTailWarmsUpOnTheNextSlot(t *testing.T) {
	const series = 4
	fixture, ports := newPrepassFixture(t, series)
	// What the abandoned Slot, a period before, wrote: every series of its
	// two landed windows at its own version (ports.older), and no marker.
	for index := range series {
		ports.persisted[execution.SeriesIdentityDigest(prepassSeries(index))] = "abandoned-slot"
	}
	for position := range 3 {
		fixture.mergeNew(t, position)
	}
	stream := fixture.stream
	apply := stream.windows.apply
	if _, first := apply.firstSeen[fixture.due.Key()]; first || !apply.changed(fixture.due) {
		t.Fatalf("first-seen=%v changed=%v, want the Plan warmed up", first, apply.changed(fixture.due))
	}
	if len(fixture.ports.events) != 0 || len(fixture.ports.stateApplied) != 0 || len(fixture.ports.guards) != 0 {
		t.Fatalf("a Plan warming up wrote events %v, State %v, gaps %+v", fixture.ports.events,
			fixture.ports.stateApplied, fixture.ports.guards)
	}
	found := false
	for _, fact := range apply.changedActivations.Facts {
		found = found || fact.Key() == fixture.due.Key()
	}
	if !found {
		t.Fatal("the warmed-up Plan is not among the activations the protection path writes WARMING for")
	}
	if reason := guardReasonFor(stream.header.DuePlans, apply.changedActivations, stream.gaps, stream.unseenPlans()); reason !=
		execution.ReasonCode(contract.ReasonPlanReactivated) {
		t.Fatalf("WARMING marker reason = %s, want %s", reason, contract.ReasonPlanReactivated)
	}
}

// 4b: a window's event bound is the event cap over the most events one series
// can send in the Slot, one per PRIMARY record it holds - three here - and
// not the cap itself: six events hold two series, so five series need three
// windows.
func TestAWindowHoldsTheEventCapOverTheMostRecordsOneSeriesHolds(t *testing.T) {
	fixture := newWindowedFixture(t)
	stream, coordinator := fixture.stream, fixture.stream.coordinator
	coordinator.budget.MaxStateMutations, coordinator.budget.MaxEvents = 100, 6
	records := make([]contract.CanonicalRecordV2, 3)
	for index := range records {
		records[index] = contract.CanonicalRecordV2{RecordID: fmt.Sprintf("record-%d", index), SourceTime: int64(60 * index)}
	}
	dataset := execution.NewDataset(records)
	view, err := execution.NewDatasetView(dataset, []uint32{0, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	for index := range 5 {
		stream.windows.prepared = append(stream.windows.prepared, preparedSeries{due: &fixture.due,
			identity: execution.SeriesIdentityDigest(fmt.Sprintf("series-%d", index)),
			inputs: []execution.SeriesEvaluationInputRequest{{Inputs: []execution.NamedInputBinding{{
				Role: execution.InputRolePrimary, Dataset: dataset, View: view}}}}})
	}
	if got := stream.eventsPerSeries(); got != 3 {
		t.Fatalf("events per series = %d, want the three records one series holds", got)
	}
	deadline := time.UnixMilli(1_800_000_000_000)
	marker := &execution.StageMarker{}
	marker.Begin(execution.SlotStageEvaluate)
	marker.Extend(deadline, deadline)
	stream.fixWindowCeiling(execution.WithStageMarker(context.Background(), marker), 5)
	if stream.windows.perWindow != 2 || stream.windows.ceilingWindows != 3 {
		t.Fatalf("s_min = %d over %d windows, want 2 over 3", stream.windows.perWindow, stream.windows.ceilingWindows)
	}
}

// 4c: a no-data round whose scan found many absences raises the ceiling once
// by their windows, the completion row says so, and the Slot is not claimed
// while its absence windows land one window bound apart; without the raise it
// would be claimed at its second boundary.
func TestAnAbsenceHeavySlotRaisesItsCeilingAndIsNotDeclinedWhileItsAbsenceWindowsLand(t *testing.T) {
	const grace = time.Minute
	due := noDataWiredPlan(t)
	evaluation := int64(1_788_000_000)
	groups, _ := rosterGroups(t, 30, evaluation-60)
	store := &rosterStore{headers: map[execution.StateGeneration]execution.NoDataMemorySnapshot{due.StateGeneration: rosterHeader(evaluation - 60)},
		groups: map[execution.StateGeneration][]execution.NoDataGroupMemory{due.StateGeneration: groups}}
	stream := rosterStream(t, due, store, 8)
	stream.coordinator.budget.MaxStateMutations = 5
	deadline := time.UnixMilli(1_800_000_000_000)
	marker := &execution.StageMarker{}
	marker.Begin(execution.SlotStageEvaluate)
	marker.Extend(deadline, deadline)
	ctx := execution.WithStageMarker(context.Background(), marker)
	stream.fixWindowCeiling(ctx, 1)
	before := stream.windows.ceilingWindows

	present := nodata.NewPresentSet(due.CompiledPlan.NoData().AggDimension)
	if _, err := stream.decideNoDataRound(ctx, due, present, execution.CompletenessFull,
		func(completedSeries) error { return nil }); err != nil {
		t.Fatal(err)
	}
	absent := uint64(len(groups))
	raised := (absent + stream.windows.perWindow - 1) / stream.windows.perWindow
	usage := stream.budgetUsage()
	if usage.WindowCeilingRaisedWindows != raised || usage.WindowCeilingWindows != before+raised {
		t.Fatalf("usage = %+v, want the ceiling raised by %d windows to %d", usage, raised, before+raised)
	}

	now := deadline
	stream.coordinator.ports.Now = func() time.Time { return now }
	for boundary := uint64(1); boundary <= usage.WindowCeilingWindows; boundary++ {
		now = deadline.Add(time.Duration(boundary-1)*windowStepBound + time.Second)
		if err := execution.EnterCommit(ctx, execution.SlotStageOutput); err != nil {
			t.Fatalf("window %d: %v", boundary, err)
		}
		stream.windowBoundary(ctx)
		if _, _, claimed := marker.Decline(now, grace); claimed {
			t.Fatalf("window %d of %d: claimed while its absence windows were landing", boundary, usage.WindowCeilingWindows)
		}
	}
}

// 4d: the present groups' NORMAL series a no-data round says are not in S
// either - S counted the groups' real series, not their synthetic ones - so
// a round whose groups all reported raises the ceiling by the windows those
// series need. Without it the ceiling of a Slot whose every group reports
// held no window for half of what it says.
func TestAPresentNoDataRoundRaisesItsCeilingByItsNormalSeries(t *testing.T) {
	due := noDataWiredPlan(t)
	evaluation := int64(1_788_000_000)
	groups, dimensions := rosterGroups(t, 30, evaluation-60)
	store := &rosterStore{headers: map[execution.StateGeneration]execution.NoDataMemorySnapshot{due.StateGeneration: rosterHeader(evaluation - 60)},
		groups: map[execution.StateGeneration][]execution.NoDataGroupMemory{due.StateGeneration: groups}}
	stream := rosterStream(t, due, store, 8)
	stream.coordinator.budget.MaxStateMutations = 5
	deadline := time.UnixMilli(1_800_000_000_000)
	marker := &execution.StageMarker{}
	marker.Begin(execution.SlotStageEvaluate)
	marker.Extend(deadline, deadline)
	ctx := execution.WithStageMarker(context.Background(), marker)
	stream.fixWindowCeiling(ctx, len(dimensions))
	before := stream.windows.ceilingWindows

	present := nodata.NewPresentSet(due.CompiledPlan.NoData().AggDimension)
	for _, host := range dimensions {
		if _, ok := present.Add(host); !ok {
			t.Fatal("fixture: a host did not project")
		}
	}
	said := 0
	round, err := stream.decideNoDataRound(ctx, due, present, execution.CompletenessFull,
		func(completedSeries) error { said++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if round.facts.Absent != 0 || said == 0 {
		t.Fatalf("facts %+v with %d series said, want every group present and its NORMAL said", round.facts, said)
	}
	raised := (uint64(said) + stream.windows.perWindow - 1) / stream.windows.perWindow
	usage := stream.budgetUsage()
	if usage.WindowCeilingRaisedWindows != raised || usage.WindowCeilingWindows != before+raised {
		t.Fatalf("usage = %+v, want the ceiling raised by %d windows for %d NORMAL series to %d",
			usage, raised, said, before+raised)
	}
}
