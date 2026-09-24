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

// Every count the absence line carries is a series from startup, and each
// judging Plan's line adds its own numbers to its own cell -- zeros included,
// which is what makes a flat zero a reading rather than an unwritten label.
func TestNoDataAbsencesAreSeriesFromStartupAndSumEachPlansCounts(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	const family = "bkmonitor_alarmd_worker_no_data_absences_total"
	before := gatherFamily(t, r, family)
	if len(before) != len(observability.NoDataAbsenceOutcomes) {
		t.Fatalf("%d series before any round, want every cell (%d) so a horizon that stopped nothing reads as zero",
			len(before), len(observability.NoDataAbsenceOutcomes))
	}
	decided := func(facts observability.NoDataAbsenceFacts) observability.Observation {
		facts.Outcome = "EVALUATED"
		return observability.Observation{
			Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
			Result:        observability.ResultSuccess,
			Trace:         observability.TraceFields{QueryGroupKey: "qg-absence", StrategyID: "4101", EvaluationTime: 600},
			NoDataAbsence: &facts,
		}
	}
	ctx := context.Background()
	// Two Plans with different numbers in every cell, so a cell fed from the
	// wrong field or added once per line shows.
	r.Observe(ctx, decided(observability.NoDataAbsenceFacts{Expected: 10, Present: 6, Absent: 2, Expired: 1, Suppressed: 1}))
	r.Observe(ctx, decided(observability.NoDataAbsenceFacts{Expected: 5, Present: 5, Unavailable: 0, Dropped: 3}))
	// A line without an outcome is nothing the emitter produces; the observer
	// drops it and the cells do not move.
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		NoDataAbsence: &observability.NoDataAbsenceFacts{Expected: 100, Suppressed: 100},
	})
	count := func(outcome string) float64 {
		return testutil.ToFloat64(r.phaseTwo.noDataAbsences.WithLabelValues(outcome))
	}
	for outcome, want := range map[string]float64{
		"expected": 15, "present": 11, "absent": 2, "unavailable": 0, "dropped": 3, "expired": 1, "suppressed": 1,
	} {
		if got := count(outcome); got != want {
			t.Fatalf("%s = %v, want %v", outcome, got, want)
		}
	}
	if after := gatherFamily(t, r, family); len(after) != len(before) {
		t.Fatalf("%d series after the rounds, want the same %d: no round may create a cell", len(after), len(before))
	}
}
