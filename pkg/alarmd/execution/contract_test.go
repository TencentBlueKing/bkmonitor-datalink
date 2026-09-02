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
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestFrozenExecutionContractRefContainsOnlyFrozenSemantics(t *testing.T) {
	ref := frozenContract()
	wantFields := []string{"Slot", "SnapshotRevision", "QueryRevision", "ScheduleRevision", "ScheduleSegmentStart", "DuePlanSetDigest"}
	typeOfRef := reflect.TypeOf(ref)
	if typeOfRef.NumField() != len(wantFields) {
		t.Fatalf("FrozenExecutionContractRef fields=%d, want=%d", typeOfRef.NumField(), len(wantFields))
	}
	for index, want := range wantFields {
		if got := typeOfRef.Field(index).Name; got != want {
			t.Fatalf("FrozenExecutionContractRef field[%d]=%s, want=%s", index, got, want)
		}
	}
}

func TestFrozenExecutionContractRefValidationFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*execution.FrozenExecutionContractRef)
	}{
		{name: "query group", mutate: func(ref *execution.FrozenExecutionContractRef) { ref.Slot.QueryGroup = "" }},
		{name: "evaluation time", mutate: func(ref *execution.FrozenExecutionContractRef) { ref.Slot.EvaluationTime = 0 }},
		{name: "snapshot revision", mutate: func(ref *execution.FrozenExecutionContractRef) { ref.SnapshotRevision = "" }},
		{name: "query revision", mutate: func(ref *execution.FrozenExecutionContractRef) { ref.QueryRevision = "" }},
		{name: "schedule revision", mutate: func(ref *execution.FrozenExecutionContractRef) { ref.ScheduleRevision = "" }},
		{name: "schedule segment start", mutate: func(ref *execution.FrozenExecutionContractRef) { ref.ScheduleSegmentStart = 0 }},
		{name: "segment after Slot", mutate: func(ref *execution.FrozenExecutionContractRef) {
			ref.ScheduleSegmentStart = ref.Slot.EvaluationTime + 1
		}},
		{name: "due plan digest", mutate: func(ref *execution.FrozenExecutionContractRef) { ref.DuePlanSetDigest = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref := frozenContract()
			test.mutate(&ref)
			if err := ref.Validate(); err == nil {
				t.Fatal("Validate() error=nil")
			}
		})
	}
}

func TestOperationSetIsClosedForC0(t *testing.T) {
	for _, operation := range []execution.Operation{
		execution.OperationNormal,
		execution.OperationRetry,
		execution.OperationReplay,
		execution.OperationProbe,
	} {
		if err := operation.Validate(); err != nil {
			t.Fatalf("Validate(%q) error=%v", operation, err)
		}
	}
	for _, operation := range []execution.Operation{"", "unknown"} {
		if err := operation.Validate(); err == nil {
			t.Fatalf("Validate(%q) error=nil", operation)
		}
	}
}

func TestResultReasonUsesFrozenObservationCatalog(t *testing.T) {
	if err := execution.ValidateResultReason(observability.ResultSuccess, observability.ReasonNone); err != nil {
		t.Fatalf("success reason validation error=%v", err)
	}
	if err := execution.ValidateResultReason(observability.ResultDegraded, execution.ReasonCode(contract.ReasonQueryPartial)); err != nil {
		t.Fatalf("degraded reason validation error=%v", err)
	}
	for _, test := range []struct {
		result execution.Result
		reason execution.ReasonCode
	}{
		{result: observability.ResultSuccess, reason: execution.ReasonCode(contract.ReasonQueryPartial)},
		{result: observability.ResultDegraded, reason: observability.ReasonNone},
		{result: observability.ResultDegraded, reason: "UNKNOWN_REASON"},
	} {
		if err := execution.ValidateResultReason(test.result, test.reason); err == nil {
			t.Fatalf("ValidateResultReason(%q, %q) error=nil", test.result, test.reason)
		}
	}
}

func TestInternalExecutionKeepsPlanDatasetCompletenessAndStateFacts(t *testing.T) {
	executionInput := validInternalExecution()
	if err := executionInput.Validate(frozenContract()); err != nil {
		t.Fatalf("Validate() error=%v", err)
	}
	if executionInput.Inputs[0].Dataset == nil || executionInput.Inputs[0].Dataset.Len() != 1 {
		t.Fatal("FULL+DATA must carry its immutable record")
	}

	unavailable := executionInput
	unavailable.Inputs = append([]execution.NamedInputBinding(nil), executionInput.Inputs...)
	unavailable.Inputs[0].Completeness = execution.CompletenessUnavailable
	unavailable.Inputs[0].DataState = execution.DataStateUnknown
	unavailable.Inputs[0].Disposition = execution.AccessUnavailable
	unavailable.Inputs[0].ReasonCode = execution.ReasonCode(contract.ReasonQueryUnavailable)
	unavailable.Inputs[0].Dataset = nil
	unavailable.Inputs[0].View = nil
	unavailable.StatePreflight = nil
	unavailable.EffectiveTimeFacts = nil
	if err := unavailable.Validate(frozenContract()); err != nil {
		t.Fatalf("UNAVAILABLE Validate() error=%v", err)
	}

	invalid := executionInput
	invalid.Inputs = append([]execution.NamedInputBinding(nil), executionInput.Inputs...)
	invalid.Inputs[0].Dataset = nil
	if err := invalid.Validate(frozenContract()); err == nil {
		t.Fatal("FULL binding with nil dataset must fail")
	}

	duplicateState := executionInput
	duplicateState.StatePreflight = append(append([]execution.StatePreflightItem(nil), executionInput.StatePreflight...), executionInput.StatePreflight[0])
	if err := duplicateState.Validate(frozenContract()); err == nil {
		t.Fatal("duplicate state preflight identity must fail before keyed sequencing")
	}

	duplicateGap := executionInput
	duplicateGap.GapPreflight = append(append([]execution.PlanGapLoadItem(nil), executionInput.GapPreflight...), executionInput.GapPreflight[0])
	if err := duplicateGap.Validate(frozenContract()); err == nil {
		t.Fatal("duplicate gap preflight identity must fail before keyed sequencing")
	}
}

func TestStateMutationPreflightClassifiesStableReplay(t *testing.T) {
	mutation := validStateMutation()
	view := execution.RuntimeStateView{
		Identity:                mutation.Identity,
		BlobRevision:            mutation.ExpectedBlobRevision,
		Status:                  execution.StateFoundReady,
		PersistedApplyVersion:   mutation.ApplyVersion,
		PersistedMutationDigest: mutation.MutationDigest,
		VersionComparison:       execution.ApplyVersionEqual,
	}
	if got := execution.ClassifyStateMutation(view, mutation); got != execution.StateAlreadyApplied {
		t.Fatalf("same version/digest=%q", got)
	}
	view.PersistedMutationDigest = execution.MutationDigest("different")
	if got := execution.ClassifyStateMutation(view, mutation); got != execution.StateVersionConflict {
		t.Fatalf("same version/different digest=%q", got)
	}
	view.PersistedApplyVersion.EvaluationTime++
	view.VersionComparison = execution.ApplyVersionPersistedNewer
	if got := execution.ClassifyStateMutation(view, mutation); got != execution.StateStaleVersion {
		t.Fatalf("newer persisted version=%q", got)
	}
	view = execution.RuntimeStateView{Identity: mutation.Identity, Status: execution.StateMissingWarming, VersionComparison: execution.ApplyVersionPersistedOlder}
	missingMutation := mutation
	missingMutation.ExpectedBlobRevision = 0
	if got := execution.ClassifyStateMutation(view, missingMutation); got != execution.StateProceed {
		t.Fatalf("missing state=%q", got)
	}
}

func TestStatePreflightPreservesUnavailableAndClassifiesBeforeEvaluation(t *testing.T) {
	input := validInternalExecution()
	request := execution.StatePreflightRequest{Contract: input.Contract, Items: input.StatePreflight}
	identity := input.StatePreflight[0].Identity
	classified, err := execution.ClassifyStatePreflight(request, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: identity, Status: execution.StateMissingWarming,
	}}})
	if err != nil || classified.Items[0].VersionComparison != execution.ApplyVersionPersistedOlder {
		t.Fatalf("missing classification=%+v error=%v", classified, err)
	}
	classified, err = execution.ClassifyStatePreflight(request, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: identity, Status: execution.StateRetryableIO, ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable),
	}}})
	if err != nil || classified.Items[0].Status != execution.StateRetryableIO {
		t.Fatalf("UNAVAILABLE state classification=%+v error=%v", classified, err)
	}
	classified, err = execution.ClassifyStatePreflight(request, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: identity, Status: execution.StateDeterministicInvalid, BlobRevision: 1,
		ReasonCode: execution.ReasonCode(contract.ReasonRecordInvalid),
	}}})
	if err != nil || classified.Items[0].Status != execution.StateDeterministicInvalid ||
		classified.Items[0].VersionComparison != execution.ApplyVersionPersistedOlder {
		t.Fatalf("TERMINAL state classification=%+v error=%v", classified, err)
	}
	classified, err = execution.ClassifyStatePreflight(request, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: identity, Status: execution.StateDeterministicInvalid,
		ReasonCode: execution.ReasonCode(contract.ReasonStateCorrupt),
	}}})
	if err != nil || classified.Items[0].BlobRevision != 0 {
		t.Fatalf("terminal decode failure without revision must remain a typed deterministic result: %+v error=%v", classified, err)
	}
	_, err = execution.ClassifyStatePreflight(request, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: identity, Status: execution.StateRetryableIO, BlobRevision: 1,
		ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable),
	}}})
	if err == nil {
		t.Fatal("UNAVAILABLE state carrying a trusted revision must fail")
	}
}

