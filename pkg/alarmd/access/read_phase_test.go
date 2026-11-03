// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestQueryGroupsOnOneScheduleAreReadAtTheirOwnMoments(t *testing.T) {
	ref, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC"}
	window := execution.QueryWindow{Start: int64(ref.Slot.EvaluationTime) - 60, End: int64(ref.Slot.EvaluationTime)}
	settled := window.End*1000 + execution.MinimumSettlingWait.Milliseconds()
	moments := map[int64]bool{}
	for i := 0; i < 50; i++ {
		ref.Slot.QueryGroup = execution.QueryGroupIdentity(fmt.Sprintf("group-%d", i))
		ready, err := frozenConsumerReadyAt(ref, requirement, window, spec, 30*time.Second, 5*time.Second, 0)
		if err != nil {
			t.Fatal(err)
		}
		if ready < settled || ready >= settled+10_000 {
			t.Fatalf("%s ready %d ms after its window, want [10000, 20000)", ref.Slot.QueryGroup, ready-window.End*1000)
		}
		if want := window.End*1000 + execution.ReadWaitWithinQueryBudget(ref.Slot.QueryGroup, 55*time.Second, 30*time.Second).Milliseconds(); ready != want {
			t.Fatalf("%s ready %d, the shared rule says %d", ref.Slot.QueryGroup, ready, want)
		}
		moments[ready] = true
	}
	if len(moments) < 40 {
		t.Fatalf("50 groups read at only %d moments", len(moments))
	}
}

func TestAReadHoldAddsToTheGroupsOwnMoment(t *testing.T) {
	ref, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC"}
	window := execution.QueryWindow{Start: int64(ref.Slot.EvaluationTime) - 60, End: int64(ref.Slot.EvaluationTime)}
	ref.Slot.QueryGroup = "group-7"
	ref.ReadHoldMillis = 60_000
	ready, err := frozenConsumerReadyAt(ref, requirement, window, spec, 30*time.Second, 5*time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := window.End*1000 + execution.ReadWaitWithinQueryBudget("group-7", 55*time.Second, 30*time.Second).Milliseconds() + 60_000; ready != want {
		t.Fatalf("held ready %d, want %d", ready, want)
	}
}

// unphasedQueryGroup is the group the readiness tests read: its FNV-1a is a
// multiple of 120,000, so its phase is zero on every spread that divides two
// minutes (the 6, 10 and 30 second spreads among them) and those tests see
// the settling rule alone. The phase has its own tests above.
const unphasedQueryGroup execution.QueryGroupIdentity = "group-20764"
