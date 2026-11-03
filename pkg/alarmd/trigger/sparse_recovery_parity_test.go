// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package trigger

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// sparsePoint is one point of a series: its source time and whether its
// detection was anomalous.
type sparsePoint struct {
	time      int64
	anomalous bool
}

// evaluateAtV2 evaluates the record at points[index] the way the production
// path does: every point up to and including it is in the history, and the
// Slot it is read in is the point's own minute, frozen with hold. The series
// is new to this round unless an earlier point exists, as a series whose
// first record this is.
func evaluateAtV2(t *testing.T, plan *strategy.CompiledPlan, points []sparsePoint, index int, evaluationTime, hold int64) EvaluationResultV2 {
	return evaluateSeenAtV2(t, plan, points, index, evaluationTime, hold, index > 0)
}

func evaluateSeenAtV2(t *testing.T, plan *strategy.CompiledPlan, points []sparsePoint, index int, evaluationTime, hold int64, seen bool) EvaluationResultV2 {
	return evaluateSlotV2(t, plan, points, index, evaluationTime, 0, hold, seen)
}

// evaluateSlotV2 is evaluateSeenAtV2 with the Slot's read end named: the
// evaluation time less the query delay, exclusive. Zero is the Slot a minute
// after evaluationTime with no delay, whose read covers every point up to it.
func evaluateSlotV2(t *testing.T, plan *strategy.CompiledPlan, points []sparsePoint, index int, evaluationTime, readEnd, hold int64, seen bool) EvaluationResultV2 {
	if readEnd == 0 {
		evaluationTime += 60
		readEnd = evaluationTime
	}
	t.Helper()
	level := plan.Levels().At(0)
	history := pointHistory{step: 60, points: make(map[int64]bool, index+1)}
	for _, point := range points[:index+1] {
		history.points[point.time] = point.anomalous
	}
	result := DetectionNormal
	if points[index].anomalous {
		result = DetectionAnomalous
	}
	request := requestV2(t, plan, points[index].time, []DetectionFact{factV2(level, result)},
		[]LevelHistory{{LevelID: level.Definition().LevelID, View: history}}, activeFactsV2(t, plan, evaluationTime))
	request.EvaluationTime, request.ReadHoldMillis, request.SeriesSeenBefore = evaluationTime, hold, seen
	request.DecisionTime = evaluationTime
	request.ReadEnd = readEnd
	evaluated, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2(%d) error = %v", points[index].time, err)
	}
	return evaluated
}

// readInTheNextSlot evaluates request in the Slot a minute after its record,
// with no query delay: that Slot's read covers every position up to the
// record's, so they are settled.
func readInTheNextSlot(t *testing.T, request *EvaluationRequestV2) {
	t.Helper()
	request.EvaluationTime = request.Record.SourceTime + 60
	request.DecisionTime = request.EvaluationTime
	request.ReadEnd = request.EvaluationTime
	request.EffectiveTimeFacts = activeFactsV2(t, request.Plan, request.EvaluationTime)
}

func recoveredV2(result EvaluationResultV2) bool {
	return result.RecordResult == contract.LevelResultRecovery
}

type pythonRecoveryCase struct {
	ID       string `json:"id"`
	Window   uint32 `json:"window"`
	Required uint32 `json:"required"`
	Recovery uint32 `json:"recovery"`
	OpenedAt *int64 `json:"opened_at"`
	Points   []struct {
		Time      int64 `json:"time"`
		Anomalous bool  `json:"anomalous"`
		Recovered bool  `json:"recovered"`
	} `json:"points"`
	// Lifecycle is the alert the first trigger opened, checked every minute
	// as check_recovery does: RECOVERED or CLOSED_NO_DATA at a minute, or
	// NEITHER by the end of the case.
	Lifecycle pythonLifecycle `json:"lifecycle"`
	// LifecycleRecoveryNoData is the same alert under a "recovery-nodata"
	// status setter, which closes it for no data (recover.py:315-321). Not
	// compared: alarmd ports no no-data close. Logged beside the outcome.
	LifecycleRecoveryNoData pythonLifecycle `json:"lifecycle_recovery_nodata"`
}

type pythonLifecycle struct {
	Outcome string `json:"outcome"`
	At      int64  `json:"at"`
}

const (
	pythonRecovered = "RECOVERED"
	pythonNeither   = "NEITHER"
)

