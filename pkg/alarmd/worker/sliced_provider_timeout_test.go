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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The provider's own timeout and alarmd's deadline, as production wiring
// returns them (slicedSource.timedOutRead): only the first is evidence of a
// slice too large.

// largeSlicedFixture is a Query Group past half its share, read in slices
// once a Slot has read it: 24 series of 40 KiB.
func largeSlicedFixture(t *testing.T) *slicedFixture {
	t.Helper()
	series := make([]slicedSeries, 24)
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	for index := range series {
		series[index] = slicedSeries{host: "node-" + string(alphabet[index*len(alphabet)/len(series)]), padding: 40 << 10}
	}
	fixture := newSlicedFixture(t, series, sliceableFacts(), 200)
	fixture.source.queryMillis = 1_000
	return fixture
}

// A Query Group never read on this replica, read whole and timed out by
// the provider (UQ's 504 at its 30 s), completes degraded and is read in
// slices next: two asked, the backend's limit over a slice's target, the
// time being all it says of the size - a starting tree's one split. Under
// the query cooldown nothing is learned from it, and it is read whole again.
func TestAQueryGroupOfUnknownSizeTimedOutWholeByTheProviderIsReadInSlicesNext(t *testing.T) {
	for _, test := range []struct {
		name     string
		cooldown bool
	}{{"learned", false}, {"under the cooldown", true}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSlicedFixture(t, slicedHosts(8, 1<<10), sliceableFacts(), 200)
			fixture.source.timeoutWhole, fixture.source.queryMillis = true, 1_000
			request := windowedRequest()
			request.QueryCooldownHeld = test.cooldown
			readWholeTimedOut(t, fixture, request)
			fixture.source.timeoutWhole = false
			request.QueryCooldownHeld = false
			first := len(fixture.source.calls)
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err != nil || !result.Completed {
				t.Fatalf("next Slot result=%+v err=%v, want it completed", result, err)
			}
			calls := fixture.source.calls[first:]
			sliced := len(calls) > 1 && !calls[0].slice.IsZero()
			if sliced == test.cooldown {
				t.Fatalf("next Slot read in slices %t (%d reads), want %t", sliced, len(calls), !test.cooldown)
			}
			if sliced && len(calls) > 5 {
				t.Fatalf("next Slot read %d slices, want the starting tree two asked make (5)", len(calls))
			}
			// No read delivered a series to choose a dimension by: the
			// first is all there is.
			if sliced && result.Usage.InputSliceCutOn != observability.InputSliceCutOnFirstDimension {
				t.Fatalf("next Slot cut on %q, want %s", result.Usage.InputSliceCutOn, observability.InputSliceCutOnFirstDimension)
			}
		})
	}
}

// One slice the provider times out while the slices before it answered in
// a second is too large for its time: it is let go and split, its children
// read in its place, and the split is kept in the Query Group's shape.
func TestASliceTheProviderTimesOutAmongQuickOnesIsSplitAndKept(t *testing.T) {
	fixture := largeSlicedFixture(t)
	request := windowedRequest()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the Slot before: result=%+v err=%v", result, err)
	}
	fixture.source.mu.Lock()
	fixture.source.answered, fixture.source.timeoutAfter, fixture.source.timeoutTimes = 0, 1, 1
	fixture.source.mu.Unlock()
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceTimeouts != 1 || result.Usage.InputSliceShapeKept != 1 {
		t.Fatalf("result=%+v err=%v, want it completed with the slice split for its time and kept", result, err)
	}
}

// Every slice the provider times out is a backend slow as a whole: no slice
// is split for it or kept, each completes degraded, and the next Slot asks
// the same slices - the query cooldown is what holds it.
func TestEverySliceTheProviderTimesOutSplitsNothing(t *testing.T) {
	fixture := largeSlicedFixture(t)
	request := windowedRequest()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the Slot before: result=%+v err=%v", result, err)
	}
	slowBackend(fixture)
	var asked []string
	for round := 1; round <= 2; round++ {
		first := len(fixture.source.calls)
		result, err := fixture.coordinator.Execute(context.Background(), request)
		if err != nil || !result.Completed || result.Usage.InputSliceTimeouts != 0 || result.Usage.InputSliceShapeKept != 0 {
			t.Fatalf("round %d: result=%+v err=%v, want it completed degraded, nothing split or kept", round, result, err)
		}
		var slices []string
		for _, call := range fixture.source.calls[first:] {
			slices = append(slices, call.slice.Digest())
		}
		if asked != nil && !reflect.DeepEqual(slices, asked) {
			t.Fatalf("round %d asked %v, round 1 %v: want the same slices", round, slices, asked)
		}
		asked = slices
	}
}

// A read alarmd's deadline ended while its answer's body was read is the
// window's, not the slice's, however quick the slices before it: it is not
// split or kept, the Slot fails as not split, and the next Slot reads the
// slices the one before read, no slicing learned from it.
func TestASliceTheDeadlineEndsIsNoSliceTooLarge(t *testing.T) {
	fixture := largeSlicedFixture(t)
	request := windowedRequest()
	first := len(fixture.source.calls)
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the Slot before: result=%+v err=%v", result, err)
	}
	before := answeredSlices(fixture.source.calls[first:])
	fixture.source.mu.Lock()
	fixture.source.timeoutKind = timeoutBody
	fixture.source.answered, fixture.source.timeoutAfter, fixture.source.timeoutTimes = 0, 1, 1
	fixture.source.mu.Unlock()
	observed := len(*fixture.observations)
	_, err := fixture.coordinator.Execute(context.Background(), request)
	if err == nil {
		t.Fatal("the Slot whose slice the deadline ended completed")
	}
	var ended []string
	for _, observation := range (*fixture.observations)[observed:] {
		if facts := observation.QueryFailure; facts != nil && facts.SliceTimeout != "" {
			ended = append(ended, facts.SliceTimeout)
		}
	}
	if len(ended) != 1 || ended[0] != observability.InputSliceTimeoutNotSplit {
		t.Fatalf("timed-out slices ended %v (err %v), want %s once", ended, err, observability.InputSliceTimeoutNotSplit)
	}
	fixture.source.mu.Lock()
	fixture.source.timeoutTimes = 0
	fixture.source.timedOut = nil
	fixture.source.mu.Unlock()
	first = len(fixture.source.calls)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Usage.InputSliceShapeStarted != 0 {
		t.Fatalf("the Slot after: result=%+v err=%v, want it completed in no shape", result, err)
	}
	if got := answeredSlices(fixture.source.calls[first:]); !reflect.DeepEqual(got, before) {
		t.Fatalf("the Slot after read %v, want the slices the one before read %v", got, before)
	}
}
