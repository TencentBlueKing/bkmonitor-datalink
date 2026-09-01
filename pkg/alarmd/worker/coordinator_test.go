// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestSlotExecutionCoordinatorUsesOneExecutePath(t *testing.T) {
	methodType := reflect.TypeOf((*worker.SlotExecutionCoordinator)(nil))
	if methodType.NumMethod() != 1 || methodType.Method(0).Name != "Execute" {
		t.Fatalf("public methods=%v, want only Execute", publicMethodNames(methodType))
	}
	for _, operation := range []execution.Operation{execution.OperationNormal, execution.OperationRetry, execution.OperationReplay} {
		t.Run(string(operation), func(t *testing.T) {
			fixture := newFixture(t, true, "")
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(operation))
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			assertTrace(t, fixture.trace, fullTrace)
			assertObservedOperations(t, fixture.observations, operation)
		})
	}
}

func TestSlotExecutionCoordinatorProbeStopsUntilReady(t *testing.T) {
	fixture := newFixture(t, false, "")
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationProbe))
	if err != nil || result.Completed {
		t.Fatalf("unready probe result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{"query", "gap_load"})

	readyFixture := newFixture(t, true, "")
	result, err = readyFixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationProbe))
	if err != nil || !result.Completed {
		t.Fatalf("ready probe result=%+v error=%v", result, err)
	}
	assertTrace(t, readyFixture.trace, fullTrace)
}

func TestSlotExecutionCoordinatorDiscardsProvisionalResultsWithoutCompletion(t *testing.T) {
	fixture := newFixture(t, true, "query_after_series")
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{"query", "gap_load", "state_load", "evaluate"})
	if fixture.ports.eventCount != 0 || fixture.ports.stateApplyCalls != 0 || fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
		t.Fatal("provisional result escaped before trustworthy completion")
	}
}

func TestSlotExecutionCoordinatorBindsAlwaysEffectiveTimeToRealSeries(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.unboundEffectiveTimeFacts = true

	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	facts := fixture.ports.lastEvaluation.Header.EffectiveTimeFacts
	if len(facts) != 1 {
		t.Fatalf("EffectiveTime facts=%d, want one fact for the selected series and Level", len(facts))
	}
	fact := facts[0]
	if fact.Consumer.Plan != planIdentity() || !fact.Consumer.HasLevel || fact.Consumer.LevelID != 5 ||
		fact.SeriesIdentity != execution.SeriesIdentityDigest(strings.Repeat("c", 64)) ||
		fact.Fact.Status() != strategy.EffectiveTimeActive {
		t.Fatalf("EffectiveTime fact=%+v, want ACTIVE fact bound to the real series and Level", fact)
	}
}

func TestSlotExecutionCoordinatorEnforcesProcessProvisionalBudget(t *testing.T) {
	fixture := newFixtureWithBudget(t, worker.ProvisionalBudget{
		MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 1, MaxEvents: 1, MaxGapMutations: 1,
	})
	fixture.ports.reverseStateReceipts = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if fixture.ports.eventCount != 0 || fixture.ports.stateApplyCalls != 0 || fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
		t.Fatal("over-budget provisional result reached side effects")
	}
	assertCapacityRejection(t, fixture.observations, observability.CapacityBudgetStateMutations)
}

func TestSlotExecutionCoordinatorBudgetsRetainedSeriesAndBytes(t *testing.T) {
	tests := []struct {
		name       string
		budget     worker.ProvisionalBudget
		wantBudget observability.CapacityBudget
	}{
		{name: "series", budget: worker.ProvisionalBudget{
			MaxSeries: 1, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10,
		}, wantBudget: observability.CapacityBudgetSeries},
		{name: "retained bytes", budget: worker.ProvisionalBudget{
			MaxSeries: 100, MaxRetainedBytes: 1, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10,
		}, wantBudget: observability.CapacityBudgetRetainedBytes},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixtureWithBudget(t, test.budget)
			fixture.ports.reverseStateReceipts = true
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err == nil || result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			if fixture.ports.eventCount != 0 || fixture.ports.stateApplyCalls != 0 || fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
				t.Fatal("series/byte budget rejection reached side effects")
			}
			assertCapacityRejection(t, fixture.observations, test.wantBudget)
		})
	}
}

func assertCapacityRejection(t *testing.T, observations *[]observability.Observation, want observability.CapacityBudget) {
	t.Helper()
	for _, observation := range *observations {
		if observation.Stage == observability.StageResourceHard && observation.Result == observability.ResultPaused {
			if observation.CapacityBudget != want {
				t.Fatalf("capacity budget = %q, want %q", observation.CapacityBudget, want)
			}
			if observation.ReasonCode != observability.ReasonCode(contract.ReasonResourceHardStop) {
				t.Fatalf("capacity rejection reason = %q, want %q", observation.ReasonCode, contract.ReasonResourceHardStop)
			}
			return
		}
	}
	t.Fatalf("capacity rejection %q was not observed", want)
}

func TestSlotExecutionCoordinatorOrdersRequiredSideEffects(t *testing.T) {
	fixture := newFixture(t, true, "")
	if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	assertTrace(t, fixture.trace, fullTrace)
	wantStages := []observability.Stage{
		observability.StageGapLoaded,
		observability.StageStatePreflight,
		observability.StageEvaluationCompleted,
		observability.StageQueryCompleted,
		observability.StageSideEffectAdmission,
		observability.StageMutationCompared,
		observability.StageSideEffectAdmission,
		observability.StageStateAdmission,
		observability.StageEventACKed,
		observability.StageStateApplied,
		observability.StageGapGuardCommitted,
		observability.StageSideEffectAdmission,
		observability.StageProgressCommitted,
	}
	gotStages := make([]observability.Stage, len(*fixture.observations))
	for index := range *fixture.observations {
		gotStages[index] = (*fixture.observations)[index].Stage
	}
	if !reflect.DeepEqual(gotStages, wantStages) {
		t.Fatalf("observed stages=%v, want=%v", gotStages, wantStages)
	}
}

func TestSlotExecutionCoordinatorRechecksActivationBeforeNormalProgress(t *testing.T) {
	fixture := newFixture(t, true, "")
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed || fixture.ports.activationLoadCalls != 2 {
		t.Fatalf("Execute() result=%+v error=%v activation calls=%d", result, err, fixture.ports.activationLoadCalls)
	}
}

func TestSlotExecutionCoordinatorDoesNotCommitNormalProgressAfterActivationChange(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.activationChangeAt = 2
	fixture.ports.persistActivatedGaps = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, append(append([]string(nil), fullTrace[:11]...),
		"sequence", "admission_progress", "gap_load", "gap_before"))
	if fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
		t.Fatalf("activation change committed Progress: %+v", fixture.ports.lastProgress)
	}
	if len(fixture.ports.gapMutations) != 2 {
		t.Fatalf("activation change gap mutations=%+v", fixture.ports.gapMutations)
	}
	mutation := fixture.ports.gapMutations[1]
	if mutation.Identity != (execution.PlanGapIdentity{Plan: planIdentity(), StateGeneration: "activation-change"}) ||
		mutation.ApplyVersion.StateApplyEpoch != 2 || mutation.ScheduleRevision != "activation-change" ||
		len(mutation.Scopes) != 1 || mutation.Scopes[0].Scope != (execution.GapScope{}) ||
		mutation.Scopes[0].Kind != execution.GapStrengthen ||
		mutation.Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) ||
		mutation.Scopes[0].RequiredFullSlots != 1 {
		t.Fatalf("activation change was not protected before retry: %+v", mutation)
	}
	wantScope := execution.SequencingScope{
		Slot:    frozenContract().Slot,
		GapKeys: []execution.PlanGapIdentity{{Plan: planIdentity(), StateGeneration: "activation-change"}},
	}
	if !reflect.DeepEqual(fixture.ports.lastSequenceScope, wantScope) {
		t.Fatalf("activation protection scope=%+v, want=%+v", fixture.ports.lastSequenceScope, wantScope)
	}

	firstEvents := fixture.ports.eventCount
	firstStateApplies := fixture.ports.stateApplyCalls
	result, err = fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed || result.Result != observability.ResultDegraded ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) ||
		fixture.ports.lastProgress.Completion.Kind != execution.CompletionPartialGap {
		t.Fatalf("second Execute() result=%+v error=%v Progress=%+v", result, err, fixture.ports.lastProgress)
	}
	if fixture.ports.eventCount != firstEvents || fixture.ports.stateApplyCalls != firstStateApplies {
		t.Fatalf("second execution replayed old side effects: events=%d want=%d state applies=%d want=%d",
			fixture.ports.eventCount, firstEvents, fixture.ports.stateApplyCalls, firstStateApplies)
	}
}

