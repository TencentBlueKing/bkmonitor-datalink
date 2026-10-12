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
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const heldFrameBytes = 1000

// frameRecordingState hands back a frame for every admitted mutation, as the
// store does, and records at each write the frames it was given and what the
// retained budget had reserved at that moment.
type frameRecordingState struct {
	execution.StateStore
	coordinator     *SlotExecutionCoordinator
	framesAtWrite   []int
	reservedAtWrite []uint64
	writeErr        error
}

func (state *frameRecordingState) AdmitRuntime(ctx context.Context, request execution.StateApplyRequest) (execution.StateAdmissionResult, error) {
	result, err := state.StateStore.AdmitRuntime(ctx, request)
	for index := range result.Items {
		if result.Items[index].Status != execution.StateAdmissionAccepted {
			continue
		}
		mutation := request.Items[index]
		result.Items[index].EncodedBytes = heldFrameBytes
		result.Items[index].Frame = &execution.EncodedStateFrame{MutationDigest: mutation.MutationDigest,
			Revision: mutation.ExpectedBlobRevision + 1, Bytes: make([]byte, heldFrameBytes)}
	}
	return result, err
}

func (state *frameRecordingState) ApplyRuntime(ctx context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	frames := 0
	for _, frame := range request.Frames {
		if frame != nil {
			frames++
		}
	}
	state.framesAtWrite = append(state.framesAtWrite, frames)
	state.reservedAtWrite = append(state.reservedAtWrite, RetainedReserved(state.coordinator))
	if state.writeErr != nil {
		return execution.StateApplyResult{}, state.writeErr
	}
	return state.StateStore.ApplyRuntime(ctx, request)
}

// retryableOutputError is an output whose acknowledgement is unknown.
type retryableOutputError struct{}

func (retryableOutputError) Error() string              { return "acknowledgement unknown" }
func (retryableOutputError) RetryableOutputDependency() {}

// commitReady gives the isolation fixture's request the frozen due Plan
// targets a Progress commit is validated against, so a Slot these tests run
// to the end can commit.
func commitReady(fixture *planIsolationFixture) {
	evaluationMillis := int64(fixture.request.Contract.Slot.EvaluationTime) * 1000
	fixture.request.DuePlanTargets = execution.FrozenDuePlanTargets{
		DuePlanSetDigest: fixture.request.Contract.DuePlanSetDigest,
		Plans:            []execution.PlanKey{fixture.header.DuePlans[0].Key(), fixture.header.DuePlans[1].Key()},
	}
	fixture.request.EarliestQueryDeadlineUnixMilli = evaluationMillis + 1_000
	fixture.request.RecoveryUntilUnixMilli = evaluationMillis + 601_000
	fixture.request.KeepUntilUnixMilli = evaluationMillis + 677_000
	fixture.coordinator.ports.Progress = &committingProgressPorts{planFailurePorts: fixture.ports}
}

func withFrameRecording(coordinator *SlotExecutionCoordinator, budget uint64) *frameRecordingState {
	coordinator.budget = ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: budget, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10}
	state := &frameRecordingState{StateStore: coordinator.ports.State, coordinator: coordinator}
	coordinator.ports.State = state
	return state
}

