// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// countPoint is one round of a count: its value and whether the detector
// found it anomalous.
type countPoint struct {
	value     string
	anomalous bool
}

// evaluateCounts evaluates every round of points, a minute apart, each in the
// Slot a minute after it, and returns each round's record result.
func evaluateCounts(t *testing.T, plan *strategy.CompiledPlan, points []countPoint, zeroIsNoObservation bool) ([]string, []EvaluationResultV2) {
	t.Helper()
	return evaluateCountsWithHoles(t, plan, points, zeroIsNoObservation, nil)
}

// evaluateCountsWithHoles is evaluateCounts with the rounds in holes left out
// of every later round's history, as a round no record was kept for.
func evaluateCountsWithHoles(t *testing.T, plan *strategy.CompiledPlan, points []countPoint, zeroIsNoObservation bool, holes map[int]bool) ([]string, []EvaluationResultV2) {
	t.Helper()
	return evaluateCountsFrom(t, plan, points, zeroIsNoObservation, holes, 0)
}

// evaluateCountsFrom is evaluateCountsWithHoles with every round's Level
// history carrying lastReal as the series' last real point.
func evaluateCountsFrom(t *testing.T, plan *strategy.CompiledPlan, points []countPoint, zeroIsNoObservation bool, holes map[int]bool, lastReal int64) ([]string, []EvaluationResultV2) {
	t.Helper()
	level := plan.Levels().At(0)
	base := int64(1000 * 60)
	results, evaluated := make([]string, len(points)), make([]EvaluationResultV2, len(points))
	for index := range points {
		history := pointHistory{step: 60, points: make(map[int64]bool, index+1)}
		for earlier := 0; earlier <= index; earlier++ {
			if holes[earlier] && earlier != index {
				continue
			}
			history.points[base+int64(earlier)*60] = points[earlier].anomalous
		}
		source := base + int64(index)*60
		result := DetectionNormal
		if points[index].anomalous {
			result = DetectionAnomalous
		}
		request := requestV2(t, plan, source, []DetectionFact{factV2(level, result)},
			[]LevelHistory{{LevelID: level.Definition().LevelID, View: history, LastRealPoint: lastReal}}, activeFactsV2(t, plan, source+60))
		request.Record.ProjectedValues[0].CanonicalDecimal = points[index].value
		request.SeriesSeenBefore = index > 0
		request.ZeroCountIsNoObservation = zeroIsNoObservation
		readInTheNextSlot(t, &request)
		got, err := EvaluateV2(request)
		if err != nil {
			t.Fatalf("round %d: EvaluateV2 error = %v", index, err)
		}
		results[index], evaluated[index] = got.RecordResult, got
	}
	return results, evaluated
}

// An event count that triggered and then went quiet: its zeros still count
// as windows that did not trigger, but a round whose own count is zero does
// not end the alert. It recovers on the first round with a count above zero.
func TestAZeroCountDoesNotEndAnAlertAndTheFirstCountAboveZeroDoes(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 1, 1, 4, nil)})
	points := []countPoint{{"5.000000", true}, {"0.000000", false}, {"0.000000", false}, {"0.000000", false}, {"0.000000", false}, {"1.000000", false}}

	got, evaluated := evaluateCounts(t, plan, points, true)
	// The first rounds may still be warming; what matters is that no zero
	// round recovers and the round with the count above zero does.
	if got[0] != contract.LevelResultAbnormal || got[4] != contract.LevelResultNormal || got[5] != contract.LevelResultRecovery {
		t.Fatalf("rounds = %v, want ABNORMAL first, NORMAL on the fourth zero, RECOVERY on the count of one", got)
	}
	for index := 1; index < 5; index++ {
		if got[index] == contract.LevelResultRecovery {
			t.Fatalf("round %d recovered on a zero count (all %v)", index, got)
		}
	}
	// The round that met the recovery windows on a zero says it was withheld.
	window := evaluated[4].LevelOutcomes[0].DecisionWindow
	if window == nil || window.Recovery.Withheld != contract.RecoveryWithheldZeroCount ||
		window.Recovery.ObservedConsecutiveMisses < window.Recovery.RequiredConsecutiveWindows {
		t.Fatalf("round 4 recovery evidence = %+v, want the windows met and withheld as zero_count", window)
	}

	// Without the flag - a time series - the same zeros recover on the fourth.
	plain, _ := evaluateCounts(t, plan, points, false)
	if plain[4] != contract.LevelResultRecovery {
		t.Fatalf("a time series' zeros: round 4 = %s, want RECOVERY (all %v)", plain[4], plain)
	}
}