func TestSlotExecutionCoordinatorProtectsActivationChangedBeforeNormalSideEffects(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.activationChangeAt = 1
	fixture.ports.persistActivatedGaps = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if fixture.ports.eventCount != 0 || fixture.ports.stateApplyCalls != 0 {
		t.Fatalf("changed frozen Plan emitted old side effects: events=%d state applies=%d",
			fixture.ports.eventCount, fixture.ports.stateApplyCalls)
	}
	if fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
		t.Fatalf("changed frozen Plan committed Progress before protection: %+v", fixture.ports.lastProgress)
	}
	if len(fixture.ports.gapMutations) != 1 {
		t.Fatalf("activation change gap mutations=%+v", fixture.ports.gapMutations)
	}
	mutation := fixture.ports.gapMutations[0]
	if mutation.Identity != (execution.PlanGapIdentity{Plan: planIdentity(), StateGeneration: "activation-change"}) ||
		mutation.ApplyVersion.StateApplyEpoch != 2 ||
		mutation.Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("activation change was not protected before retry: %+v", mutation)
	}

	result, err = fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed || result.Result != observability.ResultDegraded ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("second Execute() result=%+v error=%v", result, err)
	}
	if fixture.ports.eventCount != 0 || fixture.ports.stateApplyCalls != 0 {
		t.Fatalf("activation convergence emitted old side effects: events=%d state applies=%d",
			fixture.ports.eventCount, fixture.ports.stateApplyCalls)
	}
	if fixture.ports.lastProgress.Completion.Kind != execution.CompletionPartialGap ||
		fixture.ports.lastProgress.Completion.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("activation convergence Progress=%+v", fixture.ports.lastProgress)
	}
}

func TestSlotExecutionCoordinatorConvergesCurrentActivationSelectionChange(t *testing.T) {
	t.Run("current_to_pending", func(t *testing.T) {
		fixture := newFixture(t, true, "")
		fixture.ports.activationChangeAt = 2
		fixture.ports.activationSelection = execution.ActivationPending
		fixture.ports.persistActivatedGaps = true

		result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
		if err != nil || result.Completed || result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
			t.Fatalf("first Execute() result=%+v error=%v", result, err)
		}
		firstEvents, firstStateApplies := fixture.ports.eventCount, fixture.ports.stateApplyCalls
		result, err = fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
		if err != nil || !result.Completed || result.Result != observability.ResultDegraded ||
			result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) ||
			fixture.ports.lastProgress.Completion.Kind != execution.CompletionPartialGap {
			t.Fatalf("second Execute() result=%+v error=%v Progress=%+v", result, err, fixture.ports.lastProgress)
		}
		if fixture.ports.eventCount != firstEvents || fixture.ports.stateApplyCalls != firstStateApplies {
			t.Fatalf("pending activation replayed old side effects: events=%d want=%d state applies=%d want=%d",
				fixture.ports.eventCount, firstEvents, fixture.ports.stateApplyCalls, firstStateApplies)
		}
	})

	t.Run("current_to_none", func(t *testing.T) {
		fixture := newFixture(t, true, "")
		fixture.ports.activationChangeAt = 2
		fixture.ports.activationSelection = execution.ActivationNone

		result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
		if err != nil || !result.Completed || result.Result != observability.ResultDegraded ||
			result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) ||
			fixture.ports.lastProgress.Completion.Kind != execution.CompletionPartialGap {
			t.Fatalf("Execute() result=%+v error=%v Progress=%+v", result, err, fixture.ports.lastProgress)
		}
		if len(fixture.ports.gapMutations) != 1 {
			t.Fatalf("NONE activation must not invent a selected Plan Guard: %+v", fixture.ports.gapMutations)
		}
	})
}

func TestSlotExecutionCoordinatorReprotectsActivationChangedBetweenGuardAndProgress(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.activationChangeAt = 1
	fixture.ports.activationSecondChangeAt = 3
	fixture.ports.persistActivatedGaps = true

	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || result.Completed {
		t.Fatalf("first Execute() result=%+v error=%v", result, err)
	}
	result, err = fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || result.Completed || fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
		t.Fatalf("second Execute() result=%+v error=%v Progress=%+v", result, err, fixture.ports.lastProgress)
	}
	if _, found := fixture.ports.activatedGapMarkers[execution.PlanGapIdentity{
		Plan: planIdentity(), StateGeneration: "activation-change-2",
	}]; !found {
		t.Fatalf("activation changed after Guard was not protected: %+v", fixture.ports.activatedGapMarkers)
	}

	result, err = fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed || result.Result != observability.ResultDegraded ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("third Execute() result=%+v error=%v", result, err)
	}
	if fixture.ports.eventCount != 0 || fixture.ports.stateApplyCalls != 0 ||
		fixture.ports.lastProgress.Completion.Kind != execution.CompletionPartialGap {
		t.Fatalf("unstable activation convergence side effects/events=%d state=%d Progress=%+v",
			fixture.ports.eventCount, fixture.ports.stateApplyCalls, fixture.ports.lastProgress)
	}
}

func TestSlotExecutionCoordinatorDoesNotCommitNormalProgressWhenActivationIsUnreadable(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("read_%d", failAt), func(t *testing.T) {
			fixture := newFixture(t, true, "")
			fixture.ports.activationErrorAt = failAt
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
				result.ReasonCode != execution.ReasonCode(contract.ReasonProviderUnavailable) {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			if fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
				t.Fatalf("unreadable activation committed Progress: %+v", fixture.ports.lastProgress)
			}
		})
	}
}

func TestSlotExecutionCoordinatorRequiresAdmissionBeforeNormalProgress(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.admissionRejectAt = 3
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, fullTrace[:len(fullTrace)-1])
	if fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
		t.Fatalf("Progress admission failure committed Progress: %+v", fixture.ports.lastProgress)
	}
}

func TestSlotExecutionCoordinatorPersistsDegradedGapBeforeEvents(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.degraded = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{
		"query", "gap_load", "state_load", "evaluate", "sequence", "admission_initial", "gap_before",
		"admission_event", "state_admission", "event_ack", "state_apply", "admission_progress", "progress_commit",
	})
	if fixture.ports.lastProgress.Completion.Kind != execution.CompletionPartialGap {
		t.Fatalf("completion=%q, want=%q", fixture.ports.lastProgress.Completion.Kind, execution.CompletionPartialGap)
	}
	queryObservation := (*fixture.observations)[3]
	if queryObservation.Result != observability.ResultDegraded || queryObservation.ReasonCode != contract.ReasonQueryPartial {
		t.Fatalf("query observation result=%q reason=%q", queryObservation.Result, queryObservation.ReasonCode)
	}
}

func TestSlotExecutionCoordinatorShortCircuitsAlreadyAppliedState(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.alreadyApplied = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{
		"query", "gap_load", "state_load", "evaluate", "sequence", "admission_initial", "gap_after", "admission_progress", "progress_commit",
	})
}

func TestSlotExecutionCoordinatorRejectsInvalidRequestAndProviderDrift(t *testing.T) {
	fixture := newFixture(t, true, "")
	if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.Operation("unknown"))); err == nil {
		t.Fatal("unknown operation must fail")
	}
	assertTrace(t, fixture.trace, []string{})

	drift := newFixture(t, true, "")
	drift.ports.contractDrift = true
	if _, err := drift.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err == nil {
		t.Fatal("provider contract drift must fail")
	}
	assertTrace(t, drift.trace, []string{"query"})

	slotDrift := slotRequest(execution.OperationNormal)
	slotDrift.ExpectedNextSlot++
	if _, err := fixture.coordinator.Execute(context.Background(), slotDrift); err == nil {
		t.Fatal("expected-next-slot drift must fail before query")
	}
}

func TestSlotExecutionCoordinatorCarriesFrozenSlotIntoSequencing(t *testing.T) {
	fixture := newFixture(t, true, "")
	if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err != nil {
		t.Fatalf("Execute() error=%v", err)
	}
	if fixture.ports.lastSequenceScope.Slot != frozenContract().Slot {
		t.Fatalf("sequencing Slot=%+v, want=%+v", fixture.ports.lastSequenceScope.Slot, frozenContract().Slot)
	}
}

func TestSlotExecutionCoordinatorPreservesStateTerminalAsPlanCompletion(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.stateLoadStatus = execution.StateDeterministicInvalid
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{
		"query", "gap_load", "state_load", "evaluate", "sequence", "admission_initial", "gap_before", "admission_progress", "progress_commit",
	})
	if fixture.ports.lastProgress.Completion.Kind != execution.CompletionTerminal {
		t.Fatalf("completion=%q", fixture.ports.lastProgress.Completion.Kind)
	}
	stateObservation := (*fixture.observations)[1]
	if stateObservation.Result != observability.ResultTerminal || stateObservation.ReasonCode != contract.ReasonRecordInvalid ||
		stateObservation.Counts.Keys != 1 {
		t.Fatalf("state observation=%+v", stateObservation)
	}
}

