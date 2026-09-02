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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestSlotExecutionCoordinatorCompletesFullEmptyWithoutBusinessSideEffects(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, nil)

	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Result != observability.ResultSuccess {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertFullEmptyProgress(t, fixture, request, len(plans))
}

func TestSlotExecutionCoordinatorCompletesFullEmptySharedQueryForEveryPlan(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	second := plans[0]
	second.Identity.BusinessID = "3"
	plans = append(plans, second)
	requirements[0].Consumers = append(requirements[0].Consumers, execution.DataRequirementConsumer{
		Consumer:                           execution.ConsumerRef{Plan: second.Identity},
		ConsumerDeadlineUnixMilli:          second.CompletionDeadlineUnixMilli,
		DownstreamExecutionReserveMilliSec: 5_000,
	})
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, nil)

	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Result != observability.ResultSuccess {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertFullEmptyProgress(t, fixture, request, len(plans))
}

func TestSlotExecutionCoordinatorRejectsIncompleteFullEmptyCompletion(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	second := plans[0]
	second.Identity.BusinessID = "3"
	plans = append(plans, second)
	requirements[0].Consumers = append(requirements[0].Consumers, execution.DataRequirementConsumer{
		Consumer:                           execution.ConsumerRef{Plan: second.Identity},
		ConsumerDeadlineUnixMilli:          second.CompletionDeadlineUnixMilli,
		DownstreamExecutionReserveMilliSec: 5_000,
	})

	tests := []struct {
		name   string
		mutate func(*execution.QueryExecutionCompletion)
	}{
		{
			name: "required physical query",
			mutate: func(completion *execution.QueryExecutionCompletion) {
				completion.PhysicalQueries = nil
			},
		},
		{
			name: "due plan binding",
			mutate: func(completion *execution.QueryExecutionCompletion) {
				completion.CompletionBindings = completion.CompletionBindings[:1]
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, request := newCompletionOnlyFixture(t, plans, requirements, test.mutate)
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err == nil || result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			assertNoFullEmptyBusinessSideEffects(t, fixture)
			if fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
				t.Fatalf("incomplete completion advanced Progress: %+v", fixture.ports.lastProgress)
			}
		})
	}
}

func TestSlotExecutionCoordinatorRejectsNonFullEmptyCompletionOnlyInput(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	tests := []struct {
		name   string
		mutate func(*execution.QueryExecutionCompletion)
	}{
		{
			name: "physical query is not FULL",
			mutate: func(completion *execution.QueryExecutionCompletion) {
				completion.PhysicalQueries[0].Completeness = execution.CompletenessPartial
			},
		},
		{
			name: "ProviderResult does not match physical completion",
			mutate: func(completion *execution.QueryExecutionCompletion) {
				completion.CompletionBindings[0].ProviderResult = "another-provider-result"
			},
		},
		{
			name: "binding provenance does not match physical completion",
			mutate: func(completion *execution.QueryExecutionCompletion) {
				completion.CompletionBindings[0].Provenance.PhysicalQuery = "another-physical-query"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, request := newCompletionOnlyFixture(t, plans, requirements, test.mutate)
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err == nil || result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			assertNoFullEmptyBusinessSideEffects(t, fixture)
			if fixture.ports.lastProgress != (execution.ProgressCommitRequest{}) {
				t.Fatalf("non-FULL+EMPTY input advanced Progress: %+v", fixture.ports.lastProgress)
			}
		})
	}
}

func TestSlotExecutionCoordinatorCompletesUnavailableAfterPlanGap(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	second := plans[0]
	second.Identity.BusinessID = "3"
	plans = append(plans, second)
	requirements[0].Consumers = append(requirements[0].Consumers, execution.DataRequirementConsumer{
		Consumer:                           execution.ConsumerRef{Plan: second.Identity},
		ConsumerDeadlineUnixMilli:          second.CompletionDeadlineUnixMilli,
		DownstreamExecutionReserveMilliSec: 5_000,
	})
	reason := execution.ReasonCode(contract.ReasonQueryUnavailable)
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, func(completion *execution.QueryExecutionCompletion) {
		completion.PhysicalQueries[0].Completeness = execution.CompletenessUnavailable
		completion.PhysicalQueries[0].DataState = execution.DataStateUnknown
		for index := range completion.CompletionBindings {
			binding := &completion.CompletionBindings[index]
			binding.Dataset, binding.View = nil, nil
			binding.Completeness = execution.CompletenessUnavailable
			binding.DataState = execution.DataStateUnknown
			binding.Disposition = execution.AccessUnavailable
			binding.ReasonCode = reason
		}
	})

	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Result != observability.ResultDegraded || result.ReasonCode != reason {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertCompletionGapBeforeProgress(t, fixture, execution.CompletenessUnavailable, execution.CompletionUnavailable, reason, len(plans))
}

