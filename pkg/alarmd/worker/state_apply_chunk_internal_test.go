// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// chunkRetention is the Plan retention the chunk loop carries. chunkStore does
// not derive a TTL from it; it only has to travel with every chunk.
var chunkRetention = []execution.StateRetentionRequirement{
	{LevelID: 1, RetentionPoints: 5, EvaluationInterval: time.Minute},
}

// chunkStore is a State and Gap store that records every call it receives
// and can fail or reject one call, so the chunk loop is observed exactly.
type chunkStore struct {
	stateCalls   [][]execution.StateKeyIdentity
	admitCalls   int
	gapCalls     [][]execution.PlanGapIdentity
	fences       []execution.StateApplyFence
	encodedBytes int
	// 1-based call indexes; zero disables the behaviour.
	failCall          int
	retryableCall     int
	deterministicCall int
	cancelCall        int
	cancel            context.CancelFunc
	gapStatuses       []execution.GapGuardApplyStatus
	delay             time.Duration
}

func (store *chunkStore) LoadRuntime(context.Context, execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	panic("not used")
}

func (store *chunkStore) AdmitRuntime(_ context.Context, request execution.StateApplyRequest) (execution.StateAdmissionResult, error) {
	store.admitCalls++
	result := execution.StateAdmissionResult{Items: make([]execution.StateAdmissionItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		result.Items[index] = execution.StateAdmissionItemResult{Identity: mutation.Identity,
			Status: execution.StateAdmissionAccepted, EncodedBytes: store.encodedBytes}
	}
	return result, nil
}

func (store *chunkStore) ApplyRuntime(ctx context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	time.Sleep(store.delay)
	store.stateCalls = append(store.stateCalls, stateIdentities(request.Items))
	call := len(store.stateCalls)
	if call == store.failCall {
		return execution.StateApplyResult{}, errors.New("injected transport failure")
	}
	if call == store.cancelCall {
		store.cancel()
	}
	result := execution.StateApplyResult{Items: make([]execution.StateApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		result.Items[index] = execution.StateApplyItemResult{Identity: mutation.Identity, Status: execution.StateApplied}
	}
	switch call {
	case store.retryableCall:
		result.Items[0].Status, result.Items[0].ReasonCode = execution.StateApplyRetryable, execution.ReasonCode(contract.ReasonStateWriteRetryable)
	case store.deterministicCall:
		result.Items[0].Status, result.Items[0].ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(contract.ReasonStateCorrupt)
	}
	return result, nil
}

func (store *chunkStore) ApplyRuntimeFenced(ctx context.Context, request execution.StateApplyRequest, fence execution.StateApplyFence) (execution.StateApplyResult, error) {
	store.fences = append(store.fences, fence)
	return store.ApplyRuntime(ctx, request)
}

func (store *chunkStore) LoadGaps(context.Context, execution.GapLoadRequest) (execution.GapLoadResult, error) {
	panic("not used")
}

func (store *chunkStore) LoadGapsInto(context.Context, execution.GapLoadRequest, func(execution.GapGuardSnapshot) error) error {
	panic("not used")
}

func (store *chunkStore) ApplyGap(_ context.Context, request execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	identities := make([]execution.PlanGapIdentity, len(request.Items))
	result := execution.GapGuardApplyResult{Items: make([]execution.GapGuardApplyItemResult, len(request.Items))}
	status := execution.GapGuardApplied
	if call := len(store.gapCalls); call < len(store.gapStatuses) {
		status = store.gapStatuses[call]
	}
	for index, item := range request.Items {
		identities[index] = item.Identity
		result.Items[index] = execution.GapGuardApplyItemResult{Identity: item.Identity, Status: status}
	}
	store.gapCalls = append(store.gapCalls, identities)
	return result, nil
}

type chunkFixture struct {
	coordinator  *SlotExecutionCoordinator
	contract     execution.FrozenExecutionContractRef
	fence        execution.OwnerFence
	observations *[]observability.Observation
}

func newChunkFixture(store *chunkStore, storeMaxItems uint64) chunkFixture {
	observations := make([]observability.Observation, 0)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observability.NormalizeObservation(observation))
	})
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_000},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 1_787_999_940, DuePlanSetDigest: "due-set-v1",
	}
	coordinator := &SlotExecutionCoordinator{
		ports: Ports{State: store, GapGuard: store, Observer: observer},
		budget: ProvisionalBudget{MaxSeries: 1 << 20, MaxRetainedBytes: 1 << 30, MaxStateMutations: 1 << 20,
			MaxEvents: 1 << 20, MaxGapMutations: 1 << 20, StoreMaxItems: storeMaxItems},
	}
	return chunkFixture{coordinator: coordinator, contract: contractRef, observations: &observations,
		fence: execution.OwnerFence{QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"}}
}

func chunkMutations(total int) []execution.StateMutation {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1"}
	mutations := make([]execution.StateMutation, total)
	for index := range mutations {
		mutations[index].Identity = execution.StateKeyIdentity{Plan: plan, StateGeneration: "generation",
			SeriesIdentityDigest: execution.SeriesIdentityDigest(fmt.Sprintf("%064d", index))}
	}
	return mutations
}

func (fixture chunkFixture) chunkObservations(stage observability.Stage) []observability.Observation {
	var matched []observability.Observation
	for _, observation := range *fixture.observations {
		if observation.Stage == stage {
			matched = append(matched, observation)
		}
	}
	return matched
}

func TestApplyStateChunksAtTheStoreCallBound(t *testing.T) {
	for _, total := range []int{8192, 8193, 65536, 65537} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			store := &chunkStore{delay: time.Millisecond}
			fixture := newChunkFixture(store, 8192)
			mutations := chunkMutations(total)
			rejected, err := fixture.coordinator.applyState(context.Background(), execution.OperationNormal, fixture.contract, fixture.fence, chunkRetention, mutations, nil)
			if err != nil || len(rejected) != 0 {
				t.Fatalf("applyState() rejected=%v error=%v", rejected, err)
			}
			wantCalls := (total + 8191) / 8192
			if len(store.stateCalls) != wantCalls {
				t.Fatalf("store calls = %d, want %d for %d mutations", len(store.stateCalls), wantCalls, total)
			}
			var sent []execution.StateKeyIdentity
			for index, call := range store.stateCalls {
				if len(call) > 8192 || len(call) == 0 {
					t.Fatalf("call %d carried %d items, want 1..8192", index, len(call))
				}
				sent = append(sent, call...)
			}
			for index, identity := range stateIdentities(mutations) {
				if sent[index] != identity {
					t.Fatalf("item %d was sent out of slice order", index)
				}
			}
			if len(store.fences) != wantCalls {
				t.Fatalf("fenced calls = %d, want every chunk fenced", len(store.fences))
			}
			for index, fence := range store.fences {
				if fence.Fence != fixture.fence || fence.At.IsZero() {
					t.Fatalf("chunk %d fence = %+v, want the Slot owner fence with an instant", index, fence)
				}
				if index > 0 && !fence.At.After(store.fences[index-1].At) {
					t.Fatalf("chunk %d reused the fence instant of the previous chunk", index)
				}
			}
			applied := fixture.chunkObservations(observability.StageStateApplied)
			if len(applied) != wantCalls {
				t.Fatalf("state_applied observations = %d, want one per chunk", len(applied))
			}
			var keys int64
			for index, observation := range applied {
				facts := observation.StateApplyChunk
				keys += observation.Counts.Keys
				if observation.Result != observability.ResultSuccess || facts == nil || facts.Index != index ||
					facts.Count != wantCalls || facts.AppliedKeys != keys {
					t.Fatalf("chunk %d observation = %+v facts=%+v", index, observation, facts)
				}
			}
			if keys != int64(total) {
				t.Fatalf("observed keys = %d, want %d", keys, total)
			}
		})
	}
}

