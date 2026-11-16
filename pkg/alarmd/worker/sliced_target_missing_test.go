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
)

// A Query Group's probe at the end of its query cooldown asks only whether
// it can be read again: it is read whole, one request, though the Slots
// before it read the Query Group in slices and its memory holds them. The
// Slot before it, in slices, says the dimension its slices were cut on at
// their root; the probe, read whole, says none.
func TestAQueryCooldownProbeIsReadWholeWhereItsMemorySlices(t *testing.T) {
	fixture := largeSlicedFixture(t)
	request := windowedRequest()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the first Slot: result=%+v err=%v", result, err)
	}
	first := len(fixture.source.calls)
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || len(fixture.source.calls)-first < 2 || fixture.source.calls[first].slice.IsZero() {
		t.Fatalf("the Slot after: result=%+v err=%v, %d reads; want it read in slices", result, err, len(fixture.source.calls)-first)
	}
	if got := result.Usage.InputSliceDimension; got != "host" {
		t.Fatalf("the Slot in slices says it was cut on %q, want host", got)
	}
	probe := request
	probe.QueryCooldownProbe = true
	first = len(fixture.source.calls)
	result, err = fixture.coordinator.Execute(context.Background(), probe)
	if err != nil || !result.Completed {
		t.Fatalf("the probe: result=%+v err=%v", result, err)
	}
	if calls := fixture.source.calls[first:]; !calls[0].slice.IsZero() {
		t.Fatalf("the probe read %d slices, its first %v; want it read whole first", len(calls), calls[0].slice)
	}
	if got := result.Usage.InputSliceDimension; got != "" && len(fixture.source.calls)-first == 1 {
		t.Fatalf("the probe read whole says it was cut on %q", got)
	}
}

// A Slot every slice of which the provider answers with its target missing
// read nothing of the Query Group's size: what the reads before it kept of
// their slices is let go, and the next Slot reads the Query Group whole
// first, as one never read on this replica, not in the slices that would
// each be answered the same.
func TestASlotWhoseEverySliceIsTargetMissingLetsItsSlicesGo(t *testing.T) {
	fixture := largeSlicedFixture(t)
	request := windowedRequest()
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the first Slot: result=%+v err=%v", result, err)
	}
	fixture.source.mu.Lock()
	fixture.source.targetMissing = true
	fixture.source.mu.Unlock()
	first := len(fixture.source.calls)
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the Slot answered target missing: result=%+v err=%v", result, err)
	}
	if calls := fixture.source.calls[first:]; len(calls) < 2 || calls[0].slice.IsZero() {
		t.Fatalf("the Slot answered target missing read %d slices; want it read in the slices the one before kept", len(calls))
	}
	fixture.source.mu.Lock()
	fixture.source.targetMissing = false
	fixture.source.mu.Unlock()
	first = len(fixture.source.calls)
	if result, err := fixture.coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
		t.Fatalf("the Slot after: result=%+v err=%v", result, err)
	}
	if calls := fixture.source.calls[first:]; !calls[0].slice.IsZero() {
		t.Fatalf("the Slot after read %d slices first %v; want the Query Group read whole first", len(calls), calls[0].slice)
	}
}
