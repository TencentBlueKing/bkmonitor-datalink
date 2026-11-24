// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

// The whole-read evaluation as it stood before the round was judged one group
// at a time (decision-020 batch 8, 2.7), kept verbatim with its comments
// stripped and every name prefixed with oracle. It is the expectation the
// per-group judgement is held to (judge_equivalence_test.go): written before
// the judgement and not by it, so a rule the rewrite moved or dropped shows up
// as a difference rather than being restated on both sides.
//
// It calls the production BuildRoster, copyGroupMemory and storedGroups. The
// first is the one place the oracle is not the old code: the roster version
// moved from v1 (a canonical digest over the sorted keys, kept below as
// oracleRosterVersionV1) to the order-free fold of v2 (rosterVersionDomain),
// and the oracle takes it through BuildRoster, so the version it holds is v2
// over the roster it built. The equivalence test therefore compares the
// version on its own, against the fold over the deduplicated key set
// (assertRosterVersionIsTheFold), and everything else against the oracle.

func oracleEvaluate(input AbsenceInput) AbsenceResult {
	result := oracleEvaluateAbsence(input)

	_, result.WholeItemPresent = input.Present[WholeItemGroup().Key()]
	if len(result.Memory) != 0 || result.WholeItemPresent {
		result.TrackingExhaustedAt = 0
		return result
	}
	if result.TrackingExhaustedAt == 0 {
		result.TrackingExhaustedAt = input.TrackingExhaustedAt
	}
	return result
}

func oracleEvaluateAbsence(input AbsenceInput) AbsenceResult {
	result := AbsenceResult{
		Verdicts: make(map[string]Verdict, len(input.Roster.Groups)+1),
		Memory:   copyGroupMemory(input.Memory),
		Roster:   input.Roster,
		Facts: AbsenceFacts{
			Present: uint64(len(input.Present)), Expected: uint64(len(input.Roster.Groups)), Dropped: input.Dropped,
			RosterSource: input.Roster.Source, RosterVersion: input.Roster.Version,
		},
	}

	if input.Completeness != execution.CompletenessFull || input.AnswerTruncated || input.RosterUnresolved {
		oracleJudgePresent(&result, input)
		for key := range input.Roster.Groups {
			if _, present := input.Present[key]; present {
				if _, foreign := input.OutOfBusiness[key]; foreign && input.Memory[key].SuppressedAt == 0 {
					result.Verdicts[key] = VerdictNormal
				}
				if _, judged := result.Verdicts[key]; judged || oracleHeldPresent(input, key) || input.Memory[key].SuppressedAt != 0 {
					continue
				}
			}
			result.Verdicts[key] = VerdictUnavailable
			result.Facts.Unavailable++
		}
		return result
	}

	whole := WholeItemGroup().Key()

	if len(input.Roster.Groups) == 0 && input.Roster.Source == RosterTargetPlan {
		oracleJudgePresent(&result, input)
		oracleCloseDroppedAbsences(&result, input, false)
		delete(result.Memory, whole)
		return result
	}
	if len(input.Roster.Groups) == 0 {
		if len(input.Present) == 0 {
			if input.TrackingExhaustedAt != 0 {
				oracleCloseDroppedAbsences(&result, input, true)
				return result
			}
			entry := result.Memory[whole]
			if entry.SuppressedAt != 0 {
				result.Facts.Suppressed++
				oracleCloseDroppedAbsences(&result, input, true)
				return result
			}
			if entry.FirstAbsent == 0 {
				entry.FirstAbsent = input.EvaluationTime
			}
			entry.LastSeen = 0
			if trackingExpired(entry.FirstAbsent, input.EvaluationTime, input.TrackingHorizonSeconds) {
				result.Facts.Expired++
				entry.SuppressedAt = input.EvaluationTime
				result.Memory[whole] = entry
				oracleCloseDroppedAbsences(&result, input, true)
				return result
			}
			result.Verdicts[whole] = VerdictAnomaly
			result.Facts.Absent++
			result.Facts.AbsentAges.count(entry.FirstAbsent, input.EvaluationTime)
			result.Memory[whole] = entry
			oracleCloseDroppedAbsences(&result, input, true)
			return result
		}
		oracleJudgePresent(&result, input)
		oracleCloseDroppedAbsences(&result, input, true)
		return result
	}

	oracleJudgePresent(&result, input)
	for key := range input.Roster.Groups {
		if _, foreign := input.OutOfBusiness[key]; foreign {
			if input.Memory[key].SuppressedAt == 0 {
				result.Verdicts[key] = VerdictNormal
			}
			delete(result.Memory, key)
			continue
		}
		if _, judged := result.Verdicts[key]; judged || oracleHeldPresent(input, key) {
			continue
		}
		entry := result.Memory[key]
		if entry.SuppressedAt != 0 {
			if input.Roster.Source == RosterHistory &&
				trackingExpired(entry.FirstAbsent, input.EvaluationTime, input.TrackingHorizonSeconds) {
				result.Facts.Expired++
				delete(result.Memory, key)
				continue
			}
			result.Facts.Suppressed++
			continue
		}
		if entry.FirstAbsent == 0 {
			entry.FirstAbsent = input.EvaluationTime
		}
		if trackingExpired(entry.FirstAbsent, input.EvaluationTime, input.TrackingHorizonSeconds) {
			result.Facts.Expired++
			if input.Roster.Source == RosterHistory {
				delete(result.Memory, key)
				continue
			}
			entry.SuppressedAt = input.EvaluationTime
			result.Memory[key] = entry
			continue
		}
		result.Verdicts[key] = VerdictAnomaly
		result.Facts.Absent++
		result.Facts.AbsentAges.count(entry.FirstAbsent, input.EvaluationTime)
		result.Memory[key] = entry
	}

	oracleCloseDroppedAbsences(&result, input, false)

	if input.Roster.Source == RosterHistory && result.Facts.Expired > 0 &&
		len(result.Memory) == 0 && len(input.Present) == 0 {
		result.TrackingExhaustedAt = input.EvaluationTime
	}
	return result
}

