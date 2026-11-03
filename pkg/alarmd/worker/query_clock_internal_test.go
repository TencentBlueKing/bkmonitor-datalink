// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The clock a Slot reports is its slowest PRIMARY query's, whatever order the
// queries come back in: that is the one a backend's answer time is read
// from. A dependency query's clock and an unmeasured query do not count.
func TestTheSlotsQueryClockIsItsSlowestPrimary(t *testing.T) {
	primary := execution.NamedInputBinding{Role: execution.InputRolePrimary}
	dependency := execution.NamedInputBinding{Role: execution.InputRoleAlgorithmDependency}
	clocked := func(budget, elapsed int64) execution.PhysicalQueryCompletion {
		return execution.PhysicalQueryCompletion{Completeness: execution.CompletenessFull,
			Clock: &execution.PhysicalQueryClock{BudgetMillis: budget, ElapsedMillis: elapsed}}
	}
	for _, order := range [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {1, 3, 0, 2}} {
		bindings := []execution.NamedInputBinding{primary, primary, dependency, primary}
		physical := []execution.PhysicalQueryCompletion{clocked(25_000, 3_000), clocked(24_000, 9_000), clocked(55_000, 50_000),
			{Completeness: execution.CompletenessFull}}
		var evidence queryAvailabilityEvidence
		for _, index := range order {
			evidence.observe(bindings[index], physical[index], true, true)
		}
		if evidence.clock == nil || *evidence.clock != (execution.PhysicalQueryClock{BudgetMillis: 24_000, ElapsedMillis: 9_000}) {
			t.Fatalf("order %v: clock %+v, want the slowest primary's {24000 9000}", order, evidence.clock)
		}
	}
}