func TestGapLoadPreservesUnavailableAndTerminalFacts(t *testing.T) {
	input := validInternalExecution()
	request := execution.GapLoadRequest{Contract: input.Contract, Items: input.GapPreflight}
	for _, test := range []struct {
		status execution.GapLoadStatus
		reason execution.ReasonCode
	}{
		{status: execution.GapUnavailable, reason: execution.ReasonCode(contract.ReasonRedisUnavailable)},
		{status: execution.GapTerminal, reason: execution.ReasonCode(contract.ReasonRecordInvalid)},
	} {
		result := execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
			Identity: request.Items[0].Identity, Status: test.status, ReasonCode: test.reason,
		}}}
		if err := execution.ValidateGapLoad(request, result); err != nil {
			t.Fatalf("ValidateGapLoad(%q) error=%v", test.status, err)
		}
		if result.Items[0].Status != test.status || result.Items[0].ReasonCode != test.reason {
			t.Fatalf("typed gap fact changed: %+v", result.Items[0])
		}
	}
	invalid := execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: request.Items[0].Identity, Status: execution.GapUnavailable, MarkerRevision: 1,
		ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable),
	}}}
	if err := execution.ValidateGapLoad(request, invalid); err == nil {
		t.Fatal("UNAVAILABLE gap carrying a trusted revision must fail")
	}
	invalid = execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: request.Items[0].Identity, Status: execution.GapFound, MarkerRevision: 1,
		PersistedApplyVersion: frozenApplyVersion(), PersistedMutationDigest: "gap-digest",
		LastScheduleRevision: "plan-schedule-v1",
		Scopes: []execution.GapScopeState{{
			Scope: execution.GapScope{LevelID: 5}, Status: execution.GapStatusGapped,
			ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped),
		}},
	}}}
	if err := execution.ValidateGapLoad(request, invalid); err == nil {
		t.Fatal("Gap scope with inconsistent HasLevel/LevelID must fail")
	}
}

func TestApplyVersionMustMatchFrozenSlotAndPlan(t *testing.T) {
	input := validInternalExecution()
	input.StatePreflight[0].ApplyVersion.SlotDigest = "drift"
	if err := input.Validate(frozenContract()); err == nil {
		t.Fatal("state ApplyVersion drift must fail")
	}
	input = validInternalExecution()
	input.GapPreflight[0].ApplyVersion.EvaluationTime++
	if err := input.Validate(frozenContract()); err == nil {
		t.Fatal("gap ApplyVersion drift must fail")
	}
	input = validInternalExecution()
	input.GapPreflight[0].ScheduleRevision = "another"
	if err := input.Validate(frozenContract()); err == nil {
		t.Fatal("gap plan schedule revision drift must fail")
	}
}

func TestCompletionKindFoldsInputAndPlanFacts(t *testing.T) {
	input := validInternalExecution()
	result := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{{Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecided}}}
	if got, err := execution.DeriveCompletionKind(input, result); err != nil || got != execution.CompletionFull {
		t.Fatalf("FULL+DATA completion=%q error=%v", got, err)
	}
	result.Plans[0].Disposition = execution.PlanUnavailable
	result.Plans[0].GuardBeforeEvents = []execution.PlanGapMutation{{}}
	if got, err := execution.DeriveCompletionKind(input, result); err != nil || got != execution.CompletionUnavailable {
		t.Fatalf("UNAVAILABLE completion=%q error=%v", got, err)
	}
	result.Plans[0].Disposition = execution.PlanTerminal
	if got, err := execution.DeriveCompletionKind(input, result); err != nil || got != execution.CompletionTerminal {
		t.Fatalf("TERMINAL completion=%q error=%v", got, err)
	}
	result.Plans[0].Disposition = execution.PlanReadinessGap
	if got, err := execution.DeriveCompletionKind(input, result); err != nil || got != execution.CompletionUnavailable {
		t.Fatalf("READINESS_GAP completion=%q error=%v", got, err)
	}
}

func TestPlanGapOpenAndStrengthenShareIdempotentDigest(t *testing.T) {
	input := validInternalExecution()
	base := execution.PlanGapMutation{
		Identity: input.GapPreflight[0].Identity, ApplyVersion: input.GapPreflight[0].ApplyVersion,
		ScheduleRevision: input.GapPreflight[0].ScheduleRevision,
		Scopes: []execution.GapScopeMutation{{
			Kind: execution.GapOpen, ReasonCode: execution.ReasonCode(contract.ReasonQueryPartial), RequiredFullSlots: 1,
		}},
	}
	opened := mustPlanGapMutation(base)
	base.Scopes[0].Kind = execution.GapStrengthen
	strengthened := mustPlanGapMutation(base)
	if opened.MutationDigest != strengthened.MutationDigest {
		t.Fatalf("OPEN digest=%q STRENGTHEN digest=%q", opened.MutationDigest, strengthened.MutationDigest)
	}
}

func TestDependencyCompletenessDoesNotReplacePrimaryCompletionFact(t *testing.T) {
	input := validInternalExecution()
	dependency := input.Inputs[0]
	dependency.RequirementID = "history"
	dependency.DatasetName = "history"
	dependency.Role = execution.InputRoleAlgorithmDependency
	dependency.ProviderResult = "provider-result-history"
	dependency.Completeness = execution.CompletenessUnavailable
	dependency.DataState = execution.DataStateUnknown
	dependency.Disposition = execution.AccessUnavailable
	dependency.ReasonCode = execution.ReasonCode(contract.ReasonQueryUnavailable)
	dependency.Dataset = nil
	dependency.View = nil
	input.Requirements = append(input.Requirements, execution.DataRequirement{
		RequirementID: "history", DatasetName: "history", Role: execution.InputRoleAlgorithmDependency,
		LogicalQueryRef: "query-history",
		RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -86_460, EndOffsetSeconds: -86_400, HalfOpen: true},
		StepMillis:      60_000, AlignmentMillis: 60_000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen,
		ReadinessClass: execution.ReadinessFinalizedRequired, RequiredColumns: []string{"value"},
		Consumers: []execution.DataRequirementConsumer{{
			Consumer: dependency.Consumer, ConsumerDeadlineUnixMilli: input.DuePlans[0].CompletionDeadlineUnixMilli,
			DownstreamExecutionReserveMilliSec: 5_000,
		}},
	})
	dependency.QueryWindow = input.Requirements[1].AbsoluteWindow(input.Contract.Slot.EvaluationTime)
	input.Inputs = append(input.Inputs, dependency)
	digest, err := execution.DeriveDuePlanSetDigest(input.DuePlans, input.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	input.Contract.DuePlanSetDigest = digest
	if err := input.Validate(input.Contract); err != nil {
		t.Fatalf("InternalExecution.Validate() error=%v", err)
	}
	primary, err := execution.DerivePrimaryInputFact(input)
	if err != nil || primary.Completeness != execution.CompletenessFull || primary.DataState != execution.DataStateData {
		t.Fatalf("PRIMARY fact=%+v error=%v", primary, err)
	}
	result := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{{Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecided}}}
	if got, err := execution.DeriveCompletionKind(input, result); err != nil || got != execution.CompletionFull {
		t.Fatalf("completion=%q error=%v", got, err)
	}
}

func TestPrimaryInputFactIsOrderIndependent(t *testing.T) {
	input := validInternalExecution()
	partialData := input.Inputs[0]
	partialData.RequirementID = "partial-data"
	partialData.DatasetName = "partial-data"
	partialData.ProviderResult = "provider-result-partial"
	partialData.Completeness = execution.CompletenessPartial
	partialData.DataState = execution.DataStateData
	partialData.Disposition = execution.AccessDegraded
	partialData.ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	partialData.Dataset = execution.NewDataset([]contract.CanonicalRecordV2{{}})
	partialData.PartialEvidence = &execution.PartialEvidence{
		Kind: execution.PartialEvidenceOmissionStable, Version: 1, EvidenceDigest: strings.Repeat("d", 64),
		OmissionOnly: true, ReturnedRecordsStable: true,
	}
	input.Inputs = append(input.Inputs, partialData)

	forward, err := execution.DerivePrimaryInputFact(input)
	if err != nil {
		t.Fatalf("forward PRIMARY fact error=%v", err)
	}
	input.Inputs[0], input.Inputs[1] = input.Inputs[1], input.Inputs[0]
	reverse, err := execution.DerivePrimaryInputFact(input)
	if err != nil {
		t.Fatalf("reverse PRIMARY fact error=%v", err)
	}
	if forward != reverse || forward.Completeness != execution.CompletenessPartial || forward.DataState != execution.DataStateData {
		t.Fatalf("forward=%+v reverse=%+v", forward, reverse)
	}
}

