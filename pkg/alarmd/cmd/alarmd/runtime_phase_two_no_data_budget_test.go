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

// The per-Slot State cap is a window's bound, not the Slot's (decision-020
// batch 8, 2.1): a no-data Plan whose series do not fit beside the threshold
// ones is written in the next window, not skipped, and the threshold results
// are committed as they are without it.
//
// Host A's threshold series takes one State write. The no-data round makes
// three series: host A present, host B absent, and the whole item present. A
// cap of three fits the no-data series alone and not beside A's series, so
// the Slot is written in two windows; a cap of four fits all four in one.
// Either way the round is EVALUATED, host B's absence starts at this round,
// and all four series are in the store.
func TestANoDataPlanSpendsOnlyWhatThresholdDetectionLeft(t *testing.T) {
	for _, test := range []struct {
		name string
		cap  uint64
	}{{"does not fit beside the threshold series", 3}, {"fits beside the threshold series", 4}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := startFollowsFixture(t, test.cap)
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
