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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// An exhausted Plan that is given an explicit target can write again, and the
// round that writes clears the mark.
//
// This is the case decision-018 4.1 settles. A Plan whose history roster the
// horizon emptied carries the Plan-level mark over an empty memory. An operator
// then gives it a target, so the next round has groups again while no data has
// arrived yet - and a memory that states the mark while holding groups is one
// the contract layer refuses by name. Refused every round: this Plan's memory
// never takes another write, and nothing says so except a growing count of a
// refusal nobody is watching.
//
// Clearing costs nothing, which is why it is safe: with a non-empty roster the
// mark has no reader at all, and clearing it revives no group's absence, since
// those are held down one by one by their own suppressions.
func TestAnExhaustedPlanWritesAgainOnceItsRosterComesBack(t *testing.T) {
	snapshot := storedSnapshot(t, 0)
	snapshot.TrackingExhaustedAt = 900

	input := planSlotInput(snapshot)
	input.TrackingHorizonSeconds = trackingHorizon

	result, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatalf("an exhausted Plan whose roster came back could not write at all: %v", err)
	}
	if result.Outcome != OutcomeEvaluated {
		t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeEvaluated)
	}
	if result.Mutation == nil {
		t.Fatal("the round that gave this Plan a roster again wrote nothing")
	}
	if result.Mutation.TrackingExhaustedAt != 0 {
		t.Fatalf("mutation carries tracking-exhausted %d; a memory stating it while holding groups "+
			"is refused by the contract layer, so this Plan would never write again",
			result.Mutation.TrackingExhaustedAt)
	}
	if result.Mutation.GroupCount == 0 {
		t.Fatal("the roster came back with hosts but the memory remembers none of them")
	}
	if err := result.Mutation.ValidateDigest(); err != nil {
		t.Fatalf("the mutation this round produced is not canonical: %v", err)
	}
}

// A stored record that states the mark while holding groups is repaired by the
// next round rather than left alone.
//
// No correct writer produces that record - the contract layer refuses it - so
// this hands one to the evaluation directly. It is the one shape where the
// Plan-level mark moves while nothing else does: the groups are unchanged, the
// roster version is unchanged, and the present-as-of is unchanged, so a
// "nothing moved" check that compares only those three drops the write and the
// record keeps stating an exhaustion that is not true, for as long as the Plan
// exists.
func TestARecordStatingExhaustionOverGroupsIsRepairedNotKept(t *testing.T) {
	first, second := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2")
	snapshot := storedSnapshot(t, 0,
		execution.NoDataGroupMemory{GroupKey: first.Key(), FirstAbsent: 940},
		execution.NoDataGroupMemory{GroupKey: second.Key(), FirstAbsent: 940},
	)
	snapshot.TrackingExhaustedAt = 900

	input := planSlotInput(snapshot)
	// No horizon, so nothing about these two absences changes this round: they
	// stay open at the round they began, which is what leaves the mark as the
	// only thing that moved.
	result, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mutation == nil {
		t.Fatal("the round read a record stating exhaustion over two groups and wrote nothing, " +
			"so the record goes on stating it")
	}
	if result.Mutation.TrackingExhaustedAt != 0 {
		t.Fatalf("mutation carries tracking-exhausted %d, want it cleared", result.Mutation.TrackingExhaustedAt)
	}
	// The three the check already compared really are unchanged, so the write
	// above is owed to the mark and not to something else moving.
	if len(result.Mutation.Set) != 0 || len(result.Mutation.Del) != 0 {
		t.Fatalf("set = %+v del = %+v, want a round where no group moved",
			result.Mutation.Set, result.Mutation.Del)
	}
	if result.Mutation.PresentAsOf != snapshot.PresentAsOf {
		t.Fatalf("present-as-of = %d, want the stored %d", result.Mutation.PresentAsOf, snapshot.PresentAsOf)
	}
	if result.Mutation.RosterVersion != snapshot.RosterVersion {
		t.Fatalf("roster version = %q, want the stored %q", result.Mutation.RosterVersion, snapshot.RosterVersion)
	}
}

// emptyHistoryRosterVersion is what an exhausted Plan's expected set derives
// to: the history roster of a memory holding nothing. Read from the derivation
// rather than written out, so the fixture cannot drift from it.
func emptyHistoryRosterVersion(t *testing.T) string {
	t.Helper()
	roster, err := BuildRoster(RosterRequest{AggDimension: []string{HostIPDimension, HostCloudDimension}})
	if err != nil {
		t.Fatal(err)
	}
	if roster.Source != RosterHistory {
		t.Fatalf("roster source = %q, want the history roster an exhausted Plan derives", roster.Source)
	}
	return roster.Version
}

