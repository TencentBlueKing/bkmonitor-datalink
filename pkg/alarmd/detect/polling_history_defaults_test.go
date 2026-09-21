// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestPollingHistoryDefaultRequiresCompleteQueries(t *testing.T) {
	cases := []struct {
		kind   string
		config map[string]any
	}{
		{strategy.DetectorKindSimpleRingRatio, map[string]any{"ceil": 50}},
		{strategy.DetectorKindSimpleYearRound, map[string]any{"ceil": 50}},
		{strategy.DetectorKindAdvancedRingRatio, map[string]any{"ceil": 50, "ceil_interval": 2, "fetch_type": "avg"}},
		{strategy.DetectorKindAdvancedYearRound, map[string]any{"ceil": 50, "ceil_interval": 2, "fetch_type": "last"}},
		{strategy.DetectorKindRingRatioAmplitude, map[string]any{"ratio": 1, "shock": 0, "threshold": 0}},
		{strategy.DetectorKindYearRoundRange, map[string]any{"ratio": 1, "shock": 0, "days": 2, "method": "gt"}},
		{strategy.DetectorKindYearRoundAmplitude, map[string]any{"ratio": 1, "shock": 0, "days": 2, "method": "gt"}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			if strategy.IsTraditionalComparison(tc.kind) {
				tc.config["data_unit"] = "none"
				tc.config["algorithm_unit"] = ""
				tc.config["precision"] = 6
			}
			var baseline string
			for _, defaultZero := range []bool{false, true} {
				tc.config["missing_history_as_zero"] = defaultZero
				plan := compileNamedInputPlan(t, tc.kind, tc.config, strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
				algorithm := plan.Levels()[0].Algorithms()[0]
				if !defaultZero {
					baseline = algorithm.AlgorithmPlanID()
				} else if baseline == algorithm.AlgorithmPlanID() {
					t.Fatal("history semantics did not change algorithm fingerprint")
				}
				for _, quality := range []execution.Completeness{execution.CompletenessFull, execution.CompletenessPartial, execution.CompletenessUnavailable} {
					bindings, primary := namedBindings(t, algorithm.InputRequirements(), map[string][]contract.CanonicalRecordV2{
						"primary": {namedRecord(t, 864000, "10", nil)},
					})
					for i := range bindings {
						if bindings[i].Role == execution.InputRoleAlgorithmDependency {
							bindings[i].Completeness = quality
						}
					}
					input := execution.SeriesEvaluationInputRequest{Inputs: bindings}
					var got namedAlgorithmResult
					if tc.kind == strategy.DetectorKindSimpleRingRatio {
						got = (simpleRingRatioDetector{}).Evaluate(context.Background(), algorithm, input, primary)
					} else {
						got = (traditionalComparisonDetector{tc.kind}).Evaluate(context.Background(), algorithm, input, primary)
					}
					want := FactResultUnavailable
					if defaultZero && quality == execution.CompletenessFull {
						want = FactResultAnomalous
					}
					if got.result != want {
						t.Fatalf("defaultZero=%v quality=%s got=%+v want=%s", defaultZero, quality, got, want)
					}
				}
			}
		})
	}
}