func TestSlotExecutionCoordinatorCompletesPartialEmptyAfterPlanGap(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	reason := execution.ReasonCode(contract.ReasonQueryPartial)
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, func(completion *execution.QueryExecutionCompletion) {
		completion.PhysicalQueries[0].Completeness = execution.CompletenessPartial
		for index := range completion.CompletionBindings {
			binding := &completion.CompletionBindings[index]
			binding.Completeness = execution.CompletenessPartial
			binding.Disposition = execution.AccessDegraded
			binding.ReasonCode = reason
		}
	})

	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Result != observability.ResultDegraded || result.ReasonCode != reason {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertCompletionGapBeforeProgress(t, fixture, execution.CompletenessPartial, execution.CompletionPartialGap, reason, len(plans))
}

func assertCompletionGapBeforeProgress(
	t *testing.T,
	fixture fixture,
	completeness execution.Completeness,
	kind execution.CompletionKind,
	reason execution.ReasonCode,
	wantGaps int,
) {
	t.Helper()
	if fixture.ports.stateLoadCalls != 0 || fixture.ports.stateAdmissionCalls != 0 ||
		fixture.ports.stateApplyCalls != 0 || fixture.ports.eventCount != 0 {
		t.Fatalf("completion-only gap produced Event/State: state_load=%d state_admit=%d state_apply=%d events=%d",
			fixture.ports.stateLoadCalls, fixture.ports.stateAdmissionCalls, fixture.ports.stateApplyCalls, fixture.ports.eventCount)
	}
	if len(fixture.ports.gapMutations) != wantGaps {
		t.Fatalf("gap mutations=%+v", fixture.ports.gapMutations)
	}
	for _, mutation := range fixture.ports.gapMutations {
		if len(mutation.Scopes) != 1 || mutation.Scopes[0].ReasonCode != reason ||
			mutation.Scopes[0].RequiredFullSlots == 0 {
			t.Fatalf("gap mutation=%+v", mutation)
		}
	}
	progress := fixture.ports.lastProgress
	if progress.Completion.Kind != kind || progress.Completion.Primary == nil ||
		progress.Completion.Primary.Completeness != completeness || progress.Completion.Result != observability.ResultDegraded ||
		progress.Completion.ReasonCode != reason {
		t.Fatalf("Progress=%+v", progress)
	}
	gapIndex, progressIndex := -1, -1
	for index, stage := range *fixture.trace {
		switch stage {
		case "gap_before":
			gapIndex = index
		case "progress_commit":
			progressIndex = index
		}
	}
	if gapIndex < 0 || progressIndex < 0 || gapIndex >= progressIndex {
		t.Fatalf("Guard did not precede Progress: %v", *fixture.trace)
	}
}

type completionOnlyQuerySource struct {
	trace      *[]string
	header     execution.InternalExecutionHeader
	completion execution.QueryExecutionCompletion
}

func (source completionOnlyQuerySource) Execute(
	ctx context.Context,
	_ execution.QueryExecutionRequest,
	consumer execution.QueryExecutionConsumer,
) (execution.QueryExecutionCompletion, error) {
	*source.trace = append(*source.trace, "query")
	if err := consumer.Begin(ctx, source.header); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	return source.completion, nil
}