// A round where the mark did not move either writes nothing, so the check above
// is a comparison rather than an unconditional write.
func TestAnExhaustedPlanThatIsStillExhaustedWritesNothing(t *testing.T) {
	snapshot := execution.NoDataMemorySnapshot{
		Identity: planSlotIdentity(), Status: execution.NoDataMemoryFound,
		Representation: execution.NoDataRepresentationPerGroup,
		MarkerRevision: 4, SchemaVersion: execution.NoDataMemorySchemaV3,
		PersistedApplyVersion:   execution.ApplyVersion{StateApplyEpoch: 6, EvaluationTime: 940, SlotDigest: "slot"},
		PersistedMutationDigest: "digest", LastScheduleRevision: "revision-1",
		RosterVersion: emptyHistoryRosterVersion(t), TrackingExhaustedAt: 900,
	}
	input := planSlotInput(snapshot)
	// No target scope, so the roster is the history one this Plan's own memory
	// derives - and that memory is empty, which is the state the mark exists to
	// explain.
	input.Scope = nil
	input.TrackingHorizonSeconds = trackingHorizon

	result, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mutation != nil {
		t.Fatalf("a round that changed nothing wrote %+v", result.Mutation)
	}
	if len(result.Series) != 0 {
		t.Fatalf("an exhausted Plan produced %d synthetic series, want none", len(result.Series))
	}
}

// wholeItemRosterVersion is what an item with no no-data dimensions derives its
// expected set to.
func wholeItemRosterVersion(t *testing.T) (string, RosterSource) {
	t.Helper()
	roster, err := BuildRoster(RosterRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return roster.Version, roster.Source
}

// An exhausted Plan in whole-item mode writes the round its data came back.
//
// This is the same rule as the evaluation-level case, taken through the seam
// that builds the mutation, because the two halves of it live on opposite sides
// of that seam. Clearing the mark is the evaluation's answer; the present-as-of
// that makes the clearing legal is computed here, and the contract layer refuses
// a memory that cleared the mark without the present-as-of advancing. Stated
// only on the evaluation side, the rule reads as satisfied while every write
// this Plan makes is refused.
func TestWholeItemDataLetsAnExhaustedPlanWriteAgain(t *testing.T) {
	version, source := wholeItemRosterVersion(t)
	snapshot := execution.NoDataMemorySnapshot{
		Identity: planSlotIdentity(), Status: execution.NoDataMemoryFound,
		Representation: execution.NoDataRepresentationPerGroup,
		MarkerRevision: 4, SchemaVersion: execution.NoDataMemorySchemaV3,
		PersistedApplyVersion:   execution.ApplyVersion{StateApplyEpoch: 6, EvaluationTime: 940, SlotDigest: "slot"},
		PersistedMutationDigest: "digest", LastScheduleRevision: "revision-1",
		RosterVersion: version, TrackingExhaustedAt: 900,
	}
	input := planSlotInput(snapshot, presentSeries())
	// No no-data dimensions and no target: every series projects onto the
	// whole-item group, which is the mode an exhausted history Plan lands in
	// when its agg_dimension is emptied.
	input.NoData.AggDimension = nil
	input.Scope = nil
	input.TrackingHorizonSeconds = trackingHorizon

	result, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatalf("an exhausted Plan whose data came back could not write: %v", err)
	}
	if result.Facts.RosterSource != source {
		t.Fatalf("roster source = %q, want %q", result.Facts.RosterSource, source)
	}
	if result.Mutation == nil {
		t.Fatal("the round the data came back wrote nothing, so the record goes on stating the exhaustion")
	}
	if result.Mutation.TrackingExhaustedAt != 0 {
		t.Fatalf("mutation carries tracking-exhausted %d, want it cleared", result.Mutation.TrackingExhaustedAt)
	}
	if result.Mutation.PresentAsOf != input.EvaluationTime {
		t.Fatalf("present-as-of = %d, want this round (%d): the contract layer refuses a cleared mark "+
			"without it, so the clearing above would never reach the store",
			result.Mutation.PresentAsOf, input.EvaluationTime)
	}
	if result.Mutation.GroupCount != 0 {
		t.Fatalf("group count = %d, want none: the whole-item group is never remembered", result.Mutation.GroupCount)
	}
}