func TestEveryDuePlanRequiresPrimaryInput(t *testing.T) {
	input := validInternalExecution()
	input.Inputs[0].Role = execution.InputRoleAlgorithmDependency
	if err := input.Validate(frozenContract()); err == nil {
		t.Fatal("due Plan without PRIMARY input must fail")
	}
}

func TestOptionalLevelIdentityIsCanonical(t *testing.T) {
	for _, mutate := range []func(*execution.InternalExecution){
		func(input *execution.InternalExecution) { input.Inputs[0].Consumer.LevelID = 5 },
		func(input *execution.InternalExecution) { input.Inputs[0].Consumer.HasLevel = true },
	} {
		input := validInternalExecution()
		mutate(&input)
		if err := input.Validate(frozenContract()); err == nil {
			t.Fatal("ConsumerRef with inconsistent HasLevel/LevelID must fail")
		}
	}
}

func TestCompletionFoldDoesNotGuessLoadedGuardFacts(t *testing.T) {
	input := validInternalExecution()
	result := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{{
		Plan: input.DuePlans[0].Identity, Disposition: execution.PlanUnavailable,
	}}}
	if got, err := execution.DeriveCompletionKind(input, result); err != nil || got != execution.CompletionUnavailable {
		t.Fatalf("completion=%q error=%v", got, err)
	}
}

func TestEvaluationResultRejectsCrossPlanAndDoubleGapMutation(t *testing.T) {
	input := validInternalExecution()
	gapIdentity := input.GapPreflight[0].Identity
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: input.StatePreflight[0].Identity, Status: execution.StateMissingWarming,
	}}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: gapIdentity, Status: execution.GapMissing,
	}}})
	mutation := mustPlanGapMutation(execution.PlanGapMutation{
		Identity:     gapIdentity,
		ApplyVersion: input.GapPreflight[0].ApplyVersion, ScheduleRevision: input.GapPreflight[0].ScheduleRevision,
		Scopes: []execution.GapScopeMutation{{
			Kind: execution.GapOpen, ReasonCode: execution.ReasonCode(contract.ReasonQueryPartial), RequiredFullSlots: 1,
		}},
	})
	result := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultSuccess,
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecided,
			LevelOutcomes:     []execution.LevelOutcome{normalLevelOutcome()},
			StateResults:      []execution.StateEvaluation{normalStateEvaluation()},
			GuardBeforeEvents: []execution.PlanGapMutation{mutation},
		}},
	}
	result.Plans[0].GuardBeforeEvents[0].Identity.Plan.StrategyID = "another"
	if err := result.Validate(request); err == nil {
		t.Fatal("cross-Plan gap mutation must fail")
	}
	result.Plans[0].GuardBeforeEvents[0] = mutation
	result.Plans[0].GuardAfterState = []execution.PlanGapMutation{mutation}
	if err := result.Validate(request); err == nil {
		t.Fatal("one marker cannot be opened and cleared in the same Slot")
	}
}

func TestEvaluationResultRejectsUnsafePrimaryAndLoadFactsBeforeSideEffects(t *testing.T) {
	input := validInternalExecution()
	state := execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: input.StatePreflight[0].Identity, Status: execution.StateMissingWarming,
	}}}
	gaps := execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapMissing,
	}}}
	request := evaluationRequest(input, state, gaps)
	decided := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultSuccess,
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecided,
			LevelOutcomes: []execution.LevelOutcome{normalLevelOutcome()},
			StateResults:  []execution.StateEvaluation{normalStateEvaluation()},
		}},
	}
	if err := decided.Validate(request); err != nil {
		t.Fatalf("FULL decided result error=%v", err)
	}

	partialInput := input
	partialInput.Inputs = append([]execution.NamedInputBinding(nil), input.Inputs...)
	partialInput.Inputs[0].Completeness = execution.CompletenessPartial
	partialInput.Inputs[0].Disposition = execution.AccessDegraded
	partialInput.Inputs[0].ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	if err := decided.Validate(evaluationRequest(partialInput, state, gaps)); err == nil {
		t.Fatal("PARTIAL PRIMARY cannot produce an unqualified decision")
	}

	request.State.Items[0].Status = execution.StateRetryableIO
	request.State.Items[0].ReasonCode = execution.ReasonCode(contract.ReasonRedisUnavailable)
	if err := decided.Validate(request); err == nil {
		t.Fatal("unavailable State load cannot be ignored by evaluation")
	}
}

func TestEvaluationResultCannotIgnoreExactLoadedGuards(t *testing.T) {
	input := validInternalExecution()
	seriesWarmup, err := execution.DeriveRuntimeSeriesWarmupRequirementRef(input.DuePlans[0].CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	decided := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultSuccess,
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecided,
			LevelOutcomes: []execution.LevelOutcome{normalLevelOutcome()},
			StateResults:  []execution.StateEvaluation{normalStateEvaluation()},
		}},
	}
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: input.StatePreflight[0].Identity, Status: execution.StateFoundGapped,
		SeriesGuard: &execution.StateGuardFact{
			Status: execution.HistoryGapped, ReasonCode: execution.ReasonCode(contract.ReasonQueryPartial),
			WarmupRequirementRef: seriesWarmup,
		},
	}}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapMissing,
	}}})
	if err := decided.Validate(request); err == nil {
		t.Fatal("series guard must prevent NORMAL")
	}

	request.State.Items[0] = execution.RuntimeStateView{
		Identity: input.StatePreflight[0].Identity, Status: execution.StateMissingWarming,
	}
	request.Gaps.Items[0] = execution.GapGuardSnapshot{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapFound,
		Scopes: []execution.GapScopeState{{
			Scope: execution.GapScope{LevelID: 5, HasLevel: true}, Status: execution.GapStatusGapped,
			ReasonCode: execution.ReasonCode(contract.ReasonQueryPartial), RequiredFullSlots: 1,
		}},
	}
	if err := decided.Validate(request); err == nil {
		t.Fatal("matching Level gap scope must prevent NORMAL")
	}

}

func TestEvaluationAllowsWarmingStateAdvanceWithGuardedUnknown(t *testing.T) {
	input := validInternalExecution()
	state := normalStateEvaluation()
	state.Mutation.ExpectedBlobRevision = 1
	state.Mutation.Levels[0].HistoryCompleteness = execution.HistoryWarming
	state.Mutation.Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonHistoryWarming)
	state.Mutation.SeriesGuard = &execution.StateGuardFact{
		Status: execution.HistoryWarming, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
		WarmupRequirementRef: mustSeriesWarmupRequirementRef(input.DuePlans[0].CompiledPlan),
	}
	state.Mutation.MutationDigest = ""
	state.Mutation = mustStateMutation(state.Mutation)
	outcome := normalLevelOutcome()
	outcome.Outcome = execution.LevelOutcomeUnknown
	outcome.ReasonCode = execution.ReasonCode(contract.ReasonHistoryWarming)
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
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
	}}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapMissing,
	}}})
	result := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultDegraded,
		ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecidedDegraded,
			ReasonCode:    execution.ReasonCode(contract.ReasonHistoryWarming),
			LevelOutcomes: []execution.LevelOutcome{outcome}, StateResults: []execution.StateEvaluation{state},
		}},
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("guarded WARMING state advance rejected: %v", err)
	}

	unguarded := result
	unguarded.Plans = append([]execution.PlanEvaluationResult(nil), result.Plans...)
	unguarded.Plans[0].StateResults = append([]execution.StateEvaluation(nil), result.Plans[0].StateResults...)
	mutation := unguarded.Plans[0].StateResults[0].Mutation
	mutation.SeriesGuard = nil
	mutation.Levels[0].HistoryCompleteness = execution.HistoryFull
	mutation.Levels[0].GapReasonCode = ""
	mutation.MutationDigest = ""
	unguarded.Plans[0].StateResults[0].Mutation = mustStateMutation(mutation)
	if err := unguarded.Validate(request); err == nil {
		t.Fatal("UNKNOWN without an exact durable mutation guard must not advance history")
	}

	for _, completeness := range []execution.Completeness{execution.CompletenessPartial} {
		degradedInput := input
		degradedInput.Inputs = append([]execution.NamedInputBinding(nil), input.Inputs...)
		degradedInput.Inputs[0].Completeness = completeness
		degradedInput.Inputs[0].Disposition = execution.AccessDegraded
		degradedInput.Inputs[0].ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
		if err := result.Validate(evaluationRequest(degradedInput, request.State, request.Gaps)); err == nil {
			t.Fatalf("%s input must not advance guarded UNKNOWN history", completeness)
		}
	}
}

