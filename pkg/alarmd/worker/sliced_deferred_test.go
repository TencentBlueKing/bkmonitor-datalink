// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A slice the provider times out with no slice read before it in the Slot
// cannot be put on its size, and completes degraded; the next Slot reads it
// after the others, against which it is judged as any slice is
// (sliceReading.pending). A Slot some of whose slices the provider timed out
// completes COMPLETED_WITH_UNAVAILABLE; its QueryAvailability is
// UNAVAILABLE only when no slice answered (execution QueryAvailability).

// cloudZeroFixture is largeSlicedFixture grouped by cloud and host, cloud
// "0" throughout, whose reads the provider times out from holding series
// on: 24 hosts node-<c>, six in each class of the fixed alphabet.
func cloudZeroFixture(t *testing.T, holding int) *slicedFixture {
	t.Helper()
	fixture := largeSlicedFixture(t)
	for index := range fixture.source.series {
		fixture.source.series[index].cloud = "0"
	}
	fixture.source.facts = cloudHostFacts()
	fixture.source.timeoutBySize, fixture.source.timeoutHolding = true, holding
	return fixture
}

// A Query Group never read, cloud "0" throughout, the provider timing out a
// read of 13 series or more - the whole Query Group, 24, and nothing of six.
//
//   - Slot 1 reads it whole: timed out, degraded, its size unknown; the next
//     is sliced by that time, two slices, on its first dimension, no read
//     having delivered a series to choose another by.
//   - Slot 2: cut on cloud by the fixed alphabet, "0" puts all 24 in [0-8],
//     the first slice made and read: timed out with none before it, planned
//     at 30 s over two slices, 15 s, which is not over a slice's target -
//     degraded, as before; the four others empty.
//   - Slot 3: [0-8] read last, after four empty slices: its 30 s is twice
//     their median and more, split for its time, with no sample (a read
//     timed out delivers none) on cloud further - "0", one character, all in
//     the exhausted child, read after the four others made with it, and
//     timed out again: a Slot splits so once, and it fails, that slice split
//     into its shape - on host, cloud having no character left.
//   - Slot 3 again: in its shape, host's four classes six series each, none
//     timed out: completed, every query answered.
//
// Read first every Slot, as before, [0-8] degrades every Slot for good.
func TestAFirstDimensionOfOneValueTimedOutWholeIsReadByTheThirdSlot(t *testing.T) {
	fixture := cloudZeroFixture(t, 13)
	fixture.source.queryMillis = 1_000
	request := windowedRequest()
	readWholeTimedOut(t, fixture, request)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.CompletionKind != execution.CompletionUnavailable ||
		result.Usage.InputSliceCutOn != observability.InputSliceCutOnFirstDimension {
		t.Fatalf("Slot 2: result=%+v err=%v, want it completed degraded, cut on its first dimension", result, err)
	}
	if _, err := fixture.coordinator.Execute(context.Background(), request); err == nil {
		t.Fatal("Slot 3 completed, want it failed with the slice read last split and timed out again")
	}
	result, err = fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.QueryAvailability != execution.QueryAvailabilityAvailable ||
		result.Usage.InputSliceShapeStarted != 1 {
		t.Fatalf("Slot 3 again: result=%+v err=%v, want it completed in its shape, every query answered", result, err)
	}
}

// A backend that times out every read, whatever it holds: every slice of
// Slot 2 times out with none or one slow one before it - 30 s against a
// median of 30 s is not twice - and completes degraded, as does Slot 3,
// every slice read last for it, in the same order. Three Slots in a row
// degraded are what the scheduler takes a Query Group into its query
// cooldown on (unavailableThreshold); under it, nothing is split either.
// Nothing is split, kept or discarded at any point, and the slices asked
// stay the same: it stops there.
func TestABackendThatTimesOutEveryReadStopsDegradedUnsplit(t *testing.T) {
	fixture := cloudZeroFixture(t, 0)
	fixture.source.queryMillis = 1_000
	request := windowedRequest()
	readWholeTimedOut(t, fixture, request)
	var asked map[string]bool
	for slot := 2; slot <= 4; slot++ {
		request.QueryCooldownHeld = slot == 4
		first := len(fixture.source.calls)
		result, err := fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed || result.CompletionKind != execution.CompletionUnavailable ||
			result.Usage.InputSliceTimeouts != 0 || result.Usage.InputSliceShapeKept != 0 || result.Usage.InputSlicesDiscarded != 0 {
			t.Fatalf("Slot %d: result=%+v err=%v, want it completed degraded, nothing split, kept or discarded", slot, result, err)
		}
		if slot == 3 && result.Usage.InputSlicesDeferred != 5 {
			t.Fatalf("Slot 3 read %d slices last, want all five Slot 2 degraded", result.Usage.InputSlicesDeferred)
		}
		slices := map[string]bool{}
		for _, call := range fixture.source.calls[first:] {
			slices[call.slice.Digest()] = true
		}
		if asked != nil && !equalSets(slices, asked) {
			t.Fatalf("Slot %d asked %d slices, Slot 2 %d: want the same", slot, len(slices), len(asked))
		}
		asked = slices
	}
}

// A Query Group read before - its size known - whose slice the provider
// times out after one that took 20 s: 30 s is not twice that, and the slice
// is not put on its size, read last or not; it completes degraded, unsplit,
// the Slot after reading it last and degrading it again.
func TestAKnownQueryGroupsSliceUnderTwiceTheMedianIsNoSliceTooLarge(t *testing.T) {
	fixture := largeSlicedFixture(t)
	fixture.source.queryMillis = 20_000
	request := windowedRequest()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the Slot before: result=%+v err=%v", result, err)
	}
	fixture.source.mu.Lock()
	fixture.source.answered, fixture.source.timeoutAfter, fixture.source.timeoutTimes = 0, 1, 1
	fixture.source.mu.Unlock()
	for slot, deferred := range []uint64{0, 1} {
		result, err := fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed || result.CompletionKind != execution.CompletionUnavailable ||
			result.Usage.InputSliceTimeouts != 0 || result.Usage.InputSliceShapeKept != 0 || result.Usage.InputSlicesDeferred != deferred {
			t.Fatalf("Slot %d after: result=%+v err=%v, want it completed degraded, unsplit, %d read last", slot+1, result, err, deferred)
		}
	}
}

func equalSets(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if !right[key] {
			return false
		}
	}
	return true
}