// Only zeros: the alert never ends, however many there are.
func TestOnlyZeroCountsNeverEndAnAlert(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 1, 1, 4, nil)})
	points := []countPoint{{"5.000000", true}}
	for range 12 {
		points = append(points, countPoint{"0.000000", false})
	}
	got, _ := evaluateCounts(t, plan, points, true)
	for index, result := range got {
		if result == contract.LevelResultRecovery {
			t.Fatalf("round %d recovered on a zero count (all %v)", index, got)
		}
	}
}

// The flag changes recovery and nothing the trigger decides: over shapes that
// fire on counts above a threshold, on counts below one (a zero is
// anomalous), and M of N, every round that is not a recovery reads the same
// with and without it.
func TestAZeroCountChangesNoTriggerDecision(t *testing.T) {
	type shape struct {
		name           string
		window, needed uint32
		points         []countPoint
	}
	above := []countPoint{{"0.000000", false}, {"3.000000", true}, {"0.000000", false}, {"0.000000", false}, {"4.000000", true}, {"4.000000", true}, {"0.000000", false}, {"0.000000", false}, {"0.000000", false}, {"0.000000", false}, {"2.000000", true}}
	below := []countPoint{{"2.000000", false}, {"0.000000", true}, {"0.000000", true}, {"1.000000", false}, {"0.000000", true}, {"3.000000", false}, {"3.000000", false}, {"3.000000", false}, {"3.000000", false}, {"0.000000", true}}
	for _, item := range []shape{
		{"count above a threshold", 1, 1, above},
		{"count below one", 1, 1, below},
		{"two of three", 3, 2, above},
		{"two of three below one", 3, 2, below},
	} {
		plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, item.window, item.needed, 4, nil)})
		with, _ := evaluateCounts(t, plan, item.points, true)
		without, _ := evaluateCounts(t, plan, item.points, false)
		for index := range with {
			if without[index] == contract.LevelResultRecovery {
				continue
			}
			if with[index] != without[index] {
				t.Fatalf("%s round %d: %s with the flag, %s without", item.name, index, with[index], without[index])
			}
		}
	}
}

// A Level written to end its alert closed says so on its RECOVERY, and only
// there: not on the ABNORMAL before it, and not on a recovery a zero withheld.
func TestARecoveryWrittenToCloseSaysSoOnlyOnItsRecovery(t *testing.T) {
	level := levelV2(5, 1, 1, 1, 1, nil)
	level.RecoveryPlan.Config = mustJSONV2(map[string]any{"enabled": true, "consecutive_windows": 1, "end": contract.RecoveryEndClose})
	plan := compilePlanV2(t, []contract.LevelIRV2{level})
	end := func(result EvaluationResultV2) string {
		if window := result.LevelOutcomes[0].DecisionWindow; window != nil {
			return window.Recovery.End
		}
		return ""
	}

	got, evaluated := evaluateCounts(t, plan, []countPoint{{"5.000000", true}, {"1.000000", false}}, false)
	if got[0] != contract.LevelResultAbnormal || got[1] != contract.LevelResultRecovery {
		t.Fatalf("rounds = %v, want ABNORMAL then RECOVERY", got)
	}
	if end(evaluated[0]) != "" || end(evaluated[1]) != contract.RecoveryEndClose {
		t.Fatalf("ends = %q, %q, want none on the ABNORMAL and close on the RECOVERY", end(evaluated[0]), end(evaluated[1]))
	}

	got, evaluated = evaluateCounts(t, plan, []countPoint{{"5.000000", true}, {"0.000000", false}}, true)
	if got[1] != contract.LevelResultNormal || end(evaluated[1]) != "" {
		t.Fatalf("withheld round = %s with end %q, want NORMAL and no end", got[1], end(evaluated[1]))
	}
}

