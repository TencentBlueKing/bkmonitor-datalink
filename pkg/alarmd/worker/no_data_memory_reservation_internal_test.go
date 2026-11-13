// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// noDataGroupKeyLength is the length of the group keys below, a digest's.
const noDataGroupKeyLength = 64

// wholeMemory is a first write of the fixture Plan's memory: derived from no
// record, every one of groups groups in it.
func wholeMemory(t *testing.T, groups int) execution.PlanNoDataMutation {
	t.Helper()
	memory := make([]execution.NoDataGroupMemory, groups)
	for index := range memory {
		memory[index] = execution.NoDataGroupMemory{GroupKey: fmt.Sprintf("%064d", index), LastSeen: 1000}
	}
	mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationNone,
		Identity: execution.PlanNoDataIdentity{
			Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "10", StrategyID: "856"},
			StateGeneration: "generation",
		},
		ApplyVersion:     execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 60, SlotDigest: "slot"},
		ScheduleRevision: "plan-r1", RosterVersion: "HISTORY/1", PresentAsOf: 1000, Memory: memory,
		// The item's own bound, so the store takes as many groups as asked.
		GroupLimit: uint32(groups),
	})
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

// pooledNoDataStore applies every write and says what the pool held while it
// was asked to.
type pooledNoDataStore struct {
	coordinator *SlotExecutionCoordinator
	writes      int
	heldAtWrite uint64
}

func (store *pooledNoDataStore) LoadNoData(context.Context, execution.NoDataLoadRequest) (execution.NoDataLoadResult, error) {
	return execution.NoDataLoadResult{}, nil
}

func (store *pooledNoDataStore) ApplyNoData(_ context.Context, request execution.NoDataApplyRequest) (execution.NoDataApplyResult, error) {
	store.writes += len(request.Items)
	store.heldAtWrite = RetainedReserved(store.coordinator)
	result := execution.NoDataApplyResult{Items: make([]execution.NoDataApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		result.Items[index] = execution.NoDataApplyItemResult{Identity: mutation.Identity, Status: execution.NoDataApplied}
	}
	return result, nil
}

// pooledNoDataFixture is a coordinator whose retained pool is pool bytes.
func pooledNoDataFixture(pool uint64) (*SlotExecutionCoordinator, *pooledNoDataStore, *[]observability.Observation) {
	observed := make([]observability.Observation, 0, 2)
	coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: 1 << 20, MaxRetainedBytes: pool}}
	store := &pooledNoDataStore{coordinator: coordinator}
	coordinator.ports = Ports{NoData: store, Hosts: SharedHostBusiness,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observed = append(observed, observability.NormalizeObservation(observation))
		})}
	return coordinator, store, &observed
}

// A first write holds, while it is written, at least what its groups' keys
// take twice - in the statement and in the stored field names - and gives it
// all back once written.
func TestAFirstNoDataWriteIsHeldAgainstThePoolWhileItIsWritten(t *testing.T) {
	coordinator, store, _ := pooledNoDataFixture(64 << 20)
	const groups = 1000
	err := coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{Operation: execution.OperationNormal},
		refusedMemoryDue(t), []execution.PlanNoDataMutation{wholeMemory(t, groups)})
	if err != nil || store.writes != 1 {
		t.Fatalf("the first write ended %v after %d writes; want it written", err, store.writes)
	}
	if floor := uint64(groups * 2 * noDataGroupKeyLength); store.heldAtWrite < floor {
		t.Fatalf("the pool held %d while %d groups were written, want at least their keys twice, %d", store.heldAtWrite, groups, floor)
	}
	if held := RetainedReserved(coordinator); held != 0 {
		t.Fatalf("the pool still holds %d after the write, want it all given back", held)
	}
}

