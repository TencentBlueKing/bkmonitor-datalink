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

// Across the primary queries of one Slot: one emptied by the target is the
// claim, any one that withheld a series for another reason takes it back,
// and a primary that is not FULL and EMPTY never carries it.
func TestEveryPrimaryQueryHasToAgreeThatTheTargetEmptiedIt(t *testing.T) {
	primary := execution.NamedInputBinding{Role: execution.InputRolePrimary}
	empty := func(withheld, outside uint64) execution.PhysicalQueryCompletion {
		return execution.PhysicalQueryCompletion{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
			Withheld: withheld, WithheldOutsideTarget: outside}
	}
	fullEmpty := execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty}
	for _, test := range []struct {
		name    string
		queries []execution.PhysicalQueryCompletion
		fact    execution.PrimaryInputFact
		want    bool
	}{
		{"emptied by the target beside a query that returned nothing", []execution.PhysicalQueryCompletion{empty(2, 2), empty(0, 0)}, fullEmpty, true},
		{"beside a query that withheld for another reason", []execution.PhysicalQueryCompletion{empty(2, 2), empty(1, 0)}, fullEmpty, false},
		{"the other order", []execution.PhysicalQueryCompletion{empty(1, 0), empty(2, 2)}, fullEmpty, false},
		// Only a query that itself came back empty was emptied by the
		// target: one that kept data and withheld the rest was not.
		{"a query that kept data", []execution.PhysicalQueryCompletion{
			{Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Withheld: 2, WithheldOutsideTarget: 2}, empty(0, 0)}, fullEmpty, false},
		{"a primary that is not empty", []execution.PhysicalQueryCompletion{empty(2, 2)},
			execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, false},
		{"a primary that is not full", []execution.PhysicalQueryCompletion{empty(2, 2)},
			execution.PrimaryInputFact{Completeness: execution.CompletenessPartial, DataState: execution.DataStateEmpty}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var evidence queryAvailabilityEvidence
			for _, query := range test.queries {
				evidence.observe(primary, query, false, false)
			}
			if got := evidence.emptiedByTarget(test.fact); got != test.want {
				t.Fatalf("emptied by the target = %v, want %v", got, test.want)
			}
		})
	}
}