func TestApplyStateStopsAtTheFirstFailedChunk(t *testing.T) {
	mutations := chunkMutations(3 * 8192)
	t.Run("transport failure in chunk 2", func(t *testing.T) {
		store := &chunkStore{failCall: 2}
		fixture := newChunkFixture(store, 8192)
		_, err := fixture.coordinator.applyState(context.Background(), execution.OperationNormal, fixture.contract, fixture.fence, chunkRetention, mutations, nil)
		if err == nil || !strings.Contains(err.Error(), "injected transport failure") || len(store.stateCalls) != 2 {
			t.Fatalf("applyState() error=%v calls=%d, want the failure after two calls", err, len(store.stateCalls))
		}
		applied := fixture.chunkObservations(observability.StageStateApplied)
		if len(applied) != 2 || applied[1].Result != observability.ResultFailed || applied[1].StateApplyChunk.Index != 1 {
			t.Fatalf("state_applied observations = %+v", applied)
		}
	})
	t.Run("retryable item in chunk 2", func(t *testing.T) {
		store := &chunkStore{retryableCall: 2}
		fixture := newChunkFixture(store, 8192)
		_, err := fixture.coordinator.applyState(context.Background(), execution.OperationNormal, fixture.contract, fixture.fence, chunkRetention, mutations, nil)
		if err == nil || !strings.Contains(err.Error(), "did not complete: RETRYABLE_IO") || len(store.stateCalls) != 2 {
			t.Fatalf("applyState() error=%v calls=%d, want retryable stop after two calls", err, len(store.stateCalls))
		}
		applied := fixture.chunkObservations(observability.StageStateApplied)
		if len(applied) != 2 || applied[1].ReasonCode != execution.ReasonCode(contract.ReasonStateWriteRetryable) {
			t.Fatalf("failed chunk observation = %+v", applied)
		}
	})
	t.Run("deterministic item in chunk 3", func(t *testing.T) {
		store := &chunkStore{deterministicCall: 3}
		fixture := newChunkFixture(store, 8192)
		rejected, err := fixture.coordinator.applyState(context.Background(), execution.OperationNormal, fixture.contract, fixture.fence, chunkRetention, mutations, nil)
		if err != nil || len(store.stateCalls) != 3 {
			t.Fatalf("applyState() error=%v calls=%d, want every chunk sent", err, len(store.stateCalls))
		}
		if reason, found := rejected[mutations[2*8192].Identity]; !found || reason != execution.ReasonCode(contract.ReasonStateCorrupt) || len(rejected) != 1 {
			t.Fatalf("rejected = %v, want only the first item of chunk 3", rejected)
		}
		applied := fixture.chunkObservations(observability.StageStateApplied)
		if len(applied) != 3 || applied[2].Result != observability.ResultTerminal || applied[1].Result != observability.ResultSuccess {
			t.Fatalf("state_applied observations = %+v", applied)
		}
	})
}

func TestApplyStateStopsBetweenChunksOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &chunkStore{cancelCall: 1, cancel: cancel}
	fixture := newChunkFixture(store, 8192)
	_, err := fixture.coordinator.applyState(ctx, execution.OperationNormal, fixture.contract, fixture.fence, chunkRetention, chunkMutations(2*8192), nil)
	if !errors.Is(err, context.Canceled) || len(store.stateCalls) != 1 {
		t.Fatalf("applyState() error=%v calls=%d, want cancellation before chunk 2", err, len(store.stateCalls))
	}
}

func TestForEachChunkBoundsTheChunkCount(t *testing.T) {
	calls := 0
	count := func(applyChunk) error { calls++; return nil }
	if err := forEachChunk(context.Background(), 64, 1, count); err != nil || calls != 64 {
		t.Fatalf("64 chunks error=%v calls=%d", err, calls)
	}
	if err := forEachChunk(context.Background(), 65, 1, count); err == nil || !strings.Contains(err.Error(), "more than the 64 allowed") {
		t.Fatalf("65 chunks error=%v, want the chunk cap", err)
	}
	calls = 0
	if err := forEachChunk(context.Background(), 0, 1, count); err != nil || calls != 0 {
		t.Fatalf("empty apply error=%v calls=%d", err, calls)
	}
	if err := forEachChunk(context.Background(), 5, 0, count); err != nil || calls != 1 {
		t.Fatalf("unbounded store error=%v calls=%d, want one chunk", err, calls)
	}
}

func TestAdmitStateChunksAndMeasuresEncodedBytes(t *testing.T) {
	store := &chunkStore{encodedBytes: 10}
	fixture := newChunkFixture(store, 8192)
	mutations := chunkMutations(8193)
	rejected, encodedBytes, err := fixture.coordinator.admitState(context.Background(), execution.OperationNormal, fixture.contract, chunkRetention, mutations)
	if err != nil || len(rejected) != 0 || store.admitCalls != 2 || len(encodedBytes) != len(mutations) {
		t.Fatalf("admitState() rejected=%v bytes=%d calls=%d error=%v", rejected, len(encodedBytes), store.admitCalls, err)
	}
	for index, size := range encodedBytes {
		if size != 10 {
			t.Fatalf("mutation %d encoded bytes = %d, want the store measurement", index, size)
		}
	}
	admitted := fixture.chunkObservations(observability.StageStateAdmission)
	if len(admitted) != 2 || admitted[0].Counts.StateBytes != 81920 || admitted[1].Counts.StateBytes != 10 ||
		admitted[1].StateApplyChunk.AppliedBytes != 81930 || admitted[1].StateApplyChunk.AppliedKeys != 8193 {
		t.Fatalf("state_admission observations = %+v", admitted)
	}
}