func TestEvaluationAllowsFullInactiveStateAdvance(t *testing.T) {
	input := validInternalExecution()
	compiled := compiledPlanWithTriggerConfig(t, json.RawMessage(`{
		"window_size":1,"required_anomalies":1,"step_seconds":60,"timezone_ref":"BUSINESS_LOCAL",
		"uptime":{"time_ranges":[{"start":"00:00","end":"00:01"}]}
	}`))
	input.DuePlans[0].CompiledPlan = compiled
	provider := strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(
		func(context.Context, string, string, string) (*time.Location, error) { return time.UTC, nil },
	))
	facts, err := provider.Resolve(context.Background(), []strategy.EffectiveTimeRequest{{
		TenantID: "tenant", BusinessID: "2", EvaluationTime: int64(input.Contract.Slot.EvaluationTime),
		Requirement: compiled.Levels()[0].EffectiveTimeRequirement(),
	}})
	if err != nil || len(facts) != 1 || facts[0].Status() != strategy.EffectiveTimeInactive {
		t.Fatalf("inactive EffectiveTime fact = %+v, %v", facts, err)
	}
	input.EffectiveTimeFacts[0].Fact = facts[0]

	state := normalStateEvaluation()
	state.Mutation.ExpectedBlobRevision = 1
	state.Mutation.MutationDigest = ""
	state.Mutation = mustStateMutation(state.Mutation)
	outcome := normalLevelOutcome()
	outcome.Outcome = execution.LevelOutcomeUnknown
	outcome.ReasonCode = execution.ReasonCode(contract.ReasonEffectiveTimeInactive)
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: input.StatePreflight[0].Identity, BlobRevision: 1,
		PersistedApplyVersion:   olderApplyVersion(input.StatePreflight[0].ApplyVersion),
		PersistedMutationDigest: "loaded-state", Status: execution.StateFoundReady,
		Levels: []execution.RuntimeLevelStateView{{
			LevelID:                 state.Mutation.Levels[0].LevelID,
			LevelStateCompatibility: state.Mutation.Levels[0].LevelStateCompatibility,
			HistoryCompleteness:     execution.HistoryFull,
			WarmupRequirementRef:    state.Mutation.Levels[0].WarmupRequirementRef,
		}},
	}}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapMissing,
	}}})
	result := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultDegraded,
		ReasonCode: execution.ReasonCode(contract.ReasonEffectiveTimeInactive),
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecidedDegraded,
			ReasonCode:    execution.ReasonCode(contract.ReasonEffectiveTimeInactive),
			LevelOutcomes: []execution.LevelOutcome{outcome}, StateResults: []execution.StateEvaluation{state},
		}},
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("FULL INACTIVE state advance rejected: %v", err)
	}
}

func TestEvaluationAllowsLoadedSeriesWarmingToCompleteWithFinalFullMutation(t *testing.T) {
	for _, outcomeKind := range []execution.LevelOutcomeKind{
		execution.LevelOutcomeNormal, execution.LevelOutcomeAbnormal, execution.LevelOutcomeRecovery,
	} {
		t.Run(string(outcomeKind), func(t *testing.T) {
			result, request := loadedSeriesWarmingCompletion(t, outcomeKind)
			if err := result.Validate(request); err != nil {
				t.Fatalf("loaded series-local WARMING must accept %s after canonical FULL mutation: %v", outcomeKind, err)
			}
		})
	}
}

func TestEvaluationAllowsUnknownOwnReasonAfterLoadedSeriesWarmingCompletes(t *testing.T) {
	result, request := loadedSeriesWarmingInactiveCompletion(t)
	if err := result.Validate(request); err != nil {
		t.Fatalf("completed loaded WARMING must not replace the current UNKNOWN reason: %v", err)
	}
}

func TestEvaluationRejectsWrongUnknownReasonAfterLoadedSeriesWarmingCompletes(t *testing.T) {
	result, request := loadedSeriesWarmingInactiveCompletion(t)
	wrongReason := execution.ReasonCode(contract.ReasonHistoryWarming)
	result.ReasonCode = wrongReason
	result.Plans[0].ReasonCode = wrongReason
	result.Plans[0].LevelOutcomes[0].ReasonCode = wrongReason
	if err := result.Validate(request); err == nil {
		t.Fatal("completed loaded WARMING must not authorize an UNKNOWN reason unrelated to the current fact")
	}
}

func TestEvaluationRejectsUnknownWhenLoadedSeriesWarmingIsNotCompleted(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*execution.EvaluationResult, *execution.EvaluationRequest)
	}{
		{
			name: "no final mutation",
			mutate: func(result *execution.EvaluationResult, _ *execution.EvaluationRequest) {
				result.Plans[0].StateResults = nil
			},
		},
		{
			name: "mutation remains warming",
			mutate: func(result *execution.EvaluationResult, _ *execution.EvaluationRequest) {
				mutation := result.Plans[0].StateResults[0].Mutation
				mutation.Levels = append([]execution.RuntimeLevelStateMutation(nil), mutation.Levels...)
				mutation.Levels[0].HistoryCompleteness = execution.HistoryWarming
				mutation.Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonHistoryWarming)
				mutation.MutationDigest = ""
				result.Plans[0].StateResults[0].Mutation = mustStateMutation(mutation)
			},
		},
		{
			name: "loaded gapped",
			mutate: func(_ *execution.EvaluationResult, request *execution.EvaluationRequest) {
				request.State.Items[0].Status = execution.StateFoundGapped
				request.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryGapped
				request.State.Items[0].Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonHistoryGapped)
			},
		},
		{
			name: "series guard",
			mutate: func(_ *execution.EvaluationResult, request *execution.EvaluationRequest) {
				request.State.Items[0].SeriesGuard = &execution.StateGuardFact{
					Status: execution.HistoryWarming, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
					WarmupRequirementRef: mustSeriesWarmupRequirementRef(request.Header.DuePlans[0].CompiledPlan),
				}
			},
		},
		{
			name: "plan gap",
			mutate: func(_ *execution.EvaluationResult, request *execution.EvaluationRequest) {
				request.Gaps.Items[0].Status = execution.GapFound
				request.Gaps.Items[0].MarkerRevision = 1
				request.Gaps.Items[0].Scopes = []execution.GapScopeState{{
					Status: execution.GapStatusWarming, ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
					RequiredFullSlots: 9, ObservedFullSlots: 8,
				}}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, request := loadedSeriesWarmingInactiveCompletion(t)
			test.mutate(&result, &request)
			if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "active guard reason") {
				t.Fatalf("incomplete loaded WARMING proof must preserve its active reason, got %v", err)
			}
		})
	}
}

func TestEvaluationRejectsLoadedSeriesWarmingWithoutExclusiveFinalFullProof(t *testing.T) {
	t.Run("no final mutation", func(t *testing.T) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeNormal)
		result.Plans[0].StateResults = nil
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "active Runtime State or Plan gap guard") {
			t.Fatalf("loaded WARMING without a final State mutation must reject NORMAL at the guard, got %v", err)
		}
	})

	t.Run("mutation remains warming", func(t *testing.T) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeRecovery)
		mutation := result.Plans[0].StateResults[0].Mutation
		mutation.Levels = append([]execution.RuntimeLevelStateMutation(nil), mutation.Levels...)
		mutation.Levels[0].HistoryCompleteness = execution.HistoryWarming
		mutation.Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonHistoryWarming)
		mutation.MutationDigest = ""
		result.Plans[0].StateResults[0].Mutation = mustStateMutation(mutation)
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "active Runtime State or Plan gap guard") {
			t.Fatalf("a WARMING final mutation must not clear loaded WARMING for RECOVERY, got %v", err)
		}
	})

	t.Run("loaded gapped", func(t *testing.T) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeNormal)
		request.State.Items[0].Status = execution.StateFoundGapped
		request.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryGapped
		request.State.Items[0].Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonHistoryGapped)
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "active Runtime State or Plan gap guard") {
			t.Fatalf("loaded GAPPED must continue to reject NORMAL at the guard, got %v", err)
		}
	})

	t.Run("series guard", func(t *testing.T) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeNormal)
		request.State.Items[0].SeriesGuard = &execution.StateGuardFact{
			Status: execution.HistoryWarming, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
			WarmupRequirementRef: mustSeriesWarmupRequirementRef(validInternalExecution().DuePlans[0].CompiledPlan),
		}
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "active Runtime State or Plan gap guard") {
			t.Fatalf("a loaded series guard must continue to reject NORMAL at the guard, got %v", err)
		}
	})

	for _, scope := range []execution.GapScope{{}, {HasLevel: true, LevelID: 5}} {
		name := "plan gap"
		if scope.HasLevel {
			name = "level gap"
		}
		t.Run(name, func(t *testing.T) {
			result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeRecovery)
			request.Gaps.Items[0].Status = execution.GapFound
			request.Gaps.Items[0].MarkerRevision = 1
			request.Gaps.Items[0].Scopes = []execution.GapScopeState{{
				Scope: scope, Status: execution.GapStatusGapped,
				ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 1,
			}}
			if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "active Runtime State or Plan gap guard") {
				t.Fatalf("loaded %s must continue to reject RECOVERY at the guard, got %v", name, err)
			}
		})
	}

	t.Run("successful outcome lacks current State fact", func(t *testing.T) {
		result, request := loadedSeriesWarmingCompletion(t, execution.LevelOutcomeNormal)
		mutation := result.Plans[0].StateResults[0].Mutation
		mutation.Points = append([]execution.StateHistoryPoint(nil), mutation.Points...)
		mutation.Points[len(mutation.Points)-1].Levels = nil
		mutation.MutationDigest = ""
		result.Plans[0].StateResults[0].Mutation = mustStateMutation(mutation)
		if err := result.Validate(request); err == nil || !strings.Contains(err.Error(), "successful Level outcome lacks its State fact") {
			t.Fatalf("successful outcome without its current State fact must reject at State conservation, got %v", err)
		}
	})
}