func oracleCloseDroppedAbsences(result *AbsenceResult, input AbsenceInput, wholeItemDecided bool) {
	whole := WholeItemGroup().Key()
	for key, entry := range input.Memory {
		if entry.FirstAbsent == 0 {
			continue
		}
		if wholeItemDecided && key == whole {
			continue
		}
		if _, expected := input.Roster.Groups[key]; expected {
			continue
		}
		if _, judged := result.Verdicts[key]; judged || oracleHeldPresent(input, key) {
			continue
		}
		if entry.SuppressedAt != 0 {
			delete(result.Memory, key)
			continue
		}
		result.Verdicts[key] = VerdictNormal
		if result.Closing == nil {
			result.Closing = map[string]bool{}
		}
		result.Closing[key] = true
		delete(result.Memory, key)
	}
}

func oracleJudgePresent(result *AbsenceResult, input AbsenceInput) {
	whole := WholeItemGroup().Key()
	for key := range input.Present {
		if _, foreign := input.OutOfBusiness[key]; foreign {
			delete(result.Memory, key)
			continue
		}
		if key == whole {
			continue
		}
		if !input.RecoveryDisabled {
			result.Verdicts[key] = VerdictNormal
		}
		result.Memory[key] = GroupMemory{LastSeen: input.EvaluationTime}
	}
	if len(input.Present) > 0 {
		if !input.RecoveryDisabled {
			result.Verdicts[whole] = VerdictNormal
		}
		delete(result.Memory, whole)
	}
}

func oracleHeldPresent(input AbsenceInput, key string) bool {
	if !input.RecoveryDisabled {
		return false
	}
	if key == WholeItemGroup().Key() {
		return len(input.Present) > 0
	}
	_, present := input.Present[key]
	_, foreign := input.OutOfBusiness[key]
	return present && !foreign
}

