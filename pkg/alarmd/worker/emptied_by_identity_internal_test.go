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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Across the primary queries of one Slot: every withheld series one the
// target's identity could not read is the claim; any query that withheld a
// series for another reason, or kept data, takes it back; a primary that is
// not FULL and EMPTY never carries it. The evidence adds the counts and
// keeps at most the bounded samples.
func TestEveryPrimaryQueryHasToAgreeThatTheTargetsIdentityReadNone(t *testing.T) {
	primary := execution.NamedInputBinding{Role: execution.InputRolePrimary}
	sample := execution.IdentitySample{Fields: []execution.IdentityField{{Name: "cw_object_model_id", Expected: "8", Actual: "cw_object_model_id", Present: true}}}
	empty := func(withheld, outside, keyMissing uint64, samples int) execution.PhysicalQueryCompletion {
		completion := execution.PhysicalQueryCompletion{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
			Withheld: withheld, WithheldOutsideTarget: outside, WithheldKeyMissing: keyMissing}
		for index := 0; index < samples; index++ {
			completion.IdentitySamples = append(completion.IdentitySamples, sample)
		}
		return completion
	}
	fullEmpty := execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty}
	for _, test := range []struct {
		name    string
		queries []execution.PhysicalQueryCompletion
		fact    execution.PrimaryInputFact
		want    *execution.IdentityEvidence
	}{
		{"every series key missing, beside a query that returned nothing",
			[]execution.PhysicalQueryCompletion{empty(2, 0, 2, 2), empty(0, 0, 0, 0)}, fullEmpty,
			&execution.IdentityEvidence{Withheld: 2, KeyMissing: 2, Samples: []execution.IdentitySample{sample, sample}}},
		{"samples across queries stop at the bound", []execution.PhysicalQueryCompletion{empty(3, 0, 3, 3), empty(2, 0, 2, 2)}, fullEmpty,
			&execution.IdentityEvidence{Withheld: 5, KeyMissing: 5, Samples: []execution.IdentitySample{sample, sample, sample}}},
		{"beside a query that withheld outside the target", []execution.PhysicalQueryCompletion{empty(2, 0, 2, 1), empty(1, 1, 0, 0)}, fullEmpty, nil},
		{"one series of a query withheld for another reason", []execution.PhysicalQueryCompletion{empty(3, 0, 2, 1)}, fullEmpty, nil},
		{"a query that kept data", []execution.PhysicalQueryCompletion{
			{Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Withheld: 2, WithheldKeyMissing: 2}, empty(0, 0, 0, 0)}, fullEmpty, nil},
		{"a primary that is not full", []execution.PhysicalQueryCompletion{empty(2, 0, 2, 1)},
			execution.PrimaryInputFact{Completeness: execution.CompletenessPartial, DataState: execution.DataStateEmpty}, nil},
		{"nothing withheld", []execution.PhysicalQueryCompletion{empty(0, 0, 0, 0)}, fullEmpty, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			var evidence queryAvailabilityEvidence
			for _, query := range test.queries {
				evidence.observe(primary, query, false, false)
			}
			if got := evidence.identityEvidence(test.fact); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("identity evidence = %+v, want %+v", got, test.want)
			}
		})
	}
}
