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
	"strings"
	"testing"
	"time"
)

// Strategies whose interval does not hold the measured post-query tail are a warning on the overview, naming them and
// saying they keep running; with none, there is no notice.
func TestOverviewWarnsOfTightIntervalsWithoutStoppingThem(t *testing.T) {
	health := HealthResponse{PerReplica: []ReplicaView{
		{Replica: "a", Capacity: &Capacity{Shape: &SchedulerShape{PostQueryTailMillis: 12_000, LiveReserveFallbacks: 3,
			TightQueryGroups: []string{"qg-1"}, TightStrategies: []StrategyRef{{StrategyID: "42", BusinessID: "2"}}}}},
		{Replica: "b", Capacity: &Capacity{Shape: &SchedulerShape{PostQueryTailMillis: 4_000,
			TightQueryGroups: []string{"qg-2"}, TightStrategies: []StrategyRef{{StrategyID: "7", BusinessID: "2"}, {StrategyID: "42", BusinessID: "2"}}}}},
		{Replica: "c"},
	}}
	notice, tight := overviewTightIntervalNotice(health)
	if !tight || notice.Kind != "interval_tight" {
		t.Fatalf("got %+v tight=%v", notice, tight)
	}
	for _, want := range []string{"2 个检测对象", "12s", "检测照常进行", "涉及策略 42、7"} {
		if !strings.Contains(notice.Line, want) {
			t.Fatalf("line %q lacks %q", notice.Line, want)
		}
	}
	overview := overviewOf(health, nil, false, time.Unix(0, 0))
	found := false
	for _, item := range overview.Runtime.Attention {
		found = found || item.Kind == "interval_tight"
	}
	if !found {
		t.Fatalf("the overview carries no tight-interval notice: %+v", overview.Runtime.Attention)
	}

	if _, tight := overviewTightIntervalNotice(HealthResponse{PerReplica: []ReplicaView{
		{Replica: "a", Capacity: &Capacity{Shape: &SchedulerShape{PostQueryTailMillis: 3_000}}}}}); tight {
		t.Fatal("no fallback, no notice")
	}
}