func loadedSeriesWarmingInactiveCompletion(
	t testing.TB,
) (execution.EvaluationResult, execution.EvaluationRequest) {
	t.Helper()
	input := validInternalExecution()
	compiled := compiledPlanWithTriggerConfig(t, json.RawMessage(`{
		"window_size":1,"required_anomalies":1,"step_seconds":60,"timezone_ref":"BUSINESS_LOCAL",
		"uptime":{"time_ranges":[{"start":"00:00","end":"00:01"}]}
	}`))
	input.DuePlans[0].CompiledPlan = compiled
	provider := strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(
		func(context.Context, string, string, string) (*time.Location, error) { return time.UTC, nil },
	))
	facts, err := provider.Resolve(context.Background(), []strategy.EffectiveTimeRequest{{
		TenantID: "tenant", BusinessID: "2", EvaluationTime: int64(input.Contract.Slot.EvaluationTime),
		Requirement: compiled.Levels()[0].EffectiveTimeRequirement(),
	}})
	if err != nil || len(facts) != 1 || facts[0].Status() != strategy.EffectiveTimeInactive {
		t.Fatalf("inactive EffectiveTime fact = %+v, %v", facts, err)
	}
	input.EffectiveTimeFacts[0].Fact = facts[0]

	refs, err := execution.DeriveRuntimeLevelContractRefs(compiled)
	if err != nil {
		t.Fatal(err)
	}
	state := normalStateEvaluation()
	state.Mutation.ExpectedBlobRevision = 1
	state.Mutation.Levels = append([]execution.RuntimeLevelStateMutation(nil), state.Mutation.Levels...)
	state.Mutation.Levels[0].LevelStateCompatibility = refs[0].LevelStateCompatibility
	state.Mutation.Levels[0].WarmupRequirementRef = refs[0].WarmupRequirementRef
	state.Mutation.Points = append([]execution.StateHistoryPoint(nil), state.Mutation.Points...)
	state.Mutation.Points[0].Levels = append([]execution.StateLevelFact(nil), state.Mutation.Points[0].Levels...)
	state.Mutation.Points[0].Levels[0].DetectFingerprint = refs[0].DetectFingerprint
	state.Mutation.MutationDigest = ""
	state.Mutation = mustStateMutation(state.Mutation)
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: input.StatePreflight[0].Identity, BlobRevision: 1,
		PersistedApplyVersion:   olderApplyVersion(input.StatePreflight[0].ApplyVersion),
		PersistedMutationDigest: "loaded-state", Status: execution.StateFoundWarming,
		Levels: []execution.RuntimeLevelStateView{{
			LevelID: 5, LevelStateCompatibility: refs[0].LevelStateCompatibility,
			HistoryCompleteness:  execution.HistoryWarming,
			GapReasonCode:        execution.ReasonCode(contract.ReasonGapSkipped),
			WarmupRequirementRef: refs[0].WarmupRequirementRef,
		}},
	}}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapMissing,
	}}})
	outcome := normalLevelOutcome()
	outcome.Outcome = execution.LevelOutcomeUnknown
	outcome.ReasonCode = execution.ReasonCode(contract.ReasonEffectiveTimeInactive)
	return execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultDegraded, ReasonCode: outcome.ReasonCode,
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecidedDegraded,
			ReasonCode: outcome.ReasonCode, LevelOutcomes: []execution.LevelOutcome{outcome},
			StateResults: []execution.StateEvaluation{state},
		}},
	}, request
}

func TestTerminalAggregateReasonMustComeFromTerminalPlan(t *testing.T) {
	input := validInternalExecution()
	gap := input.GapPreflight[0]
	terminalReason := execution.ReasonCode(contract.ReasonRecordInvalid)
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: input.StatePreflight[0].Identity, Status: execution.StateDeterministicInvalid, ReasonCode: terminalReason,
	}}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: gap.Identity, Status: execution.GapMissing,
	}}})
	result := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultTerminal, ReasonCode: terminalReason,
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanTerminal, ReasonCode: terminalReason,
			LevelOutcomes: []execution.LevelOutcome{{
				Plan: input.DuePlans[0].Identity, LevelID: 5,
				SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("c", 64)),
				Record:               execution.RecordAnchor{RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000},
				Outcome:              execution.LevelOutcomeTerminal, ReasonCode: terminalReason,
			}},
			GuardBeforeEvents: []execution.PlanGapMutation{mustPlanGapMutation(execution.PlanGapMutation{
				Identity: gap.Identity, ApplyVersion: gap.ApplyVersion, ScheduleRevision: gap.ScheduleRevision,
				Scopes: []execution.GapScopeMutation{{Kind: execution.GapOpen, ReasonCode: terminalReason, RequiredFullSlots: 1}},
			})},
		}},
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("valid terminal evaluation error=%v", err)
	}
	result.ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	if err := result.Validate(request); err == nil {
		t.Fatal("terminal aggregate reason from a non-terminal Plan must fail")
	}
}

func TestLocalizedTerminalRequiresAndAcceptsExactSeriesGuard(t *testing.T) {
	input := validInternalExecution()
	reason := execution.ReasonCode(contract.ReasonRecordInvalid)
	levelRefs, err := execution.DeriveRuntimeLevelContractRefs(input.DuePlans[0].CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	seriesWarmup, err := execution.DeriveRuntimeSeriesWarmupRequirementRef(input.DuePlans[0].CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: input.StatePreflight[0].Identity, Status: execution.StateDeterministicInvalid,
		BlobRevision: 1, ReasonCode: reason,
	}}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapMissing,
	}}})
	guard, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: input.StatePreflight[0].Identity, ExpectedBlobRevision: 1,
		ApplyVersion: input.StatePreflight[0].ApplyVersion,
		AffectedRecords: []execution.RecordAnchor{{
			RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000,
		}},
		SeriesGuard: &execution.StateGuardFact{
			Status: execution.HistoryGapped, ReasonCode: reason, WarmupRequirementRef: seriesWarmup,
		},
		Levels: []execution.RuntimeLevelStateMutation{{
			LevelID: 5, LevelStateCompatibility: levelRefs[0].LevelStateCompatibility, HistoryCompleteness: execution.HistoryGapped,
			GapReasonCode: reason, WarmupRequirementRef: levelRefs[0].WarmupRequirementRef,
		}},
	})
	if err != nil {
		t.Fatalf("BuildStateMutation() error=%v", err)
	}
	result := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultTerminal, ReasonCode: reason,
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecidedDegraded, ReasonCode: reason,
			LevelOutcomes: []execution.LevelOutcome{{
				Plan: input.DuePlans[0].Identity, LevelID: 5,
				SeriesIdentityDigest: input.StatePreflight[0].Identity.SeriesIdentityDigest,
				Record:               execution.RecordAnchor{RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000},
				Outcome:              execution.LevelOutcomeTerminal, ReasonCode: reason,
			}},
			StateResults: []execution.StateEvaluation{{Mutation: guard}},
		}},
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("exact series guard result error=%v", err)
	}

	wrong := result
	wrong.Plans = append([]execution.PlanEvaluationResult(nil), result.Plans...)
	wrong.Plans[0].StateResults = append([]execution.StateEvaluation(nil), result.Plans[0].StateResults...)
	wrong.Plans[0].StateResults[0].Mutation.Identity.SeriesIdentityDigest = execution.SeriesIdentityDigest(strings.Repeat("d", 64))
	if err := wrong.Validate(request); err == nil {
		t.Fatal("sibling series guard must not close localized terminal outcome")
	}
}

