// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evaluation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

// CheckASupplementLeavesARaisedNoDataAlertStanding is test-only access for
// the case that brings a real store: the item goes without data round after
// round until its no-data alert is raised, through the worker's own no-data
// rounds against the store, and then a series of the item arrives late for
// the Slot the alert was raised at. The supplement does not take it: the
// store records the item absent at that Slot, and the alert is left to
// recover as it would rather than be written over.
func CheckASupplementLeavesARaisedNoDataAlertStanding(t *testing.T, store *state.ExecutionStore) {
	t.Helper()
	ctx := context.Background()
	due := noDataWiredPlan(t)
	detector, err := detect.NewEvaluator(detect.NewDefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := evaluation.New(detector, evaluation.Limits{MaxPlans: 4, MaxRecords: 16, MaxLevels: 16,
		Trigger: trigger.EvaluationLimitsV2{MaxLevels: 16, MaxTriggerWindowSize: 16,
			MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32,
			MaxLevelResultsPerEvent: 16, MaxEvidenceBytesPerEvent: 1 << 20, MaxComputeCost: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := execution.PlanViewFor(due, execution.SeriesKindNoData)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := execution.DeriveStateRetentionRequirement(view.CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	wire := func(stream *streamedExecution) {
		stream.coordinator.budget.MaxStateMutations, stream.coordinator.budget.MaxEvents = 100, 100
		stream.coordinator.ports.State, stream.coordinator.ports.Evaluator = store, evaluator
		stream.coordinator.ports.Observer = observability.ObserverFunc(func(context.Context, observability.Observation) {})
	}
	var raisedAt execution.EvaluationTime
	for index := 0; index < 5 && raisedAt == 0; index++ {
		stream := noDataWiredStream(t, due, store)
		wire(stream)
		stream.header.Contract.Slot.EvaluationTime += execution.EvaluationTime(index * 60)
		stream.header.ExecutionID = "supplement-no-data"
		stream.request = execution.SlotExecutionRequest{Contract: stream.header.Contract, Operation: execution.OperationNormal}
		stream.effective = mustPrepareAlwaysEffectiveTimeFacts(t, stream.header)
		stream.gaps = execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{Identity: due.GapIdentity(), Status: execution.GapMissing}}}
		stream.bindings = []execution.NamedInputBinding{{Consumer: execution.ConsumerRef{Plan: due.Identity}, Completeness: execution.CompletenessFull}}
		if err := stream.loadNoDataMemory(ctx); err != nil {
			t.Fatal(err)
		}
		if err := stream.evaluateNoData(ctx, nil, 4); err != nil {
			t.Fatalf("round %d: %v", index+1, err)
		}
		var mutations []execution.StateMutation
		for _, plan := range stream.evaluated.Plans {
			for _, result := range plan.StateResults {
				mutations = append(mutations, result.Mutation)
				for _, event := range result.Events {
					if event.EventKind == contract.TriggerEventAbnormal {
						raisedAt = stream.header.Contract.Slot.EvaluationTime
					}
				}
			}
		}
		if len(mutations) > 0 {
			if _, err := store.ApplyRuntime(ctx, execution.StateApplyRequest{Contract: stream.header.Contract, Items: mutations, Retention: retention}); err != nil {
				t.Fatal(err)
			}
		}
		if err := stream.coordinator.applyNoDataMemory(ctx, stream.request, stream.header.DuePlans, stream.noDataMutations); err != nil {
			t.Fatal(err)
		}
		stream.releaseProvisional()
	}
	if raisedAt == 0 {
		t.Fatal("the item's no-data alert was never raised")
	}

	stream := noDataWiredStream(t, due, store)
	wire(stream)
	stream.header.Contract.Slot.EvaluationTime = raisedAt
	stream.request = execution.SlotExecutionRequest{Contract: stream.header.Contract, Operation: execution.OperationSupplement}
	late := supplementSeries(t, due, int64(raisedAt), map[string]string{"bk_target_ip": "192.0.2.10", "bk_target_cloud_id": "0"})
	stream.supplement = newSupplementRun(execution.SupplementScope{Series: []execution.SeriesIdentityDigest{late.identity}})
	if err := stream.loadNoDataMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if stream.supplementTakes(late) {
		t.Fatalf("a late series was taken at the Slot its item's no-data alert was raised at; memory %+v", stream.noData.Items)
	}
	if want := (execution.SupplementFacts{Candidates: 1, NoDataFact: 1}); stream.supplement.facts != want {
		t.Fatalf("facts %+v, want %+v", stream.supplement.facts, want)
	}
}
