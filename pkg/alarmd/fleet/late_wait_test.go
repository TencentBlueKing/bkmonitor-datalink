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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// lateRig is strategy 4101's two objects as the replicas publish them:
// qg-4101-a on pod-a, qg-4101-b on pod-b, each with its census and due lists,
// and whatever readings each replica holds.
func lateRig(t *testing.T, dueA, dueB *DueClasses, onA, onB []Anomaly, totalA int) *diagnosisRig {
	t.Helper()
	snapshots := healthySnapshots()
	snapshots[0].Owned, snapshots[0].Determined = 2, 2
	snapshots[0].OwnedObjects = []string{"qg-4101-a", "qg-other"}
	snapshots[0].Anomalies, snapshots[0].TotalAnomalies = onA, totalA
	snapshots[0].Schedule, snapshots[0].Due = &ScheduleCensus{Late: len(dueA.Late), Overdue: len(dueA.Overdue)}, dueA
	snapshots[1].Owned, snapshots[1].Determined = 1, 1
	snapshots[1].OwnedObjects = []string{"qg-4101-b"}
	snapshots[1].Anomalies, snapshots[1].TotalAnomalies = onB, len(onB)
	snapshots[1].Schedule, snapshots[1].Due = &ScheduleCensus{Late: len(dueB.Late), Overdue: len(dueB.Overdue)}, dueB
	rig := newDiagnosisRigFrom(t, diagnosisFacts(), nil, snapshots)
	rig.universe = []string{"4101"}
	return rig
}

func (rig *diagnosisRig) view(t *testing.T, strategyID string) StrategyViewActual {
	t.Helper()
	w := httptest.NewRecorder()
	rig.handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/diagnose?view=1&strategy_id="+strategyID, nil))
	var view StrategyViewActual
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &view) != nil {
		t.Fatalf("view status %d body %s", w.Code, w.Body.String())
	}
	return view
}

// warmingReading is pod-b's reading of qg-4101-b: the evaluation waiting on
// history, its windows short, most of them new series, one empty, two held
// by a guard.
func warmingReading(reasonSince time.Time) Anomaly {
	row := anomaly("qg-4101-b")
	row.Kind, row.ReasonCode, row.ReasonSince = KindDegradedRun, "HISTORY_WARMING", reasonSince
	row.Strategies = []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}
	row.Coverage = &HistoryCoverage{Levels: 8, Short: 6, Fresh: 5, ShortFresh: 4, Empty: 1, Guarded: 2}
	return row
}

// A late strategy carries what its worst late object waited on, read from
// that object's own replica: here the overdue object on pod-b waiting on
// history, with the warming split the replica published, and since when the
// reason has held. The drawer groups both late objects by their wait: the
// one pod-a found nothing wrong with is named as having no reading, not left
// blank.
func TestALateStrategySaysWhatItsLateObjectsWaitedOn(t *testing.T) {
	since := now.Add(-7 * time.Minute)
	rig := lateRig(t, &DueClasses{Late: []string{"qg-4101-a"}}, &DueClasses{Overdue: []string{"qg-4101-b"}},
		nil, []Anomaly{warmingReading(since)}, 0)
	code, board, raw := rig.board(t, "board=1")
	if code != http.StatusOK || len(board.Rows) != 1 {
		t.Fatalf("board %d %+v", code, board)
	}
	row := board.Rows[0]
	want := LateWait{Stage: StageEvaluate, Reason: "HISTORY_WARMING", Since: &since,
		Warming: &WarmingSplit{Short: 6, ShortFresh: 4, Empty: 1, Guarded: 2}}
	if row.Punctuality != PunctualityOverdue || row.LateWait == nil || !sameWait(*row.LateWait, want) {
		t.Fatalf("row %s late_wait %+v, want overdue waiting %+v", row.Punctuality, row.LateWait, want)
	}
	// On the wire, under the names the page reads.
	rows, _ := raw["rows"].([]any)
	wire, _ := rows[0].(map[string]any)["late_wait"].(map[string]any)
	warming, _ := wire["warming"].(map[string]any)
	if wire["stage"] != "EVALUATE" || wire["reason"] != "HISTORY_WARMING" || warming["short_fresh"] != 4.0 || warming["empty"] != 1.0 || warming["guarded"] != 2.0 {
		t.Fatalf("late_wait on the wire %v", wire)
	}

	view := rig.view(t, "4101")
	if len(view.LateWaits) != 2 {
		t.Fatalf("late_waits %+v, want the two late objects in two groups", view.LateWaits)
	}
	byWait := map[string]LateWaitGroup{}
	for _, group := range view.LateWaits {
		byWait[group.Reason+group.Unknown] = group
	}
	if group := byWait["HISTORY_WARMING"]; group.Objects != 1 || !sameWait(group.LateWait, want) {
		t.Errorf("warming group %+v, want one object waiting %+v", group, want)
	}
	if group := byWait[LateWaitNoReading]; group.Objects != 1 || group.Stage != "" || group.Reason != "" || group.Since != nil {
		t.Errorf("unread group %+v, want one object named as having no reading", group)
	}
	composed := ComposeStrategyView(view, nil)
	if len(composed.Actual.LateWaits) != 2 {
		t.Errorf("composed view lost the late waits: %+v", composed.Actual)
	}
}

