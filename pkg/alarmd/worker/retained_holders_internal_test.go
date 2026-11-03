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
	"maps"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// readHolders reads the coordinator's holders and checks the one figure
// every reading owes: what the rows add up to against what the pool holds,
// short by unnamed when a reservation was taken that no hold names.
func readHolders(t *testing.T, coordinator *SlotExecutionCoordinator, limit int, unnamed uint64) RetainedHolderReading {
	t.Helper()
	reading := RetainedHolders(coordinator, limit)
	if pool := RetainedReserved(coordinator); reading.Held != pool || reading.Attributed+unnamed != pool {
		t.Fatalf("attributed %d and held %d against a pool of %d with %d unnamed", reading.Attributed, reading.Held, pool, unnamed)
	}
	return reading
}

func wantHolder(t *testing.T, got RetainedHolder, slot execution.SlotIdentity, byKind map[string]uint64) {
	t.Helper()
	var bytes uint64
	for _, value := range byKind {
		bytes += value
	}
	if got.Slot != slot || got.Operation != execution.OperationNormal || got.Bytes != bytes || !maps.Equal(got.ByKind, byKind) {
		t.Fatalf("holder = %+v, want %s %d bytes as %v", got, slot.QueryGroup, bytes, byKind)
	}
}

// What the retained pool holds is named, one row per Slot, by what each
// part was taken for, the largest first: a Slot's stream (its input as a
// query reserves it, its gap facts as they load) and a Plan's kept frames
// fold into the Slot's one row, which began when the first of them did.
// The Slot holding the longest is named whether or not it is listed. The
// pool's own figure is read beside the rows, so a reservation no hold names
// shows as the difference, and the rows go as each hold gives back.
func TestTheRetainedPoolIsNamedBySlotAndWhatItHolds(t *testing.T) {
	coordinator, _, gapRequest := gapReservationFixture()
	coordinator.budget.MaxGapMutations = 100
	ctx := context.Background()
	slotA := execution.SlotIdentity{QueryGroup: "group-a", EvaluationTime: 1_788_000_000}
	slotB := execution.SlotIdentity{QueryGroup: "group-b", EvaluationTime: 1_788_000_000}
	stream := func(slot execution.SlotIdentity) *streamedExecution {
		return &streamedExecution{coordinator: coordinator, began: true, request: execution.SlotExecutionRequest{
			Contract: execution.FrozenExecutionContractRef{Slot: slot}, Operation: execution.OperationNormal}}
	}

	b := stream(slotB)
	if err := b.ReserveRetainedBytes(ctx, 100); err != nil {
		t.Fatal(err)
	}
	a := stream(slotA)
	beganA := time.Now()
	if err := a.ReserveRetainedBytes(ctx, 300); err != nil {
		t.Fatal(err)
	}
	before := RetainedReserved(coordinator)
	if _, err := a.loadGapFacts(ctx, gapRequest); err != nil {
		t.Fatal(err)
	}
	gap := RetainedReserved(coordinator) - before
	heldA := time.Now()
	if gap == 0 {
		t.Fatal("the gap fact reserved nothing")
	}
	frames := &heldFrames{coordinator: coordinator, slot: slotA, operation: execution.OperationNormal}
	frame := func() admittedState {
		return admittedState{frame: &execution.EncodedStateFrame{Bytes: make([]byte, heldFrameBytes)}}
	}
	frames.keep([]admittedState{frame(), frame()})
	frames.settle([]admittedState{frame()})
	a.ReleaseRetainedBytes(50)

	reading := readHolders(t, coordinator, -1, 0)
	if reading.Total != 2 || len(reading.Holders) != 2 {
		t.Fatalf("%d holders of %d, want both Slots: %+v", len(reading.Holders), reading.Total, reading.Holders)
	}
	wantHolder(t, reading.Holders[0], slotA, map[string]uint64{RetainedKindInput: 250, RetainedKindGap: gap, RetainedKindFrames: oneFrame()})
	wantHolder(t, reading.Holders[1], slotB, map[string]uint64{RetainedKindInput: 100})
	if since := reading.Holders[0].Since; since.Before(beganA) || since.After(heldA) {
		t.Fatalf("the Slot is held since %v, want its stream's first reservation in [%v, %v]", since, beganA, heldA)
	}

	reading = readHolders(t, coordinator, 1, 0)
	if reading.Total != 2 || len(reading.Holders) != 1 || reading.Holders[0].Slot != slotA {
		t.Fatalf("limited to one: %d of %d %+v, want the largest of two", len(reading.Holders), reading.Total, reading.Holders)
	}
	if reading.Oldest == nil || reading.Oldest.Slot != slotB || reading.Oldest.Since.After(beganA) {
		t.Fatalf("oldest = %+v, want the Slot that reserved first though it is not listed", reading.Oldest)
	}

	if !coordinator.tryAcquireRetained(77) {
		t.Fatal("the budget refused an unnamed reservation")
	}
	readHolders(t, coordinator, -1, 77)
	coordinator.releaseProvisional(0, 77)

	a.releaseProvisional()
	holders := readHolders(t, coordinator, -1, 0).Holders
	if len(holders) != 2 {
		t.Fatalf("after the stream: %+v, want the kept frames and the other Slot", holders)
	}
	wantHolder(t, holders[0], slotA, map[string]uint64{RetainedKindFrames: oneFrame()})
	wantHolder(t, holders[1], slotB, map[string]uint64{RetainedKindInput: 100})
	if holders[0].Since.Before(heldA) {
		t.Fatalf("the frames alone are held since %v, before they were kept (%v)", holders[0].Since, heldA)
	}

	frames.release()
	b.releaseProvisional()
	if reading = readHolders(t, coordinator, -1, 0); reading.Total != 0 || len(reading.Holders) != 0 || reading.Oldest != nil {
		t.Fatalf("after every hold gave back: %+v", reading)
	}
	if live := len(coordinator.holders.live); live != 0 {
		t.Fatalf("%d holds still registered after giving everything back", live)
	}
	// A released stream does not join again: its since is written once.
	b.retainBytes(retainPhaseInput, 1)
	if live := len(coordinator.holders.live); live != 0 {
		t.Fatalf("a released stream joined the registry again (%d holds)", live)
	}
}