func TestSlotExecutionCoordinatorIsolatesDeterministicStateAdmission(t *testing.T) {
	for _, rejectedLast := range []bool{false, true} {
		t.Run(fmt.Sprintf("rejected_last_%t", rejectedLast), func(t *testing.T) {
			fixture := newFixture(t, true, "")
			fixture.ports.reverseStateReceipts = true
			fixture.ports.stateAdmissionDeterministic = true
			fixture.ports.stateAdmissionDeterministicLast = rejectedLast

			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err != nil || !result.Completed || result.Result != observability.ResultTerminal ||
				result.ReasonCode != execution.ReasonCode(contract.ReasonRecordInvalid) {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			assertTrace(t, fixture.trace, []string{
				"query", "gap_load", "state_load", "evaluate", "state_load", "evaluate", "sequence", "admission_initial",
				"admission_event", "state_admission", "event_ack", "state_apply", "gap_after",
				"admission_progress", "progress_commit",
			})
			if fixture.ports.eventCount != 1 || fixture.ports.lastProgress.Completion.Kind != execution.CompletionTerminal {
				t.Fatalf("events=%d completion=%+v", fixture.ports.eventCount, fixture.ports.lastProgress.Completion)
			}
			if fixture.ports.stateApplyCalls != 1 || len(fixture.ports.lastStateApply.Items) != 1 {
				t.Fatalf("state apply calls=%d request=%+v", fixture.ports.stateApplyCalls, fixture.ports.lastStateApply)
			}
			wantSeries := execution.SeriesIdentityDigest(strings.Repeat("d", 64))
			if rejectedLast {
				wantSeries = execution.SeriesIdentityDigest(strings.Repeat("c", 64))
			}
			if got := fixture.ports.lastStateApply.Items[0].Identity.SeriesIdentityDigest; got != wantSeries {
				t.Fatalf("applied series=%q, want healthy sibling %q", got, wantSeries)
			}
			if got := fixture.ports.lastEvents[0].RecordRef.DimensionIdentityDigest; got != string(wantSeries) {
				t.Fatalf("event series=%q, want healthy sibling %q", got, wantSeries)
			}
		})
	}
}

func TestSlotExecutionCoordinatorRejectsDeterministicStateApplyAsContractViolation(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.stateApplyDeterministic = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{
		"query", "gap_load", "state_load", "evaluate", "sequence", "admission_initial",
		"admission_event", "state_admission", "event_ack", "state_apply",
	})
	if fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
		t.Fatalf("contract violation must not commit Progress: %+v", fixture.ports.lastProgress)
	}
}

func TestSlotExecutionCoordinatorObservesMixedStateApplyReceiptReasonIndependentOfOrder(t *testing.T) {
	for _, failureFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure_first_%t", failureFirst), func(t *testing.T) {
			fixture := newFixture(t, true, "")
			fixture.ports.reverseStateReceipts = true
			fixture.ports.stateApplyDeterministic = true
			fixture.ports.stateApplyDeterministicLast = failureFirst
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err == nil || result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			var found bool
			for _, observation := range *fixture.observations {
				if observation.Stage == observability.StageStateApplied {
					found = true
					if observation.Result != observability.ResultTerminal ||
						observation.ReasonCode != execution.ReasonCode(contract.ReasonRecordInvalid) {
						t.Fatalf("mixed receipt observation=%+v", observation)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s observation", observability.StageStateApplied)
			}
		})
	}
}

func TestSlotExecutionCoordinatorObservesGapUnavailableWithoutCallingItSuccess(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.gapLoadStatus = execution.GapUnavailable
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || result.Completed || result.Result != observability.ResultRetrying {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{"query", "gap_load", "state_load", "evaluate", "sequence", "admission_initial"})
	gapObservation := (*fixture.observations)[0]
	if gapObservation.Result != observability.ResultDegraded || gapObservation.ReasonCode != contract.ReasonRedisUnavailable ||
		gapObservation.Counts.Keys != 1 {
		t.Fatalf("gap observation=%+v", gapObservation)
	}
}

func TestSlotExecutionCoordinatorDoesNotCommitProgressForUnavailableState(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.stateLoadStatus = execution.StateRetryableIO
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || result.Completed || result.Result != observability.ResultRetrying {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{"query", "gap_load", "state_load", "evaluate", "sequence", "admission_initial"})
	if fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
		t.Fatalf("retry-pending Slot must not commit Progress: %+v", fixture.ports.lastProgress)
	}
}

func TestSlotExecutionCoordinatorCompletesSiblingSeriesButKeepsSlotRetryPending(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.reverseStateReceipts = true
	fixture.ports.stateRetryableFirstOnly = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || result.Completed || result.Result != observability.ResultRetrying {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{
		"query", "gap_load", "state_load", "evaluate", "state_load", "evaluate", "sequence", "admission_initial",
		"admission_event", "state_admission", "event_ack", "state_apply", "gap_after",
	})
	if fixture.ports.eventCount != 1 || fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
		t.Fatalf("sibling evidence events=%d progress=%+v", fixture.ports.eventCount, fixture.ports.lastProgress)
	}
}

func TestSlotExecutionCoordinatorRejectsTriggerEventDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*recordingPorts)
	}{
		{name: "series", mutate: func(ports *recordingPorts) { ports.eventSeriesDrift = true }},
		{name: "evaluation time", mutate: func(ports *recordingPorts) { ports.eventTimeDrift = true }},
		{name: "record", mutate: func(ports *recordingPorts) { ports.eventRecordDrift = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t, true, "")
			test.mutate(fixture.ports)
			if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err == nil {
				t.Fatal("TriggerEvent drift must fail")
			}
			assertTrace(t, fixture.trace, []string{"query", "gap_load", "state_load", "evaluate"})
		})
	}
}

func TestSlotExecutionCoordinatorObserverPanicIsFailOpen(t *testing.T) {
	fixture := newFixtureWithObserver(t, true, "", observability.ObserverFunc(func(context.Context, observability.Observation) {
		panic("injected observer panic")
	}))
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{
		"query", "gap_load", "state_load", "evaluate", "sequence", "admission_initial",
		"admission_event", "state_admission", "event_ack", "state_apply", "gap_after", "admission_progress", "progress_commit",
	})
}

func TestSlotExecutionCoordinatorRejectsMismatchedStoreReceipts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*recordingPorts)
	}{
		{name: "gap identity", mutate: func(ports *recordingPorts) { ports.wrongGapIdentity = true }},
		{name: "state admission identity", mutate: func(ports *recordingPorts) { ports.wrongStateAdmissionIdentity = true }},
		{name: "state apply identity", mutate: func(ports *recordingPorts) { ports.wrongStateApplyIdentity = true }},
		{name: "progress conflict", mutate: func(ports *recordingPorts) { ports.progressConflict = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t, true, "")
			test.mutate(fixture.ports)
			if result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err == nil || result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			last := (*fixture.observations)[len(*fixture.observations)-1]
			if last.Result != observability.ResultFailed {
				t.Fatalf("last observation=%+v, want failed", last)
			}
		})
	}
}

func TestSlotExecutionCoordinatorAcceptsUnorderedStoreReceipts(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.reverseStateReceipts = true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{
		"query", "gap_load", "state_load", "evaluate", "state_load", "evaluate", "sequence", "admission_initial",
		"admission_event", "state_admission", "event_ack", "state_apply", "gap_after", "admission_progress", "progress_commit",
	})
	if len(fixture.ports.lastSequenceScope.StateKeys) != 2 || len(fixture.ports.lastSequenceScope.GapKeys) != 1 {
		t.Fatalf("sequencing scope=%+v", fixture.ports.lastSequenceScope)
	}
}