func oracleEvaluateSlot(input SlotInput) (AbsenceResult, SlotOutcome, error) {
	if input.Plan == nil || input.Plan.NoData == nil {
		return AbsenceResult{}, OutcomeNone, nil
	}
	config := input.Plan.NoData
	class, err := ClassifyTarget(input.Plan.TargetScope, input.Plan.TargetPlan, config.AggDimension)
	if err != nil {
		return AbsenceResult{}, OutcomeNone, err
	}
	var members []string
	targetSkip := OutcomeNone
	if class.Source == RosterTargetPlan {
		switch {
		case input.TargetResolution == nil:
			targetSkip = OutcomeSkippedTargetSelectorUnavailable
		case input.TargetResolution.State == TargetResolutionUnavailable:
			targetSkip = OutcomeSkippedTargetSelectorUnavailable
			members = input.TargetResolution.Members
		case input.TargetResolution.State == TargetResolutionIncomplete:
			targetSkip = OutcomeSkippedTargetMembersDropped
			members = input.TargetResolution.Members
		case input.TargetResolution.State == TargetResolutionComplete:
			members = input.TargetResolution.Members
		default:
			return AbsenceResult{}, OutcomeNone, fmt.Errorf("alarmd nodata: unknown target resolution state %q", input.TargetResolution.State)
		}
	}
	if class.Source == RosterTargetStatic && !input.HostsResolved {
		return AbsenceResult{}, OutcomeSkippedHostsUnresolved, nil
	}
	tally := ProjectSeries(input.Series, config.AggDimension)
	memory, manualClosed := oracleApplyManualCloses(input.Memory, input.ManualCloses, input.EvaluationTime)
	roster, err := BuildRoster(RosterRequest{
		AggDimension:  config.AggDimension,
		Scope:         input.Plan.TargetScope,
		Plan:          input.Plan.TargetPlan,
		TargetMembers: members,
		KnownHosts:    input.KnownHosts,
		Memory:        memory,
	})
	if err != nil {
		return AbsenceResult{}, OutcomeNone, err
	}
	outcome := OutcomeEvaluated
	switch {
	case targetSkip != OutcomeNone:
		outcome = targetSkip
	case input.Completeness != execution.CompletenessFull:
		outcome = OutcomeSkippedQueryNotFull
	case input.AnswerTruncated:
		outcome = OutcomeSkippedAnswerTruncated
	}
	result := oracleEvaluate(AbsenceInput{
		EvaluationTime:         input.EvaluationTime,
		PeriodSeconds:          input.PeriodSeconds,
		Completeness:           input.Completeness,
		Present:                tally.Groups,
		Dropped:                tally.Dropped,
		Roster:                 roster,
		Memory:                 memory,
		OutOfBusiness:          input.OutOfBusiness,
		TrackingHorizonSeconds: input.TrackingHorizonSeconds,
		TrackingExhaustedAt:    input.TrackingExhaustedAt,
		RecoveryDisabled:       config.RecoveryDisabled,
		AnswerTruncated:        input.AnswerTruncated,
		RosterUnresolved:       targetSkip != OutcomeNone,
	})
	result.Facts.ManualClosed = manualClosed
	return result, outcome, nil
}

func oracleEvaluatePlanSlot(input PlanSlotInput) (PlanSlotResult, error) {
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

	memory := oracleLoadedMemory(input.Snapshot)
	result, outcome, err := oracleEvaluateSlot(SlotInput{
		Plan:                   &contract.EvaluationPlanV2{NoData: input.NoData, TargetScope: input.Scope, TargetPlan: input.Plan},
		EvaluationTime:         input.EvaluationTime,
		PeriodSeconds:          input.PeriodSeconds,
		Completeness:           input.Completeness,
		Series:                 input.Series,
		KnownHosts:             input.KnownHosts,
		HostsResolved:          input.HostsResolved,
		OutOfBusiness:          input.OutOfBusiness,
		TargetResolution:       input.TargetResolution,
		AnswerTruncated:        input.AnswerTruncated,
		Memory:                 memory,
		TrackingHorizonSeconds: input.TrackingHorizonSeconds,
		TrackingExhaustedAt:    input.Snapshot.TrackingExhaustedAt,
		ManualCloses:           input.ManualCloses,
	})
	if err != nil {
		return PlanSlotResult{}, err
	}

	slot := PlanSlotResult{Outcome: outcome, Facts: result.Facts}
	partial := outcome == OutcomeSkippedQueryNotFull || outcome == OutcomeSkippedAnswerTruncated ||
		outcome == OutcomeSkippedTargetSelectorUnavailable || outcome == OutcomeSkippedTargetMembersDropped
	if outcome != OutcomeEvaluated && (!partial || !oracleJudgedPresent(result, input.EvaluationTime)) {
		return slot, nil
	}
	slot.Series, err = oracleSyntheticSeriesFor(SyntheticInput{
		EvaluationTime: input.EvaluationTime,
		PeriodSeconds:  input.PeriodSeconds,
		Result:         result,
		Memory:         result.Memory,
		Roster:         result.Roster,
	})
	if err != nil {
		return PlanSlotResult{}, err
	}

	groups := storedGroups(result.Memory)
	rosterVersion := result.Roster.Version
	if partial && input.Snapshot.RosterVersion != "" {
		rosterVersion = input.Snapshot.RosterVersion
	}
	presentAsOf := oraclePresentAsOf(groups, input.Snapshot.PresentAsOf, input.EvaluationTime, result.WholeItemPresent)
	if oracleSameStoredGroups(groups, input.Snapshot.Groups) && input.Snapshot.RosterVersion == rosterVersion &&
		presentAsOf == input.Snapshot.PresentAsOf &&
		result.TrackingExhaustedAt == input.Snapshot.TrackingExhaustedAt {
		return slot, nil
	}
	mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		Identity:                  input.Identity,
		GroupLimit:                input.MaxGroups,
		DerivedFrom:               input.Snapshot.Representation,
		LoadedApplyVersion:        input.Snapshot.PersistedApplyVersion,
		ExpectedMarkerRevision:    input.Snapshot.MarkerRevision,
		ApplyVersion:              input.ApplyVersion,
		ScheduleRevision:          input.ScheduleRevision,
		RosterVersion:             rosterVersion,
		PresentAsOf:               presentAsOf,
		Memory:                    groups,
		Loaded:                    input.Snapshot.Groups,
		LoadedPresentAsOf:         input.Snapshot.PresentAsOf,
		TrackingExhaustedAt:       result.TrackingExhaustedAt,
		LoadedTrackingExhaustedAt: input.Snapshot.TrackingExhaustedAt,
	})
	if err != nil {
		return PlanSlotResult{}, err
	}
	slot.Mutation = &mutation
	return slot, nil
}

