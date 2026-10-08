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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// admitFramesStore hands back a frame for every admitted mutation, as the
// store does, and records what the retained budget had reserved as each
// admission call began. It can fail one call, or end the context after one.
type admitFramesStore struct {
	*chunkStore
	coordinator     *SlotExecutionCoordinator
	reservedAtAdmit []uint64
	failCall        int
	cancelAfterCall int
	cancel          context.CancelFunc
}

func (store *admitFramesStore) AdmitRuntime(ctx context.Context, request execution.StateApplyRequest) (execution.StateAdmissionResult, error) {
	store.reservedAtAdmit = append(store.reservedAtAdmit, RetainedReserved(store.coordinator))
	result, err := store.chunkStore.AdmitRuntime(ctx, request)
	if store.chunkStore.admitCalls == store.failCall {
		return execution.StateAdmissionResult{}, errors.New("injected admission failure")
	}
	for index := range result.Items {
		mutation := request.Items[index]
		result.Items[index].EncodedBytes = heldFrameBytes
		result.Items[index].Frame = &execution.EncodedStateFrame{MutationDigest: mutation.MutationDigest,
			Revision: mutation.ExpectedBlobRevision + 1, Bytes: make([]byte, heldFrameBytes)}
	}
	if store.chunkStore.admitCalls == store.cancelAfterCall {
		store.cancel()
	}
	return result, err
}

// framesFixture admits in chunks of two against a retained budget.
func framesFixture(budget uint64) (chunkFixture, *admitFramesStore) {
	fixture := newChunkFixture(&chunkStore{}, 2)
	store := &admitFramesStore{chunkStore: fixture.coordinator.ports.State.(*chunkStore), coordinator: fixture.coordinator}
	fixture.coordinator.ports.State = store
	fixture.coordinator.budget.MaxRetainedBytes = budget
	return fixture, store
}

// oneFrame is what one kept frame holds of the budget: its bytes and the
// frame around them.
func oneFrame() uint64 { return heldFrameBytes + frameHeaderBytes }

func keptFrames(admitted []admittedState) []bool {
	kept := make([]bool, len(admitted))
	for index, state := range admitted {
		kept[index] = state.frame != nil
	}
	return kept
}

// A Plan's frames are reserved as each chunk comes back: the second chunk is
// admitted with the first one's frames already on the budget, the third with
// both, and every frame is counted with the frame around its bytes. They
// used to be reserved after the last chunk, held on nothing until then. Read
// through RetainedReserved, the reading capacity_reserved publishes: it grows
// with each chunk while the Plan admits and is back to zero after the release.
func TestAPlansFramesAreReservedAsEachChunkIsAdmitted(t *testing.T) {
	fixture, store := framesFixture(1 << 20)
	_, admitted, held, err := fixture.coordinator.admitState(context.Background(), execution.OperationNormal, fixture.contract, chunkRetention, 0, chunkMutations(6))
	if err != nil {
		t.Fatalf("admitState() error = %v", err)
	}
	want := []uint64{0, 2 * oneFrame(), 4 * oneFrame()}
	if len(store.reservedAtAdmit) != 3 || store.reservedAtAdmit[0] != want[0] || store.reservedAtAdmit[1] != want[1] || store.reservedAtAdmit[2] != want[2] {
		t.Fatalf("reserved as each chunk was admitted = %v, want %v", store.reservedAtAdmit, want)
	}
	if reserved := RetainedReserved(fixture.coordinator); reserved != 6*oneFrame() || held.bytes != reserved {
		t.Fatalf("after admission %d reserved, held %d, want every frame's %d", reserved, held.bytes, 6*oneFrame())
	}
	for index, kept := range keptFrames(admitted) {
		if !kept {
			t.Fatalf("frame %d let go with the budget room for all of them", index)
		}
	}
	held.release()
	if reserved := RetainedReserved(fixture.coordinator); reserved != 0 {
		t.Fatalf("%d bytes still reserved after the release", reserved)
	}
}

// When the budget does not take a chunk, the Plan keeps the frames it had
// reserved and lets that chunk's frames go, and every later chunk's too --
// even a smaller one the budget could still take.
func TestFramesTheBudgetStopsTakingMidPlanAreLetGo(t *testing.T) {
	fixture, _ := framesFixture(3 * oneFrame())
	_, admitted, held, err := fixture.coordinator.admitState(context.Background(), execution.OperationNormal, fixture.contract, chunkRetention, 0, chunkMutations(5))
	if err != nil {
		t.Fatalf("admitState() error = %v", err)
	}
	defer held.release()
	want := []bool{true, true, false, false, false}
	for index, kept := range keptFrames(admitted) {
		if kept != want[index] {
			t.Fatalf("frames kept = %v, want %v: the first chunk's, and nothing after the chunk the budget refused", keptFrames(admitted), want)
		}
	}
	if reserved := RetainedReserved(fixture.coordinator); reserved != 2*oneFrame() || held.bytes != reserved {
		t.Fatalf("%d reserved, held %d, want the first chunk's %d", reserved, held.bytes, 2*oneFrame())
	}
}

// An admission that does not complete gives back everything it had
// reserved before it returns: a chunk the store failed, a context that
// ended between chunks.
func TestAnAdmissionThatStopsMidPlanReleasesWhatItReserved(t *testing.T) {
	for name, stop := range map[string]func(*admitFramesStore, context.CancelFunc){
		"a chunk the store failed": func(store *admitFramesStore, _ context.CancelFunc) { store.failCall = 3 },
		"a context that ended mid-admission": func(store *admitFramesStore, cancel context.CancelFunc) {
			store.cancelAfterCall, store.cancel = 2, cancel
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture, store := framesFixture(1 << 20)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stop(store, cancel)
			_, _, held, err := fixture.coordinator.admitState(ctx, execution.OperationNormal, fixture.contract, chunkRetention, 0, chunkMutations(6))
			if err == nil || held != nil {
				t.Fatalf("admitState() = %v, %v, want the admission to stop", held, err)
			}
			if len(store.reservedAtAdmit) < 2 || store.reservedAtAdmit[1] == 0 {
				t.Fatalf("setup: reserved as each chunk was admitted = %v, want frames reserved before it stopped", store.reservedAtAdmit)
			}
			if reserved := RetainedReserved(fixture.coordinator); reserved != 0 {
				t.Fatalf("%d bytes still reserved after an admission that stopped", reserved)
			}
		})
	}
}