func TestSlotExecutionCoordinatorFailsClosedBeforeLaterSideEffects(t *testing.T) {
	tests := []struct {
		stage string
		want  []string
	}{
		{stage: "query", want: []string{"query", "gap_load"}},
		{stage: "sequence", want: fullTrace[:5]},
		{stage: "state_load", want: fullTrace[:3]},
		{stage: "gap_load", want: fullTrace[:2]},
		{stage: "evaluate", want: fullTrace[:4]},
		{stage: "admission_initial", want: fullTrace[:6]},
		{stage: "admission_event", want: fullTrace[:7]},
		{stage: "state_admission", want: fullTrace[:8]},
		{stage: "event_ack", want: fullTrace[:9]},
		{stage: "state_apply", want: fullTrace[:10]},
		{stage: "gap_after", want: fullTrace[:11]},
		{stage: "admission_progress", want: fullTrace[:12]},
		{stage: "progress_commit", want: fullTrace[:13]},
	}
	for _, test := range tests {
		t.Run(test.stage, func(t *testing.T) {
			fixture := newFixture(t, true, test.stage)
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err == nil || result.Completed {
				t.Fatalf("failed result=%+v error=%v", result, err)
			}
			assertTrace(t, fixture.trace, test.want)
			if test.stage != "sequence" {
				last := (*fixture.observations)[len(*fixture.observations)-1]
				if last.Result != observability.ResultFailed || last.Operation != observability.OperationNormal {
					t.Fatalf("last observation=%+v", last)
				}
				if test.stage == "event_ack" {
					if last.Stage != observability.StageEventACKed ||
						last.ReasonCode != execution.ReasonCode(contract.ReasonOutputACKUnknown) {
						t.Fatalf("event ACK observation=%+v", last)
					}
					if fixture.ports.stateApplyCalls != 0 || fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
						t.Fatalf("unknown event ACK advanced state/progress: state=%d progress=%+v",
							fixture.ports.stateApplyCalls, fixture.ports.lastProgress)
					}
				}
			}
		})
	}
}

var fullTrace = []string{
	"query", "gap_load", "state_load", "evaluate", "sequence", "admission_initial",
	"admission_event", "state_admission", "event_ack", "state_apply", "gap_after", "admission_progress", "progress_commit",
}

type fixture struct {
	trace        *[]string
	observations *[]observability.Observation
	ports        *recordingPorts
	coordinator  *worker.SlotExecutionCoordinator
}

func newFixture(t *testing.T, ready bool, failStage string) fixture {
	t.Helper()
	observations := make([]observability.Observation, 0, len(fullTrace)+1)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observability.NormalizeObservation(observation))
	})
	return buildFixture(t, ready, failStage, observer, &observations)
}

func newFixtureWithObserver(t *testing.T, ready bool, failStage string, observer observability.Observer) fixture {
	t.Helper()
	observations := make([]observability.Observation, 0, len(fullTrace)+1)
	return buildFixture(t, ready, failStage, observer, &observations)
}

func newFixtureWithBudget(t *testing.T, budget worker.ProvisionalBudget) fixture {
	t.Helper()
	observations := make([]observability.Observation, 0, len(fullTrace)+1)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observability.NormalizeObservation(observation))
	})
	return buildFixtureWithBudget(t, true, "", observer, &observations, budget)
}

func buildFixture(
	t *testing.T,
	ready bool,
	failStage string,
	observer observability.Observer,
	observations *[]observability.Observation,
) fixture {
	return buildFixtureWithBudget(t, ready, failStage, observer, observations,
		worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
}

func buildFixtureWithBudget(
	t *testing.T,
	ready bool,
	failStage string,
	observer observability.Observer,
	observations *[]observability.Observation,
	budget worker.ProvisionalBudget,
) fixture {
	t.Helper()
	trace := make([]string, 0, len(fullTrace))
	ports := &recordingPorts{trace: &trace, ready: ready, failStage: failStage}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: ports, Activation: ports,
		Query: ports, Sequencer: ports, Evaluator: ports, Admission: ports, GapGuard: ports,
		Events: ports, State: ports, Progress: ports,
		Observer: observer,
	}, budget)
	if err != nil {
		t.Fatalf("NewSlotExecutionCoordinator() error: %v", err)
	}
	return fixture{trace: &trace, observations: observations, ports: ports, coordinator: coordinator}
}

func (ports *recordingPorts) ResolveFinalization(
	_ context.Context,
	request execution.SlotExecutionRequest,
) (execution.QueryFreeFinalization, error) {
	return execution.QueryFreeFinalization{Contract: request.Contract, Mode: execution.FinalizationQueryRequired}, nil
}

func (ports *recordingPorts) VerifyFrozenDuePlanTargets(
	context.Context,
	execution.FrozenExecutionContractRef,
	execution.FrozenDuePlanTargets,
) error {
	return nil
}

func (ports *recordingPorts) LoadActivations(
	_ context.Context,
	request execution.PlanActivationRequest,
) (execution.PlanActivationResult, error) {
	ports.activationLoadCalls++
	if ports.activationErrorAt == ports.activationLoadCalls {
		return execution.PlanActivationResult{}, errors.New("injected activation read")
	}
	facts := make([]execution.PlanActivationFact, len(request.Plans))
	for index, plan := range request.Plans {
		facts[index] = execution.PlanActivationFact{
			Plan: plan, Selection: execution.ActivationCurrent,
			Selected: execution.ActivatedPlan{
				Identity: plan, StateGeneration: "state-v1", StateApplyEpoch: 1,
				ScheduleRevision: "plan-schedule-v1", RequiredFullSlots: 1,
			},
		}
		if ports.activationChangeAt != 0 && ports.activationLoadCalls >= ports.activationChangeAt {
			selection := ports.activationSelection
			if selection == "" {
				selection = execution.ActivationCurrent
			}
			facts[index] = execution.PlanActivationFact{
				Plan: plan, Selection: selection,
				Selected: execution.ActivatedPlan{
					Identity: plan, StateGeneration: "activation-change", StateApplyEpoch: 2,
					ScheduleRevision: "activation-change", RequiredFullSlots: 1,
				},
			}
			if selection == execution.ActivationNone {
				facts[index].Selected = execution.ActivatedPlan{}
			}
		}
		if ports.activationSecondChangeAt != 0 && ports.activationLoadCalls >= ports.activationSecondChangeAt {
			facts[index] = execution.PlanActivationFact{
				Plan: plan, Selection: execution.ActivationCurrent,
				Selected: execution.ActivatedPlan{
					Identity: plan, StateGeneration: "activation-change-2", StateApplyEpoch: 3,
					ScheduleRevision: "activation-change-2", RequiredFullSlots: 1,
				},
			}
		}
	}
	if ports.activationLoadCalls >= 2 {
		ports.progressActivationChecked = true
	}
	return execution.PlanActivationResult{Contract: request.Contract, Facts: facts}, nil
}

type recordingPorts struct {
	trace                           *[]string
	ready                           bool
	failStage                       string
	admissionCalls                  int
	contractDrift                   bool
	alreadyApplied                  bool
	degraded                        bool
	wrongGapIdentity                bool
	wrongStateAdmissionIdentity     bool
	wrongStateApplyIdentity         bool
	progressConflict                bool
	reverseStateReceipts            bool
	unboundEffectiveTimeFacts       bool
	stateAdmissionDeterministic     bool
	stateApplyDeterministic         bool
	stateAdmissionDeterministicLast bool
	stateApplyDeterministicLast     bool
	stateAdmissionCalls             int
	stateApplyCalls                 int
	activationLoadCalls             int
	activationErrorAt               int
	activationChangeAt              int
	activationSecondChangeAt        int
	activationSelection             execution.ActivationSelection
	persistActivatedGaps            bool
	activatedGapMarkers             map[execution.PlanGapIdentity]execution.GapGuardSnapshot
	progressActivationChecked       bool
	admissionRejectAt               int
	stateLoadStatus                 execution.StateLoadStatus
	stateRetryableFirstOnly         bool
	stateLoadCalls                  int
	gapLoadStatus                   execution.GapLoadStatus
	eventSeriesDrift                bool
	eventTimeDrift                  bool
	eventRecordDrift                bool
	lastSequenceScope               execution.SequencingScope
	lastStateApply                  execution.StateApplyRequest
	lastProgress                    execution.ProgressCommitRequest
	lastEvents                      []contract.TriggerEventV1
	lastEvaluation                  execution.EvaluationRequest
	eventCount                      int
	gapMutations                    []execution.PlanGapMutation
}

