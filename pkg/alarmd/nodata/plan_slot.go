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
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// PlanSlotInput is one Plan's whole no-data round: what the Slot saw, what the
// store held, and the version facts a write would need.
type PlanSlotInput struct {
	// NoData and Scope are the two things the decision reads off the Plan. The
	// worker holds the compiled form rather than the frozen one, and passing a
	// half-filled Plan across so this could read two fields off it would put an
	// object in the code that looks like a Plan and is not one.
	NoData           *contract.NoDataConfigV1
	Scope            *contract.TargetScopeV2
	Identity         execution.PlanNoDataIdentity
	Snapshot         execution.NoDataMemorySnapshot
	ApplyVersion     execution.ApplyVersion
	ScheduleRevision execution.PlanScheduleRevision
	EvaluationTime   int64
	PeriodSeconds    int64
	Completeness     execution.Completeness
	Series           []map[string]string
	KnownHosts       map[string]struct{}
	HostsResolved    bool
	OutOfBusiness    map[string]struct{}
}

// PlanSlotResult is everything the worker needs from one Plan's no-data round.
//
// Mutation is nil when the memory must not be touched, which is three different
// situations wearing one shape: the round judged nothing, the memory came back
// unreadable, or the memory is already exactly what this round would write.
// Outcome is what tells them apart, and it is the reason Outcome exists.
type PlanSlotResult struct {
	Outcome  SlotOutcome
	Series   []SyntheticSeries
	Mutation *execution.PlanNoDataMutation
	Facts    AbsenceFacts
}

// EvaluatePlanSlot turns one Plan's Slot into its no-data decision, the series
// that decision becomes, and the memory to store.
//
// A store that could not be read is an error rather than an outcome. It is a
// dependency failure, the same kind the gap load already raises, and the Slot
// has machinery for those. That is exactly why an unreadable schema is not one:
// a record written by a newer build is not a failure of anything, it is a state
// the system is briefly in, and routing it through the error path would take
// the Plan's threshold detection down with it.
func EvaluatePlanSlot(input PlanSlotInput) (PlanSlotResult, error) {
	if input.NoData == nil {
		return PlanSlotResult{Outcome: OutcomeNone}, nil
	}
	switch input.Snapshot.Status {
	case execution.NoDataMemoryUnreadable:
		return PlanSlotResult{Outcome: OutcomeSkippedMemoryUnreadable}, nil
	case execution.NoDataMemoryMissing, execution.NoDataMemoryFound:
	default:
		return PlanSlotResult{}, fmt.Errorf(
			"alarmd nodata: no-data memory for plan %s was not read: %s (%s)",
			input.Identity.Plan.StrategyID, input.Snapshot.Status, input.Snapshot.ReasonCode)
	}

	memory := loadedMemory(input.Snapshot)
	result, outcome, err := EvaluateSlot(SlotInput{
		Plan:           &contract.EvaluationPlanV2{NoData: input.NoData, TargetScope: input.Scope},
		EvaluationTime: input.EvaluationTime,
		PeriodSeconds:  input.PeriodSeconds,
		Completeness:   input.Completeness,
		Series:         input.Series,
		KnownHosts:     input.KnownHosts,
		HostsResolved:  input.HostsResolved,
		OutOfBusiness:  input.OutOfBusiness,
		Memory:         memory,
	})
	if err != nil {
		return PlanSlotResult{}, err
	}

	slot := PlanSlotResult{Outcome: outcome, Facts: result.Facts}
	if outcome != OutcomeEvaluated {
		// A round that judged nothing writes nothing. The memory it would write
		// is the memory it started with, and writing it back would burn a state
		// mutation to store what is already there - but more importantly, the
		// absence clocks must not move on evidence the round did not have.
		return slot, nil
	}
	slot.Series = SyntheticSeriesFor(SyntheticInput{
		EvaluationTime: input.EvaluationTime,
		PeriodSeconds:  input.PeriodSeconds,
		Result:         result,
		Memory:         result.Memory,
		Roster:         result.Roster,
	})

	groups := storedGroups(result.Memory)
	presentAsOf := presentAsOf(groups, input.Snapshot.PresentAsOf, input.EvaluationTime)
	if sameStoredGroups(groups, input.Snapshot.Groups) && input.Snapshot.RosterVersion == result.Roster.Version &&
		presentAsOf == input.Snapshot.PresentAsOf {
		// Nothing moved. Sending the mutation anyway would be correct and
		// idempotent - the digest would match and the store would say so - but
		// it costs a round trip per Plan per Slot for a write that changes
		// nothing, and the Slot's mutation budget is the scarce thing here.
		//
		// The present-as-of is part of "nothing moved" because it is part of
		// the memory: it is the last-seen time of every group stored without
		// an absence, so a round where it advanced changed what those groups
		// say even though their own entries are unchanged.
		return slot, nil
	}
	mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		Identity: input.Identity,
		// Which record the revision and the loaded groups below came out of.
		// Without it the statement claims to be a delta against the per-group
		// record whatever it was actually derived from, and a Plan still on
		// the whole-memory record can never write one.
		DerivedFrom:            input.Snapshot.Representation,
		LoadedApplyVersion:     input.Snapshot.PersistedApplyVersion,
		ExpectedMarkerRevision: input.Snapshot.MarkerRevision,
		ApplyVersion:           input.ApplyVersion,
		ScheduleRevision:       input.ScheduleRevision,
		RosterVersion:          result.Roster.Version,
		PresentAsOf:            presentAsOf,
		Memory:                 groups,
		Loaded:                 input.Snapshot.Groups,
		LoadedPresentAsOf:      input.Snapshot.PresentAsOf,
	})
	if err != nil {
		return PlanSlotResult{}, err
	}
	slot.Mutation = &mutation
	return slot, nil
}

