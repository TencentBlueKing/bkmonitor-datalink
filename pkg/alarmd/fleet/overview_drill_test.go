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
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// drillDispositions is each strategy's disposition records: refusals in
// several areas, a strategy refused twice in one area, a level-scoped
// (partial) one, a noted one, one ignored by design, a source-state record
// that is no capability fact, and strategies with none.
var drillDispositions = map[string][]StrategyDisposition{
	"1": {{Disposition: dispositionCapabilityUnsupported, Reason: "QUERY_SOURCE_NOT_MIGRATED"}},
	"2": {{Disposition: dispositionCapabilityUnsupported, Reason: "QUERY_FTA_UNSUPPORTED"},
		{Disposition: dispositionCapabilityUnsupported, Reason: "QUERY_SOURCE_NOT_MIGRATED"}},
	"3": {{Disposition: dispositionCapabilityUnsupported, Reason: "ALGORITHM_NOT_MIGRATED", Scope: "LEVEL"},
		{Disposition: dispositionCapabilityUnsupported, Reason: "UNSUPPORTED_TARGET_SCOPE"}},
	"4": {{Disposition: dispositionConfigNoted, Reason: "EFFECTIVE_TIME_SCHEMA_UNSUPPORTED"}},
	"5": {{Disposition: dispositionCompatibilityIgnored, Reason: "SOME_IGNORED_CODE"}},
	"6": {{Disposition: dispositionAccepted}},
	"7": {{Disposition: dispositionConfigRejected, Reason: "NOT_A_KNOWN_REJECTION"}},
	"8": nil,
	"9": nil,
	// A refusal without a reason is counted under its disposition.
	"10": {{Disposition: dispositionConfigRejected}},
}

func drillBoard(t *testing.T) (*boardIndex, *SourceCapability) {
	t.Helper()
	universe, records := []string{}, []CapabilityRecord{}
	for id, dispositions := range drillDispositions {
		universe = append(universe, id)
		for _, disposition := range dispositions {
			records = append(records, CapabilityRecord{StrategyID: id, Disposition: disposition.Disposition, Reason: disposition.Reason, Scope: disposition.Scope})
		}
	}
	index := buildBoard("c1", time.Unix(10_000, 0), "d", universe, func(id string) DiagnosisRow {
		row := DiagnosisRow{StrategyID: id, Verdict: StateDetecting, Action: ActionNone}
		for _, disposition := range drillDispositions[id] {
			row.Dispositions = append(row.Dispositions, DiagnosisDisposition{StrategyDisposition: disposition})
		}
		return row
	}, nil, nil, nil, nil)
	return index, CountCapability(records, len(universe))
}

// Every cell of the overview's matrix and every refusal reason, followed to
// the strategy page with its drill key, lists as many strategies as the
// overview counts: the board selects on them by the Leader's own rules.
func TestOverviewDrillKeysListAsManyStrategiesAsTheyCount(t *testing.T) {
	index, counted := drillBoard(t)
	capability := overviewCapabilityOf(&SourceFacts{Capability: counted})
	cells := 0
	for _, row := range capability.Matrix {
		counts := map[string]int{CapabilitySupported: row.Supported, CapabilityPartial: row.Partial,
			CapabilityUnsupportedByDesign: row.UnsupportedByDesign, CapabilityNotYetSupported: row.NotYetSupported}
		for outcome, count := range counts {
			drill, linked := row.Drill[outcome]
			if count == 0 {
				if linked {
					t.Errorf("%s/%s: a zero cell carries a drill key", row.Area, outcome)
				}
				continue
			}
			cells++
			if !reflect.DeepEqual(drill, BoardDrill{"area": row.Area, "capability": outcome}) {
				t.Fatalf("%s/%s drill %v", row.Area, outcome, drill)
			}
			filter, _, refusal := parseBoardQuery(drillQuery(drill))
			if refusal != nil {
				t.Fatalf("%s/%s drill refused: %v", row.Area, outcome, refusal)
			}
			if listed := index.page(filter, "", BoardMaxRows, DiagnosisSummaryFreshFor).Matched; listed != count {
				t.Errorf("%s/%s: overview counts %d, the strategy page lists %d", row.Area, outcome, count, listed)
			}
		}
	}
	if cells < 8 {
		t.Fatalf("only %d non-zero cells: the fixture no longer covers the matrix", cells)
	}
	if len(capability.Reasons) < 4 {
		t.Fatalf("reasons %+v", capability.Reasons)
	}
	for _, reason := range capability.Reasons {
		filter, _, refusal := parseBoardQuery(drillQuery(reason.Drill))
		if refusal != nil || !reflect.DeepEqual(reason.Drill, BoardDrill{"reason": reason.Code}) {
			t.Fatalf("%s drill %v refused %v", reason.Code, reason.Drill, refusal)
		}
		if listed := index.page(filter, "", BoardMaxRows, DiagnosisSummaryFreshFor).Matched; listed != reason.Strategies {
			t.Errorf("reason %s: overview counts %d, the strategy page lists %d", reason.Code, reason.Strategies, listed)
		}
	}
}