// pythonRecoveryCases reads the oracle generate_sparse_recovery_python.py
// wrote by executing Python's own check_result_cache, from the pinned
// revision it names, on each case.
func pythonRecoveryCases(t *testing.T) map[string]pythonRecoveryCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/sparse_recovery_python.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Revision string               `json:"revision"`
		Cases    []pythonRecoveryCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Revision == "" || len(fixture.Cases) == 0 {
		t.Fatal("the Python oracle names no revision or holds no case")
	}
	cases := make(map[string]pythonRecoveryCase, len(fixture.Cases))
	for _, item := range fixture.Cases {
		cases[item.ID] = item
	}
	return cases
}

// pythonBase is the oracle's zero, minute 1000, so a logged time is seconds
// past it.
const pythonBase = int64(1000 * 60)

func (life pythonLifecycle) describe() string {
	if life.Outcome == pythonNeither {
		return life.Outcome
	}
	return life.Outcome + " at " + strconv.FormatInt(life.At-pythonBase, 10)
}

func (item pythonRecoveryCase) series() []sparsePoint {
	points := make([]sparsePoint, len(item.Points))
	for index, point := range item.Points {
		points[index] = sparsePoint{time: point.Time, anomalous: point.Anomalous}
	}
	return points
}

// compareWithPython evaluates every point of the case once its alert is open
// - Python checks recovery only for an open alert - and returns the times
// alarmd recovered at. At each point alarmd must recover exactly when
// Python's check_result_cache does at that point. Each point is evaluated
// twice, with the series' earlier state known and with only the retained
// positions to go on, and the two must agree.
//
// Then the case as a whole, against the alert Python's minute loop keeps
// under the default status setter: RECOVERED at a minute, alarmd recovers
// first at that minute; NEITHER, alarmd does not recover. The oracle has no
// CLOSED_NO_DATA under that setter, and alarmd ports no no-data close.
func compareWithPython(t *testing.T, item pythonRecoveryCase) (compared int, recovered []int64) {
	t.Helper()
	if item.OpenedAt == nil {
		return 0, nil
	}
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, item.Window, item.Required, item.Recovery, nil)})
	points := item.series()
	for index, point := range item.Points {
		if point.Time < *item.OpenedAt {
			continue
		}
		got := recoveredV2(evaluateSeenAtV2(t, plan, points, index, point.Time, 0, true))
		if got != point.Recovered {
			t.Fatalf("%s at %d: alarmd recovered = %t, Python recovered = %t", item.ID, point.Time, got, point.Recovered)
		}
		if fresh := recoveredV2(evaluateSeenAtV2(t, plan, points, index, point.Time, 0, false)); fresh != got {
			t.Fatalf("%s at %d: recovered = %t from the retained positions alone and %t with the state known",
				item.ID, point.Time, fresh, got)
		}
		compared++
		if got {
			recovered = append(recovered, point.Time)
		}
	}
	switch item.Lifecycle.Outcome {
	case pythonRecovered:
		if len(recovered) == 0 || recovered[0] != item.Lifecycle.At {
			t.Fatalf("%s: Python RECOVERED at %d, alarmd recovered at %v", item.ID, item.Lifecycle.At, recovered)
		}
	case pythonNeither:
		if len(recovered) > 0 {
			t.Fatalf("%s: Python neither recovered nor closed, alarmd recovered at %v", item.ID, recovered)
		}
	default:
		t.Fatalf("%s: oracle outcome %q", item.ID, item.Lifecycle.Outcome)
	}
	return compared, recovered
}

// The traced case: one trigger anomaly in five minutes, five
// recovery windows, an anomalous point at :42 and normal ones at :47 and :56.
// No window of five minutes ever holds five points, so before settled holes
// counted as non-anomalous every window was stepped over and the alert never
// closed. Python closes it at :56, the first point whose five windows all
// start after :42.
func TestTheTracedSparseCaseRecoversAtTheSameEvaluationAsPython(t *testing.T) {
	item := pythonRecoveryCases(t)["exact_42_47_56"]
	base := int64(1000 * 60)
	if len(item.Points) != 3 || item.Points[0].Time != base+42*60 || item.Points[2].Time != base+56*60 {
		t.Fatalf("oracle case = %+v, want points at :42, :47 and :56", item)
	}
	if item.Points[1].Recovered || !item.Points[2].Recovered {
		t.Fatalf("Python recovers %+v, want at :56 and not at :47", item.Points)
	}
	_, recovered := compareWithPython(t, item)
	if len(recovered) != 1 || recovered[0] != base+56*60 {
		t.Fatalf("alarmd recovered at %v, want only at :56", recovered)
	}
}

