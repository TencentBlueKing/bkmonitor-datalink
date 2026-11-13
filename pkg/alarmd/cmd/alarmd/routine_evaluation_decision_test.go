// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Plan's routine evaluations merged into one line report to the lookback
// the Level outcomes the lines report summed, as NoteSlotDecision adds the
// lines' reports up.
func TestRoutineEvaluationsMergedReportTheSlotDecisionOfTheLines(t *testing.T) {
	trace := observability.TraceFields{QueryGroupKey: "qg", EvaluationTime: 1320, StrategyID: "7"}
	var merged observability.Observation
	summed := map[string]uint64{}
	for index, outcome := range []string{"NORMAL", "NORMAL", "ABNORMAL", "NORMAL"} {
		line := observability.Observation{Component: observability.ComponentEvaluation,
			Stage: observability.StageEvaluationCompleted, Result: observability.ResultSuccess,
			Trace:         observability.TraceFields{StrategyID: "7", DimensionIdentityDigest: string(rune('a' + index))},
			LevelOutcomes: []observability.LevelOutcomeFact{{Outcome: outcome, Count: 1}}}
		report, decided := slotDecisionReportOf(line, trace)
		if !decided {
			t.Fatal("an evaluation line reported no decision")
		}
		for kind, count := range report.Levels {
			summed[kind] += count
		}
		observability.MergeRoutineEvaluation(&merged, line)
	}
	report, decided := slotDecisionReportOf(merged, trace)
	if !decided || !reflect.DeepEqual(report.Levels, summed) {
		t.Fatalf("merged line reported %v, the lines %v", report.Levels, summed)
	}
}
