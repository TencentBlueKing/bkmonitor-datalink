// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/racedetector"
)

// The budgets a deployment with a 30-second MinReadyDelay and a 5-second
// reserve gives its periods: 10s (inert), 30s, 60s, 120s and 300s.
var readPhaseBudgets = []time.Duration{5 * time.Second, 25 * time.Second, 55 * time.Second, 115 * time.Second, 295 * time.Second}

func TestAReadNeverComesBeforeItsSettlingWaitAndLeavesMostOfTheBudget(t *testing.T) {
	const configured = 30 * time.Second
	for _, budget := range readPhaseBudgets {
		settle := min(SettlingWaitWithinQueryBudget(budget, configured), MinimumSettlingWait)
		spread := min((budget-settle)*2/5, settle)
		for i := 0; i < 2000; i++ {
			queryGroup := QueryGroupIdentity(fmt.Sprintf("qg-%d", i))
			wait := ReadWaitWithinQueryBudget(queryGroup, budget, configured)
			if wait < settle {
				t.Fatalf("budget %v %s: read at %v, before the settling wait %v", budget, queryGroup, wait, settle)
			}
			if spread <= 0 {
				if wait != settle {
					t.Fatalf("budget %v %s: an inert schedule was staggered to %v", budget, queryGroup, wait)
				}
				continue
			}
			if wait-settle >= spread {
				t.Fatalf("budget %v %s: phase %v is not inside its spread %v", budget, queryGroup, wait-settle, spread)
			}
			if left := budget - wait; left*5 < (budget-settle)*3 {
				t.Fatalf("budget %v %s: only %v of %v left after the read", budget, queryGroup, left, budget-settle)
			}
		}
	}
}

func TestTheSixtySecondPeriodSpreadsOverTenSeconds(t *testing.T) {
	const configured = 30 * time.Second
	var latest time.Duration
	for i := 0; i < 2000; i++ {
		latest = max(latest, ReadWaitWithinQueryBudget(QueryGroupIdentity(fmt.Sprintf("qg-%d", i)), 55*time.Second, configured))
	}
	// Two thousand groups over ten thousand milliseconds after the 10 s
	// floor: the latest lands in the last half second unless the spread is
	// narrower than designed.
	if latest < 19500*time.Millisecond || latest >= 20*time.Second {
		t.Fatalf("latest read of a 60s group = %v, want just under 20s", latest)
	}
}

func TestAQueryGroupReadsAtTheSameMomentEveryTime(t *testing.T) {
	// A pure function of the group and its schedule: a restarted process, or
	// the replica a group is handed to, reads it at the same moment. The
	// value is pinned so a change of hash is a decision, not an accident.
	queryGroup := QueryGroupIdentity("qg:tenant/2/1001")
	first := ReadWaitWithinQueryBudget(queryGroup, 55*time.Second, 30*time.Second)
	for i := 0; i < 3; i++ {
		if again := ReadWaitWithinQueryBudget(queryGroup, 55*time.Second, 30*time.Second); again != first {
			t.Fatalf("read wait moved from %v to %v", first, again)
		}
	}
	if first != readPhasePinned {
		t.Fatalf("read wait of %s = %v, pinned %v", queryGroup, first, readPhasePinned)
	}
}

func TestReadPhasesSpreadEvenlyOverTheirSeconds(t *testing.T) {
	const groups = 10000
	var buckets [10]int
	for i := 0; i < groups; i++ {
		phase := ReadWaitWithinQueryBudget(QueryGroupIdentity(fmt.Sprintf("bk_monitor/time_series/%d", i)), 55*time.Second, 30*time.Second) - MinimumSettlingWait
		buckets[phase/time.Second]++
	}
	for second, count := range buckets {
		if count > groups/10*3/2 || count < groups/10/2 {
			t.Fatalf("second %d holds %d of %d groups: %v", second, count, groups, buckets)
		}
	}
}

func TestReadingTheReadWaitAllocatesNothing(t *testing.T) {
	queryGroup := QueryGroupIdentity("qg:tenant/2/1001")
	if allocations := testing.AllocsPerRun(100, func() { _ = ReadWaitWithinQueryBudget(queryGroup, 55*time.Second, 30*time.Second) }); !racedetector.Enabled && allocations != 0 {
		t.Fatalf("allocations = %v", allocations)
	}
}

// readPhasePinned is the read wait of qg:tenant/2/1001 on a 60-second schedule.
const readPhasePinned = 11311 * time.Millisecond

// The deployment's wait is the floor only when it is shorter than the
// product's: a deployment that says its data lands in a millisecond is read
// then, and one that says thirty seconds is read at the floor, its groups
// held the rest by what they measure.
func TestTheSettlingFloorIsTheShorterOfTheDeploymentsAndTheProducts(t *testing.T) {
	if got := ReadWaitWithinQueryBudget("qg", 55*time.Second, time.Millisecond); got >= MinimumSettlingWait {
		t.Fatalf("a 1 ms deployment wait read at %v", got)
	}
	if got := ReadWaitWithinQueryBudget("qg", 55*time.Second, 30*time.Second); got < MinimumSettlingWait || got >= 2*MinimumSettlingWait {
		t.Fatalf("a 30 s deployment wait read at %v, want the 10 s floor and a phase", got)
	}
}