// B3 since decision-020 batch 8: a first write whose whole memory is past
// the share is no longer refused. The store writes it a segment at a time, so
// the pool holds one segment while it is written - at least that segment's
// keys twice, and less than the whole memory's - the store is asked once,
// nothing is refused, and all of it is given back.
//
// The pool is scaled down from a 4 GiB worker's with the same share of it,
// so that a memory past the share is a few tens of thousands of groups; the
// memory is sized from the share so its keys alone are past it.
func TestAFirstNoDataWritePastTheShareIsWrittenASegmentAtATime(t *testing.T) {
	coordinator, store, observed := pooledNoDataFixture(4 << 20)
	share := coordinator.qgShareBytes()
	groups := int(share/(2*noDataGroupKeyLength)) + execution.NoDataFirstWriteSegmentGroups
	err := coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{Operation: execution.OperationNormal},
		refusedMemoryDue(t), []execution.PlanNoDataMutation{wholeMemory(t, groups)})
	if err != nil || store.writes != 1 {
		t.Fatalf("a memory past the share ended %v after %d writes; want it written", err, store.writes)
	}
	segmentKeys := uint64(execution.NoDataFirstWriteSegmentGroups * 2 * noDataGroupKeyLength)
	if store.heldAtWrite < segmentKeys || store.heldAtWrite >= uint64(groups*2*noDataGroupKeyLength) {
		t.Fatalf("the pool held %d while %d groups were written; want at least a segment's keys twice, %d, "+
			"and less than the whole memory's keys twice, %d", store.heldAtWrite, groups, segmentKeys, groups*2*noDataGroupKeyLength)
	}
	for _, observation := range *observed {
		if observation.Stage == observability.StageNoDataMemoryRefused {
			t.Fatalf("a memory past the share was refused: %+v", observation.NoDataMemoryRefusal)
		}
	}
	if held := RetainedReserved(coordinator); held != 0 {
		t.Fatalf("the pool holds %d after the write, want it all given back", held)
	}
}

// B3, the retried side: a first write within the share that the pool has no
// room for at the moment - another holder has it - is the Slot's
// RESOURCE_HARD_STOP, named as the pool's and not the share's, and the store
// is not asked; once the holder lets go, the same write goes through.
func TestAFirstNoDataWriteThePoolHasNoRoomForIsRetried(t *testing.T) {
	coordinator, store, observed := pooledNoDataFixture(4 << 20)
	mutation := wholeMemory(t, 2000)
	coordinator.reservations.retainedBytes = coordinator.budget.MaxRetainedBytes - 64<<10
	err := coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{Operation: execution.OperationNormal},
		refusedMemoryDue(t), []execution.PlanNoDataMutation{mutation})
	var exceeded *provisionalBudgetExceededError
	if !errors.As(err, &exceeded) || exceeded.share || store.writes != 0 {
		t.Fatalf("a write the pool has no room for ended %v after %d writes; want the pool's refusal and no write", err, store.writes)
	}
	hardStop := false
	for _, observation := range *observed {
		hardStop = hardStop || (observation.Stage == observability.StageResourceHard &&
			observation.ReasonCode == observability.ReasonCode(contract.ReasonResourceHardStop) &&
			observation.CapacityRejection != nil && observation.CapacityRejection.Phase == noDataReservationPhase)
		if observation.Stage == observability.StageNoDataMemoryRefused {
			t.Fatal("a write the pool will have room for was refused as if it never would")
		}
	}
	if !hardStop {
		t.Fatalf("no RESOURCE_HARD_STOP line for the no-data write; observed %+v", *observed)
	}
	coordinator.reservations.retainedBytes = 0
	if err := coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{Operation: execution.OperationNormal},
		refusedMemoryDue(t), []execution.PlanNoDataMutation{mutation}); err != nil || store.writes != 1 {
		t.Fatalf("with the holder gone the write ended %v after %d writes; want it written", err, store.writes)
	}
}

// loadedMemory is a found memory of groups groups for the fixture Plan.
func loadedMemory(strategyID string, groups int) execution.NoDataMemorySnapshot {
	snapshot := execution.NoDataMemorySnapshot{
		Identity: execution.PlanNoDataIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "10", StrategyID: strategyID},
			StateGeneration: "generation"},
		Status: execution.NoDataMemoryFound, Representation: execution.NoDataRepresentationPerGroup,
		Groups: make([]execution.NoDataGroupMemory, groups),
	}
	for index := range snapshot.Groups {
		snapshot.Groups[index] = execution.NoDataGroupMemory{GroupKey: fmt.Sprintf("%064d", index), LastSeen: 1000}
	}
	return snapshot
}