// The shape a real deployment reports: a point every five minutes under a
// five minute window, so exactly four holes between points and never a whole
// window. Python recovers at the first point whose windows all start after
// the anomaly, which is ten minutes after it; alarmd must do the same, at the
// same evaluation, for both anomalies of the series.
func TestASeriesReportingEveryFiveMinutesRecoversAsPythonDoes(t *testing.T) {
	item := pythonRecoveryCases(t)["every_five_minutes"]
	if item.Window != 5 || item.Required != 1 || item.Recovery != 5 {
		t.Fatalf("oracle case = %+v, want window 5, one anomaly, five recovery windows", item)
	}
	compared, recovered := compareWithPython(t, item)
	base := int64(1000 * 60)
	// Anomalies at minutes 0 and 15: the first closes at 10, the second at 25,
	// and every normal point after 25 still reads recovered.
	if compared != len(item.Points) || len(recovered) == 0 || recovered[0] != base+10*60 {
		t.Fatalf("compared %d of %d, alarmd recovered at %v, want first at minute 10", compared, len(item.Points), recovered)
	}
	afterSecond := []int64{}
	for _, at := range recovered {
		if at > base+15*60 {
			afterSecond = append(afterSecond, at)
		}
	}
	if len(afterSecond) == 0 || afterSecond[0] != base+25*60 {
		t.Fatalf("after the second anomaly alarmd recovered at %v, want first at minute 25", afterSecond)
	}
}

// Every case of the oracle: a point every k minutes for k = 1..9 under three
// trigger and recovery shapes, with the anomalies placed five ways. At every
// point after the alert opens, alarmd recovers exactly when Python's own
// check_result_cache does. k = 1 is the dense series, in the same sweep.
func TestSparseSeriesRecoverAtTheEvaluationsPythonDoes(t *testing.T) {
	cases := pythonRecoveryCases(t)
	total, recoveries, sparseRecoveries := 0, 0, 0
	ids := make([]string, 0, len(cases))
	for id := range cases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		item := cases[id]
		compared, recovered := compareWithPython(t, item)
		first := "none"
		if len(recovered) > 0 {
			first = strconv.FormatInt(recovered[0]-pythonBase, 10)
		}
		t.Logf("%s | python %s | python with recovery-nodata %s | alarmd first RECOVERY %s",
			id, item.Lifecycle.describe(), item.LifecycleRecoveryNoData.describe(), first)
		total += compared
		recoveries += len(recovered)
		if len(recovered) > 0 && len(item.Points) > 1 && item.Points[1].Time-item.Points[0].Time >= int64(item.Window)*60 {
			sparseRecoveries++
		}
	}
	// The sweep must have something to say: points compared, and recoveries
	// on series sparser than their window, which are the ones this rule is for.
	if total < 1000 || recoveries == 0 || sparseRecoveries < 10 {
		t.Fatalf("compared %d points, %d recoveries, %d sparse cases recovered: the oracle no longer exercises the rule",
			total, recoveries, sparseRecoveries)
	}
}