func TestLocalizedBadSeriesOutsideDatasetCanProduceExactGuard(t *testing.T) {
	input := validInternalExecution()
	reason := execution.ReasonCode(contract.ReasonRecordInvalid)
	badSeries := execution.SeriesIdentityDigest(strings.Repeat("d", 64))
	badAnchor := execution.RecordAnchor{RecordID: strings.Repeat("f", 64), SourceTime: 1_788_000_001}
	input.Inputs[0].Disposition = execution.AccessDegraded
	input.Inputs[0].ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	input.Inputs[0].Terminals = []execution.InputTerminal{{
		ReasonCode: reason, ImpactScope: execution.ImpactSeries,
		RecordID: badAnchor.RecordID, SourceTime: badAnchor.SourceTime, SeriesIdentity: badSeries,
	}}
	badPreflight := input.StatePreflight[0]
	badPreflight.Identity.SeriesIdentityDigest = badSeries
	input.StatePreflight = append(input.StatePreflight, badPreflight)
	badEffectiveTime := input.EffectiveTimeFacts[0]
	badEffectiveTime.SeriesIdentity = badSeries
	input.EffectiveTimeFacts = append(input.EffectiveTimeFacts, badEffectiveTime)

	levelRefs, err := execution.DeriveRuntimeLevelContractRefs(input.DuePlans[0].CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	seriesWarmup, err := execution.DeriveRuntimeSeriesWarmupRequirementRef(input.DuePlans[0].CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	badGuard, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: badPreflight.Identity, ExpectedBlobRevision: 1, ApplyVersion: badPreflight.ApplyVersion,
		AffectedRecords: []execution.RecordAnchor{badAnchor},
		SeriesGuard: &execution.StateGuardFact{
			Status: execution.HistoryGapped, ReasonCode: reason, WarmupRequirementRef: seriesWarmup,
		},
		Levels: []execution.RuntimeLevelStateMutation{{
			LevelID: 5, LevelStateCompatibility: levelRefs[0].LevelStateCompatibility,
			HistoryCompleteness: execution.HistoryGapped, GapReasonCode: reason,
			WarmupRequirementRef: levelRefs[0].WarmupRequirementRef,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{
		{Identity: input.StatePreflight[0].Identity, Status: execution.StateMissingWarming},
		{Identity: badPreflight.Identity, Status: execution.StateDeterministicInvalid, BlobRevision: 1, ReasonCode: reason},
	}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapMissing,
	}}})
	result := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultTerminal, ReasonCode: reason,
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecidedDegraded, ReasonCode: reason,
			LevelOutcomes: []execution.LevelOutcome{
				normalLevelOutcome(),
				{Plan: input.DuePlans[0].Identity, LevelID: 5, SeriesIdentityDigest: badSeries,
					Record: badAnchor, Outcome: execution.LevelOutcomeTerminal, ReasonCode: reason},
			},
			StateResults: []execution.StateEvaluation{normalStateEvaluation(), {Mutation: badGuard}},
		}},
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("localized excluded series guard error=%v", err)
	}
}

func TestDegradedOutcomeCannotClearItsOnlyFinalGuard(t *testing.T) {
	input := validInternalExecution()
	reason := execution.ReasonCode(contract.ReasonRecordInvalid)
	loadedApplyVersion := input.GapPreflight[0].ApplyVersion
	loadedApplyVersion.EvaluationTime--
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: input.StatePreflight[0].Identity, Status: execution.StateMissingWarming,
	}}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapFound, MarkerRevision: 1,
		PersistedApplyVersion: loadedApplyVersion, PersistedMutationDigest: "loaded-gap",
		LastScheduleRevision: input.GapPreflight[0].ScheduleRevision,
		Scopes: []execution.GapScopeState{{
			Status: execution.GapStatusGapped, ReasonCode: reason, RequiredFullSlots: 1,
		}},
	}}})
	clear := mustPlanGapMutation(execution.PlanGapMutation{
		Identity: input.GapPreflight[0].Identity, ExpectedMarkerRevision: 1,
		ApplyVersion: input.GapPreflight[0].ApplyVersion, ScheduleRevision: input.GapPreflight[0].ScheduleRevision,
		Scopes: []execution.GapScopeMutation{{Kind: execution.GapClear}},
	})
	result := execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultTerminal, ReasonCode: reason,
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanTerminal, ReasonCode: reason,
			LevelOutcomes: []execution.LevelOutcome{{
				Plan: input.DuePlans[0].Identity, LevelID: 5,
				SeriesIdentityDigest: input.StatePreflight[0].Identity.SeriesIdentityDigest,
				Record:               execution.RecordAnchor{RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000},
				Outcome:              execution.LevelOutcomeTerminal, ReasonCode: reason,
			}},
			GuardAfterState: []execution.PlanGapMutation{clear},
		}},
	}
	if err := result.Validate(request); err == nil {
		t.Fatal("clearing the only exact guard before Progress must fail")
	}
}

func TestQueryProviderContractIsTypedAndClosed(t *testing.T) {
	var _ execution.QueryProvider = queryProviderStub{}
	providerType := reflect.TypeOf((*execution.QueryProvider)(nil)).Elem()
	method, ok := providerType.MethodByName("Execute")
	if !ok || method.Type.NumIn() != 3 || method.Type.In(1) != reflect.TypeOf(execution.QueryAttempt{}) ||
		method.Type.In(2) != reflect.TypeOf((*execution.ProviderSeriesSink)(nil)).Elem() ||
		method.Type.NumOut() != 2 || method.Type.Out(0) != reflect.TypeOf(execution.ProviderCompletion{}) {
		t.Fatalf("QueryProvider.Execute signature=%v", method.Type)
	}

	attempt := validQueryAttempt()
	if err := attempt.Validate(); err != nil {
		t.Fatalf("QueryAttempt.Validate() error=%v", err)
	}
	recovery := attempt
	recovery.Operation = execution.OperationReplay
	if err := recovery.Validate(); err == nil {
		t.Fatal("replay without recovery permit must fail")
	}
	recovery.RecoveryPermit = &execution.RecoveryPermit{
		PermitID: "permit", Slot: recovery.Slot, Operation: recovery.Operation,
		ExpiresAtUnixMilli: recovery.DeadlineUnixMilli,
	}
	if err := recovery.Validate(); err != nil {
		t.Fatalf("replay with recovery permit error=%v", err)
	}
}

func TestQueryExecutionRequestRequiresTypedAttemptFacts(t *testing.T) {
	request := execution.QueryExecutionRequest{
		Contract: frozenContract(), Operation: execution.OperationReplay, AttemptNo: 2,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("QueryExecutionRequest.Validate() error=%v", err)
	}
	request.AttemptNo = 0
	if err := request.Validate(); err == nil {
		t.Fatal("QueryExecutionRequest accepted zero attempt number")
	}
}

func TestLocalizedBadSeriesRequiresStatePreflightOutsideDataset(t *testing.T) {
	input := validInternalExecution()
	badSeries := execution.SeriesIdentityDigest(strings.Repeat("d", 64))
	terminal := execution.InputTerminal{
		ReasonCode: execution.ReasonCode(contract.ReasonRecordInvalid), ImpactScope: execution.ImpactSeries,
		RecordID: strings.Repeat("f", 64), SourceTime: 1_788_000_001, SeriesIdentity: badSeries,
	}
	input.Inputs[0].Disposition = execution.AccessDegraded
	input.Inputs[0].ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	input.Inputs[0].Terminals = []execution.InputTerminal{terminal}
	badPreflight := input.StatePreflight[0]
	badPreflight.Identity.SeriesIdentityDigest = badSeries
	input.StatePreflight = append(input.StatePreflight, badPreflight)
	badEffectiveTime := input.EffectiveTimeFacts[0]
	badEffectiveTime.SeriesIdentity = badSeries
	input.EffectiveTimeFacts = append(input.EffectiveTimeFacts, badEffectiveTime)

	if err := input.Validate(frozenContract()); err != nil {
		t.Fatalf("localized bad series contract error=%v", err)
	}

	missing := input
	missing.StatePreflight = missing.StatePreflight[:1]
	if err := missing.Validate(frozenContract()); err == nil {
		t.Fatal("localized bad series without State preflight must fail")
	}
}

func TestInternalExecutionDataStateMatchesDatasetCardinality(t *testing.T) {
	input := validInternalExecution()
	input.Inputs[0].DataState = execution.DataStateEmpty
	if err := input.Validate(frozenContract()); err == nil {
		t.Fatal("EMPTY binding with records must fail")
	}
	input = validInternalExecution()
	if err := input.Validate(frozenContract()); err != nil {
		t.Fatalf("DATA binding with one record error=%v", err)
	}
}