func (ports *recordingPorts) Execute(ctx context.Context, request execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
	ports.record("query")
	input := validInternalExecution()
	input.Contract = request.Contract
	if ports.unboundEffectiveTimeFacts {
		for index := range input.EffectiveTimeFacts {
			input.EffectiveTimeFacts[index].SeriesIdentity = ""
		}
	}
	if ports.reverseStateReceipts {
		input.Inputs[0].Dataset = execution.NewDataset([]contract.CanonicalRecordV2{
			{
				RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000, BusinessID: "2",
				DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
				Values:            map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}, Dimensions: map[string]json.RawMessage{},
				ReceivedTime: 1_788_000_000,
			},
			{
				RecordID: strings.Repeat("f", 64), SourceTime: 1_788_000_000, BusinessID: "2",
				DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("d", 64)},
				Values:            map[string]json.RawMessage{"value": json.RawMessage(`49.9`)}, Dimensions: map[string]json.RawMessage{},
				ReceivedTime: 1_788_000_000,
			},
		})
		input.Inputs[0].View, _ = execution.NewDatasetView(input.Inputs[0].Dataset, []uint32{0, 1})
		second := execution.StatePreflightItem{
			Identity: execution.StateKeyIdentity{
				Plan: planIdentity(), StateGeneration: "state-v1",
				SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("d", 64)),
			},
			ApplyVersion: frozenApplyVersion(),
		}
		input.StatePreflight = append(input.StatePreflight, second)
		input.EffectiveTimeFacts = append(input.EffectiveTimeFacts, execution.BoundEffectiveTimeFact{
			Consumer:       execution.ConsumerRef{Plan: planIdentity(), LevelID: 5, HasLevel: true},
			SeriesIdentity: second.Identity.SeriesIdentityDigest, Fact: effectiveTimeFactForTest(input.DuePlans[0].CompiledPlan),
		})
	}
	if ports.contractDrift {
		input.Contract.QueryRevision = "query-v2"
	}
	if ports.degraded {
		input.Inputs[0].Completeness = execution.CompletenessPartial
		input.Inputs[0].Disposition = execution.AccessDegraded
		input.Inputs[0].ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
		input.Inputs[0].PartialEvidence = &execution.PartialEvidence{
			Kind: execution.PartialEvidenceOmissionStable, Version: 1, EvidenceDigest: strings.Repeat("d", 64),
			OmissionOnly: true, ReturnedRecordsStable: true,
		}
	}
	header := execution.InternalExecutionHeader{
		ExecutionID: "execution-1", Contract: input.Contract, DuePlans: input.DuePlans,
		Requirements: input.Requirements, EffectiveTimeFacts: input.EffectiveTimeFacts,
		RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{
			Digest: "physical-query-1", QueryRevision: input.Contract.QueryRevision,
		}}, DeadlineUnixMilli: 1_788_000_030_000,
	}
	if err := consumer.Begin(ctx, header); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	if err := ports.fail("query"); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	if !ports.ready {
		binding := input.Inputs[0]
		binding.Dataset, binding.View = nil, nil
		binding.Completeness, binding.DataState = execution.CompletenessUnavailable, execution.DataStateUnknown
		binding.Disposition, binding.ReasonCode = execution.AccessUnavailable, execution.ReasonCode(contract.ReasonQueryUnavailable)
		return execution.QueryExecutionCompletion{AllRequiredCompleted: true, CompletionBindings: []execution.NamedInputBinding{binding},
			PhysicalQueries: []execution.PhysicalQueryCompletion{{
				Ref: "provider-result-1", PhysicalQuery: "physical-query-1", QueryRevision: input.Contract.QueryRevision,
				Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown,
			}}}, nil
	}
	dataset := input.Inputs[0].Dataset
	var delivery execution.SeriesDelivery
	for index := 0; index < dataset.Len(); index++ {
		record, _ := dataset.Record(index)
		canonical := contract.CanonicalRecordV2{
			RecordID: record.RecordID(), SourceTime: record.SourceTime(), BusinessID: record.BusinessID(),
			DimensionIdentity: record.DimensionIdentity(), Values: record.Values(), Dimensions: record.Dimensions(),
			ReceivedTime: record.ReceivedTime(),
		}
		seriesDataset := execution.NewDataset([]contract.CanonicalRecordV2{canonical})
		view, _ := execution.NewDatasetView(seriesDataset, []uint32{0})
		binding := input.Inputs[0]
		binding.Dataset, binding.View = seriesDataset, view
		batchDelivery := execution.SeriesDelivery{
			PhysicalQuery: "physical-query-1", QueryRevision: input.Contract.QueryRevision,
			Series: 1, Records: 1, Digest: fmt.Sprintf("delivery-%d", index+1),
		}
		accumulated, accumulateErr := execution.AccumulateSeriesDelivery(delivery, batchDelivery)
		if accumulateErr != nil {
			return execution.QueryExecutionCompletion{}, accumulateErr
		}
		delivery = accumulated
		if err := consumer.ConsumeSeries(ctx, execution.SeriesExecutionBatch{
			PhysicalQuery: "physical-query-1", QueryRevision: input.Contract.QueryRevision,
			CompletionRef: "provider-result-1", Dataset: seriesDataset, Inputs: []execution.NamedInputBinding{binding},
			Delivery: batchDelivery,
		}); err != nil {
			return execution.QueryExecutionCompletion{}, err
		}
	}
	if err := ports.fail("query_after_series"); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	return execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{{
		Ref: "provider-result-1", PhysicalQuery: "physical-query-1", QueryRevision: input.Contract.QueryRevision,
		Completeness: input.Inputs[0].Completeness, DataState: input.Inputs[0].DataState, Delivery: delivery,
	}}}, nil
}

func (ports *recordingPorts) Sequence(
	ctx context.Context,
	scope execution.SequencingScope,
	run func(context.Context) error,
) error {
	ports.record("sequence")
	ports.lastSequenceScope = scope
	if err := ports.fail("sequence"); err != nil {
		return err
	}
	return run(ctx)
}