func oracleJudgedPresent(result AbsenceResult, evaluationTime int64) bool {
	if result.WholeItemPresent {
		return true
	}
	for _, entry := range result.Memory {
		if entry.FirstAbsent == 0 && entry.LastSeen == evaluationTime {
			return true
		}
	}
	return false
}

func oraclePresentAsOf(groups []execution.NoDataGroupMemory, stored int64, evaluationTime int64, wholeItemPresent bool) int64 {
	for _, group := range groups {
		if group.FirstAbsent == 0 && group.LastSeen == evaluationTime {
			return evaluationTime
		}
	}
	if wholeItemPresent {
		return evaluationTime
	}
	return stored
}

func oracleLoadedMemory(snapshot execution.NoDataMemorySnapshot) map[string]GroupMemory {
	memory := make(map[string]GroupMemory, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		memory[group.GroupKey] = GroupMemory{
			LastSeen: group.LastSeen, FirstAbsent: group.FirstAbsent, SuppressedAt: group.SuppressedAt,
		}
	}
	return memory
}

func oracleSameStoredGroups(left, right []execution.NoDataGroupMemory) bool {
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

func oracleSyntheticSeriesFor(input SyntheticInput) ([]SyntheticSeries, error) {
	keys := make([]string, 0, len(input.Result.Verdicts))
	for key, verdict := range input.Result.Verdicts {
		if verdict == VerdictUnavailable {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	series := make([]SyntheticSeries, 0, len(keys))
	sourceTime := input.EvaluationTime - input.PeriodSeconds
	for _, key := range keys {
		group, named := input.Roster.Groups[key]
		if !named {
			parsed, ok := ParseGroupKey(key)
			if !ok {
				return nil, unparseableGroupKey(key)
			}
			group = parsed
		}
		entry := SyntheticSeries{Group: group, SourceTime: sourceTime, Value: PresentValue, Closing: input.Result.Closing[key]}
		if input.Result.Verdicts[key] == VerdictAnomaly {
			entry.Value = AbsentValue
			entry.Periods = absentPeriods(input.Memory[key], input.EvaluationTime, input.PeriodSeconds)
			entry.FirstAbsent = input.Memory[key].FirstAbsent
		}
		series = append(series, entry)
	}
	return series, nil
}

func oracleApplyManualCloses(
	memory map[string]GroupMemory, closes *ManualCloses, evaluationTime int64,
) (map[string]GroupMemory, uint64) {
	if closes == nil || closes.Fingerprint == nil || len(closes.ClosedAt) == 0 || len(memory) == 0 {
		return memory, 0
	}
	var applied map[string]GroupMemory
	var stopped uint64
	for key, entry := range memory {
		if entry.FirstAbsent == 0 || entry.SuppressedAt != 0 {
			continue
		}
		group, named := ParseGroupKey(key)
		if !named {
			continue
		}
		fingerprint, err := closes.Fingerprint(group)
		if err != nil || !closes.Matches(fingerprint, entry.FirstAbsent) {
			continue
		}
		if applied == nil {
			applied = copyGroupMemory(memory)
		}
		stopped++
		entry.SuppressedAt = evaluationTime
		applied[key] = entry
	}
	if applied == nil {
		return memory, 0
	}
	return applied, stopped
}

// oracleRosterVersionV1 is the v1 roster version, verbatim: a canonical digest
// over the sorted keys. Nothing derives it any more; it is the value a record
// written by the old build carries, which this build compares for equality
// and never recomputes.
func oracleRosterVersionV1(source RosterSource, sortedKeys []string) (string, error) {
	if sortedKeys == nil {
		sortedKeys = []string{}
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-no-data-roster-v1", struct {
		Source RosterSource `json:"source"`
		Groups []string     `json:"groups"`
	}{Source: source, Groups: sortedKeys})
	if err != nil {
		return "", fmt.Errorf("alarmd nodata: derive roster version: %w", err)
	}
	return digest, nil
}
