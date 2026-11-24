// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"testing"
	"time"
)

// A replica's live heap is shown beside what its pool held at the same
// collection; a pool that moved during the reading, or one not reported, is
// unmeasured rather than read as either side.
func TestTheOverviewReadsALiveHeapAgainstTheReservationOfItsOwnCollection(t *testing.T) {
	at := time.Date(2026, 11, 10, 21, 14, 13, 0, time.UTC)
	started := at.Add(-time.Hour)
	heap := uint64(560 << 20)
	cases := []struct {
		name     string
		reserved *LiveHeapReserved
		measured bool
		value    float64
	}{
		{"held through the collection", &LiveHeapReserved{AtGCStart: 400 << 20, AtGCEnd: 400 << 20}, true, 400 << 20},
		{"given back during it", &LiveHeapReserved{AtGCStart: 800 << 20, AtGCEnd: 0}, false, 0},
		{"nothing held", &LiveHeapReserved{}, true, 0},
		{"not reported", nil, false, 0},
	}
	for _, c := range cases {
		facts := &FlowFacts{QueryDuration: FlowHistogram{Buckets: make([]uint64, len(FlowQueryBounds))},
			DecisionLag:   FlowHistogram{Buckets: make([]uint64, len(FlowDecisionBounds))},
			RuntimeRedis:  FlowHistogram{Buckets: make([]uint64, len(FlowRedisBounds))},
			LiveHeapBytes: &heap, LiveHeapReserved: c.reserved}
		overview := overviewOf(flowHealth(at, started, facts), nil, false, at)
		newOverviewFlows().apply(&overview, flowHealth(at, started, facts), at)
		replica := overview.Resources.Replicas[0]
		if !replica.LiveHeapBytes.Measured || replica.LiveHeapBytes.Value != float64(heap) {
			t.Fatalf("%s: live heap = %+v, want 560 MiB", c.name, replica.LiveHeapBytes)
		}
		if got := replica.LiveHeapReservedBytes; got.Measured != c.measured || got.Value != c.value || got.Unit != "bytes" {
			t.Fatalf("%s: reserved at the collection = %+v, want measured %v at %.0f", c.name, got, c.measured, c.value)
		}
	}
}