// The frames admission kept wait for the write under a reservation of their
// own, and the reservation is gone when the Slot is, whichever way each Plan
// left: written, held for the lease, refused by name, unacknowledged, failed
// by its output, failed by its write. Two Plans, one of them the failing
// one: the healthy Plan's write sees only its own frame reserved, so the
// failing Plan's was released when it stopped, not when the Slot did.
func TestKeptFramesAreReleasedOnEveryWayAPlanLeaves(t *testing.T) {
	for name, test := range map[string]struct {
		outputErr error
		writeErr  error
		fails     bool
		writes    int
	}{
		"every Plan written":            {writes: 2},
		"held for the lease":            {outputErr: &deferredPlanEventError{}, writes: 1},
		"refused by name":               {outputErr: &rejectedPlanEventError{reason: contract.ReasonOutputClientRejected, detail: "refused"}, writes: 1},
		"acknowledgement unknown":       {outputErr: retryableOutputError{}, writes: 1},
		"an output that fails the Slot": {outputErr: errors.New("broken sink"), fails: true, writes: 0},
		"a write that fails the Slot":   {writeErr: errors.New("broken store"), fails: true, writes: 1},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newPlanIsolationFixture(t, test.outputErr)
			commitReady(fixture)
			state := withFrameRecording(fixture.coordinator, 1<<20)
			state.writeErr = test.writeErr
			_, err := fixture.coordinator.finalizePrepared(context.Background(), fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated)
			if (err != nil) != test.fails {
				t.Fatalf("finalizePrepared() error = %v, want failure %v", err, test.fails)
			}
			if len(state.framesAtWrite) != test.writes {
				t.Fatalf("writes = %d, want %d", len(state.framesAtWrite), test.writes)
			}
			for index := range state.framesAtWrite {
				if state.framesAtWrite[index] != 1 || state.reservedAtWrite[index] != oneFrame() {
					t.Fatalf("write %d carried %d frames with %d bytes reserved, want its one frame and its own bytes alone",
						index, state.framesAtWrite[index], state.reservedAtWrite[index])
				}
			}
			if reserved := RetainedReserved(fixture.coordinator); reserved != 0 {
				t.Fatalf("%d bytes still reserved after the Slot", reserved)
			}
		})
	}
}

// A partial output drops the series it did not write and writes the rest
// of its Plan with their frames; when it drops every series of the Plan
// nothing of it is written, and its frames are released all the same. The
// sibling Plan is written either way.
func TestKeptFramesAreReleasedWhenAPartialOutputLeavesAnyNumberOfSeries(t *testing.T) {
	for name, test := range map[string]struct {
		notWritten []string
		// reserved is what each write sees reserved: the refused Plan gives
		// back the frame of the series it dropped before its write, and the
		// sibling sees only its own once the refused Plan let go of its
		// frames.
		reserved []uint64
	}{
		"one series left": {notWritten: []string{"failed-event"}, reserved: []uint64{oneFrame(), oneFrame()}},
		"no series left":  {notWritten: []string{"failed-event", "kept-event"}, reserved: []uint64{oneFrame()}},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newOutputIsolationFixture(t, test.notWritten...)
			state := withFrameRecording(fixture.coordinator, 1<<20)
			if _, err := fixture.finalize(t); err != nil {
				t.Fatalf("finalize() error = %v", err)
			}
			if len(state.framesAtWrite) != len(test.reserved) {
				t.Fatalf("writes = %d, want %d", len(state.framesAtWrite), len(test.reserved))
			}
			for index, frames := range state.framesAtWrite {
				if frames != 1 || state.reservedAtWrite[index] != test.reserved[index] {
					t.Fatalf("write %d carried %d frames with %d bytes reserved, want the one series it wrote with %d",
						index, frames, state.reservedAtWrite[index], test.reserved[index])
				}
			}
			if reserved := RetainedReserved(fixture.coordinator); reserved != 0 {
				t.Fatalf("%d bytes still reserved after the Slot", reserved)
			}
		})
	}
}

// Frames the retained budget cannot take are not kept: the write gets none
// and encodes each mutation itself, and the Slot runs as it would have.
func TestFramesTheBudgetCannotHoldAreNotKept(t *testing.T) {
	fixture := newPlanIsolationFixture(t, nil)
	commitReady(fixture)
	state := withFrameRecording(fixture.coordinator, heldFrameBytes-1)
	if _, err := fixture.coordinator.finalizePrepared(context.Background(), fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated); err != nil {
		t.Fatalf("finalizePrepared() error = %v, want the Slot to run without the frames", err)
	}
	if len(state.framesAtWrite) != 2 {
		t.Fatalf("writes = %d, want both Plans written", len(state.framesAtWrite))
	}
	for index, frames := range state.framesAtWrite {
		if frames != 0 || state.reservedAtWrite[index] != 0 {
			t.Fatalf("write %d carried %d frames with %d bytes reserved, want none kept", index, frames, state.reservedAtWrite[index])
		}
	}
}