// Nothing late, nothing carried: an on-time strategy has no wait on its row
// and none in its drawer, even while its replica holds a reading for its
// object.
func TestAnOnTimeStrategyCarriesNoWait(t *testing.T) {
	rig := lateRig(t, &DueClasses{}, &DueClasses{}, nil, []Anomaly{warmingReading(now.Add(-time.Minute))}, 0)
	_, board, raw := rig.board(t, "board=1")
	if len(board.Rows) != 1 || board.Rows[0].Punctuality != PunctualityOnTime || board.Rows[0].LateWait != nil {
		t.Fatalf("rows %+v, want one on-time row without a wait", board.Rows)
	}
	if _, present := raw["rows"].([]any)[0].(map[string]any)["late_wait"]; present {
		t.Fatal("an on-time row carries late_wait")
	}
	if view := rig.view(t, "4101"); len(view.LateWaits) != 0 {
		t.Fatalf("late_waits %+v, want none", view.LateWaits)
	}
	// Nor does a strategy whose object nobody can place: pod-a's lists were
	// cut, so qg-4101-a is unknown, which is not late.
	rig = lateRig(t, &DueClasses{LateCut: true}, &DueClasses{}, nil, []Anomaly{warmingReading(now.Add(-time.Minute))}, 0)
	_, board, _ = rig.board(t, "board=1")
	if len(board.Rows) != 1 || board.Rows[0].Punctuality != PunctualityUnknown || board.Rows[0].LateWait != nil {
		t.Fatalf("rows %+v, want one unknown row without a wait", board.Rows)
	}
	if view := rig.view(t, "4101"); len(view.LateWaits) != 0 {
		t.Fatalf("late_waits %+v for an unknown object, want none", view.LateWaits)
	}
}

// A late object no reading names while some replica's readings were cut may
// have had one past the cut, and says that rather than "no reading".
func TestALateObjectWithItsReadingsCutSaysSo(t *testing.T) {
	rig := lateRig(t, &DueClasses{Late: []string{"qg-4101-a"}}, &DueClasses{}, nil, nil, 3)
	_, board, _ := rig.board(t, "board=1")
	if len(board.Rows) != 1 || board.Rows[0].LateWait == nil || board.Rows[0].LateWait.Unknown != LateWaitReadingsCut || board.Rows[0].LateWait.Reason != "" {
		t.Fatalf("rows %+v, want the late object's wait unknown because the readings were cut", board.Rows)
	}
}

// The split is the window's account of HISTORY_WARMING and nothing else's: a
// late object waiting on a query keeps its coverage off the wait, and a
// previous owner's reading never decides an object its plan places on
// another replica.
func TestTheWarmingSplitIsOnlyOnAWarmingWaitFromTheObjectsOwnReplica(t *testing.T) {
	timeout := warmingReading(now.Add(-time.Minute))
	timeout.ReasonCode = "QUERY_TIMEOUT"
	rig := lateRig(t, &DueClasses{}, &DueClasses{Late: []string{"qg-4101-b"}}, nil, []Anomaly{timeout}, 0)
	_, board, _ := rig.board(t, "board=1")
	wait := board.Rows[0].LateWait
	if wait == nil || wait.Reason != "QUERY_TIMEOUT" || wait.Stage != StageQuery || wait.Warming != nil {
		t.Fatalf("late_wait %+v, want the query wait without a warming split", wait)
	}
	// pod-a still lists qg-4101-b from before the object moved to pod-b.
	stale := warmingReading(now.Add(-time.Hour))
	stale.Replica = "pod-a"
	rig = lateRig(t, &DueClasses{}, &DueClasses{Late: []string{"qg-4101-b"}}, []Anomaly{stale}, nil, 1)
	_, board, _ = rig.board(t, "board=1")
	if wait := board.Rows[0].LateWait; wait == nil || wait.Unknown != LateWaitNoReading || wait.Warming != nil {
		t.Fatalf("late_wait %+v, want no reading: pod-a's row is about an object it no longer owns", wait)
	}
}

func sameWait(got, want LateWait) bool {
	if got.Stage != want.Stage || got.Reason != want.Reason || got.Unknown != want.Unknown {
		return false
	}
	if (got.Since == nil) != (want.Since == nil) || got.Since != nil && !got.Since.Equal(*want.Since) {
		return false
	}
	if (got.Warming == nil) != (want.Warming == nil) || got.Warming != nil && *got.Warming != *want.Warming {
		return false
	}
	return true
}
