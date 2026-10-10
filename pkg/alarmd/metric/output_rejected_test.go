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
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every rule is a series from startup, so a deployment that refused nothing
// reads zero. Each ACK line adds its refusals by rule and strategy; a rule
// this build does not name folds to _other; and the strategy label holds the
// first OutputRejectedStrategyLabels strategies, the rest folding to _other
// and counted apart, so a converter bug that refuses every strategy cannot
// mint a series per strategy.
func TestOutputEventsRejectedAreBoundedByRuleAndStrategy(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	const family = "bkmonitor_alarmd_output_events_rejected_total"
	if before := gatherFamily(t, r, family); len(before) != len(observability.OutputRejectRules) {
		t.Fatalf("%d series before any refusal, want one per rule (%d)", len(before), len(observability.OutputRejectRules))
	}
	ctx := context.Background()
	refuse := func(rejected ...observability.OutputRejectedEvent) {
		r.Observe(ctx, observability.Observation{
			Component: observability.ComponentOutput, Stage: observability.StageEventACKed, Result: observability.ResultDegraded,
			OutputWrite: &observability.OutputWriteFacts{Published: 1, Rejected: rejected},
		})
	}
	refuse(observability.OutputRejectedEvent{Rule: observability.OutputRejectStandardTooManyLevels, StrategyID: "1001"},
		observability.OutputRejectedEvent{Rule: "a_rule_from_later", StrategyID: "1001"})
	refuse(observability.OutputRejectedEvent{Rule: observability.OutputRejectStandardTooManyLevels, StrategyID: "1001"})
	count := func(rule, strategy string) float64 {
		return testutil.ToFloat64(r.phaseTwo.outputEventsRejected.WithLabelValues(rule, strategy))
	}
	if got := count(observability.OutputRejectStandardTooManyLevels, "1001"); got != 2 {
		t.Fatalf("too_many_levels/1001 = %v, want 2: one per round the refusal held", got)
	}
	if got := count(observability.OutputRejectOther, "1001"); got != 1 {
		t.Fatalf("_other/1001 = %v, want the unnamed rule folded", got)
	}
	// 1001 holds one label; 63 more fit and the 64th new strategy folds.
	for index := 0; index < OutputRejectedStrategyLabels; index++ {
		refuse(observability.OutputRejectedEvent{Rule: observability.OutputRejectEventInvalid, StrategyID: strconv.Itoa(10000 + index)})
	}
	if got := testutil.ToFloat64(r.phaseTwo.outputRejectedStrategyOverflow); got != 1 {
		t.Fatalf("overflow = %v, want 1: the 65th strategy has no label of its own", got)
	}
	if got := count(observability.OutputRejectEventInvalid, observability.OutputRejectOther); got != 1 {
		t.Fatalf("event_invalid/_other = %v, want the folded strategy counted there", got)
	}
	// A strategy that has a label keeps it after the limit is reached.
	refuse(observability.OutputRejectedEvent{Rule: observability.OutputRejectEventInvalid, StrategyID: "10000"})
	if got := count(observability.OutputRejectEventInvalid, "10000"); got != 2 {
		t.Fatalf("event_invalid/10000 = %v, want 2: a labelled strategy keeps its label", got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.outputRejectedStrategyOverflow); got != 1 {
		t.Fatalf("overflow = %v after a labelled strategy, want still 1", got)
	}
}
