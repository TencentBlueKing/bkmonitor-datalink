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
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// liveHeapPeak samples the bytes of live heap objects while call runs, the
// collector set to run at every few percent of growth so that what it reads
// is close to what is live, and returns the most it read.
func liveHeapPeak(call func()) uint64 {
	previous := debug.SetGCPercent(5)
	defer debug.SetGCPercent(previous)
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	var peak uint64
	var mu sync.Mutex
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(time.Millisecond) // fixed-wait: the sampling interval of a measurement; it waits on no condition
		defer ticker.Stop()
		for {
			metrics.Read(sample)
			mu.Lock()
			peak = max(peak, sample[0].Value.Uint64())
			mu.Unlock()
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	call()
	close(done)
	<-finished
	return peak
}

// TestMeasureTheFirstNoDataWriteAgainstTheHeap holds what a first write is
// reserved at to the live heap it takes: a whole memory of N groups, built
// first and then written through the worker to a real Redis, the write's live
// heap peak over what was live with the statement built. The store writes a
// segment at a time, so that peak follows one segment and not the memory: it
// prints the peak, the segment the write is held at, and what the memory
// would hold written at once, and asserts the peak under a quarter of the
// last - which a write that built every group at once cannot pass, and the
// collector's slack at this heap size can. A measurement of hundreds of MiB,
// run on demand with ALARMD_MEASURE_NO_DATA_WRITE=1.
func TestMeasureTheFirstNoDataWriteAgainstTheHeap(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_NO_DATA_WRITE") == "" {
		t.Skip("a measurement: set ALARMD_MEASURE_NO_DATA_WRITE=1")
	}
	store, _ := redisNoDataStore(t)
	due := refusedMemoryDue(t)
	contractRef := noDataPreflightContract(t, due)
	for _, groups := range []int{100_000, 500_000} {
		coordinator, _ := observingCoordinator(store)
		coordinator.budget = ProvisionalBudget{MaxSeries: 1 << 20, MaxRetainedBytes: 1 << 40}
		mutation := wholeMemory(t, groups)
		var whole uint64
		for _, group := range mutation.Set {
			whole += noDataGroupWriteBytes(group)
		}
		counted := noDataFirstWriteSegmentBytes(mutation)
		runtime.GC()
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		metrics.Read(sample)
		before := sample[0].Value.Uint64()
		var writeErr error
		peak := liveHeapPeak(func() {
			writeErr = coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{
				Operation: execution.OperationNormal, Contract: contractRef,
			}, due, []execution.PlanNoDataMutation{mutation})
		})
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		live := peak - min(peak, before)
		t.Logf("groups=%d segment_counted=%d whole_at_once=%d live_peak=%d", groups, counted, whole, live)
		if live >= whole/4 {
			t.Errorf("groups=%d: the write's live peak %d is not under a quarter of the whole memory's %d; "+
				"it is not following one segment", groups, live, whole)
		}
	}
}
