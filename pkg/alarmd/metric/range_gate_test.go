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

// Every word the range gate can put on a round is a series from startup, a
// round counts under its word, and a word outside the list counts under
// unexplained rather than opening a series of its own.
func TestRangeGateOutcomesAreSeriesFromStartupAndCountByWord(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	// Read from the registry before any label is touched: asking the vector
	// for a series creates it, so a check through WithLabelValues would find
	// the zeros it made itself.
	seriesBeforeAnyRound := map[string]bool{}
	before := gatherFamily(t, r, "bkmonitor_alarmd_range_gate_total")
	if before == nil {
		t.Fatal("range_gate_total missing from the registry before any round")
	}
	for _, m := range before {
		seriesBeforeAnyRound[m.Label[0].GetValue()] = true
	}
	for _, outcome := range observability.RangeGateOutcomes {
		if !seriesBeforeAnyRound[outcome] {
			t.Fatalf("%s has no series before any round: an outcome nobody has hit yet reads as a missing family, not as zero", outcome)
		}
	}
	gate := func(outcome string) observability.Observation {
		return observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageRangeGateDecided,
			Result: observability.ResultDegraded, ReasonCode: observability.ReasonCode(outcome),
			Trace:     observability.TraceFields{QueryGroupKey: "qg-secret"},
			RangeGate: &observability.RangeGateFacts{Outcome: outcome},
		}
	}
	r.Observe(context.Background(), gate(observability.RangeGateStepsBelowOne))
	r.Observe(context.Background(), gate(observability.RangeGateStepsBelowOne))
	r.Observe(context.Background(), gate(observability.RangeGateNoRangeFlight))
	r.Observe(context.Background(), gate("qg-secret"))
	if got := testutil.ToFloat64(r.phaseTwo.rangeGateDecisions.WithLabelValues(observability.RangeGateStepsBelowOne)); got != 2 {
		t.Fatalf("steps_below_one=%v, want 2", got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.rangeGateDecisions.WithLabelValues(observability.RangeGateNoRangeFlight)); got != 1 {
		t.Fatalf("no_range_flight=%v, want 1", got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.rangeGateDecisions.WithLabelValues(observability.RangeGateUnexplained)); got != 1 {
		t.Fatalf("unexplained=%v, want the one word outside the list", got)
	}
	series := gatherFamily(t, r, "bkmonitor_alarmd_range_gate_total")
	if len(series) != len(observability.RangeGateOutcomes) {
		t.Fatalf("series=%d, want one per outcome (%d): %v", len(series), len(observability.RangeGateOutcomes), series)
	}
	for _, m := range series {
		if len(m.Label) != 1 || m.Label[0].GetName() != "outcome" || m.Label[0].GetValue() == "qg-secret" {
			t.Fatalf("unbounded labels: %v", m)
		}
	}
}
