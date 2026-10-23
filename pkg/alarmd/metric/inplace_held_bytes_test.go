// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// What an execution held while it waited in place is a distribution a scrape
// reads, not only a field on slot_completed lines the log admission drops:
// each waiting execution is one observation of its retained input bytes, and
// an execution that did not wait is none.
func TestInPlaceHeldInputBytesIsADistributionOfWaitingExecutions(t *testing.T) {
	const name = "bkmonitor_alarmd_access_inplace_held_input_bytes"
	recorder := NewRecorder(BuildInfo{})
	ctx := context.Background()
	for _, held := range []uint64{3_000, 2_000_000, 0} {
		recorder.Observe(ctx, observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted, Result: observability.ResultSuccess,
			InPlaceWait: &observability.InPlaceWaitFacts{Waits: 1, TotalMillis: 6_000, HeldInputBytes: held},
		})
	}
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted, Result: observability.ResultSuccess,
	})
	cells := gatherFamily(t, recorder, name)
	if len(cells) != 1 {
		t.Fatalf("%s = %v, want one histogram", name, cells)
	}
	histogram := cells[0].GetHistogram()
	if histogram.GetSampleCount() != 3 || histogram.GetSampleSum() != 2_003_000 {
		t.Fatalf("count %d sum %v, want 3 waiting executions summing 2003000 bytes", histogram.GetSampleCount(), histogram.GetSampleSum())
	}
	want := map[float64]uint64{1024: 1, 4096: 2, 1048576: 2, 4194304: 3, 268435456: 3}
	for _, bucket := range histogram.GetBucket() {
		if expected, ok := want[bucket.GetUpperBound()]; ok && bucket.GetCumulativeCount() != expected {
			t.Fatalf("bucket le=%v = %d, want %d", bucket.GetUpperBound(), bucket.GetCumulativeCount(), expected)
		}
		delete(want, bucket.GetUpperBound())
	}
	if len(want) != 0 {
		t.Fatalf("buckets %v missing", want)
	}
}
