// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"
)

// The no-data Plan spends from the State budget threshold detection left, not
// from the whole of it (decomposition 5.9 item 2): when its series do not fit
// in what is left, the Plan is skipped by name, and the Slot's threshold
// results are committed as they would have been without it.
//
// Host A's threshold series takes one State write. The no-data round makes
// three series: host A present, host B absent, and the whole item present. A
// budget of three fits the no-data series alone and not beside A's series,
// and a budget of four fits both. Under three, a check that counts only the
// no-data series lets them in, and the Slot-wide cap then refuses the round:
// every result is replaced by a budget gap, threshold included, and the memory
// the discarded round decided is still applied.
func TestANoDataPlanSpendsOnlyWhatThresholdDetectionLeft(t *testing.T) {
	t.Run("does not fit beside the threshold series", func(t *testing.T) {
		fixture := startFollowsFixture(t, 3)
		if !fixture.attempt(1, 0) {
			t.Fatal("round 1 was not attempted")
		}
		if outcomes := fixture.noDataOutcomes(); outcomes["SKIPPED_SLOT_BUDGET"] != 1 || outcomes["EVALUATED"] != 0 {
			t.Fatalf("no-data outcomes = %v, want the Plan skipped by name as SKIPPED_SLOT_BUDGET", outcomes)
		}
		if value, held := fixture.memoryOf(followsGroupKey(followsHostB)); held {
			t.Fatalf("the skipped round's memory was applied: host B = %q", value)
		}
		if sent := fixture.sink.noDataEventsFor(followsHostB); len(sent) != 0 {
			t.Fatalf("a skipped round sent host B's events: %+v", sent)
		}
		if got := fixture.runtimeKeys(); got != 1 {
			t.Fatalf("Runtime State keys = %d, want 1: host A's threshold series, written as it would have been "+
				"without the no-data Plan, and no no-data series", got)
		}
	})
	t.Run("fits beside the threshold series", func(t *testing.T) {
		fixture := startFollowsFixture(t, 4)
		if !fixture.attempt(1, 0) {
			t.Fatal("round 1 was not attempted")
		}
		if outcomes := fixture.noDataOutcomes(); outcomes["EVALUATED"] != 1 || outcomes["SKIPPED_SLOT_BUDGET"] != 0 {
			t.Fatalf("no-data outcomes = %v, want EVALUATED", outcomes)
		}
		if got, want := fixture.firstAbsentOf(followsGroupKey(followsHostB)), fixture.evaluationAt(1); got != want {
			t.Fatalf("host B's absence starts at %d; want %d", got, want)
		}
		if got := fixture.runtimeKeys(); got != 4 {
			t.Fatalf("Runtime State keys = %d, want 4: host A's threshold series and the three no-data series", got)
		}
	})
}

// runtimeKeys is how many Runtime State keys the store holds: one per series
// whose State was written, the threshold series and the no-data ones alike.
func (fixture *followsFixture) runtimeKeys() int {
	fixture.t.Helper()
	keys, err := fixture.redis.Keys(context.Background(), fixture.prefix+":runtime3:v2:*").Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	return len(keys)
}
