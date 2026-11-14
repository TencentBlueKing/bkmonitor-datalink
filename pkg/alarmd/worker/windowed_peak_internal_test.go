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
	"fmt"
	"testing"
)

// A Slot applied window by window gives bytes back as its windows land, so
// what it holds at its end is less than it needed. The completion row reports
// what it needed - its peak, which capacity is sized from - with each phase's
// share taken at that peak, so the phases still add up to it.
func TestTheCompletionRowReportsAWindowedSlotsPeakWithPhasesThatAddUp(t *testing.T) {
	fixture := newWindowedFixture(t)
	stream, coordinator := fixture.stream, fixture.stream.coordinator
	const series = 10
	const inputBytes, viewBytes = uint64(30_000), uint64(100_000)
	names := make([]string, series)
	for index := range names {
		names[index] = fmt.Sprintf("series-%02d", index)
		fixture.streamInput(t, names[index], inputBytes)
	}
	var held uint64
	for index, name := range names {
		fixture.mergeSeries(t, index, name, viewBytes)
		held = max(held, stream.retainedTotal())
	}
	if err := coordinator.applyWindow(context.Background(), stream, true); err != nil {
		t.Fatal(err)
	}
	if stream.windows.count < 2 {
		t.Fatalf("windows = %d, want the Slot cut by its bytes", stream.windows.count)
	}
	usage := stream.budgetUsage()
	if usage.RetainedBytes < held || usage.RetainedBytes > coordinator.qgShareBytes() {
		t.Fatalf("reported %d retained, want the peak: at least the %d seen after a merge and within the share %d",
			usage.RetainedBytes, held, coordinator.qgShareBytes())
	}
	if end := stream.retainedTotal(); usage.RetainedBytes <= end {
		t.Fatalf("reported %d retained, the Slot holds %d at its end: the row reports its end, not its peak", usage.RetainedBytes, end)
	}
	if sum := usage.RetainedInputBytes + usage.RetainedGapBytes + usage.RetainedOutputBytes + usage.RetainedStateBytes; sum != usage.RetainedBytes {
		t.Fatalf("phases add up to %d, the total is %d: they were not taken at the peak", sum, usage.RetainedBytes)
	}
}