// A point that arrives late, but inside the current Slot's read, is read by
// that Slot and blocks the recovery it would otherwise have pre-empted. The
// read was made under the group's hold precisely so that such a point lands
// first. Window 5, one anomaly, five recovery windows, a point every five
// minutes and an anomaly at :40: at :50, read in the Slot a minute later with
// a 96 s hold, the series recovers - and with the late anomalous :48 in the
// same read, it does not. Python reads the same, and so does the next point:
// at :55 a window still holds :48, at :60 none does.
//
// A point later than the hold - a hold set too low - is not this test's: the
// read hold's probation finds such reads and raises the hold
// (lookback/probation.go), and is tested there.
//
// A window no read has covered yet keeps the skip rule: with the read ending
// at :48, the windows ending :48 to :50 are stepped over, three are counted
// behind them, and the fourth meets :40.
func TestALateAnomalyInsideTheSlotsReadBlocksRecovery(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 5, 1, 5, nil)})
	const hold = int64(96 * 1000)
	minute := func(m int64) int64 { return 6000 + m*60 }
	slot := func(m int64) int64 { return minute(m) + 60 }
	before := []sparsePoint{{minute(40), true}, {minute(45), false}, {minute(50), false}}
	if !recoveredV2(evaluateSlotV2(t, plan, before, 2, slot(50), slot(50), hold, true)) {
		t.Fatal("at :50 without the late point alarmd did not recover; the fixture does not separate the two")
	}
	late := []sparsePoint{{minute(40), true}, {minute(45), false}, {minute(48), true}, {minute(50), false}, {minute(55), false}, {minute(60), false}}
	if recoveredV2(evaluateSlotV2(t, plan, late, 3, slot(50), slot(50), hold, true)) {
		t.Fatal("at :50 alarmd recovered over the anomalous :48 its own read covered")
	}
	if recoveredV2(evaluateSlotV2(t, plan, late, 4, slot(55), slot(55), hold, true)) {
		t.Fatal("at :55 alarmd recovered over the anomalous :48")
	}
	if !recoveredV2(evaluateSlotV2(t, plan, late, 5, slot(60), slot(60), hold, true)) {
		t.Fatal("at :60 every window starts after :48 and alarmd did not recover")
	}

	uncovered := evaluateSlotV2(t, plan, before, 2, slot(50), minute(48), hold, true)
	if recoveredV2(uncovered) {
		t.Fatal("with the read ending at :48 alarmd recovered from windows no read had covered")
	}
	if window := uncovered.LevelOutcomes[0].DecisionWindow; window != nil && window.Recovery.SkippedWindows != 3 {
		t.Fatalf("recovery evidence = %+v, want the three windows past the read stepped over", window.Recovery)
	}
}

// A dense series has no holes once its alert is open, so the settled rule and
// the skip rule read it the same: from the anomaly on, its recoveries fall on
// the same points whether every window is settled or every window is inside
// the hold, which is how the walk read it before. Window 5, one anomaly, five
// recovery windows, a point a minute, anomalies at minutes 3 and 4: the first
// point whose five windows all start after minute 4 is minute 13.
//
// Before its first anomaly the two rules differ for a series whose state is
// known, on windows reaching back past its first point: those minutes are
// settled and read as quiet, as Python's check would read them. No alert is
// open there, so no alert closes, and the comparison starts at the anomaly.
func TestADenseSeriesRecoversAtTheSamePointAsBefore(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 5, 1, 5, nil)})
	points := make([]sparsePoint, 20)
	for index := range points {
		points[index] = sparsePoint{time: 6000 + int64(index)*60, anomalous: index == 3 || index == 4}
	}
	first := -1
	for index := 3; index < len(points); index++ {
		settled := evaluateAtV2(t, plan, points, index, points[index].time, 0)
		unsettled := evaluateAtV2(t, plan, points, index, points[index].time, execution.MaxReadHoldMillis)
		if settled.RecordResult != unsettled.RecordResult || settled.Completion != unsettled.Completion {
			t.Fatalf("minute %d: settled reads %q/%s, inside the hold %q/%s: a dense series changed timing",
				index, settled.RecordResult, settled.Completion, unsettled.RecordResult, unsettled.Completion)
		}
		if index > 4 && first < 0 && recoveredV2(settled) {
			first = index
		}
	}
	if first != 13 {
		t.Fatalf("dense series first recovered at minute %d, want 13", first)
	}
}

// NORMAL still needs a FULL window. A sparse series never has one: with
// recovery off it reports its history short at every point, and with recovery
// on it reads RECOVERY or a short history, never NORMAL.
func TestASparseSeriesIsNeverNormal(t *testing.T) {
	points := make([]sparsePoint, 12)
	for index := range points {
		points[index] = sparsePoint{time: 6000 + int64(index)*7*60}
	}
	for _, level := range []contract.LevelIRV2{levelWithoutRecoveryV2(5, 1, 5, 1), levelV2(5, 1, 5, 2, 5, nil)} {
		plan := compilePlanV2(t, []contract.LevelIRV2{level})
		series := append([]sparsePoint(nil), points...)
		if plan.Levels().At(0).Recovery().Enabled {
			// One anomaly, under the threshold of two, so the Level never
			// reads ABNORMAL either.
			series[len(series)-1].anomalous = true
		}
		for index := range series {
			result := evaluateAtV2(t, plan, series, index, series[index].time, 0)
			if result.RecordResult == contract.LevelResultNormal {
				t.Fatalf("a sparse series read NORMAL at point %d: %+v", index, result.LevelOutcomes[0])
			}
		}
	}
}

