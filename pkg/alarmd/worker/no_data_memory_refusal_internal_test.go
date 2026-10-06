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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// refusingNoDataStore answers every write with one deterministic refusal.
type refusingNoDataStore struct {
	reason execution.ReasonCode
	size   *execution.NoDataRecordSize
}

func (store *refusingNoDataStore) LoadNoData(
	_ context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	result := execution.NoDataLoadResult{Items: make([]execution.NoDataMemorySnapshot, len(request.Items))}
	for index, item := range request.Items {
		result.Items[index] = execution.NoDataMemorySnapshot{
			// NONE rather than an unset field: the store stamps it, and a
			// double that leaves it empty is standing in for a snapshot the
			// store cannot produce.
			Identity: item.Identity, Status: execution.NoDataMemoryMissing,
			Representation: execution.NoDataRepresentationNone,
		}
	}
	return result, nil
}

func (store *refusingNoDataStore) ApplyNoData(
	_ context.Context, request execution.NoDataApplyRequest,
) (execution.NoDataApplyResult, error) {
	result := execution.NoDataApplyResult{Items: make([]execution.NoDataApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		result.Items[index] = execution.NoDataApplyItemResult{
			Identity: mutation.Identity, Status: execution.NoDataRejected,
			ReasonCode: store.reason, Size: store.size,
		}
	}
	return result, nil
}

func noDataRefusalFixture(store *refusingNoDataStore) (*SlotExecutionCoordinator, *[]observability.Observation) {
	observed := make([]observability.Observation, 0, 2)
	coordinator := &SlotExecutionCoordinator{ports: Ports{
		NoData: store, Hosts: SharedHostBusiness,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observed = append(observed, observability.NormalizeObservation(observation))
		}),
	}}
	return coordinator, &observed
}

func refusedMemoryMutation(t *testing.T) execution.PlanNoDataMutation {
	t.Helper()
	mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		DerivedFrom:            execution.NoDataRepresentationPerGroup,
		LoadedApplyVersion:     execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 60, SlotDigest: "slot"},
		ExpectedMarkerRevision: 1,
		Identity: execution.PlanNoDataIdentity{
			Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "10", StrategyID: "8946"},
			StateGeneration: "generation",
		},
		ApplyVersion:     execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 60, SlotDigest: "slot"},
		ScheduleRevision: "plan-r1", RosterVersion: "HISTORY/1", PresentAsOf: 1000,
		Memory: []execution.NoDataGroupMemory{{GroupKey: "a", FirstAbsent: 940}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

// A refusal the store will give again is reported and does not fail the Slot.
//
// It used to. The Slot returned the refusal as its error, the scheduler had no
// classifier for it, and the page got a failed Slot with reason_code
// internal_unknown on operation retry -- once a minute, for as long as the
// condition lasted, for every Plan of the Query Group. What the retry could not
// do is make the write succeed: a deterministic refusal is deterministic. So
// the round's threshold results, which were already computed and already sent,
// were thrown away and computed again to be thrown away again.
func TestARefusedNoDataMemoryIsReportedRatherThanFailingTheSlot(t *testing.T) {
	store := &refusingNoDataStore{
		reason: execution.ReasonCode(contract.ReasonStateBudgetExceeded),
		size: &execution.NoDataRecordSize{
			Record: execution.NoDataRecordGroups, Groups: 120000, Limit: 100000,
		},
	}
	coordinator, observed := noDataRefusalFixture(store)

	err := coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{
		Operation: execution.OperationNormal,
	}, []execution.PlanNoDataMutation{refusedMemoryMutation(t)})
	if err != nil {
		t.Fatalf("a deterministic refusal failed the Slot: %v", err)
	}

	var refusals []observability.Observation
	for _, observation := range *observed {
		if observation.Stage == observability.StageNoDataMemoryRefused {
			refusals = append(refusals, observation)
		}
	}
	if len(refusals) != 1 {
		t.Fatalf("observed %d refusal lines, want one; observations=%+v", len(refusals), *observed)
	}
	facts := refusals[0].NoDataMemoryRefusal
	if facts == nil {
		t.Fatal("the refusal line carried no facts; the failed Slot it replaces at least named itself")
	}
	// The numbers, not just the name. A reason code on its own does not say
	// whether one object is a little over the bound or many times it, and the
	// two are different situations with different remedies.
	want := observability.NoDataMemoryRefusalFacts{
		Reason: contract.ReasonStateBudgetExceeded, Record: string(execution.NoDataRecordGroups),
		Groups: 120000, Limit: 100000,
	}
	if *facts != want {
		t.Fatalf("refusal facts = %+v, want %+v", *facts, want)
	}
	if refusals[0].Trace.StrategyID != "8946" {
		t.Fatalf("refusal strategy = %q, want the Plan whose memory was refused", refusals[0].Trace.StrategyID)
	}
	if refusals[0].ReasonCode != observability.ReasonCode(contract.ReasonStateBudgetExceeded) {
		t.Fatalf("refusal reason_code = %q, want the store's own code rather than internal_unknown",
			refusals[0].ReasonCode)
	}
}

// A refusal that is not about size reports its reason and no numbers.
//
// Writing zeroes instead would be worse than writing nothing: a reader would
// see a record of zero bytes against a bound of zero and have to know which
// reasons carry a measurement to discount it.
func TestANoDataRefusalWithoutASizeReportsNoNumbers(t *testing.T) {
	store := &refusingNoDataStore{reason: execution.ReasonCode(contract.ReasonStateCorrupt)}
	coordinator, observed := noDataRefusalFixture(store)

	if err := coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{
		Operation: execution.OperationNormal,
	}, []execution.PlanNoDataMutation{refusedMemoryMutation(t)}); err != nil {
		t.Fatalf("a deterministic refusal failed the Slot: %v", err)
	}

	for _, observation := range *observed {
		if observation.Stage != observability.StageNoDataMemoryRefused {
			continue
		}
		want := observability.NoDataMemoryRefusalFacts{Reason: contract.ReasonStateCorrupt}
		if observation.NoDataMemoryRefusal == nil || *observation.NoDataMemoryRefusal != want {
			t.Fatalf("refusal facts = %+v, want the reason and no measurement", observation.NoDataMemoryRefusal)
		}
		return
	}
	t.Fatal("a refusal that is not about size was not reported at all")
}

// A store that could not be reached still fails the Slot.
//
// This is the boundary the change has to keep. A refusal is an answer: the
// store looked and said no, and saying it again will not change that. A store
// that did not answer is not an answer, and containing it would turn the state
// backend being down into a per-Plan line nobody reads while the Slot reports
// success.
func TestANoDataStoreThatDidNotAnswerStillFailsTheSlot(t *testing.T) {
	coordinator := &SlotExecutionCoordinator{ports: Ports{
		NoData: &unreachableNoDataStore{}, Hosts: SharedHostBusiness,
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
	}}
	err := coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{
		Operation: execution.OperationNormal,
	}, []execution.PlanNoDataMutation{refusedMemoryMutation(t)})
	if err == nil {
		t.Fatal("a store that did not answer was contained as if it had refused")
	}
}

type unreachableNoDataStore struct{}

func (*unreachableNoDataStore) LoadNoData(
	context.Context, execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	return execution.NoDataLoadResult{}, context.DeadlineExceeded
}

func (*unreachableNoDataStore) ApplyNoData(
	context.Context, execution.NoDataApplyRequest,
) (execution.NoDataApplyResult, error) {
	return execution.NoDataApplyResult{}, context.DeadlineExceeded
}