func (ports *recordingPorts) Evaluate(_ context.Context, request execution.EvaluationRequest) (execution.EvaluationResult, error) {
	ports.record("evaluate")
	ports.lastEvaluation = request
	seriesDigest := string(request.State.Items[0].Identity.SeriesIdentityDigest)
	recordID := strings.Repeat("b", 64)
	if seriesDigest == strings.Repeat("d", 64) {
		recordID = strings.Repeat("f", 64)
	}
	mutation := stateMutationForTest(seriesDigest, recordID, execution.LevelFactAnomalous)
	mutation.ExpectedBlobRevision = request.State.Items[0].BlobRevision
	disposition := execution.PlanDecided
	reason := observability.ReasonNone
	result := observability.Result(observability.ResultSuccess)
	var guardBefore []execution.PlanGapMutation
	var guardAfter []execution.PlanGapMutation
	if ports.stateLoadStatus == execution.StateDeterministicInvalid || ports.gapLoadStatus == execution.GapTerminal {
		reason = execution.ReasonCode(contract.ReasonRecordInvalid)
		result = observability.ResultTerminal
		guardBefore = []execution.PlanGapMutation{mustPlanGapMutation(execution.PlanGapMutation{
			Identity:               execution.PlanGapIdentity{Plan: planIdentity(), StateGeneration: "state-v1"},
			ExpectedMarkerRevision: request.Gaps.Items[0].MarkerRevision,
			ApplyVersion:           mutation.ApplyVersion, ScheduleRevision: "plan-schedule-v1",
			Scopes: []execution.GapScopeMutation{{Kind: execution.GapOpen, ReasonCode: reason, RequiredFullSlots: 1}},
		})}
		return execution.EvaluationResult{
			Contract: request.Header.Contract,
			Plans: []execution.PlanEvaluationResult{{
				Plan: planIdentity(), Disposition: execution.PlanTerminal, ReasonCode: reason,
				LevelOutcomes:     []execution.LevelOutcome{validLevelOutcome(execution.LevelOutcomeTerminal, reason, false)},
				GuardBeforeEvents: guardBefore,
			}},
			Result: result, ReasonCode: reason,
		}, ports.fail("evaluate")
	}
	var retryReason execution.ReasonCode
	gapUnavailable := false
	for _, state := range request.State.Items {
		if state.Status == execution.StateRetryableIO {
			retryReason = state.ReasonCode
			break
		}
	}
	if retryReason == "" {
		for _, gap := range request.Gaps.Items {
			if gap.Status == execution.GapUnavailable {
				retryReason = gap.ReasonCode
				gapUnavailable = true
				break
			}
		}
	}
	if retryReason != "" {
		outcomes := make([]execution.LevelOutcome, 0, len(request.State.Items))
		stateResults := make([]execution.StateEvaluation, 0, len(request.State.Items))
		for _, state := range request.State.Items {
			recordID := strings.Repeat("b", 64)
			if state.Identity.SeriesIdentityDigest == execution.SeriesIdentityDigest(strings.Repeat("d", 64)) {
				recordID = strings.Repeat("f", 64)
			}
			if gapUnavailable || state.Status == execution.StateRetryableIO {
				outcomes = append(outcomes, execution.LevelOutcome{
					Plan: planIdentity(), LevelID: 5, SeriesIdentityDigest: state.Identity.SeriesIdentityDigest,
					Record:  execution.RecordAnchor{RecordID: recordID, SourceTime: 1_788_000_000},
					Outcome: execution.LevelOutcomeUnknown, ReasonCode: retryReason,
				})
				continue
			}
			mutation := stateMutationForTest(string(state.Identity.SeriesIdentityDigest), recordID, execution.LevelFactAnomalous)
			mutation.ExpectedBlobRevision = state.BlobRevision
			stateResults = append(stateResults, execution.StateEvaluation{
				Mutation: mutation, Events: []contract.TriggerEventV1{validTriggerEventFor(recordID, string(state.Identity.SeriesIdentityDigest))},
			})
			outcomes = append(outcomes, execution.LevelOutcome{
				Plan: planIdentity(), LevelID: 5, SeriesIdentityDigest: state.Identity.SeriesIdentityDigest,
				Record:  execution.RecordAnchor{RecordID: recordID, SourceTime: 1_788_000_000},
				Outcome: execution.LevelOutcomeAbnormal,
			})
		}
		return execution.EvaluationResult{
			Contract: request.Header.Contract,
			Plans: []execution.PlanEvaluationResult{{
				Plan: planIdentity(), Disposition: execution.PlanRetryPending, ReasonCode: retryReason,
				LevelOutcomes: outcomes, StateResults: stateResults,
			}},
			Result: observability.ResultRetrying, ReasonCode: retryReason,
		}, ports.fail("evaluate")
	}
	if ports.degraded {
		disposition = execution.PlanDecidedDegraded
		reason = execution.ReasonCode(contract.ReasonQueryPartial)
		result = observability.ResultDegraded
		mutation = partialStateMutationForTest()
		guardBefore = []execution.PlanGapMutation{mustPlanGapMutation(execution.PlanGapMutation{
			Identity:     execution.PlanGapIdentity{Plan: planIdentity(), StateGeneration: "state-v1"},
			ApplyVersion: mutation.ApplyVersion, ScheduleRevision: "plan-schedule-v1",
			Scopes: []execution.GapScopeMutation{{Kind: execution.GapOpen, ReasonCode: reason, RequiredFullSlots: 1}},
		})}
	} else {
		guardAfter = []execution.PlanGapMutation{mustPlanGapMutation(execution.PlanGapMutation{
			Identity:               execution.PlanGapIdentity{Plan: planIdentity(), StateGeneration: "state-v1"},
			ExpectedMarkerRevision: 1, ApplyVersion: mutation.ApplyVersion, ScheduleRevision: "plan-schedule-v1",
			Scopes: []execution.GapScopeMutation{{Kind: execution.GapClear}},
		})}
	}
	event := validTriggerEventFor(recordID, seriesDigest)
	if ports.eventSeriesDrift {
		event.RecordRef.DimensionIdentityDigest = strings.Repeat("d", 64)
	}
	if ports.eventTimeDrift {
		event.EvaluationTime++
	}
	if ports.eventRecordDrift {
		event.RecordRef.RecordID = strings.Repeat("a", 64)
	}
	stateResults := []execution.StateEvaluation{{Mutation: mutation, Events: []contract.TriggerEventV1{event}}}
	levelOutcome := validLevelOutcome(execution.LevelOutcomeAbnormal, observability.ReasonNone, ports.degraded)
	levelOutcome.SeriesIdentityDigest = execution.SeriesIdentityDigest(seriesDigest)
	levelOutcome.Record = execution.RecordAnchor{RecordID: recordID, SourceTime: 1_788_000_000}
	levelOutcomes := []execution.LevelOutcome{levelOutcome}
	return execution.EvaluationResult{
		Contract: request.Header.Contract,
		Plans: []execution.PlanEvaluationResult{{
			Plan: planIdentity(), Disposition: disposition, ReasonCode: reason,
			LevelOutcomes:     levelOutcomes,
			GuardBeforeEvents: guardBefore,
			StateResults:      stateResults,
			GuardAfterState:   guardAfter,
		}},
		Result: result, ReasonCode: reason,
	}, ports.fail("evaluate")
}

func (ports *recordingPorts) LoadRuntime(_ context.Context, request execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	ports.record("state_load")
	ports.stateLoadCalls++
	items := make([]execution.RuntimeStateView, len(request.Items))
	for index, item := range request.Items {
		items[index] = execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateMissingWarming}
		if ports.stateLoadStatus == execution.StateRetryableIO || (ports.stateRetryableFirstOnly && ports.stateLoadCalls == 1 && index == 0) {
			items[index].Status = execution.StateRetryableIO
			items[index].ReasonCode = execution.ReasonCode(contract.ReasonRedisUnavailable)
		}
		if ports.stateLoadStatus == execution.StateDeterministicInvalid {
			items[index].Status = execution.StateDeterministicInvalid
			items[index].BlobRevision = 1
			items[index].ReasonCode = execution.ReasonCode(contract.ReasonRecordInvalid)
		}
		if ports.alreadyApplied {
			mutation := validStateMutation()
			refs, err := execution.DeriveRuntimeLevelContractRefs(compiledPlanForTest(nil))
			if err != nil {
				panic(err)
			}
			items[index].Status = execution.StateFoundReady
			items[index].BlobRevision = 1
			items[index].PersistedApplyVersion = mutation.ApplyVersion
			items[index].PersistedMutationDigest = mutation.MutationDigest
			items[index].Levels = []execution.RuntimeLevelStateView{{
				LevelID: 5, LevelStateCompatibility: refs[0].LevelStateCompatibility, HistoryCompleteness: execution.HistoryFull,
				WarmupRequirementRef: refs[0].WarmupRequirementRef,
			}}
		}
	}
	return execution.StatePreflightResult{Items: items}, ports.fail("state_load")
}

func (ports *recordingPorts) LoadGaps(_ context.Context, request execution.GapLoadRequest) (execution.GapLoadResult, error) {
	ports.record("gap_load")
	items := make([]execution.GapGuardSnapshot, len(request.Items))
	for index, item := range request.Items {
		if marker, found := ports.activatedGapMarkers[item.Identity]; found {
			items[index] = marker
			continue
		}
		items[index] = execution.GapGuardSnapshot{Identity: item.Identity, Status: execution.GapMissing}
		if ports.gapLoadStatus == execution.GapUnavailable {
			items[index].Status = execution.GapUnavailable
			items[index].ReasonCode = execution.ReasonCode(contract.ReasonRedisUnavailable)
			continue
		}
		if ports.gapLoadStatus == execution.GapTerminal {
			items[index].Status = execution.GapTerminal
			items[index].ReasonCode = execution.ReasonCode(contract.ReasonRecordInvalid)
			continue
		}
		if !ports.degraded {
			items[index].Status = execution.GapFound
			items[index].MarkerRevision = 1
			items[index].LastScheduleRevision = "plan-schedule-v1"
			items[index].PersistedMutationDigest = "previous-gap"
			items[index].PersistedApplyVersion = execution.ApplyVersion{
				StateApplyEpoch: 1, EvaluationTime: frozenContract().Slot.EvaluationTime - 60, SlotDigest: "previous-slot",
			}
			items[index].Scopes = []execution.GapScopeState{{
				Status: execution.GapStatusWarming, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming), RequiredFullSlots: 1,
			}}
		}
	}
	return execution.GapLoadResult{Items: items}, ports.fail("gap_load")
}

func (ports *recordingPorts) Check(context.Context, execution.SideEffectAdmissionRequest) (execution.SideEffectAdmissionResult, error) {
	ports.admissionCalls++
	stage := "admission_initial"
	if ports.admissionCalls > 1 {
		stage = "admission_event"
	}
	if ports.progressActivationChecked {
		stage = "admission_progress"
	}
	ports.record(stage)
	if ports.admissionRejectAt == ports.admissionCalls {
		return execution.SideEffectAdmissionResult{
			Admitted: false, ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift),
		}, nil
	}
	return execution.SideEffectAdmissionResult{Admitted: true}, ports.fail(stage)
}

