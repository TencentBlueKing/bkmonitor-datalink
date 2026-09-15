// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A record evaluated again lands on the point the history already holds for
// it, and a Level the round neither advanced nor guarded keeps the fact an
// earlier round recorded there. That fact was checked against the outcome of
// the round that produced it. When this round's outcome for the same Level is
// UNKNOWN under thinner input, the contract must not read the carried fact as
// this round's claim and reject the mutation; a fact the loaded history did
// not hold is the round's own and is still held to its outcome. The producer
// of this shape is a Plan with more Levels than the round has fresh facts for;
// the contract sees only the merged point, so one Level is enough to state
// the rule.
func TestStateFactCarriedFromTheLoadedHistoryIsNotJudgedByThisRoundsThinnerOutcome(t *testing.T) {
	input := validInternalExecution()
	input.Inputs = append([]execution.NamedInputBinding(nil), input.Inputs...)
	input.Inputs[0].Completeness = execution.CompletenessPartial
	input.Inputs[0].Disposition = execution.AccessDegraded
	input.Inputs[0].ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	state := normalStateEvaluation()
	anchor := state.Mutation.AffectedRecords[0]
	carried := execution.StateLevelFact{
		LevelID: 5, DetectFingerprint: state.Mutation.Points[0].Levels[0].DetectFingerprint, Result: execution.LevelFactAnomalous,
	}
	heldPoint := execution.StateHistoryPoint{RecordID: anchor.RecordID, SourceTime: anchor.SourceTime,
		Levels: []execution.StateLevelFact{carried}}
	mutation := state.Mutation
	mutation.ExpectedBlobRevision = 1
	mutation.Levels[0].HistoryCompleteness = execution.HistoryWarming
	mutation.Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonHistoryWarming)
	mutation.SeriesGuard = &execution.StateGuardFact{
		Status: execution.HistoryWarming, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
		WarmupRequirementRef: mustSeriesWarmupRequirementRef(input.DuePlans[0].CompiledPlan),
	}
	// The round's point for the record is the held point unchanged: nothing
	// fresh was written for the Level, the carried fact is all it has.
	mutation.Points = []execution.StateHistoryPoint{heldPoint}
	mutation.MutationDigest = ""
	state.Mutation = mustStateMutation(mutation)
	outcome := normalLevelOutcome()
	outcome.Outcome = execution.LevelOutcomeUnknown
	outcome.ReasonCode = execution.ReasonCode(contract.ReasonHistoryWarming)
	loadedView := execution.RuntimeStateView{
		Identity: input.StatePreflight[0].Identity, BlobRevision: 1,
		PersistedApplyVersion:   olderApplyVersion(input.StatePreflight[0].ApplyVersion),
		PersistedMutationDigest: "loaded-state", Status: execution.StateFoundWarming,
		SeriesGuard: state.Mutation.SeriesGuard,
		Levels: []execution.RuntimeLevelStateView{{
			LevelID:                 state.Mutation.Levels[0].LevelID,
			LevelStateCompatibility: state.Mutation.Levels[0].LevelStateCompatibility,
			HistoryCompleteness:     execution.HistoryWarming,
			GapReasonCode:           execution.ReasonCode(contract.ReasonHistoryWarming),
			WarmupRequirementRef:    state.Mutation.Levels[0].WarmupRequirementRef,
		}},
		History: []execution.StateHistoryPoint{heldPoint},
	}
	gaps := execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapMissing,
	}}}
	result := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultDegraded,
		ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecidedDegraded,
			ReasonCode:    execution.ReasonCode(contract.ReasonHistoryWarming),
			LevelOutcomes: []execution.LevelOutcome{outcome}, StateResults: []execution.StateEvaluation{state},
		}},
	}
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{loadedView}}, gaps)
	if err := result.Validate(request); err != nil {
		t.Fatalf("a fact the loaded history already held was judged by this round's UNKNOWN outcome: %v", err)
	}

	// The same fact with no history behind it is this round's own claim, and
	// an ANOMALOUS claim against an UNKNOWN outcome under thin input stays a
	// contradiction.
	freshView := loadedView
	freshView.History = nil
	fresh := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{freshView}}, gaps)
	err := result.Validate(fresh)
	if err == nil || !strings.Contains(err.Error(), "State Level fact contradicts its Level outcome") {
		t.Fatalf("a fresh contradicting fact was accepted: %v", err)
	}

	// A held fact that differs from the one the round carries is not the same
	// statement: the round changed a loaded point, and the history replacement
	// rule refuses that before the fact is ever compared with an outcome.
	changedView := loadedView
	changedView.History = []execution.StateHistoryPoint{{RecordID: anchor.RecordID, SourceTime: anchor.SourceTime,
		Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: carried.DetectFingerprint, Result: execution.LevelFactNormal}}}}
	changed := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{changedView}}, gaps)
	err = result.Validate(changed)
	if err == nil || !strings.Contains(err.Error(), "State history replacement changes a loaded point") {
		t.Fatalf("a carried fact that differs from the held one was accepted: %v", err)
	}
}
