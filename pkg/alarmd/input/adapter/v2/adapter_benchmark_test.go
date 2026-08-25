// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func BenchmarkAdapterDecode(b *testing.B) {
	for _, planCount := range []int{1, 8} {
		b.Run(benchmarkName(planCount), func(b *testing.B) {
			envelope := validEnvelope(b, 100)
			ranges := []contract.SelectorRangeV2{{Start: 0, End: 100}}
			envelope.PlanSet.EvaluationPlans = make([]contract.EvaluationPlanV2, planCount)
			envelope.Selectors = make([]contract.PlanSelectorV2, planCount)
			for index := 0; index < planCount; index++ {
				planID := benchmarkPlanID(index)
				envelope.PlanSet.EvaluationPlans[index] = validPlan(planID, uint32(index+1))
				envelope.Selectors[index] = contract.PlanSelectorV2{
					PlanOrdinal: uint32(index), Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &ranges},
				}
			}
			envelope.PlanSet.PlanCount = uint32(planCount)
			payload := encodeEnvelope(b, envelope)
			adapter := New(readerLimits())
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				result, err := adapter.Decode(context.Background(), payload)
				if err != nil || result.Rejected {
					b.Fatalf("Decode() = (%#v, %v)", result, err)
				}
			}
		})
	}
}

func benchmarkName(planCount int) string {
	if planCount == 1 {
		return "plans_1_records_100"
	}
	return "plans_8_records_100"
}

func benchmarkPlanID(index int) string {
	return string(rune('1'+index)) + "001"
}