// A Plan's frames kept for its write are named by the Slot running it, on
// the production path from admission to the write, and are gone with it.
// The snapshot reads from another goroutine while the Slot runs, as the
// publisher does; under the race detector that reader is the test.
func TestAPlansKeptFramesAreNamedByItsSlotWhileItWrites(t *testing.T) {
	fixture := newPlanIsolationFixture(t, nil)
	commitReady(fixture)
	state := withFrameRecording(fixture.coordinator, 1<<20)
	var atWrite [][]RetainedHolder
	state.atWrite = func() {
		atWrite = append(atWrite, readHolders(t, fixture.coordinator, -1, 0).Holders)
	}
	done, read := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(read)
		for {
			select {
			case <-done:
				return
			default:
				RetainedHolders(fixture.coordinator, 1)
			}
		}
	}()
	_, err := fixture.coordinator.finalizePrepared(context.Background(), fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated)
	close(done)
	<-read
	if err != nil {
		t.Fatalf("finalizePrepared() error = %v", err)
	}
	if len(atWrite) != 2 {
		t.Fatalf("%d writes, want both Plans'", len(atWrite))
	}
	for index, holders := range atWrite {
		if len(holders) != 1 {
			t.Fatalf("write %d: holders %+v, want the Slot alone", index, holders)
		}
		wantHolder(t, holders[0], fixture.request.Contract.Slot, map[string]uint64{RetainedKindFrames: oneFrame()})
	}
	if reading := readHolders(t, fixture.coordinator, -1, 0); reading.Total != 0 {
		t.Fatalf("after the Slot: %+v", reading)
	}
}