func (ports *recordingPorts) ApplyGap(_ context.Context, request execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	stage := "gap_before"
	if len(request.Items) > 0 && len(request.Items[0].Scopes) > 0 && request.Items[0].Scopes[0].Kind == execution.GapClear {
		stage = "gap_after"
	}
	ports.record(stage)
	ports.gapMutations = append(ports.gapMutations, request.Items...)
	items := make([]execution.GapGuardApplyItemResult, len(request.Items))
	for index, item := range request.Items {
		items[index] = execution.GapGuardApplyItemResult{Identity: item.Identity, Status: execution.GapGuardApplied}
		if ports.persistActivatedGaps && item.Identity.StateGeneration != "state-v1" {
			if ports.activatedGapMarkers == nil {
				ports.activatedGapMarkers = make(map[execution.PlanGapIdentity]execution.GapGuardSnapshot)
			}
			if marker, found := ports.activatedGapMarkers[item.Identity]; found &&
				marker.PersistedApplyVersion == item.ApplyVersion && marker.PersistedMutationDigest == item.MutationDigest {
				items[index].Status = execution.GapGuardAlreadyApplied
			} else {
				scopes := make([]execution.GapScopeState, len(item.Scopes))
				for scopeIndex, scope := range item.Scopes {
					scopes[scopeIndex] = execution.GapScopeState{
						Scope: scope.Scope, Status: execution.GapStatusGapped, ReasonCode: scope.ReasonCode,
						RequiredFullSlots: scope.RequiredFullSlots,
					}
				}
				ports.activatedGapMarkers[item.Identity] = execution.GapGuardSnapshot{
					Identity: item.Identity, MarkerRevision: 1, Status: execution.GapFound,
					PersistedApplyVersion: item.ApplyVersion, PersistedMutationDigest: item.MutationDigest,
					LastScheduleRevision: item.ScheduleRevision, Scopes: scopes,
				}
			}
		}
		if ports.wrongGapIdentity {
			items[index].Identity.Plan.StrategyID = "another"
		}
	}
	return execution.GapGuardApplyResult{Items: items}, ports.fail(stage)
}

func (ports *recordingPorts) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	ports.record("event_ack")
	ports.eventCount += len(events)
	ports.lastEvents = append([]contract.TriggerEventV1(nil), events...)
	return ports.fail("event_ack")
}

func (ports *recordingPorts) AdmitRuntime(_ context.Context, request execution.StateApplyRequest) (execution.StateAdmissionResult, error) {
	ports.record("state_admission")
	ports.stateAdmissionCalls++
	items := make([]execution.StateAdmissionItemResult, len(request.Items))
	deterministicIndex := 0
	if ports.stateAdmissionDeterministicLast {
		deterministicIndex = len(request.Items) - 1
	}
	for index, item := range request.Items {
		items[index] = execution.StateAdmissionItemResult{Identity: item.Identity, Status: execution.StateAdmissionAccepted}
		if ports.wrongStateAdmissionIdentity {
			items[index].Identity.SeriesIdentityDigest = "another"
		}
		if ports.stateAdmissionDeterministic && ports.stateAdmissionCalls == 1 && index == deterministicIndex {
			items[index].Status = execution.StateAdmissionDeterministicInvalid
			items[index].ReasonCode = execution.ReasonCode(contract.ReasonRecordInvalid)
		}
	}
	if ports.reverseStateReceipts && len(items) > 1 {
		items[0], items[len(items)-1] = items[len(items)-1], items[0]
	}
	return execution.StateAdmissionResult{Items: items}, ports.fail("state_admission")
}

func (ports *recordingPorts) ApplyRuntime(_ context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	ports.record("state_apply")
	ports.stateApplyCalls++
	ports.lastStateApply = request
	items := make([]execution.StateApplyItemResult, len(request.Items))
	deterministicIndex := 0
	if ports.stateApplyDeterministicLast {
		deterministicIndex = len(request.Items) - 1
	}
	for index, item := range request.Items {
		items[index] = execution.StateApplyItemResult{Identity: item.Identity, Status: execution.StateApplied}
		if ports.wrongStateApplyIdentity {
			items[index].Identity.SeriesIdentityDigest = "another"
		}
		if ports.stateApplyDeterministic && ports.stateApplyCalls == 1 && index == deterministicIndex {
			items[index].Status = execution.StateApplyDeterministicInvalid
			items[index].ReasonCode = execution.ReasonCode(contract.ReasonRecordInvalid)
		}
	}
	if ports.reverseStateReceipts && len(items) > 1 {
		items[0], items[len(items)-1] = items[len(items)-1], items[0]
	}
	return execution.StateApplyResult{Items: items}, ports.fail("state_apply")
}

func (ports *recordingPorts) CommitProgress(_ context.Context, request execution.ProgressCommitRequest) (execution.ProgressCommitResult, error) {
	ports.record("progress_commit")
	ports.lastProgress = request
	if ports.progressConflict {
		return execution.ProgressCommitResult{Status: execution.ProgressConflict}, nil
	}
	return execution.ProgressCommitResult{Status: execution.ProgressCommitted}, ports.fail("progress_commit")
}

func (ports *recordingPorts) LoadProgress(context.Context, execution.ProgressIdentity) (execution.ProgressLoadResult, error) {
	return execution.ProgressLoadResult{Status: execution.ProgressMissing}, nil
}

func (ports *recordingPorts) record(stage string) { *ports.trace = append(*ports.trace, stage) }
func (ports *recordingPorts) fail(stage string) error {
	if ports.failStage == stage {
		return errors.New("injected " + stage)
	}
	return nil
}

func slotRequest(operation execution.Operation) execution.SlotExecutionRequest {
	return execution.SlotExecutionRequest{
		Contract: frozenContract(), Operation: operation,
		OwnerFence:       execution.OwnerFence{QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot: frozenContract().Slot.EvaluationTime,
	}
}

func frozenContract() execution.FrozenExecutionContractRef {
	plans, requirements := baseDuePlanAndRequirements()
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		panic(err)
	}
	return execution.FrozenExecutionContractRef{
		Slot:                 execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_000},
		SnapshotRevision:     "snapshot-v1",
		QueryRevision:        "query-v1",
		ScheduleRevision:     "schedule-v1",
		ScheduleSegmentStart: 1_787_999_940,
		DuePlanSetDigest:     digest,
	}
}

func planIdentity() execution.PlanIdentity {
	return execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
}

func validInternalExecution() execution.InternalExecution {
	plan := planIdentity()
	plans, requirements := baseDuePlanAndRequirements()
	due := plans[0]
	compiled := due.CompiledPlan
	identity := execution.StateKeyIdentity{Plan: plan, StateGeneration: "state-v1", SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("c", 64))}
	applyVersion := frozenApplyVersion()
	consumer := requirements[0].Consumers[0].Consumer
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{{
		RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}, Dimensions: map[string]json.RawMessage{},
		ReceivedTime: 1_788_000_000,
	}})
	view, err := execution.NewDatasetView(dataset, []uint32{0})
	if err != nil {
		panic(err)
	}
	return execution.InternalExecution{
		Contract: frozenContract(), DuePlans: plans, Requirements: requirements,
		Inputs: []execution.NamedInputBinding{{
			Consumer: consumer, RequirementID: "main", DatasetName: "main",
			Role: execution.InputRolePrimary, ProviderResult: "provider-result-1",
			Provenance:  execution.InputProvenance{PhysicalQuery: "physical-query-1", AttemptNo: 1},
			QueryWindow: execution.QueryWindow{Start: 1_787_999_940, End: 1_788_000_000}, ImpactScope: execution.ImpactPlan,
			Dataset: dataset, View: view,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable,
		}},
		StatePreflight: []execution.StatePreflightItem{{
			Identity:     identity,
			ApplyVersion: applyVersion,
		}},
		EffectiveTimeFacts: []execution.BoundEffectiveTimeFact{{
			Consumer:       execution.ConsumerRef{Plan: plan, LevelID: 5, HasLevel: true},
			SeriesIdentity: identity.SeriesIdentityDigest, Fact: effectiveTimeFactForTest(compiled),
		}},
		GapPreflight: []execution.PlanGapLoadItem{{
			Identity: execution.PlanGapIdentity{Plan: plan, StateGeneration: "state-v1"}, ApplyVersion: applyVersion,
			ScheduleRevision: "plan-schedule-v1",
		}},
	}
}

func baseDuePlanAndRequirements() ([]execution.DuePlan, []execution.DataRequirement) {
	plan := planIdentity()
	due := execution.DuePlan{
		Identity: plan, CompiledPlan: compiledPlanForTest(nil),
		StateGeneration: "state-v1", StateApplyEpoch: 1, ScheduleRevision: "plan-schedule-v1",
		CompletionDeadlineUnixMilli: 1_788_000_060_000,
		PartialCapabilities: []execution.LevelPartialCapability{{
			LevelID: 5, Policy: execution.PartialProvableAbnormalOnly,
			Proof: &execution.PartialProofRef{
				EvidenceKind: execution.PartialEvidenceOmissionStable, EvidenceVersion: 1,
				RuleID: "threshold-gte", RuleVersion: 1, ProofDigest: strings.Repeat("e", 64),
			},
		}},
	}
	consumer := execution.ConsumerRef{Plan: plan}
	requirement := execution.DataRequirement{
		RequirementID: "main", DatasetName: "main", Role: execution.InputRolePrimary, LogicalQueryRef: "query-main",
		RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: -60, EndOffsetSeconds: 0, HalfOpen: true},
		StepMillis:     60_000, AlignmentMillis: 60_000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen,
		ReadinessClass: execution.ReadinessEager, RequiredColumns: []string{"value"},
		Consumers: []execution.DataRequirementConsumer{{
			Consumer: consumer, ConsumerDeadlineUnixMilli: due.CompletionDeadlineUnixMilli,
			DownstreamExecutionReserveMilliSec: 5_000,
		}},
	}
	return []execution.DuePlan{due}, []execution.DataRequirement{requirement}
}