// A Slot holds the memories it loaded against the pool for as long as it
// runs: at least their keys. One that would take the Slot past its share is
// refused for this Slot - STATE_BUDGET_EXCEEDED, record BYTES - and holds
// nothing, while the ones that fit are held; one the pool has no room for is
// the Slot's RESOURCE_HARD_STOP.
func TestALoadedNoDataMemoryIsHeldOrRefusedForItsPlanAlone(t *testing.T) {
	coordinator, _, _ := pooledNoDataFixture(256 << 10)
	share := coordinator.qgShareBytes()
	stream := &streamedExecution{coordinator: coordinator, began: true}
	small, large := loadedMemory("100", 100), loadedMemory("856", 5000)
	stream.noData = execution.NoDataLoadResult{Items: []execution.NoDataMemorySnapshot{small, large}}
	if err := stream.reserveNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	refusal, refused := stream.noDataRefused[large.Identity]
	if _, alsoRefused := stream.noDataRefused[small.Identity]; !refused || alsoRefused || refusal.Size == nil ||
		refusal.ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) || refusal.Size.Record != execution.NoDataRecordBytes ||
		refusal.Size.Groups != 5000 || refusal.Size.Bytes <= share {
		t.Fatalf("refused %v (%+v); want the 5000-group memory refused past the share %d and the 100-group one held", stream.noDataRefused, refusal, share)
	}
	held := RetainedReserved(coordinator)
	if held < 100*noDataGroupKeyLength || held > share {
		t.Fatalf("the pool holds %d for the memory that fit, want at least its keys and within the share", held)
	}

	full, _, _ := pooledNoDataFixture(4 << 20)
	full.reservations.retainedBytes = full.budget.MaxRetainedBytes - 1<<10
	crowded := &streamedExecution{coordinator: full, began: true}
	crowded.noData = execution.NoDataLoadResult{Items: []execution.NoDataMemorySnapshot{small}}
	var exceeded *provisionalBudgetExceededError
	if err := crowded.reserveNoDataMemory(context.Background()); !errors.As(err, &exceeded) || exceeded.share {
		t.Fatalf("a memory the pool has no room for ended %v; want the pool's refusal", err)
	}
}

// A Plan whose loaded memory was refused for the Slot skips its no-data
// round by name, SKIPPED_SLOT_BUDGET, reports the BYTES refusal, and neither
// judges nor queues a memory; the same Plan not refused judges and stores,
// which is the control that makes the skip mean something.
func TestAPlanWhoseMemoryWasRefusedSkipsItsNoDataRound(t *testing.T) {
	due := noDataWiredPlan(t)
	control := noDataWiredStream(t, due, &emptyNoDataStore{})
	if err := control.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if whole, err := control.noDataRoundFor(due, nil, execution.CompletenessFull); err != nil || whole.mutation == nil {
		t.Fatalf("not refused the round ended %v with mutation %v; the control has to judge", err, whole.mutation != nil)
	}

	var refusals []observability.Observation
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	stream.coordinator.ports.Observer = observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.Stage == observability.StageNoDataMemoryRefused {
			refusals = append(refusals, observation)
		}
	})
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	stream.noDataRefused = map[execution.PlanNoDataIdentity]execution.NoDataApplyItemResult{
		due.NoDataIdentity(): noDataBytesRefusal(due.NoDataIdentity(), 5000, 1<<30, 768<<20)}
	// Nothing reaches the batch, so the fixture's missing state port is the
	// assertion: a series sent on would panic here.
	if err := stream.evaluateNoData(context.Background(), nil, 16); err != nil {
		t.Fatal(err)
	}
	if len(stream.noDataOutcomes) != 1 || stream.noDataOutcomes[0] != nodata.OutcomeSkippedSlotBudget ||
		stream.seriesCensus.Due != 0 || len(stream.noDataMutations) != 0 || len(refusals) != 1 ||
		refusals[0].NoDataMemoryRefusal == nil || refusals[0].NoDataMemoryRefusal.Record != string(execution.NoDataRecordBytes) {
		t.Fatalf("outcomes %v, %d series sent, %d memories queued, refusals %+v; want SKIPPED_SLOT_BUDGET, nothing judged "+
			"or queued, and one BYTES refusal", stream.noDataOutcomes, stream.seriesCensus.Due, len(stream.noDataMutations), refusals)
	}
}

func (store *pooledNoDataStore) ReadNoDataHeaders(ctx context.Context, request execution.NoDataLoadRequest) (execution.NoDataLoadResult, error) {
	return LoadedRoster(store.LoadNoData).Headers(ctx, request)
}

func (store *pooledNoDataStore) RecheckNoDataHeader(ctx context.Context, identity execution.PlanNoDataIdentity) (execution.NoDataMemorySnapshot, error) {
	return LoadedRoster(store.LoadNoData).Recheck(ctx, identity)
}

func (store *pooledNoDataStore) ScanNoDataGroups(ctx context.Context, request execution.NoDataScanRequest) (execution.NoDataScanPage, error) {
	return LoadedRoster(store.LoadNoData).Scan(ctx, request)
}

func (store *pooledNoDataStore) ReadNoDataGroups(ctx context.Context, request execution.NoDataGroupsRequest) (execution.NoDataGroupsResult, error) {
	return LoadedRoster(store.LoadNoData).Read(ctx, request)
}