// On a window with a hole a recovery may be decided (decision-022) but
// NORMAL may not. So a recovery a zero withholds there is what any round
// that did not recover is: unavailable, with the history reason - not
// NORMAL, which the contract refuses on a window that is not whole, and not
// RECOVERY. Without the flag the same round recovers.
func TestAZeroWithheldOnAWindowWithAHoleIsUnavailableNotNormal(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 2, 1, 3, nil)})
	points := []countPoint{{"5.000000", true}, {"0.000000", false}, {"0.000000", false}, {"0.000000", false}, {"0.000000", false}}
	holes := map[int]bool{1: true}

	plain, _ := evaluateCountsWithHoles(t, plan, points, false, holes)
	if plain[4] != contract.LevelResultRecovery {
		t.Fatalf("without the flag round 4 = %q, want RECOVERY across the hole (all %v)", plain[4], plain)
	}
	got, evaluated := evaluateCountsWithHoles(t, plan, points, true, holes)
	if got[4] != "" || evaluated[4].LevelOutcomes[0].UnavailableReason == "" {
		t.Fatalf("round 4 = %q with outcome %+v, want unavailable with the history reason (all %v)", got[4], evaluated[4].LevelOutcomes[0], got)
	}
}

// A Level written to end on no data, whose series' last real point is the
// count of five that opened it: its zeros meet the recovery windows on round
// 4 and are withheld, and the round (R + T + 1) periods after that point -
// round 6, with N = 1 and R = 4 - ends it, saying no data did, as the
// platform's recovery check times it. One period short it does not. Without
// the setting the same zeros never end it; with the last point unknown they
// end nothing and say why; and a zero that is itself anomalous triggers
// rather than ends.
func TestNoDataEndsALevelWrittenSoOnceItsLastRealPointIsOldEnough(t *testing.T) {
	compile := func(missingData bool) *strategy.CompiledPlan {
		level := levelV2(5, 1, 1, 1, 4, nil)
		recovery := map[string]any{"enabled": true, "consecutive_windows": 4}
		if missingData {
			recovery["missing_data"] = contract.RecoveryMissingDataEvaluateAsZero
		}
		level.RecoveryPlan.Config = mustJSONV2(recovery)
		return compilePlanV2(t, []contract.LevelIRV2{level})
	}
	points := []countPoint{{"5.000000", true}}
	for range 7 {
		points = append(points, countPoint{"0.000000", false})
	}
	base := int64(1000 * 60)
	recovery := func(result EvaluationResultV2) contract.RecoveryWindowEvidenceV1 {
		if window := result.LevelOutcomes[0].DecisionWindow; window != nil {
			return window.Recovery
		}
		return contract.RecoveryWindowEvidenceV1{}
	}

	got, evaluated := evaluateCountsFrom(t, compile(true), points, true, nil, base)
	for index := 1; index <= 5; index++ {
		if got[index] == contract.LevelResultRecovery {
			t.Fatalf("round %d recovered before (R + T + 1) periods without a real point (all %v)", index, got)
		}
	}
	if got[5] != contract.LevelResultNormal || recovery(evaluated[5]).Withheld != contract.RecoveryWithheldZeroCount {
		t.Fatalf("round 5 = %s %+v, want NORMAL withheld on the zero", got[5], recovery(evaluated[5]))
	}
	if got[6] != contract.LevelResultRecovery || recovery(evaluated[6]).EndedBy != contract.RecoveryEndedByNoData || recovery(evaluated[6]).Withheld != "" {
		t.Fatalf("round 6 = %s %+v, want RECOVERY ended by no data (all %v)", got[6], recovery(evaluated[6]), got)
	}
	if evaluated[6].LevelOutcomes[0].RealPoint || !evaluated[0].LevelOutcomes[0].RealPoint {
		t.Fatal("a zero that is no observation was read as a real point, or the count of five was not")
	}

	plain, _ := evaluateCountsFrom(t, compile(false), points, true, nil, base)
	for index, result := range plain {
		if result == contract.LevelResultRecovery {
			t.Fatalf("without missing_data round %d recovered on zeros (all %v)", index, plain)
		}
	}

	anomalous := []countPoint{{"5.000000", true}}
	for range 7 {
		anomalous = append(anomalous, countPoint{"0.000000", true})
	}
	if fired, _ := evaluateCountsFrom(t, compile(true), anomalous, true, nil, base); fired[6] != contract.LevelResultAbnormal {
		t.Fatalf("an anomalous zero on round 6 = %s, want ABNORMAL: no data never ends a Level whose trigger holds (all %v)", fired[6], fired)
	}

	unknown, unknownEvaluated := evaluateCountsFrom(t, compile(true), points, true, nil, 0)
	if unknown[6] != contract.LevelResultNormal || recovery(unknownEvaluated[6]).Withheld != contract.RecoveryWithheldNoDataLastPointUnknown {
		t.Fatalf("last point unknown: round 6 = %s %+v, want NORMAL withheld as no_data_last_point_unknown", unknown[6], recovery(unknownEvaluated[6]))
	}
}

