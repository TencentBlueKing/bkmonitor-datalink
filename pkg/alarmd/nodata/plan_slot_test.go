// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// planSlotRosterVersion is what this fixture's expected set derives to. It is
// read from the derivation rather than written out, because a hand-written one
// is a second statement of the same thing and they drift.
func planSlotRosterVersion(t *testing.T) string {
	t.Helper()
	roster, err := BuildRoster(RosterRequest{
		AggDimension: []string{HostIPDimension, HostCloudDimension},
		Scope:        hostScope("10.0.0.1|0", "10.0.0.2|0"),
		KnownHosts:   knownHosts("10.0.0.1|0", "10.0.0.2|0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return roster.Version
}

func planSlotIdentity() execution.PlanNoDataIdentity {
	return execution.PlanNoDataIdentity{
		Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
		StateGeneration: "generation-1",
	}
}

func planSlotInput(snapshot execution.NoDataMemorySnapshot, series ...map[string]string) PlanSlotInput {
	scope := hostScope("10.0.0.1|0", "10.0.0.2|0")
	return PlanSlotInput{
		NoData:           &contract.NoDataConfigV1{Continuous: 3, Level: 2, AggDimension: []string{HostIPDimension, HostCloudDimension}},
		Scope:            scope,
		Identity:         planSlotIdentity(),
		Snapshot:         snapshot,
		ApplyVersion:     execution.ApplyVersion{StateApplyEpoch: 7, EvaluationTime: 1000, SlotDigest: "slot"},
		ScheduleRevision: "revision-1",
		EvaluationTime:   1000,
		PeriodSeconds:    60,
		Completeness:     execution.CompletenessFull,
		Series:           series,
		KnownHosts:       knownHosts("10.0.0.1|0", "10.0.0.2|0"),
		HostsResolved:    true,
	}
}

func presentSeries() map[string]string {
	return map[string]string{HostIPDimension: "10.0.0.1", HostCloudDimension: "0"}
}

func storedSnapshot(t *testing.T, groups ...execution.NoDataGroupMemory) execution.NoDataMemorySnapshot {
	return execution.NoDataMemorySnapshot{
		Identity: planSlotIdentity(), Status: execution.NoDataMemoryFound,
		MarkerRevision: 4, SchemaVersion: execution.NoDataMemorySchemaV1,
		PersistedApplyVersion:   execution.ApplyVersion{StateApplyEpoch: 6, EvaluationTime: 940, SlotDigest: "slot"},
		PersistedMutationDigest: "digest", LastScheduleRevision: "revision-1",
		RosterVersion: planSlotRosterVersion(t), Groups: groups,
	}
}

// The whole round for a Plan that has never stored anything: one host reports,
// the other does not, and the memory that comes back is what the store should
// hold next.
func TestPlanSlotProducesSeriesAndTheMemoryToStore(t *testing.T) {
	result, err := EvaluatePlanSlot(planSlotInput(
		execution.NoDataMemorySnapshot{Identity: planSlotIdentity(), Status: execution.NoDataMemoryMissing},
		presentSeries(),
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeEvaluated {
		t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeEvaluated)
	}
	if len(result.Series) != 2 {
		t.Fatalf("series = %+v, want one per judged host", result.Series)
	}
	if result.Mutation == nil {
		t.Fatal("a round that judged two hosts stored nothing")
	}
	if err := result.Mutation.ValidateDigest(); err != nil {
		t.Fatalf("the mutation this round produced is not canonical: %v", err)
	}
	if result.Mutation.ExpectedMarkerRevision != 0 {
		t.Fatalf("expected marker revision = %d, want 0 for a record that does not exist yet",
			result.Mutation.ExpectedMarkerRevision)
	}
	if result.Mutation.RosterVersion != planSlotRosterVersion(t) {
		t.Fatalf("mutation roster version = %q, want the one the round decided against",
			result.Mutation.RosterVersion)
	}
	// Both hosts are remembered: the one that reported by when it was seen, the
	// one that did not by when its absence started.
	if len(result.Mutation.Groups) != 2 {
		t.Fatalf("stored groups = %+v, want both hosts", result.Mutation.Groups)
	}
}

// A round that did not see its whole period judges nothing, produces no series
// and writes nothing. The absence clocks must not move on evidence the round
// did not have.
func TestPlanSlotWritesNothingOnAnIncompleteRound(t *testing.T) {
	input := planSlotInput(storedSnapshot(t,
		execution.NoDataGroupMemory{GroupKey: hostTargetGroup(HostIdentity{IP: "10.0.0.1", CloudID: "0"}).Key(), LastSeen: 940},
	))
	input.Completeness = execution.CompletenessPartial

	result, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeSkippedQueryNotFull {
		t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeSkippedQueryNotFull)
	}
	if len(result.Series) != 0 || result.Mutation != nil {
		t.Fatalf("an incomplete round produced %d series and mutation %+v, want neither",
			len(result.Series), result.Mutation)
	}

	// And it still writes nothing when something other than the timestamps
	// moved. An incomplete round leaves the memory as it found it, so "the
	// memory did not change" stops the write on its own - which means deleting
	// the rule that an unjudged round writes nothing changes nothing here, and
	// a mutation run found exactly that. The state that tells them apart is a
	// roster version that moved during a round that judged nothing: the later
	// check sees a difference and would write, having decided nothing.
	stale := input
	stale.Snapshot.RosterVersion = "HISTORY/1"
	staleResult, err := EvaluatePlanSlot(stale)
	if err != nil {
		t.Fatal(err)
	}
	if staleResult.Mutation != nil {
		t.Fatalf("a round that judged nothing rewrote the memory because the roster version moved: %+v",
			staleResult.Mutation.Groups)
	}
}

// A record written by a newer build pauses this Plan's no-data detection and
// nothing else: no series, no write, and no error that would take the Plan's
// threshold detection down with it.
func TestPlanSlotPausesOnAMemoryItCannotRead(t *testing.T) {
	result, err := EvaluatePlanSlot(planSlotInput(execution.NoDataMemorySnapshot{
		Identity: planSlotIdentity(), Status: execution.NoDataMemoryUnreadable,
		SchemaVersion: execution.MaxSupportedNoDataMemorySchema + 1,
	}, presentSeries()))
	if err != nil {
		t.Fatalf("an unreadable record is a state, not a failure: %v", err)
	}
	if result.Outcome != OutcomeSkippedMemoryUnreadable {
		t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeSkippedMemoryUnreadable)
	}
	if len(result.Series) != 0 || result.Mutation != nil {
		t.Fatalf("a paused round produced %d series and mutation %+v, want neither",
			len(result.Series), result.Mutation)
	}
}

// A store that could not be read is a dependency failure, not a state. Carrying
// on with empty memory would restart every absence clock this Plan holds, which
// is the reading that turns an outage into a fresh one every Slot.
func TestPlanSlotRefusesAMemoryThatCouldNotBeRead(t *testing.T) {
	for _, status := range []execution.NoDataLoadStatus{
		execution.NoDataMemoryUnavailable, execution.NoDataMemoryTerminal,
	} {
		t.Run(string(status), func(t *testing.T) {
			_, err := EvaluatePlanSlot(planSlotInput(execution.NoDataMemorySnapshot{
				Identity: planSlotIdentity(), Status: status,
			}, presentSeries()))
			if err == nil {
				t.Fatal("EvaluatePlanSlot() carried on with memory that was never read")
			}
		})
	}
}

// A round whose memory comes out exactly as it went in writes nothing. Sending
// it would be correct and idempotent, and it would spend a mutation from the
// Slot's budget to store what is already stored.
func TestPlanSlotWritesNothingWhenTheMemoryDidNotMove(t *testing.T) {
	first, err := EvaluatePlanSlot(planSlotInput(
		execution.NoDataMemorySnapshot{Identity: planSlotIdentity(), Status: execution.NoDataMemoryMissing},
		presentSeries(),
	))
	if err != nil || first.Mutation == nil {
		t.Fatalf("fixture: the first round must store something: %+v, %v", first.Mutation, err)
	}

	// Feed the memory it just produced back in as the stored record, with the
	// same Slot. Nothing about the round changed, so nothing is written.
	repeat, err := EvaluatePlanSlot(planSlotInput(storedSnapshot(t, first.Mutation.Groups...), presentSeries()))
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Outcome != OutcomeEvaluated {
		t.Fatalf("outcome = %q, want the round to have judged", repeat.Outcome)
	}
	if repeat.Mutation != nil {
		t.Fatalf("an unchanged memory was written again: %+v", repeat.Mutation.Groups)
	}
	// It still produced the series - the trigger needs a point every round, and
	// skipping the write is about the store, not about the evaluation.
	if len(repeat.Series) != 2 {
		t.Fatalf("series = %d, want one per judged host even when nothing is written", len(repeat.Series))
	}
}

// The roster version is part of what the memory means, so a round decided
// against a different expected set writes, even when every timestamp is equal.
func TestPlanSlotWritesWhenOnlyTheRosterVersionMoved(t *testing.T) {
	first, err := EvaluatePlanSlot(planSlotInput(
		execution.NoDataMemorySnapshot{Identity: planSlotIdentity(), Status: execution.NoDataMemoryMissing},
		presentSeries(),
	))
	if err != nil || first.Mutation == nil {
		t.Fatalf("fixture: %+v, %v", first.Mutation, err)
	}
	stored := storedSnapshot(t, first.Mutation.Groups...)
	stored.RosterVersion = "HISTORY/1"

	result, err := EvaluatePlanSlot(planSlotInput(stored, presentSeries()))
	if err != nil {
		t.Fatal(err)
	}
	if result.Mutation == nil {
		t.Fatal("the memory was decided against a different roster and was not rewritten")
	}
	if result.Mutation.RosterVersion != planSlotRosterVersion(t) {
		t.Fatalf("mutation roster version = %q, want this round's", result.Mutation.RosterVersion)
	}
}

// A Plan that does not detect no-data is answered with nothing to do, so the
// caller can ask every Plan.
func TestPlanSlotSaysNothingForAPlanWithoutNoData(t *testing.T) {
	result, err := EvaluatePlanSlot(PlanSlotInput{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeNone || result.Mutation != nil || len(result.Series) != 0 {
		t.Fatalf("result = %+v, want nothing to do", result)
	}
}

// The stored record holds timestamps only for groups that remember something.
// A group whose clocks are both zero says nothing and would be refused by the
// mutation contract, so it must not reach it.
func TestPlanSlotStoresOnlyGroupsThatRememberSomething(t *testing.T) {
	groups := storedGroups(map[string]GroupMemory{
		"a": {LastSeen: 90},
		"b": {},
		"c": {FirstAbsent: 100},
	})
	if len(groups) != 2 {
		t.Fatalf("stored groups = %+v, want the two that remember something", groups)
	}
	if groups[0].GroupKey != "a" || groups[1].GroupKey != "c" {
		t.Fatalf("stored groups = %+v, want canonical order", groups)
	}
}