func newCompletionOnlyFixture(
	t *testing.T,
	plans []execution.DuePlan,
	requirements []execution.DataRequirement,
	mutate func(*execution.QueryExecutionCompletion),
) (fixture, execution.SlotExecutionRequest) {
	t.Helper()
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		t.Fatalf("DeriveDuePlanSetDigest() error: %v", err)
	}
	contractRef := frozenContract()
	contractRef.DuePlanSetDigest = digest
	header := execution.InternalExecutionHeader{
		ExecutionID: "empty-execution-1", Contract: contractRef, DuePlans: plans, Requirements: requirements,
		RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{
			Digest: "physical-query-1", QueryRevision: contractRef.QueryRevision,
		}},
		DeadlineUnixMilli: 1_788_000_030_000,
	}
	empty := execution.NewDataset(nil)
	bindings := make([]execution.NamedInputBinding, 0)
	for _, requirement := range requirements {
		for _, consumer := range requirement.Consumers {
			view, viewErr := execution.NewDatasetView(empty, nil)
			if viewErr != nil {
				t.Fatalf("NewDatasetView() error: %v", viewErr)
			}
			bindings = append(bindings, execution.NamedInputBinding{
				Consumer: consumer.Consumer, RequirementID: requirement.RequirementID,
				DatasetName: requirement.DatasetName, Role: requirement.Role,
				ProviderResult: "provider-result-1",
				QueryWindow:    requirement.AbsoluteWindow(contractRef.Slot.EvaluationTime),
				Dataset:        empty, View: view,
				Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
				Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactPlan,
				Provenance: execution.InputProvenance{PhysicalQuery: "physical-query-1", AttemptNo: 1},
			})
		}
	}
	completion := execution.QueryExecutionCompletion{
		AllRequiredCompleted: true,
		PhysicalQueries: []execution.PhysicalQueryCompletion{{
			Ref: "provider-result-1", PhysicalQuery: "physical-query-1", QueryRevision: contractRef.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
		}},
		CompletionBindings: bindings,
	}
	if mutate != nil {
		mutate(&completion)
	}

	observations := make([]observability.Observation, 0)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observability.NormalizeObservation(observation))
	})
	trace := make([]string, 0)
	ports := &recordingPorts{trace: &trace, ready: true}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: ports, Activation: ports,
		Query:     completionOnlyQuerySource{trace: &trace, header: header, completion: completion},
		Sequencer: ports, Evaluator: ports, Admission: ports, GapGuard: ports,
		Events: ports, State: ports, Progress: ports, Observer: observer,
	}, worker.ProvisionalBudget{
		MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10,
	})
	if err != nil {
		t.Fatalf("NewSlotExecutionCoordinator() error: %v", err)
	}
	request := slotRequest(execution.OperationNormal)
	request.Contract = contractRef
	request.ExpectedNextSlot = contractRef.Slot.EvaluationTime
	return fixture{trace: &trace, observations: &observations, ports: ports, coordinator: coordinator}, request
}

func assertFullEmptyProgress(t *testing.T, fixture fixture, request execution.SlotExecutionRequest, plans int) {
	t.Helper()
	assertNoFullEmptyBusinessSideEffects(t, fixture)
	if fixture.ports.admissionCalls != plans*2 {
		t.Fatalf("admission calls=%d, want every due Plan admitted before completion and Progress", fixture.ports.admissionCalls)
	}
	progress := fixture.ports.lastProgress
	if progress.ExpectedNextSlot != request.ExpectedNextSlot ||
		progress.Completion.Kind != execution.CompletionFullEmpty ||
		progress.Completion.Result != observability.ResultSuccess ||
		progress.Completion.Primary == nil ||
		progress.Completion.Primary.Completeness != execution.CompletenessFull ||
		progress.Completion.Primary.DataState != execution.DataStateEmpty {
		t.Fatalf("FULL+EMPTY Progress=%+v", progress)
	}
}

func assertNoFullEmptyBusinessSideEffects(t *testing.T, fixture fixture) {
	t.Helper()
	if fixture.ports.stateLoadCalls != 0 || fixture.ports.stateAdmissionCalls != 0 ||
		fixture.ports.stateApplyCalls != 0 || fixture.ports.eventCount != 0 || len(fixture.ports.gapMutations) != 0 {
		t.Fatalf("FULL+EMPTY produced business side effects: state_load=%d state_admit=%d state_apply=%d events=%d gaps=%d",
			fixture.ports.stateLoadCalls, fixture.ports.stateAdmissionCalls, fixture.ports.stateApplyCalls,
			fixture.ports.eventCount, len(fixture.ports.gapMutations))
	}
	for _, stage := range *fixture.trace {
		if stage == "evaluate" {
			t.Fatal("FULL+EMPTY called Evaluator")
		}
	}
}
