// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Observe is called from every Slot's goroutine at once, and the object
// detail and the page read the tracker from others. A query's completion -
// the ranges it asked, the answer it may have cut - is kept for an object
// the tracker may not have seen yet, which adds the object to the table:
// that write is inside the tracker's lock like every other. Under -race a
// write outside it fails this whatever the timing: each goroutine's first
// write comes before any lock it takes, so nothing orders the writes of two
// of them.
func TestRoundsOnManyGoroutinesAndAReaderShareTheTracker(t *testing.T) {
	tracker := NewTracker(nil, "replica-1", nil)
	const writers = 8
	group := func(index int) string { return fmt.Sprintf("qg-%d", index) }
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < writers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			tracker.Observe(context.Background(), observability.Observation{Component: observability.ComponentAccess,
				Stage: observability.StageQueryCompleted, Result: observability.ResultSuccess,
				QueryRanges:     []observability.QueryRangeFacts{{Digest: "query-a", AskedSeconds: 600, AcceptedSeconds: 60}},
				QueryTruncation: []observability.QueryTruncationFacts{{Source: "custom/event", Dimension: "tags.host"}},
				Trace:           observability.TraceFields{QueryGroupKey: group(index), StrategyID: "410", BusinessID: "2", EvaluationTime: 60}})
		}(index)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for index := 0; index < writers; index++ {
			tracker.QueryRanges(group(index))
		}
		_ = tracker.Tracked()
	}()
	close(start)
	wg.Wait()
	for index := 0; index < writers; index++ {
		if _, known := tracker.QueryRanges(group(index)); !known {
			t.Fatalf("%s: the ranges its round asked are not kept", group(index))
		}
	}
}
