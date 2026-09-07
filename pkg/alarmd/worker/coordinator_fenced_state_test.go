// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// fencedStatePorts is a State store that can verify the owner fence inside
// the write. It records every fence the coordinator hands over.
type fencedStatePorts struct {
	*recordingPorts
	fences     []execution.StateApplyFence
	staleFence bool
}

func (ports *fencedStatePorts) ApplyRuntimeFenced(
	ctx context.Context, request execution.StateApplyRequest, fence execution.StateApplyFence,
) (execution.StateApplyResult, error) {
	ports.fences = append(ports.fences, fence)
	if ports.staleFence {
		ports.record("state_apply")
		return execution.StateApplyResult{}, fmt.Errorf("state: runtime state apply: %w", ownership.ErrStaleFence)
	}
	return ports.recordingPorts.ApplyRuntime(ctx, request)
}

func newFencedFixture(t *testing.T, staleFence bool) (fixture, *fencedStatePorts) {
	t.Helper()
	trace := make([]string, 0, len(fullTrace))
	ports := &recordingPorts{trace: &trace, ready: true}
	fenced := &fencedStatePorts{recordingPorts: ports, staleFence: staleFence}
	observations := make([]observability.Observation, 0, len(fullTrace)+1)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observability.NormalizeObservation(observation))
	})
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: ports, Activation: ports,
		Query: ports, Sequencer: ports, Evaluator: ports, Admission: ports, GapGuard: ports,
		Events: ports, State: fenced, Progress: ports,
		Observer: observer,
	}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
	if err != nil {
		t.Fatalf("NewSlotExecutionCoordinator() error: %v", err)
	}
	return fixture{trace: &trace, observations: &observations, ports: ports, coordinator: coordinator}, fenced
}

func TestSlotExecutionCoordinatorAppliesStateThroughFencedStoreWithSlotFence(t *testing.T) {
	fixture, fenced := newFencedFixture(t, false)
	request := slotRequest(execution.OperationNormal)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, fullTrace)
	if len(fenced.fences) != 1 || fenced.fences[0].Fence != request.OwnerFence || fenced.fences[0].At.IsZero() {
		t.Fatalf("fenced apply received %+v, want the Slot owner fence %+v with a comparison instant", fenced.fences, request.OwnerFence)
	}
	if fenced.fences[0].Validate(request.Contract) != nil {
		t.Fatalf("coordinator handed over an invalid apply fence: %+v", fenced.fences[0])
	}
}

func TestSlotExecutionCoordinatorStaleFenceAtStateApplyStopsPlanBeforeProgress(t *testing.T) {
	fixture, fenced := newFencedFixture(t, true)
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if !errors.Is(err, ownership.ErrStaleFence) || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v, want ErrStaleFence", result, err)
	}
	// Admission, event ACK and the fenced apply ran; nothing after the apply.
	assertTrace(t, fixture.trace, fullTrace[:10])
	if len(fenced.fences) != 1 {
		t.Fatalf("fenced apply calls = %d", len(fenced.fences))
	}
	if !isZeroProgressCommit(fixture.ports.lastProgress) {
		t.Fatalf("stale fence committed Progress: %+v", fixture.ports.lastProgress)
	}
}