// presentAsOf is the round this Plan last had data in.
//
// It is read off the memory the round produced rather than off the round's
// inputs: every group the evaluation saw is written with this round's time and
// no absence, by the two places that record presence, so a memory holding one
// of those is a round that had data. Taking it from the inputs instead would
// have to decide what "had data" means for a series that arrived for a group
// the roster had already dropped, and the memory has already decided that.
//
// With nothing seen, the stored value is carried forward. It must never go
// backwards: it is the last-seen time of every group stored as present, so
// moving it back would move those groups' clocks back with it.
func presentAsOf(groups []execution.NoDataGroupMemory, stored int64, evaluationTime int64) int64 {
	for _, group := range groups {
		if group.FirstAbsent == 0 && group.LastSeen == evaluationTime {
			return evaluationTime
		}
	}
	return stored
}

// loadedMemory is the stored record as the evaluation reads it. A record that
// was never written and a record that remembers nothing load the same way,
// which is right: both mean this Plan knows nothing about any group yet.
func loadedMemory(snapshot execution.NoDataMemorySnapshot) map[string]GroupMemory {
	memory := make(map[string]GroupMemory, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		memory[group.GroupKey] = GroupMemory{LastSeen: group.LastSeen, FirstAbsent: group.FirstAbsent}
	}
	return memory
}

// storedGroups is the evaluation's memory as the record holds it: canonical
// order, and nothing that remembers nothing.
func storedGroups(memory map[string]GroupMemory) []execution.NoDataGroupMemory {
	groups := make([]execution.NoDataGroupMemory, 0, len(memory))
	for key, entry := range memory {
		if entry.LastSeen == 0 && entry.FirstAbsent == 0 {
			continue
		}
		groups = append(groups, execution.NoDataGroupMemory{
			GroupKey: key, LastSeen: entry.LastSeen, FirstAbsent: entry.FirstAbsent,
		})
	}
	sort.Slice(groups, func(left, right int) bool { return groups[left].GroupKey < groups[right].GroupKey })
	return groups
}

func sameStoredGroups(left, right []execution.NoDataGroupMemory) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
