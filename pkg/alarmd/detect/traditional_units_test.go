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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestTraditionalDecimalUnitSequentialRoundingBoundary(t *testing.T) {
	config := map[string]any{"days": 1, "method": "gt", "ratio": 0, "shock": 460962397203.8692, "data_unit": "decmbytes", "algorithm_unit": "", "precision": 6}
	plan := compileNamedInputPlan(t, strategy.DetectorKindYearRoundRange, config, strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	c, _ := plan.Levels()[0].Algorithms()[0].TraditionalComparisonConfig()
	previous := float64(1)
	if got := evaluateTraditionalComparison(strategy.DetectorKindYearRoundRange, c, 460962.39720386924, map[int64]*float64{86400: &previous}); got != pureDetectionNormal {
		t.Fatalf("got %s; Python sequential conversion is equal, not greater", got)
	}
}
