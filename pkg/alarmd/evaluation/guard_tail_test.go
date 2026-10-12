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

// An UNKNOWN under a standing guard is marked as the guard's alone only when
// the trigger left it UNKNOWN for its history and the round proposes no guard
// of its own for the Level. Every case answers every input FULL and
// available, so the inputs alone cannot tell them apart.
//
//   - A short history is the tail, WARMING or GAPPED: the record advances
//     State and the guard's warmup counts it. GAPPED is the shape a series
//     that comes back after the gap has - a hole in its window.
//   - A dependency point missing for this series is this record's own UNKNOWN:
//     State does not advance, the guard never warms, and calling it warming
//     would say "nothing to do" for as long as the point stays missing.
//   - A dependency that answered with no rows is whole to PlanInputsWhole - on
//     a round with no series that is all a warmup needs - but for a series
//     that needed it the round proposes its own QUERY_EMPTY guard.
func TestAnUnknownIsMarkedAsTheGuardsAloneOnlyWhenItIsItsHistoryAndTheRoundProposesNothing(t *testing.T) {
	projection := strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	config := func() map[string]any { return map[string]any{"floor": 20, "ceil": nil} }
	single := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, config(), projection)
	double := compiledG4PlanWithTrigger(t, strategy.DetectorKindSimpleRingRatio, config(), projection, 2, 2)
	normal := func(id string, sourceTime int64) execution.StateHistoryPoint {
		return execution.StateHistoryPoint{RecordID: strings.Repeat(id, 64), SourceTime: sourceTime,
			Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: double.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactNormal}}}
	}
	guard := execution.ReasonCode(contract.ReasonQueryUnavailable)
	for _, testCase := range []struct {
		name     string
		plan     *strategy.CompiledPlan
		gapped   bool
		history  []execution.StateHistoryPoint
		primary  contract.CanonicalRecordV2
		previous []contract.CanonicalRecordV2
		reason   execution.ReasonCode
		tail     bool
	}{
		// A hole before the new record, in a history still warming.
		{name: "history warming", plan: double, history: []execution.StateHistoryPoint{normal("d", 180), normal("e", 240)},
			primary: g4Record(360, `80`, nil), previous: []contract.CanonicalRecordV2{g4Record(300, `100`, nil)},
			reason: guard, tail: true},
		// A history already gapped, one point short of a full window at the
		// last processed record, so the guard's GAPPED still decides.
		{name: "history gapped", plan: double, gapped: true, history: []execution.StateHistoryPoint{normal("e", 240)},
			primary: g4Record(360, `80`, nil), previous: []contract.CanonicalRecordV2{g4Record(300, `100`, nil)},
			reason: guard, tail: true},
		// Rows, none at the offset this record needs.
		{name: "dependency point missing", plan: single,
			primary: g4Record(99, `80`, nil), previous: []contract.CanonicalRecordV2{g4OffsetMissRecord(39, `100`)},
			reason: guard, tail: false},
		{name: "dependency answered empty", plan: single,
			primary: g4Record(99, `80`, nil), previous: []contract.CanonicalRecordV2{},
			reason: execution.ReasonCode(contract.ReasonQueryEmpty), tail: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := requestFixtureForPlan(t, testCase.plan, []contract.CanonicalRecordV2{testCase.primary}, testCase.history)
			request.State.Items[0].Status = execution.StateFoundWarming
			request.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryWarming
			if testCase.gapped {
				request.State.Items[0].Status = execution.StateFoundGapped
				request.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryGapped
			}
			request.State.Items[0].Levels[0].GapReasonCode = guard
			if len(testCase.history) != 0 {
				request.State.Items[0].Levels[0].LastProcessedEventTime = testCase.history[len(testCase.history)-1].SourceTime
			}
			input := g4Input(t, request, map[string][]contract.CanonicalRecordV2{"primary": {testCase.primary}, "previous": testCase.previous})
			request.Inputs = []execution.SeriesEvaluationInputRequest{input}
			if !execution.PlanInputsWhole(input.Inputs, request.Header.DuePlans[0].Identity) {
				t.Fatal("fixture: every input must answer FULL and available, so only the mark can tell the cases apart")
			}

			result, err := newEvaluator(t).Evaluate(context.Background(), request)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			outcomes := result.Plans[0].LevelOutcomes
			if len(outcomes) != 1 || outcomes[0].Outcome != execution.LevelOutcomeUnknown || outcomes[0].ReasonCode != testCase.reason {
				t.Fatalf("outcomes = %+v, want one UNKNOWN %s", outcomes, testCase.reason)
			}
			if outcomes[0].GuardTail != testCase.tail {
				t.Fatalf("GuardTail = %t, want %t", outcomes[0].GuardTail, testCase.tail)
			}
			if got := execution.NewSlotInputWholeness(input.Inputs).UnknownIsGuardTail(outcomes[0]); got != testCase.tail {
				t.Fatalf("UnknownIsGuardTail() = %t, want %t", got, testCase.tail)
			}
		})
	}
}