func TestStoreAndProgressReceiptStatusReasonContracts(t *testing.T) {
	stateIdentity := validStateMutation().Identity
	gapIdentity := validInternalExecution().GapPreflight[0].Identity
	retryable := execution.ReasonCode(contract.ReasonRedisUnavailable)
	deterministic := execution.ReasonCode(contract.ReasonRecordInvalid)

	tests := []struct {
		name  string
		valid func() error
		bad   func() error
	}{
		{
			name: "gap success",
			valid: func() error {
				return (execution.GapGuardApplyResult{Items: []execution.GapGuardApplyItemResult{{Identity: gapIdentity, Status: execution.GapGuardApplied}}}).Validate()
			},
			bad: func() error {
				return (execution.GapGuardApplyResult{Items: []execution.GapGuardApplyItemResult{{Identity: gapIdentity, Status: execution.GapGuardApplied, ReasonCode: retryable}}}).Validate()
			},
		},
		{
			name: "gap retryable class",
			valid: func() error {
				return (execution.GapGuardApplyResult{Items: []execution.GapGuardApplyItemResult{{Identity: gapIdentity, Status: execution.GapGuardRetryable, ReasonCode: retryable}}}).Validate()
			},
			bad: func() error {
				return (execution.GapGuardApplyResult{Items: []execution.GapGuardApplyItemResult{{Identity: gapIdentity, Status: execution.GapGuardRetryable, ReasonCode: deterministic}}}).Validate()
			},
		},
		{
			name: "state admission success",
			valid: func() error {
				return (execution.StateAdmissionResult{Items: []execution.StateAdmissionItemResult{{Identity: stateIdentity, Status: execution.StateAdmissionAccepted}}}).Validate()
			},
			bad: func() error {
				return (execution.StateAdmissionResult{Items: []execution.StateAdmissionItemResult{{Identity: stateIdentity, Status: execution.StateAdmissionAccepted, ReasonCode: retryable}}}).Validate()
			},
		},
		{
			name: "state apply retryable class",
			valid: func() error {
				return (execution.StateApplyResult{Items: []execution.StateApplyItemResult{{Identity: stateIdentity, Status: execution.StateApplyRetryable, ReasonCode: retryable}}}).Validate()
			},
			bad: func() error {
				return (execution.StateApplyResult{Items: []execution.StateApplyItemResult{{Identity: stateIdentity, Status: execution.StateApplyRetryable, ReasonCode: deterministic}}}).Validate()
			},
		},
		{
			name:  "progress success",
			valid: func() error { return (execution.ProgressCommitResult{Status: execution.ProgressCommitted}).Validate() },
			bad: func() error {
				return (execution.ProgressCommitResult{Status: execution.ProgressCommitted, ReasonCode: retryable}).Validate()
			},
		},
		{
			name: "progress retryable class",
			valid: func() error {
				return (execution.ProgressCommitResult{Status: execution.ProgressRetryableIO, ReasonCode: retryable}).Validate()
			},
			bad: func() error {
				return (execution.ProgressCommitResult{Status: execution.ProgressRetryableIO, ReasonCode: deterministic}).Validate()
			},
		},
		{
			name:  "progress conflict has no copied reason",
			valid: func() error { return (execution.ProgressCommitResult{Status: execution.ProgressConflict}).Validate() },
			bad: func() error {
				return (execution.ProgressCommitResult{Status: execution.ProgressConflict, ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable)}).Validate()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.valid(); err != nil {
				t.Fatalf("valid receipt error=%v", err)
			}
			if err := test.bad(); err == nil {
				t.Fatal("invalid status/reason pair must fail")
			}
		})
	}
}

func TestProgressCommitRequiresSelfConsistentPrimaryFact(t *testing.T) {
	request := execution.ProgressCommitRequest{
		Identity:         execution.ProgressIdentity{QueryGroup: frozenContract().Slot.QueryGroup},
		OwnerFence:       execution.OwnerFence{QueryGroup: frozenContract().Slot.QueryGroup, OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		ExpectedNextSlot: frozenContract().Slot.EvaluationTime,
		Completion: execution.SlotCompletion{
			Contract: frozenContract(), Kind: execution.CompletionFullEmpty,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty},
			Result:  observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid Progress request error=%v", err)
	}
	request.Completion.Kind = execution.CompletionFull
	if err := request.Validate(); err == nil {
		t.Fatal("FULL completion with EMPTY PRIMARY must fail")
	}
	request.Completion.Kind = execution.CompletionUnavailable
	request.Completion.Primary = &execution.PrimaryInputFact{Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateEmpty}
	if err := request.Validate(); err == nil {
		t.Fatal("UNAVAILABLE PRIMARY with EMPTY state must fail")
	}
}

func TestProgressLoadDistinguishesFoundAndMissing(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "query-group"}
	if err := (execution.ProgressLoadResult{Status: execution.ProgressMissing}).Validate(identity); err != nil {
		t.Fatalf("MISSING Progress error=%v", err)
	}
	progress := &execution.ScheduleProgress{Identity: identity, NextSlot: 120}
	if err := (execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: progress}).Validate(identity); err != nil {
		t.Fatalf("FOUND Progress error=%v", err)
	}
	if err := (execution.ProgressLoadResult{Status: execution.ProgressFound}).Validate(identity); err == nil {
		t.Fatal("FOUND without Progress must fail")
	}
	if err := (execution.ProgressLoadResult{Status: execution.ProgressMissing, Progress: progress}).Validate(identity); err == nil {
		t.Fatal("MISSING with persisted facts must fail")
	}
}

func TestPublicExecutionContractsHaveNoOpaquePayload(t *testing.T) {
	assertNoOpaqueFields(t, reflect.TypeOf(execution.InternalExecution{}), map[reflect.Type]bool{})
	assertNoOpaqueFields(t, reflect.TypeOf(execution.EvaluationResult{}), map[reflect.Type]bool{})
	assertNoOpaqueFields(t, reflect.TypeOf(execution.PhysicalQuerySpec{}), map[reflect.Type]bool{})
	assertNoOpaqueFields(t, reflect.TypeOf(execution.QueryAttempt{}), map[reflect.Type]bool{})
}

func assertNoOpaqueFields(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = typ.Elem()
	}
	if typ.PkgPath() != "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution" || seen[typ] {
		return
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	seen[typ] = true
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.Type.Kind() == reflect.Interface || (field.Type.Kind() == reflect.Map && field.Type.Elem().Kind() == reflect.Interface) {
			t.Fatalf("%s.%s uses opaque type %s", typ.Name(), field.Name, field.Type)
		}
		assertNoOpaqueFields(t, field.Type, seen)
	}
}

func frozenContract() execution.FrozenExecutionContractRef {
	plans, requirements := baseDuePlanAndRequirements()
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		panic(err)
	}
	return execution.FrozenExecutionContractRef{
		Slot: execution.SlotIdentity{
			QueryGroup:     execution.QueryGroupIdentity("query-group"),
			EvaluationTime: execution.EvaluationTime(1_788_000_000),
		},
		SnapshotRevision:     execution.SnapshotRevision("snapshot-v1"),
		QueryRevision:        execution.QueryRevision("query-v1"),
		ScheduleRevision:     execution.ScheduleRevision("schedule-v1"),
		ScheduleSegmentStart: execution.EvaluationTime(1_787_999_940),
		DuePlanSetDigest:     digest,
	}
}

type queryProviderStub struct{}

func (queryProviderStub) Execute(context.Context, execution.QueryAttempt, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	return execution.ProviderCompletion{}, nil
}

func validQueryAttempt() execution.QueryAttempt {
	planFacts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-route-v1",
		TenantID: "tenant", BusinessID: "business", SpaceScope: "space",
		QueryList: []execution.QueryClause{{
			ReferenceName: "a", DataSource: "bk_monitor", TableID: "system.cpu", FieldName: "usage",
			Driver: "influxdb", TimeField: "time",
			Functions:       []execution.QueryFunction{{Method: "avg", Position: 1}},
			TimeAggregation: execution.QueryFunction{Method: "avg", Position: 1, Window: "60s"},
		}},
		MetricMerge: "a", StepMillis: 60_000, AlignmentMillis: 60_000, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{
			DatasetContract: contract.DatasetContractV2{
				SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64),
				IdentityFields: []string{"bk_target_ip"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time",
			},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1",
		},
	})
	if err != nil {
		panic(err)
	}
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{
		PlanFacts:       planFacts,
		LogicalWindow:   execution.QueryWindow{Start: 40, End: 100},
		ProviderRange:   execution.QueryWindow{Start: 20, End: 120},
		AcceptedRange:   execution.QueryWindow{Start: 40, End: 100},
		RequiredColumns: []string{"usage"},
	})
	if err != nil {
		panic(err)
	}
	return execution.QueryAttempt{
		Spec: spec,
		Slot: frozenContract().Slot, Operation: execution.OperationNormal, AttemptNo: 1, DeadlineUnixMilli: 1_788_000_030_000,
	}
}

func evaluationRequest(
	input execution.InternalExecution,
	state execution.StatePreflightResult,
	gaps execution.GapLoadResult,
) execution.EvaluationRequest {
	binding := input.Inputs[0]
	header := execution.InternalExecutionHeader{
		ExecutionID: "execution-test", Contract: input.Contract, DuePlans: input.DuePlans,
		Requirements: input.Requirements, EffectiveTimeFacts: input.EffectiveTimeFacts,
		RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{
			Digest: binding.Provenance.PhysicalQuery, QueryRevision: input.Contract.QueryRevision,
		}}, DeadlineUnixMilli: 1_788_000_030_000,
	}
	return execution.EvaluationRequest{
		Header: header,
		Batch: execution.SeriesExecutionBatch{
			PhysicalQuery: binding.Provenance.PhysicalQuery, QueryRevision: input.Contract.QueryRevision,
			CompletionRef: binding.ProviderResult, Dataset: binding.Dataset, Inputs: input.Inputs,
			Delivery: execution.SeriesDelivery{
				PhysicalQuery: binding.Provenance.PhysicalQuery, QueryRevision: input.Contract.QueryRevision,
				Series: 1, Records: uint64(binding.Dataset.Len()), Digest: "delivery-test",
			},
		},
		State: state, Gaps: gaps,
	}
}

