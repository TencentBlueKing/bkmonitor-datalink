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
	"testing"
	"time"
)

// A row decides ahead of another by its check first, then by the earlier
// onset -- no onset after every onset -- then by the object's identity.
func TestARowDecidesByCheckThenOnsetThenObject(t *testing.T) {
	early, late := now.Add(-2*time.Hour), now.Add(-time.Hour)
	for name, tc := range map[string]struct {
		rank     int
		row      Anomaly
		bestRank int
		best     Anomaly
		want     bool
	}{
		"more severe":              {1, Anomaly{QueryGroup: "qg-z", Since: late}, 2, Anomaly{QueryGroup: "qg-a", Since: early}, true},
		"less severe":              {2, Anomaly{QueryGroup: "qg-a", Since: early}, 1, Anomaly{QueryGroup: "qg-z", Since: late}, false},
		"earlier onset":            {1, Anomaly{QueryGroup: "qg-z", Since: early}, 1, Anomaly{QueryGroup: "qg-a", Since: late}, true},
		"later onset":              {1, Anomaly{QueryGroup: "qg-a", Since: late}, 1, Anomaly{QueryGroup: "qg-z", Since: early}, false},
		"an onset over none":       {1, Anomaly{QueryGroup: "qg-z", Since: late}, 1, Anomaly{QueryGroup: "qg-a"}, true},
		"none under an onset":      {1, Anomaly{QueryGroup: "qg-a"}, 1, Anomaly{QueryGroup: "qg-z", Since: late}, false},
		"same onset, first object": {1, Anomaly{QueryGroup: "qg-a", Since: late}, 1, Anomaly{QueryGroup: "qg-z", Since: late}, true},
		"same onset, later object": {1, Anomaly{QueryGroup: "qg-z", Since: late}, 1, Anomaly{QueryGroup: "qg-a", Since: late}, false},
		"neither onset, by object": {1, Anomaly{QueryGroup: "qg-a"}, 1, Anomaly{QueryGroup: "qg-z"}, true},
	} {
		if got := decidesBefore(tc.rank, tc.row, tc.bestRank, tc.best); got != tc.want {
			t.Errorf("%s: decides before = %v, want %v", name, got, tc.want)
		}
	}
}

// Rows of one strategy under one check decide its line the same way in any
// order they are walked in: the earliest onset, then the first object.
func TestAStrategysLineIsDecidedTheSameWhateverOrderItsRowsComeIn(t *testing.T) {
	strategy := []StrategyRef{{StrategyID: "4101", BusinessID: "7"}}
	onset := now.Add(-time.Hour)
	rows := []Anomaly{
		{QueryGroup: "qg-c", Finding: Finding{Check: CheckWindowUndecided}, ReasonLastAt: now, Strategies: strategy,
			Coverage: shortWindows(6, 9, VerdictDataAbsentWhenQueried)},
		{QueryGroup: "qg-b", Finding: Finding{Check: CheckWindowUndecided}, Since: onset, SinceFrom: SinceBusinessState, ReasonLastAt: now,
			Strategies: strategy, Coverage: shortWindows(6, 9, VerdictDataAbsentWhenQueried)},
		{QueryGroup: "qg-a", Finding: Finding{Check: CheckWindowUndecided}, Since: onset, SinceFrom: SinceBusinessState, ReasonLastAt: now,
			Strategies: strategy, Coverage: shortWindows(6, 9, VerdictDataAbsentWhenQueried)},
	}
	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}} {
		walked := make([]Anomaly, 0, len(rows))
		for _, index := range order {
			walked = append(walked, rows[index])
		}
		lines := StrategyLines(&View{Anomalies: walked}, now)
		if len(lines) != 1 || lines[0].DecidingObject != "qg-a" {
			t.Errorf("walked %v: lines %+v, want qg-a deciding", order, lines)
		}
	}
}

// A strategy's diagnosis is decided by the same rule as its line: of two
// rows under one check on its two objects, the one with the earlier onset,
// whichever replica holds it and whichever Plan is walked first.
func TestAStrategysDiagnosisIsDecidedByTheEarlierOnsetWhicheverObjectComesFirst(t *testing.T) {
	onObject := func(queryGroup, replica string, since time.Time) Anomaly {
		row := anomaly(queryGroup)
		row.Replica, row.Since = replica, since
		row.Strategies = []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}
		return row
	}
	early, late := now.Add(-3*time.Hour), now.Add(-time.Hour)
	for want, onsets := range map[string][2]time.Time{"qg-4101-a": {early, late}, "qg-4101-b": {late, early}} {
		rig := newDiagnosisRigHolding(t, diagnosisFacts(), nil,
			[]Anomaly{onObject("qg-4101-a", "pod-a", onsets[0])}, []Anomaly{onObject("qg-4101-b", "pod-b", onsets[1])})
		rig.universe = []string{"4101"}
		body := rig.page(t, "", 0)
		if len(body.Strategies) != 1 || body.Strategies[0].DecidingObject != want {
			t.Errorf("onsets %v: rows %+v, want %s deciding", onsets, body.Strategies, want)
		}
	}
}