func effectiveTimeFactForTest(plan *strategy.CompiledPlan) strategy.EffectiveTimeFact {
	level := plan.Levels()[0]
	provider := strategy.NewStaticScheduleProvider(nil)
	facts, err := provider.Resolve(context.Background(), []strategy.EffectiveTimeRequest{{
		TenantID: "tenant", BusinessID: "2", EvaluationTime: int64(frozenContract().Slot.EvaluationTime),
		Requirement: level.EffectiveTimeRequirement(),
	}})
	if err != nil || len(facts) != 1 {
		panic(fmt.Sprintf("resolve EffectiveTime fact: %v", err))
	}
	return facts[0]
}

func validStateMutation() execution.StateMutation {
	return stateMutationForTest(strings.Repeat("c", 64), strings.Repeat("b", 64), execution.LevelFactAnomalous)
}

func partialStateMutationForTest() execution.StateMutation {
	mutation := stateMutationForTest(strings.Repeat("c", 64), strings.Repeat("b", 64), execution.LevelFactAnomalous)
	seriesWarmup, err := execution.DeriveRuntimeSeriesWarmupRequirementRef(compiledPlanForTest(nil))
	if err != nil {
		panic(err)
	}
	mutation.MutationDigest = ""
	mutation.Levels[0].HistoryCompleteness = execution.HistoryWarming
	mutation.Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	mutation.SeriesGuard = &execution.StateGuardFact{
		Status: execution.HistoryWarming, ReasonCode: execution.ReasonCode(contract.ReasonQueryPartial),
		WarmupRequirementRef: seriesWarmup,
	}
	built, err := execution.BuildStateMutation(mutation)
	if err != nil {
		panic(err)
	}
	return built
}

func stateMutationForTest(seriesDigest, recordID string, factResult execution.LevelFactResult) execution.StateMutation {
	refs, err := execution.DeriveRuntimeLevelContractRefs(compiledPlanForTest(nil))
	if err != nil {
		panic(err)
	}
	mutation, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: execution.StateKeyIdentity{
			Plan: planIdentity(), StateGeneration: "state-v1", SeriesIdentityDigest: execution.SeriesIdentityDigest(seriesDigest),
		},
		ExpectedBlobRevision: 0,
		ApplyVersion:         frozenApplyVersion(),
		AffectedRecords:      []execution.RecordAnchor{{RecordID: recordID, SourceTime: 1_788_000_000}},
		Levels: []execution.RuntimeLevelStateMutation{{
			LevelID: 5, LevelStateCompatibility: refs[0].LevelStateCompatibility, HistoryCompleteness: execution.HistoryFull,
			WarmupRequirementRef: refs[0].WarmupRequirementRef, LastProcessedEventTime: 1_788_000_000,
		}},
		Points: []execution.StateHistoryPoint{{
			RecordID: recordID, SourceTime: 1_788_000_000,
			Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: refs[0].DetectFingerprint, Result: factResult}},
		}},
	})
	if err != nil {
		panic(err)
	}
	return mutation
}

func validLevelOutcome(kind execution.LevelOutcomeKind, reason execution.ReasonCode, partial bool) execution.LevelOutcome {
	outcome := execution.LevelOutcome{
		Plan: planIdentity(), LevelID: 5, SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("c", 64)),
		Record:  execution.RecordAnchor{RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000},
		Outcome: kind, ReasonCode: reason,
	}
	if partial {
		outcome.PartialProofs = []execution.PartialDecisionProof{{
			RequirementID: "main", DatasetName: "main", EvidenceDigest: strings.Repeat("d", 64),
			Proof: execution.PartialProofRef{
				EvidenceKind: execution.PartialEvidenceOmissionStable, EvidenceVersion: 1,
				RuleID: "threshold-gte", RuleVersion: 1, ProofDigest: strings.Repeat("e", 64),
			},
			Result: execution.PartialProofProvenAbnormal,
		}}
	}
	return outcome
}

func validTriggerEvent() contract.TriggerEventV1 {
	return validTriggerEventFor(strings.Repeat("b", 64), strings.Repeat("c", 64))
}

func validTriggerEventFor(recordID, seriesDigest string) contract.TriggerEventV1 {
	compiled := compiledPlanForTest(nil)
	fingerprints := compiled.Fingerprints()
	event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{
		EventKind: contract.TriggerEventAbnormal, TenantID: "tenant", BusinessID: "2",
		PlanRef: compiled.PlanRef(),
		RecordRef: contract.TriggerRecordRefV1{
			RecordID: recordID, SourceTime: 1_788_000_000,
			DimensionIdentityDigest: seriesDigest, Dimensions: map[string]json.RawMessage{},
		},
		Observed: contract.TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}},
		LevelResults: []contract.LevelResultV1{{
			LevelID: 5, Priority: 1, Result: contract.LevelResultAbnormal,
			LevelTriggerFingerprint: strings.Repeat("e", 64),
			DecisionWindow: contract.DecisionWindowV1{
				Type: "N_OF_M_WITH_CONTINUOUS_MISS", Version: 1, SourceTime: 1_788_000_000,
				Trigger: contract.TriggerWindowEvidenceV1{
					WindowStart: 1_788_000_000, WindowEnd: 1_788_000_000, WindowSize: 1,
					RequiredAnomalies: 1, ObservedAnomalies: 1,
				},
				Recovery: contract.RecoveryWindowEvidenceV1{
					Enabled: true, RequiredConsecutiveWindows: 1, OldestWindowStart: 1_788_000_000,
				},
				HistoryCompleteness: "FULL",
				WindowEvidence:      contract.WindowEvidenceV1{AnomalyTimestampsDigest: strings.Repeat("9", 64)},
			},
			DetectEvidence: contract.DetectEvidenceV1{
				DetectionResult: "ANOMALOUS", PredicateDigest: strings.Repeat("8", 64),
				NormalizedValue: json.RawMessage(`50.1`), EffectiveTimeStatus: "ACTIVE",
			},
		}},
		EvaluationTime: 1_788_000_000, DetectPlanFingerprint: fingerprints.Detect,
		TriggerStateFingerprint: fingerprints.Trigger, ExecutionID: "execution-1", MaxEvidenceBytes: 4096,
	})
	if err != nil {
		panic(err)
	}
	return *event
}

func compiledPlanForTest(t testing.TB) *strategy.CompiledPlan {
	if t != nil {
		t.Helper()
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "worker-test-v1",
	})
	if err != nil {
		panic(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "strategy-v1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	plan := contract.EvaluationPlanV2{
		PlanID: "7", StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref,
			InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{
				EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300,
				AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120,
			},
			Levels: []contract.LevelIRV2{{
				Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1}, Connector: contract.LevelConnectorAND,
				DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{
					Type: "Threshold", Version: 1,
					Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`),
				}}},
				TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
				RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
			}},
		},
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan,
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64),
			IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time",
		},
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1",
			HistoryCellSemanticsVersion: "detect-history-cell-v1",
		},
	})
	if err != nil {
		panic(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		panic("test plan did not compile")
	}
	return compiled
}

func frozenApplyVersion() execution.ApplyVersion {
	version, err := execution.BuildApplyVersion(frozenContract(), 1)
	if err != nil {
		panic(err)
	}
	return version
}

func mustPlanGapMutation(mutation execution.PlanGapMutation) execution.PlanGapMutation {
	built, err := execution.BuildPlanGapMutation(mutation)
	if err != nil {
		panic(err)
	}
	return built
}

func assertObservedOperations(t *testing.T, observations *[]observability.Observation, operation execution.Operation) {
	t.Helper()
	for _, observation := range *observations {
		if observation.Operation != observability.Operation(operation) {
			t.Fatalf("observation operation=%q, want=%q", observation.Operation, operation)
		}
	}
}

func assertTrace(t *testing.T, got *[]string, want []string) {
	t.Helper()
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("trace=%v, want=%v", *got, want)
	}
}

func publicMethodNames(value reflect.Type) []string {
	methods := make([]string, value.NumMethod())
	for index := 0; index < value.NumMethod(); index++ {
		methods[index] = value.Method(index).Name
	}
	return methods
}