func TestApplyActivatedPlanGapsChunksAndConjoinsAlreadyApplied(t *testing.T) {
	items := make([]execution.PlanGapMutation, 3)
	for index := range items {
		items[index].Identity = execution.PlanGapIdentity{
			Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strconv.Itoa(index)}, StateGeneration: "generation",
		}
	}
	t.Run("one chunk not already applied", func(t *testing.T) {
		store := &chunkStore{gapStatuses: []execution.GapGuardApplyStatus{execution.GapGuardAlreadyApplied, execution.GapGuardApplied}}
		fixture := newChunkFixture(store, 2)
		allAlready, err := fixture.coordinator.applyActivatedPlanGaps(context.Background(), execution.OperationNormal, fixture.contract, items, nil)
		if err != nil || allAlready || len(store.gapCalls) != 2 || len(store.gapCalls[0]) != 2 || len(store.gapCalls[1]) != 1 {
			t.Fatalf("applyActivatedPlanGaps() allAlready=%t calls=%v error=%v", allAlready, store.gapCalls, err)
		}
		committed := fixture.chunkObservations(observability.StageGapGuardCommitted)
		if len(committed) != 2 || committed[1].StateApplyChunk.Index != 1 || committed[1].StateApplyChunk.AppliedKeys != 3 {
			t.Fatalf("gap_guard_committed observations = %+v", committed)
		}
	})
	t.Run("every chunk already applied", func(t *testing.T) {
		store := &chunkStore{gapStatuses: []execution.GapGuardApplyStatus{execution.GapGuardAlreadyApplied, execution.GapGuardAlreadyApplied}}
		fixture := newChunkFixture(store, 2)
		allAlready, err := fixture.coordinator.applyActivatedPlanGaps(context.Background(), execution.OperationNormal, fixture.contract, items, nil)
		if err != nil || !allAlready {
			t.Fatalf("applyActivatedPlanGaps() allAlready=%t error=%v", allAlready, err)
		}
	})
	t.Run("redo must converge in its chunk", func(t *testing.T) {
		store := &chunkStore{gapStatuses: []execution.GapGuardApplyStatus{execution.GapGuardAlreadyApplied, execution.GapGuardApplied}}
		fixture := newChunkFixture(store, 2)
		redo := map[execution.PlanGapIdentity]struct{}{items[2].Identity: {}}
		_, err := fixture.coordinator.applyActivatedPlanGaps(context.Background(), execution.OperationNormal, fixture.contract, items, redo)
		if err == nil || !strings.Contains(err.Error(), "did not converge") || len(store.gapCalls) != 2 {
			t.Fatalf("applyActivatedPlanGaps() error=%v calls=%d", err, len(store.gapCalls))
		}
	})
}

func TestSlotBudgetDerivesThePerSlotCaps(t *testing.T) {
	tests := []struct {
		name                          string
		storeItems, process, wantSlot uint64
	}{
		{name: "process budget within chunked apply", storeItems: 8192, process: 65536, wantSlot: 65536},
		{name: "process budget above chunked apply", storeItems: 8192, process: 1 << 20, wantSlot: 524288},
		{name: "store without call bound", storeItems: 0, process: 1 << 20, wantSlot: 1 << 20},
		{name: "exact chunked apply", storeItems: 8192, process: 524288, wantSlot: 524288},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{
				MaxStateMutations: test.process, MaxGapMutations: test.process, MaxEvents: test.process, StoreMaxItems: test.storeItems,
			}}
			budget := coordinator.slotBudget()
			if budget.MaxStateMutations != test.wantSlot || budget.MaxGapMutations != test.wantSlot || budget.MaxEvents != test.process {
				t.Fatalf("slotBudget() = %+v, want state/gap %d and events %d", budget, test.wantSlot, test.process)
			}
		})
	}
}