func drillQuery(drill BoardDrill) url.Values {
	query := url.Values{"board": {"1"}}
	for key, value := range drill {
		query.Set(key, value)
	}
	return query
}

// The late line is the scheduling census, the number the evaluate stage's
// failures carry, with the strategy page's selection for late and overdue;
// without a census it is unmeasured and says so, with the same selection.
func TestOverviewLateLineIsTheCensusWithItsSelection(t *testing.T) {
	health := HealthResponse{Schedule: &ScheduleCensus{Late: 3, Overdue: 2, Never: 9}}
	overview := overviewOf(health, nil, false, time.Unix(1, 0))
	want := OverviewLate{Objects: overviewMeasured(5, "objects"), Line: "5 个检测对象迟到或超期", Drill: BoardDrill{"punctuality": "late,overdue"}}
	if !reflect.DeepEqual(overview.Late, want) {
		t.Fatalf("late = %+v, want %+v", overview.Late, want)
	}
	for _, stage := range overview.Flow {
		if stage.Stage == "evaluate" && stage.Failures.Value != overview.Late.Objects.Value {
			t.Errorf("evaluate failures %v, late objects %v", stage.Failures.Value, overview.Late.Objects.Value)
		}
	}
	filter, _, refusal := parseBoardQuery(drillQuery(overview.Late.Drill))
	if refusal != nil || !filter.Punctuality[PunctualityLate] || !filter.Punctuality[PunctualityOverdue] || len(filter.Punctuality) != 2 {
		t.Fatalf("late drill %+v refused %v", filter, refusal)
	}
	blind := overviewOf(HealthResponse{}, nil, false, time.Unix(1, 0))
	if blind.Late.Objects.Measured || !strings.Contains(blind.Late.Line, "还没读到") || !reflect.DeepEqual(blind.Late.Drill, want.Drill) {
		t.Fatalf("without a census: %+v", blind.Late)
	}
}

// An area reads capability words in it, so it comes with them; an unknown
// area is refused; a reason is any code; the cursor is bound to both.
func TestTheBoardsAreaAndReasonSelections(t *testing.T) {
	for _, query := range []string{"board=1&area=source", "board=1&area=nowhere&capability=partial"} {
		values, _ := url.ParseQuery(query)
		if _, _, refusal := parseBoardQuery(values); refusal == nil {
			t.Errorf("%s accepted", query)
		}
	}
	index, _ := drillBoard(t)
	first := index.page(BoardFilter{Area: CapabilitySource, Capability: map[string]bool{CapabilityNotYetSupported: true}}, "", 1, DiagnosisSummaryFreshFor)
	if first.NextCursor == "" {
		t.Fatalf("first page %+v", first)
	}
	other := index.page(BoardFilter{Area: CapabilityTarget, Capability: map[string]bool{CapabilityNotYetSupported: true}}, first.NextCursor, 1, DiagnosisSummaryFreshFor)
	if !other.Reread {
		t.Error("a cursor from another area continued")
	}
	byReason := index.page(BoardFilter{Reason: "QUERY_SOURCE_NOT_MIGRATED"}, first.NextCursor, 1, DiagnosisSummaryFreshFor)
	if !byReason.Reread {
		t.Error("a cursor from an area continued under a reason")
	}
	if none := index.page(BoardFilter{Reason: "NOBODY_HAS_THIS"}, "", 10, DiagnosisSummaryFreshFor); none.Matched != 0 {
		t.Errorf("an unknown reason matched %d", none.Matched)
	}
}

// The board's HTTP selection takes the overview's drill keys as they are.
func TestTheBoardTakesTheOverviewsDrillKeysOverHTTP(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"77"}
	for _, query := range []string{"board=1&area=source&capability=not_yet_supported", "board=1&reason=QUERY_SOURCE_NOT_MIGRATED", "board=1&punctuality=late,overdue"} {
		if code, _, _ := rig.board(t, query); code != http.StatusOK {
			t.Errorf("%s: status %d", query, code)
		}
	}
	if code, _, _ := rig.board(t, "board=1&area=source"); code != http.StatusBadRequest {
		t.Errorf("an area alone: status %d, want refused", code)
	}
}