func validInternalExecution() execution.InternalExecution {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	identity := execution.StateKeyIdentity{
		Plan: plan, StateGeneration: "state-v1", SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("c", 64)),
	}
	applyVersion := frozenApplyVersion()
	plans, requirements := baseDuePlanAndRequirements()
	due := plans[0]
	compiled := due.CompiledPlan
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
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
			Disposition: execution.AccessAvailable,
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

func normalLevelOutcome() execution.LevelOutcome {
	return execution.LevelOutcome{
		Plan:    execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
		LevelID: 5, SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("c", 64)),
		Record:  execution.RecordAnchor{RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000},
		Outcome: execution.LevelOutcomeNormal,
	}
}

func normalStateEvaluation() execution.StateEvaluation {
	refs, err := execution.DeriveRuntimeLevelContractRefs(compiledPlanForTest(nil))
	if err != nil {
		panic(err)
	}
	mutation, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: execution.StateKeyIdentity{
			Plan:                 execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
			StateGeneration:      "state-v1",
			SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("c", 64)),
		},
		ApplyVersion: frozenApplyVersion(),
		AffectedRecords: []execution.RecordAnchor{{
			RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000,
		}},
		Levels: []execution.RuntimeLevelStateMutation{{
			LevelID: 5, LevelStateCompatibility: refs[0].LevelStateCompatibility, HistoryCompleteness: execution.HistoryFull,
			WarmupRequirementRef: refs[0].WarmupRequirementRef, LastProcessedEventTime: 1_788_000_000,
		}},
		Points: []execution.StateHistoryPoint{{
			RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000,
			Levels: []execution.StateLevelFact{{
				LevelID: 5, DetectFingerprint: refs[0].DetectFingerprint, Result: execution.LevelFactNormal,
			}},
		}},
	})
	if err != nil {
		panic(err)
	}
	return execution.StateEvaluation{Mutation: mutation}
}

func loadedSeriesWarmingCompletion(
	t testing.TB,
	outcomeKind execution.LevelOutcomeKind,
) (execution.EvaluationResult, execution.EvaluationRequest) {
	t.Helper()
	input := validInternalExecution()
	state := normalStateEvaluation()
	state.Mutation.ExpectedBlobRevision = 1
	state.Mutation.MutationDigest = ""
	state.Mutation = mustStateMutation(state.Mutation)
	refs, err := execution.DeriveRuntimeLevelContractRefs(input.DuePlans[0].CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	loadedPoint := execution.StateHistoryPoint{
		RecordID: strings.Repeat("d", 64), SourceTime: 1_787_999_940,
		Levels: []execution.StateLevelFact{{
			LevelID: 5, DetectFingerprint: refs[0].DetectFingerprint, Result: execution.LevelFactNormal,
		}},
	}
	request := evaluationRequest(input, execution.StatePreflightResult{Items: []execution.RuntimeStateView{{
		Identity: input.StatePreflight[0].Identity, BlobRevision: 1,
		PersistedApplyVersion:   olderApplyVersion(input.StatePreflight[0].ApplyVersion),
		PersistedMutationDigest: "loaded-state", Status: execution.StateFoundWarming,
		Levels: []execution.RuntimeLevelStateView{{
			LevelID: 5, LevelStateCompatibility: refs[0].LevelStateCompatibility,
			HistoryCompleteness:  execution.HistoryWarming,
			GapReasonCode:        execution.ReasonCode(contract.ReasonHistoryWarming),
			WarmupRequirementRef: refs[0].WarmupRequirementRef,
		}},
		History: []execution.StateHistoryPoint{loadedPoint},
	}}}, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: input.GapPreflight[0].Identity, Status: execution.GapMissing,
	}}})
	outcome := normalLevelOutcome()
	outcome.Outcome = outcomeKind
	if outcomeKind == execution.LevelOutcomeAbnormal {
		mutation := state.Mutation
		mutation.Points = append([]execution.StateHistoryPoint(nil), mutation.Points...)
		current := len(mutation.Points) - 1
		mutation.Points[current].Levels = append([]execution.StateLevelFact(nil), mutation.Points[current].Levels...)
		mutation.Points[current].Levels[0].Result = execution.LevelFactAnomalous
		mutation.MutationDigest = ""
		state.Mutation = mustStateMutation(mutation)
	}
	if outcomeKind == execution.LevelOutcomeAbnormal || outcomeKind == execution.LevelOutcomeRecovery {
		state.Events = []contract.TriggerEventV1{triggerEventForOutcome(input, outcomeKind)}
	}
	return execution.EvaluationResult{
		Contract: input.Contract, Result: observability.ResultSuccess,
		Plans: []execution.PlanEvaluationResult{{
			Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecided,
			LevelOutcomes: []execution.LevelOutcome{outcome}, StateResults: []execution.StateEvaluation{state},
		}},
	}, request
}

func triggerEventForOutcome(input execution.InternalExecution, outcomeKind execution.LevelOutcomeKind) contract.TriggerEventV1 {
	compiled := input.DuePlans[0].CompiledPlan
	fingerprints := compiled.Fingerprints()
	eventKind := contract.TriggerEventAbnormal
	levelResult := contract.LevelResultAbnormal
	observedAnomalies := uint32(1)
	observedMisses := uint32(0)
	if outcomeKind == execution.LevelOutcomeRecovery {
		eventKind = contract.TriggerEventRecovery
		levelResult = contract.LevelResultRecovery
		observedAnomalies = 0
		observedMisses = 1
	}
	event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{
		EventKind: eventKind, TenantID: "tenant", BusinessID: "2",
		PlanRef: compiled.PlanRef(),
		RecordRef: contract.TriggerRecordRefV1{
			RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000,
			DimensionIdentityDigest: strings.Repeat("c", 64), Dimensions: map[string]json.RawMessage{},
		},
		Observed: contract.TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}},
		LevelResults: []contract.LevelResultV1{{
			LevelID: 5, Priority: 1, Result: levelResult,
			LevelTriggerFingerprint: strings.Repeat("e", 64),
			DecisionWindow: contract.DecisionWindowV1{
				Type: "N_OF_M_WITH_CONTINUOUS_MISS", Version: 1, SourceTime: 1_788_000_000,
				Trigger: contract.TriggerWindowEvidenceV1{
					WindowStart: 1_788_000_000, WindowEnd: 1_788_000_000, WindowSize: 1,
					RequiredAnomalies: 1, ObservedAnomalies: observedAnomalies,
				},
				Recovery: contract.RecoveryWindowEvidenceV1{
					Enabled: true, RequiredConsecutiveWindows: 1, ObservedConsecutiveMisses: observedMisses,
					OldestWindowStart: 1_788_000_000,
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
		TriggerStateFingerprint: fingerprints.Trigger, ExecutionID: "execution-test", MaxEvidenceBytes: 4096,
	})
	if err != nil {
		panic(err)
	}
	return *event
}

func mustStateMutation(mutation execution.StateMutation) execution.StateMutation {
	built, err := execution.BuildStateMutation(mutation)
	if err != nil {
		panic(err)
	}
	return built
}

func mustSeriesWarmupRequirementRef(plan *strategy.CompiledPlan) string {
	ref, err := execution.DeriveRuntimeSeriesWarmupRequirementRef(plan)
	if err != nil {
		panic(err)
	}
	return ref
}

func olderApplyVersion(version execution.ApplyVersion) execution.ApplyVersion {
	version.EvaluationTime--
	return version
}

func mustPlanGapMutation(mutation execution.PlanGapMutation) execution.PlanGapMutation {
	built, err := execution.BuildPlanGapMutation(mutation)
	if err != nil {
		panic(err)
	}
	return built
}

func baseDuePlanAndRequirements() ([]execution.DuePlan, []execution.DataRequirement) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
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

func compiledPlanForTest(t testing.TB) *strategy.CompiledPlan {
	return compiledPlanWithTriggerConfig(t, json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`))
}

func compiledPlanWithTriggerConfig(t testing.TB, triggerConfig json.RawMessage) *strategy.CompiledPlan {
	if t != nil {
		t.Helper()
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "execution-test-v1",
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
				TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: triggerConfig},
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
	if len(compiled.Levels()) == 0 {
		panic(fmt.Sprintf("test plan has no compiled Levels: %+v", result.LevelTerminals()))
	}
	return compiled
}

func validStateMutation() execution.StateMutation {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	return execution.StateMutation{
		Identity:             execution.StateKeyIdentity{Plan: plan, StateGeneration: "state-v1", SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("c", 64))},
		ExpectedBlobRevision: 1,
		ApplyVersion:         frozenApplyVersion(),
		MutationDigest:       "mutation-digest",
		Points:               []execution.StateHistoryPoint{{RecordID: "record", SourceTime: 1_788_000_000}},
	}
}

func frozenApplyVersion() execution.ApplyVersion {
	version, err := execution.BuildApplyVersion(frozenContract(), 1)
	if err != nil {
		panic(err)
	}
	return version
}