// Python gives up when its whole range holds no check result
// (recover.py:429-431). A history with a point long before the range and
// nothing in it - only reachable here, the production path always holds the
// record's own point - must not recover from windows read out of nothing.
func TestARecoveryWalkOverNoPointIsNotARecovery(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 5, 1, 5, nil)})
	level := plan.Levels().At(0)
	const source = int64(60000)
	history := pointHistory{step: 60, points: map[int64]bool{source - 20*60: false}}
	request := requestV2(t, plan, source, []DetectionFact{factV2(level, DetectionNormal)},
		[]LevelHistory{{LevelID: 5, View: history}}, activeFactsV2(t, plan, source))
	readInTheNextSlot(t, &request)
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredV2(result) {
		t.Fatal("alarmd recovered from a range that holds no point")
	}
	// One point at the oldest second Python reads is enough for it, and so
	// for alarmd.
	history.points[pythonRecoveryRangeStartV2(source, level.Trigger(), level.Recovery())] = false
	result, err = EvaluateV2(request)
	if err != nil {
		t.Fatal(err)
	}
	if !recoveredV2(result) {
		t.Fatal("a point at the start of Python's range did not let alarmd recover")
	}
}

func TestARequestWithAReadHoldOutOfRangeIsRefused(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 5, 1, 5, nil)})
	points := []sparsePoint{{time: 6000}}
	for _, hold := range []int64{-1, execution.MaxReadHoldMillis + 1} {
		request := requestV2(t, plan, 6000, []DetectionFact{factV2(plan.Levels().At(0), DetectionNormal)},
			[]LevelHistory{{LevelID: 5, View: pointHistory{step: 60, points: map[int64]bool{points[0].time: false}}}},
			activeFactsV2(t, plan, 6000))
		request.ReadHoldMillis = hold
		if _, err := EvaluateV2(request); err == nil {
			t.Fatalf("read hold %d was accepted", hold)
		}
	}
}

// A round of the Plan that was not read whole leaves a hole nobody read, and
// while its gap marker is open that round may be in any window the walk
// reads: none of them is settled. The traced case that recovers at :56 with
// the marker closed does not while it is open.
func TestAnOpenReadGapKeepsEveryWindowUnsettled(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 5, 1, 5, nil)})
	minute := func(m int64) int64 { return 6000 + m*60 }
	points := []sparsePoint{{minute(42), true}, {minute(47), false}, {minute(56), false}}
	evaluate := func(open bool) EvaluationResultV2 {
		level := plan.Levels().At(0)
		history := pointHistory{step: 60, points: map[int64]bool{}}
		for _, point := range points {
			history.points[point.time] = point.anomalous
		}
		request := requestV2(t, plan, minute(56), []DetectionFact{factV2(level, DetectionNormal)},
			[]LevelHistory{{LevelID: 5, View: history, ReadGapOpen: open}}, activeFactsV2(t, plan, minute(56)))
		request.SeriesSeenBefore = true
		readInTheNextSlot(t, &request)
		result, err := EvaluateV2(request)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if !recoveredV2(evaluate(false)) {
		t.Fatal("with no read gap open :56 does not recover; the fixture does not separate the two")
	}
	if recoveredV2(evaluate(true)) {
		t.Fatal("with a read gap open alarmd recovered: a round nobody read was counted as a quiet minute")
	}
}

// Points every five minutes and five recovery windows, under a trigger
// window of two and of five: the two shapes production Python's recoveries
// were read from. The oracle recovers both ten minutes after the anomaly, the
// first point whose five windows all start after it, and alarmd recovers at
// that same point.
func TestEveryFiveMinutesUnderWindowsOfTwoAndFiveRecoversAtPythonsMinute(t *testing.T) {
	cases := pythonRecoveryCases(t)
	for _, id := range []string{"every_five_minutes_window_2", "every_five_minutes_window_5"} {
		item := cases[id]
		if item.Lifecycle.Outcome != pythonRecovered || item.OpenedAt == nil {
			t.Fatalf("%s: oracle = %+v, want a RECOVERED alert", id, item.Lifecycle)
		}
		_, recovered := compareWithPython(t, item)
		if len(recovered) == 0 || recovered[0]-*item.OpenedAt != 10*60 {
			t.Fatalf("%s: alarmd recovered at %v, opened at %d, want ten minutes after the anomaly as Python",
				id, recovered, *item.OpenedAt)
		}
	}
}