// No data does not end a Level whose trigger window still holds an anomaly.
// T = 3, N = 1, R = 1: an earlier zero was anomalous (as a ring ratio may
// judge a zero against the recent average) and this round's is not, so the
// window still triggers on round 5 though its last real point is long past
// (R + T + 1). The round is decided as the window says - never a RECOVERY
// the event contract refuses, which failed the series every round.
func TestNoDataDoesNotEndALevelWhoseTriggerWindowStillHoldsAnAnomaly(t *testing.T) {
	level := levelV2(5, 1, 3, 1, 1, nil)
	level.RecoveryPlan.Config = mustJSONV2(map[string]any{"enabled": true, "consecutive_windows": 1, "missing_data": contract.RecoveryMissingDataEvaluateAsZero})
	plan := compilePlanV2(t, []contract.LevelIRV2{level})
	points := []countPoint{{"5.000000", true}, {"0.000000", false}, {"0.000000", false}, {"0.000000", false}, {"0.000000", true}, {"0.000000", false}}
	got, evaluated := evaluateCountsFrom(t, plan, points, true, nil, int64(1000*60))
	if got[5] == contract.LevelResultRecovery {
		t.Fatalf("round 5 = RECOVERY beside a window holding an anomaly (all %v)", got)
	}
	if window := evaluated[5].LevelOutcomes[0].DecisionWindow; window != nil && window.Recovery.EndedBy != "" {
		t.Fatalf("round 5 evidence says no data ended it: %+v", window.Recovery)
	}
}

// No data ends only a round whose own value is a zero that is no
// observation: a count above zero, however old the last real point, is a
// real point and recovers as its windows say, never as no data.
func TestNoDataNeverEndsARoundWithACountAboveZero(t *testing.T) {
	level := levelV2(5, 1, 1, 1, 1, nil)
	level.RecoveryPlan.Config = mustJSONV2(map[string]any{"enabled": true, "consecutive_windows": 1, "missing_data": contract.RecoveryMissingDataEvaluateAsZero})
	plan := compilePlanV2(t, []contract.LevelIRV2{level})
	points := []countPoint{{"5.000000", true}, {"0.000000", false}, {"0.000000", false}, {"0.000000", false}, {"1.000000", false}}
	got, evaluated := evaluateCountsFrom(t, plan, points, true, nil, int64(1000*60))
	if got[4] != contract.LevelResultRecovery || evaluated[4].LevelOutcomes[0].DecisionWindow.Recovery.EndedBy != "" {
		t.Fatalf("round 4 = %s %+v, want a RECOVERY its windows decided", got[4], evaluated[4].LevelOutcomes[0].DecisionWindow.Recovery)
	}
	// Its windows not met, a count of one beside a last real point long past
	// is still a real point: nothing ends it.
	slow := levelV2(5, 1, 1, 1, 3, nil)
	slow.RecoveryPlan.Config = mustJSONV2(map[string]any{"enabled": true, "consecutive_windows": 3, "missing_data": contract.RecoveryMissingDataEvaluateAsZero})
	one, _ := evaluateCountsFrom(t, compilePlanV2(t, []contract.LevelIRV2{slow}), []countPoint{{"1.000000", false}}, true, nil, int64(1000*60)-3600)
	if one[0] == contract.LevelResultRecovery {
		t.Fatalf("a count of one was ended as no data: %v", one)
	}
}
