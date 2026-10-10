// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A refused coverage fact set is counted once under the rule it broke; every
// rule is a series from startup; an accepted set moves nothing. The recorder
// sees the observation after normalize, which is where the rule is decided.
func TestARefusedCoverageIsCountedUnderItsRule(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	const family = "bkmonitor_alarmd_history_coverage_rejected_total"
	if before := gatherFamily(t, r, family); len(before) != len(observability.CoverageRejectionRules) {
		t.Fatalf("%d series before any round, want every rule (%d) so a zero is a reading", len(before), len(observability.CoverageRejectionRules))
	}
	committed := func(facts *observability.HistoryCoverageFacts) observability.Observation {
		return observability.Observation{
			Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted, Result: observability.ResultSuccess,
			Trace:           observability.TraceFields{QueryGroupKey: "qg-coverage", StrategyID: "4101", EvaluationTime: 600},
			HistoryCoverage: facts,
		}
	}
	ctx := context.Background()
	r.Observe(ctx, committed(&observability.HistoryCoverageFacts{Levels: 2, Short: 3}))
	r.Observe(ctx, committed(&observability.HistoryCoverageFacts{Levels: 2, Short: 3}))
	r.Observe(ctx, committed(&observability.HistoryCoverageFacts{Levels: 2, Short: 1, Empty: 1, WorstValid: 4, WorstRequired: 9}))
	r.Observe(ctx, committed(&observability.HistoryCoverageFacts{Levels: 2, Short: 1, WorstValid: 4, WorstRequired: 9}))
	r.Observe(ctx, committed(nil))
	count := func(rule observability.CoverageRejectionRule) float64 {
		return testutil.ToFloat64(r.phaseTwo.historyCoverageRejected.WithLabelValues(string(rule)))
	}
	if got := count(observability.CoverageRejectShortOverLevels); got != 2 {
		t.Fatalf("SHORT_OVER_LEVELS = %v, want 2", got)
	}
	if got := count(observability.CoverageRejectEmptyWithValidPoints); got != 1 {
		t.Fatalf("EMPTY_WITH_VALID_POINTS = %v, want 1", got)
	}
	total := 0.0
	for _, rule := range observability.CoverageRejectionRules {
		total += count(rule)
	}
	if total != 3 {
		t.Fatalf("rejections over every rule = %v, want 3: the accepted set and the absent set count nowhere", total)
	}
}