// The whole-range-empty branch, recover.py:429-431: an open alert whose
// recovery range, as Python bounds it, holds no present settled point is not
// recovered, and stays open - the no-data close is not ported. The alert
// opened at :00; at :20 the Slot's read ends at :20, so the record's own
// point is past it - not settled - and Python's range :10 to :20 holds
// nothing else. Its pair is the same range with one present normal settled
// point, at :12, which recovers at :20 as Python's check does.
func TestAnOpenAlertWithNoSettledPointInItsRecoveryRangeStaysOpen(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 5, 1, 5, nil)})
	minute := func(m int64) int64 { return 6000 + m*60 }
	if start := pythonRecoveryRangeStartV2(minute(20), plan.Levels().At(0).Trigger(), plan.Levels().At(0).Recovery()); start != minute(10)+1 {
		t.Fatalf("Python's range starts at %d, want one second past :10", start)
	}
	empty := []sparsePoint{{minute(0), true}, {minute(20), false}}
	result := evaluateSlotV2(t, plan, empty, 1, minute(20), minute(20), 0, true)
	if recoveredV2(result) {
		t.Fatal("alarmd recovered an alert whose recovery range holds no present settled point")
	}
	one := []sparsePoint{{minute(0), true}, {minute(12), false}, {minute(20), false}}
	if !recoveredV2(evaluateSlotV2(t, plan, one, 2, minute(20), minute(20), 0, true)) {
		t.Fatal("with one present normal settled point in the range alarmd did not recover at :20")
	}
	// The same with the read covering the record's own point: it is the one
	// present settled point, and the alert recovers, as Python's does.
	if !recoveredV2(evaluateSeenAtV2(t, plan, empty, 1, minute(20), 0, true)) {
		t.Fatal("with the record's own point settled alarmd did not recover at :20")
	}
}

// A production alert's timing and flags: trigger window 2 needing 2, five
// recovery windows, anomalous points at :22, :23 and :32, a normal one at
// :33, nothing between. Python's check passes on the :32 point - the lone
// anomaly there is under the threshold of two, and every window is a miss -
// and alarmd, reading each point in the Slot a minute after it, recovers on
// that same point.
func TestAProductionShapedSeriesWithGapsRecoversAtPythonsMinute(t *testing.T) {
	item := pythonRecoveryCases(t)["production_w2_n2_r5"]
	if item.Lifecycle.Outcome != pythonRecovered || item.OpenedAt == nil {
		t.Fatalf("oracle = %+v, want a RECOVERED alert", item.Lifecycle)
	}
	compareWithPython(t, item)
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, item.Window, item.Required, item.Recovery, nil)})
	points := item.series()
	first := int64(-1)
	for index, point := range points {
		if point.time < *item.OpenedAt {
			continue
		}
		if recoveredV2(evaluateSeenAtV2(t, plan, points, index, point.time+60, 0, true)) {
			first = point.time
			break
		}
	}
	if first != item.Lifecycle.At {
		t.Fatalf("read a Slot after each point, alarmd first recovered on %d, Python at %d", first, item.Lifecycle.At)
	}
}

