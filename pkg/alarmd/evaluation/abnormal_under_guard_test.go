// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A guard withholds the verdicts that need a whole window, not the one that
// does not: when the history is short, an anomaly with enough evidence still
// fires, and a normal or recovered verdict without enough evidence is not
// made up. Under a Plan gap marker - what a Slot finalized as
// SNAPSHOT_UNAVAILABLE opens, the shape of a long-period strategy's first
// rounds back - and under a series' own gapped state alike, the anomalies the
// window does hold decide: two out of five required, two observed, ABNORMAL,
// and the result contract takes it; one observed, UNKNOWN under the guard's
// reason, never NORMAL.
func TestAnAnomalyWithEnoughEvidenceFiresUnderAGuard(t *testing.T) {
	plan := compiledG4PlanWithTrigger(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20, "ceil": nil},
		strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}, 5, 2)
	guard := execution.ReasonCode(contract.ReasonSnapshotUnavailable)
	point := func(result execution.LevelFactResult) execution.StateHistoryPoint {
		return execution.StateHistoryPoint{RecordID: strings.Repeat("d", 64), SourceTime: 300,
			Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: result}}}
	}
	for _, guardKind := range []string{"plan gap marker", "gapped series state"} {
		for _, testCase := range []struct {
			earlier execution.LevelFactResult
			want    execution.LevelOutcomeKind
		}{
			{earlier: execution.LevelFactAnomalous, want: execution.LevelOutcomeAbnormal},
			{earlier: execution.LevelFactNormal, want: execution.LevelOutcomeUnknown},
		} {
			t.Run(guardKind+"/earlier "+string(testCase.earlier), func(t *testing.T) {
				record := g4Record(360, `80`, nil)
				history := []execution.StateHistoryPoint{point(testCase.earlier)}
				request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{record}, history)
				request.State.Items[0].Levels[0].LastProcessedEventTime = 300
				if guardKind == "plan gap marker" {
					request = planGapMarkerFixtureOn(t, request, execution.GapScope{}, string(guard), 9, 0, "")
				} else {
					request.State.Items[0].Status = execution.StateFoundGapped
					request.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryGapped
					request.State.Items[0].Levels[0].GapReasonCode = guard
				}
				request.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, request,
					map[string][]contract.CanonicalRecordV2{"primary": {record}, "previous": {g4Record(300, `100`, nil)}})}

				result, err := newEvaluator(t).Evaluate(context.Background(), request)
				if err != nil {
					t.Fatalf("Evaluate() error = %v", err)
				}
				outcomes := result.Plans[0].LevelOutcomes
				if len(outcomes) != 1 || outcomes[0].Outcome != testCase.want {
					t.Fatalf("outcomes = %+v, want %s", outcomes, testCase.want)
				}
				if testCase.want == execution.LevelOutcomeUnknown && outcomes[0].ReasonCode != guard {
					t.Fatalf("UNKNOWN reason = %s, want the guard's %s", outcomes[0].ReasonCode, guard)
				}
				if err := result.Validate(request); err != nil {
					t.Fatalf("the result contract refused it: %v", err)
				}
			})
		}
	}
}
