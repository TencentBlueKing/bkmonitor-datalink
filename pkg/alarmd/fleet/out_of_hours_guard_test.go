// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// offHoursGuardedRounds is an object outside its active hours whose Level is
// held by a guard over a gapped history: nine rounds, a short window of nine
// with holes, every round suppressed. reasons is each round's completion
// reason, the last one the row's.
func offHoursGuardedRounds(t *testing.T, reasons []string) *Tracker {
	t.Helper()
	at := &clock{at: now}
	tracker := newTracker(t, at)
	base := now.Unix()/60*60 - 20*60
	holes := []int64{}
	for i, reason := range reasons {
		minute := base + int64(i)*60
		if i%3 != 0 {
			holes = append(holes, minute)
		}
		window := []int64{}
		for _, hole := range holes {
			if hole > minute-9*60 {
				window = append(window, hole)
			}
		}
		observation := completion("qg-offhours-guarded", "COMPLETED_WITH_UNAVAILABLE", "854")
		observation.ProgressCompletionCause = "LEVEL_OUTCOME_UNKNOWN"
		observation.ProgressCompletionReason = reason
		observation.Trace.EvaluationTime = minute + 60
		observation.HistoryCoverage = &observability.HistoryCoverageFacts{Levels: 3, Short: 1, Guarded: 1,
			WorstValid: 9 - uint32(len(window)), WorstRequired: 9, End: minute, WindowStart: minute - 8*60,
			Windows: []observability.HistoryWindowFact{{Strategy: "854", Series: "s", Level: 3, Valid: 9 - uint32(len(window)), Required: 9,
				End: minute, Missing: window, MissingTotal: uint32(len(window)), Guarded: true, GuardReason: "HISTORY_GAPPED"}}}
		tracker.Observe(context.Background(), observation)
	}
	return tracker
}

// The shape a strategy outside its hours had under a guard over its gapped
// history: before the evaluator said why a suppressed Level was not
// evaluated, a suppressed round under the guard took the guard's reason, the
// object's rounds mixed EFFECTIVE_TIME_INACTIVE with HISTORY_GAPPED, and the
// row went on the to-do list under HISTORY_GAPPED - read as data that did
// not arrive, for a strategy that was configured not to run. Every
// suppressed round now says it was out of hours, and the object is by
// design, under no line.
func TestAnObjectOutsideItsHoursUnderAGapGuardIsUnderNoLine(t *testing.T) {
	off, gapped := "EFFECTIVE_TIME_INACTIVE", "HISTORY_GAPPED"
	mixed := offHoursGuardedRounds(t, []string{off, gapped, off, gapped, off, off, gapped, off, gapped})
	if anomalies := mixed.Anomalies(); len(anomalies) != 1 || anomalies[0].CauseReason != gapped {
		t.Fatalf("the mixed rounds are no longer on the to-do list under the guard's reason: %+v", anomalies)
	}
	suppressed := offHoursGuardedRounds(t, []string{off, off, off, off, off, off, off, off, off})
	if anomalies := suppressed.Anomalies(); len(anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want none: every round was outside the strategy's hours", anomalies)
	}
	if byDesign := suppressed.ByDesign(); len(byDesign) != 1 {
		t.Fatalf("by-design = %+v, want the one object", byDesign)
	}
}