// The read hold does not delay a sparse series' recovery. Every point is read
// in the Slot a minute after it plus a 60 s query delay, under holds of 96,
// 120, 300 and 600 s, for points every three and every five minutes under
// windows of two and five with five recovery windows: alarmd recovers at the
// oracle's minute every time. A position counts once the current Slot's own
// read covered it, and the hold only decides how late that read was made.
// Before, the boundary was the evaluation time less the hold, and a 600 s
// hold under window 5 put every retained window past it: no recovery at all.
func TestTheReadHoldDoesNotDelayASparseSeriesRecovery(t *testing.T) {
	cases := pythonRecoveryCases(t)
	const delay = int64(60)
	for _, id := range []string{"every_three_minutes_window_2", "every_three_minutes_window_5",
		"every_five_minutes_window_2", "every_five_minutes_window_5"} {
		item := cases[id]
		if item.Lifecycle.Outcome != pythonRecovered || item.OpenedAt == nil {
			t.Fatalf("%s: oracle = %+v, want a RECOVERED alert", id, item.Lifecycle)
		}
		plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, item.Window, item.Required, item.Recovery, nil)})
		points := item.series()
		for _, holdSeconds := range []int64{96, 120, 300, 600} {
			first := int64(-1)
			for index, point := range points {
				if point.time <= *item.OpenedAt {
					continue
				}
				slot := point.time + 60 + delay
				if recoveredV2(evaluateSlotV2(t, plan, points, index, slot, slot-delay, holdSeconds*1000, true)) {
					first = point.time
					break
				}
			}
			if first != item.Lifecycle.At {
				t.Errorf("%s, %d s hold: alarmd first recovered at %d, Python at %d", id, holdSeconds, first-pythonBase, item.Lifecycle.At-pythonBase)
			}
		}
	}
}

// A gap marker clears after RequiredFullSlots whole Slots, the most history
// points any Level needs (W + R - 1), and the recovery walk reads positions on
// the trigger's step. The two count the same thing only if a Slot advances the
// window by one position, so a Plan detected more often than it aggregates -
// a 60 s detection over a 300 s aggregation - is the case to check.
//
// It does: the trigger's step must equal the evaluation interval, which is
// the Slot cadence (strategy compiler; controlplane planScheduleSpec holds the
// schedule to the same value). A 60 s interval over a 300 s aggregation runs
// its windows on 60 s positions, one per Slot, so W + R - 1 Slots are W + R - 1
// positions; a 300 s step under a 60 s interval is refused rather than run.
func TestAGapMarkerCountsSlotsInTheWalksOwnPositions(t *testing.T) {
	notTimeAligned := func(plan *contract.EvaluationPlanV2) {
		plan.StrategyIR.ExecutionSemantics.EvaluationInterval = 60
		plan.StrategyIR.ExecutionSemantics.AggregationInterval = 300
		plan.StrategyIR.ExecutionSemantics.QueryWindow = 300
	}
	level := levelV2(5, 1, 5, 1, 5, nil)
	plan := compilePlanV2WithOutput(t, []contract.LevelIRV2{level}, notTimeAligned)
	compiled := plan.Levels().At(0)
	if step := compiled.Trigger().StepSeconds; step != plan.EvaluationSemantics().EvaluationInterval {
		t.Fatalf("trigger step %d s under a %d s interval: a Slot would move the window by more or less than one position",
			step, plan.EvaluationSemantics().EvaluationInterval)
	}
	if points := compiled.RequiredDetectHistoryPoints(); points != 5+5-1 {
		t.Fatalf("required history %d positions, want W + R - 1 = 9 Slots of 60 s", points)
	}

	stepped := levelV2(5, 1, 5, 1, 5, nil)
	config := map[string]any{"window_size": 5, "required_anomalies": 1, "step_seconds": 300}
	stepped.TriggerPlan.Config = mustJSONV2(config)
	result := compileResultV2(t, []contract.LevelIRV2{stepped}, notTimeAligned)
	if _, ok := result.Plan(); ok && len(result.LevelTerminals()) == 0 {
		t.Fatal("a 300 s trigger step compiled under a 60 s evaluation interval: a marker counted in Slots would " +
			"clear while its unread bucket is still inside the walk")
	}
}

// A request that names no read settles nothing: every window keeps the skip
// rule, so a missing requirement can delay a recovery and never bring one
// early. The traced case recovers at :56 read in the next Slot, and does not
// when the same request names no read end.
func TestARequestWithNoReadEndSettlesNothing(t *testing.T) {
	plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 5, 1, 5, nil)})
	minute := func(m int64) int64 { return 6000 + m*60 }
	history := pointHistory{step: 60, points: map[int64]bool{minute(42): true, minute(47): false, minute(56): false}}
	request := requestV2(t, plan, minute(56), []DetectionFact{factV2(plan.Levels().At(0), DetectionNormal)},
		[]LevelHistory{{LevelID: 5, View: history}}, activeFactsV2(t, plan, minute(56)))
	request.SeriesSeenBefore = true
	readInTheNextSlot(t, &request)
	if result, err := EvaluateV2(request); err != nil || !recoveredV2(result) {
		t.Fatalf("read in the next Slot :56 did not recover (err %v)", err)
	}
	request.ReadEnd = 0
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredV2(result) {
		t.Fatal("with no read end alarmd recovered: holes no read is known to have covered were read as quiet minutes")
	}
	if window := result.LevelOutcomes[0].DecisionWindow; window != nil && window.Recovery.ObservedConsecutiveMisses != 0 {
		t.Fatalf("recovery evidence = %+v, want no window counted", window.Recovery)
	}
}

