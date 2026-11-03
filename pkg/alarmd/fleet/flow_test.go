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
	"encoding/json"
	"testing"
)

// The flow block stays within its bound at its largest: every counter and
// bucket at a process's lifetime ceiling -- 10^12, a year at thirty thousand
// a second, since the counters start over with the process -- every failure
// cause and every role.
const flowCeiling = uint64(1_000_000_000_000)

func TestTheFlowBlockStaysWithinItsBound(t *testing.T) {
	full := func(bounds []float64) FlowHistogram {
		histogram := FlowHistogram{Buckets: make([]uint64, len(bounds)), Count: flowCeiling}
		for index := range histogram.Buckets {
			histogram.Buckets[index] = flowCeiling
		}
		return histogram
	}
	heap := flowCeiling
	facts := FlowFacts{Queries: flowCeiling, QueryDuration: full(FlowQueryBounds), Detections: flowCeiling,
		DecisionLag: full(FlowDecisionBounds), StateWrites: flowCeiling, StateFailures: flowCeiling,
		RuntimeRedis: full(FlowRedisBounds), EventsEmitted: flowCeiling, EventsRejected: flowCeiling,
		LiveHeapBytes: &heap, Hosts: flowCeiling,
		QueryFailures: map[string]uint64{"timeout": flowCeiling, "refused": flowCeiling, "not_found": flowCeiling,
			"transport": flowCeiling, "decode": flowCeiling, "other": flowCeiling},
		Roles: []string{"control", "worker", "channel"}}
	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if buckets := len(FlowQueryBounds) + len(FlowDecisionBounds) + len(FlowRedisBounds); buckets != 37 || len(encoded) > FlowFactsMaxBytes {
		t.Fatalf("flow block is %d bytes with %d buckets, want at most %d with 37", len(encoded), buckets, FlowFactsMaxBytes)
	}
}
