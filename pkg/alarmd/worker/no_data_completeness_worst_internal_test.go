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

// The completeness a Plan's round is judged under is the worst of its
// bindings, whichever order they come in: UNAVAILABLE (nothing came back) is
// worse than PARTIAL (some of it did), and both are worse than FULL.
func TestOnePlansCompletenessIsTheWorstOfItsBindings(t *testing.T) {
	const (
		full        = execution.CompletenessFull
		partial     = execution.CompletenessPartial
		unavailable = execution.CompletenessUnavailable
	)
	for _, pair := range []struct{ first, second, worst execution.Completeness }{
		{full, partial, partial}, {partial, full, partial},
		{full, unavailable, unavailable}, {unavailable, full, unavailable},
		{partial, unavailable, unavailable}, {unavailable, partial, unavailable},
	} {
		due := noDataWiredPlan(t)
		stream := noDataWiredStream(t, due, &emptyNoDataStore{})
		stream.bindings = []execution.NamedInputBinding{
			{Consumer: execution.ConsumerRef{Plan: due.Identity, LevelID: 5, HasLevel: true}, RequirementID: "a_primary", Completeness: pair.first},
			{Consumer: execution.ConsumerRef{Plan: due.Identity, LevelID: 5, HasLevel: true}, RequirementID: "b_secondary", Completeness: pair.second},
		}
		if got := stream.noDataCompleteness(due); got != pair.worst {
			t.Errorf("bindings %s then %s: completeness %s, want the worst, %s", pair.first, pair.second, got, pair.worst)
		}
	}
}