// A window is a miss when it holds fewer anomalies than the strategy's
// required count, not when it holds none: four of five, five recovery
// windows, anomalies at :13 to :16. The window ending :17 holds four and
// still triggers; the one ending :18 holds three and is the first miss, so
// the fifth miss is the window ending :22, the last anomaly plus 360 s -
// Python's decision minute in production and in the oracle. Read in the Slot
// a minute after each point, alarmd decides RECOVERY on the :22 point, in the
// :23 Slot. With a point every five minutes after the anomalies, the first
// point whose windows all hold fewer than four is :26, for both.
func TestFourOfFiveRecoversWhenAWindowHoldsFewerThanFour(t *testing.T) {
	cases := pythonRecoveryCases(t)
	for _, want := range []struct {
		id     string
		minute int64
	}{{"four_of_five_dense", 22}, {"four_of_five_sparse_after", 26}} {
		item := cases[want.id]
		if item.Required != 4 || item.Window != 5 || item.Recovery != 5 {
			t.Fatalf("%s: oracle case %d of %d, R %d, want 4 of 5, R 5", want.id, item.Required, item.Window, item.Recovery)
		}
		if item.Lifecycle.Outcome != pythonRecovered || item.Lifecycle.At != pythonBase+want.minute*60 {
			t.Fatalf("%s: oracle = %+v, want RECOVERED at minute %d", want.id, item.Lifecycle, want.minute)
		}
		compareWithPython(t, item)
		plan := compilePlanV2(t, []contract.LevelIRV2{levelV2(5, 1, 5, 4, 5, nil)})
		points := item.series()
		first := int64(-1)
		for index, point := range points {
			if point.time <= *item.OpenedAt {
				continue
			}
			slot := point.time + 60
			if recoveredV2(evaluateSlotV2(t, plan, points, index, slot, slot, 0, true)) {
				first = point.time
				break
			}
		}
		if first != item.Lifecycle.At {
			t.Fatalf("%s: read a Slot after each point, alarmd first recovered on minute %d, Python on %d",
				want.id, (first-pythonBase)/60, want.minute)
		}
	}
	if dense := cases["four_of_five_dense"]; dense.Lifecycle.At-(pythonBase+16*60) != 360 {
		t.Fatalf("dense recovery %d s after the last anomaly, want 360", dense.Lifecycle.At-(pythonBase+16*60))
	}
}

// Shaped like a real Plan detected every 60 s over a 120 s aggregation
// window (fictional ids): it compiles, its walk positions are 60 s apart -
// one per Slot - and its gap marker waits for as many Slots as its walk has
// positions to read.
func TestADetectionEveryMinuteOverATwoMinuteAggregation(t *testing.T) {
	shape := func(plan *contract.EvaluationPlanV2) {
		plan.StrategyIR.ExecutionSemantics.EvaluationInterval = 60
		plan.StrategyIR.ExecutionSemantics.AggregationInterval = 120
		plan.StrategyIR.ExecutionSemantics.QueryWindow = 120
	}
	plan := compilePlanV2WithOutput(t, []contract.LevelIRV2{levelV2(5, 1, 5, 4, 5, nil)}, shape)
	level := plan.Levels().At(0)
	if step := level.Trigger().StepSeconds; step != 60 || plan.EvaluationSemantics().EvaluationInterval != 60 {
		t.Fatalf("trigger step %d s, evaluation interval %d s, want 60 and 60", step, plan.EvaluationSemantics().EvaluationInterval)
	}
	if got, want := plan.RequiredFullSlots(), level.RequiredDetectHistoryPoints(); got != want || want != 5+5-1 {
		t.Fatalf("marker waits %d Slots, the walk reads %d positions, want both W + R - 1 = 9", got, want)
	}
}
