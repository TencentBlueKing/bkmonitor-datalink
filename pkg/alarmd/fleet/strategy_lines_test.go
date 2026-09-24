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
	"strings"
	"testing"
	"time"
)

// A strategy is one line, whatever its objects are under: the most severe
// check by the table's order decides the words, the onset is the earliest
// object's, the last good time the latest, and the objects are counted
// once each. A strategy on two rows under two checks was two lines in two
// places before; here it is one.
func TestAStrategyIsOneLineOverEveryObjectThatRunsIt(t *testing.T) {
	strategy := []StrategyRef{{StrategyID: "4101", BusinessID: "7"}}
	other := []StrategyRef{{StrategyID: "4102", BusinessID: "7"}}
	rows := []Anomaly{
		// Under the undecided window, holes the data's, seen an hour ago.
		{QueryGroup: "qg-window", Finding: Finding{Check: CheckWindowUndecided}, Since: now.Add(-time.Hour), SinceFrom: SinceBusinessState,
			LastHealthyAt: now.Add(-2 * time.Hour), ReasonLastAt: now, Coverage: shortWindows(6, 9, VerdictDataAbsentWhenQueried), Strategies: strategy},
		// The same strategy's other object, under a dependency that is
		// down -- earlier in the table, so it decides the line -- since two
		// hours, healthy more recently.
		{QueryGroup: "qg-redis", Finding: Finding{Check: CheckDependencyDown}, Since: now.Add(-2 * time.Hour), SinceFrom: SinceBusinessState,
			LastHealthyAt: now.Add(-90 * time.Minute), ReasonLastAt: now, Strategies: strategy},
		// A second strategy sharing the first object.
		{QueryGroup: "qg-window", Finding: Finding{Check: CheckWindowUndecided}, Since: now.Add(-time.Hour), SinceFrom: SinceBusinessState,
			ReasonLastAt: now, Coverage: shortWindows(6, 9, VerdictDataAbsentWhenQueried), Strategies: other},
	}
	view := &View{Anomalies: rows}
	lines := StrategyLines(view, now)
	if len(lines) != 2 {
		t.Fatalf("lines = %+v, want one per strategy", lines)
	}
	first := lines[0]
	if first.StrategyID != "4101" || first.Objects != 2 || first.DecidingObject != "qg-redis" {
		t.Fatalf("first line = %+v, want strategy 4101 over 2 objects decided by the dependency row", first)
	}
	if first.Standing.Check != CheckDependencyDown || first.Standing.State != StateDependencyUnanswered || first.Standing.Action != ActionServiceFix {
		t.Fatalf("first standing = %+v, want the dependency's words: the more severe check decides", first.Standing)
	}
	if first.SinceBasis != SinceExact {
		t.Fatalf("since basis = %s, want EXACT for a business-state onset", first.SinceBasis)
	}
	if first.Since == nil || !first.Since.Equal(now.Add(-2*time.Hour)) || first.LastGoodAt == nil || !first.LastGoodAt.Equal(now.Add(-90*time.Minute)) {
		t.Fatalf("first clocks = since %v / last good %v, want the earliest onset and the latest healthy completion", first.Since, first.LastGoodAt)
	}
	if !strings.HasPrefix(first.Line, "策略 4101 · 2 个对象 · 依赖没应答 · 本服务处理") {
		t.Fatalf("first line reads %q", first.Line)
	}
	second := lines[1]
	if second.StrategyID != "4102" || second.Objects != 1 || second.Standing.Action != ActionDataCheck ||
		!strings.Contains(second.Line, "数据没到 · 数据负责人查 · 最差窗口 6/9，缺的 3 分钟查询都正常返回、序列不在结果里") {
		t.Fatalf("second line = %+v", second)
	}
	// The summary counts every word, zeros included, and leads with the
	// first line that asks somebody to act.
	summary := SummarizeStrategyLines(lines)
	if summary.Strategies != 2 || summary.ByAction[ActionServiceFix] != 1 || summary.ByAction[ActionDataCheck] != 1 || summary.ByAction[ActionNone] != 0 ||
		summary.ByState[StateDependencyUnanswered] != 1 || summary.ByState[StateDataAbsent] != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Lead == nil || summary.Lead.StrategyID != "4101" {
		t.Fatalf("lead = %+v, want the most severe line that asks for a hand", summary.Lead)
	}
	waiting := []StrategyLine{{StrategyID: "1", Standing: Standing{Action: ActionWatch}}, {StrategyID: "2", Standing: Standing{Action: ActionNone}},
		{StrategyID: "3", Standing: Standing{Action: ActionStrategyEdit}}}
	if lead := SummarizeStrategyLines(waiting).Lead; lead == nil || lead.StrategyID != "3" {
		t.Fatalf("lead over waits = %+v, want the first line that asks for a hand, past the waits", lead)
	}
	if lead := SummarizeStrategyLines(waiting[:2]).Lead; lead != nil {
		t.Fatalf("lead with nothing to do = %+v, want none", lead)
	}
	// The filter keeps only the words asked for.
	if kept := FilterStrategyLines(lines, "", ActionDataCheck); len(kept) != 1 || kept[0].StrategyID != "4102" {
		t.Fatalf("filtered by DATA_CHECK = %+v", kept)
	}
	if kept := FilterStrategyLines(lines, StateDefect, ""); len(kept) != 0 {
		t.Fatalf("filtered by DEFECT = %+v, want none", kept)
	}
}

// The evidence clause is one number and where the holes fall, by the
// verdict's own order; a row with nothing short supplies none.
func TestTheEvidenceClauseSaysWhereTheHolesFall(t *testing.T) {
	for name, testCase := range map[string]struct {
		coverage *HistoryCoverage
		want     string
	}{
		"nothing short":    {&HistoryCoverage{Levels: 2}, ""},
		"no window named":  {&HistoryCoverage{Levels: 2, Short: 1, WorstValid: 6, WorstRequired: 9}, "最差窗口 6/9"},
		"the data's":       {shortWindows(6, 9, VerdictDataAbsentWhenQueried), "最差窗口 6/9，缺的 3 分钟查询都正常返回、序列不在结果里"},
		"incomplete first": {shortWindows(6, 9, VerdictInputIncomplete, VerdictPointsUnusable), "最差窗口 6/9，缺的分钟里 1 分钟本侧没查全"},
		"unusable next":    {shortWindows(6, 9, VerdictPointsUnusable, VerdictUnknown), "最差窗口 6/9，1 分钟的记录检测用不了"},
		"unknown last":     {shortWindows(6, 9, VerdictUnknown, VerdictDataAbsentWhenQueried), "最差窗口 6/9，缺的分钟里 1 分钟说不出是谁的"},
	} {
		if got := evidenceClause(Anomaly{Coverage: testCase.coverage}); got != testCase.want {
			t.Errorf("%s: clause = %q, want %q", name, got, testCase.want)
		}
	}
	if got := evidenceClause(Anomaly{}); got != "" {
		t.Errorf("no coverage: clause = %q", got)
	}
}

// The list endpoint: one line per strategy with the vocabulary beside it,
// filtered by words from the closed lists and refused for words outside
// them, bounded by the limit and saying so.
func TestTheStrategyListIsServedWithItsWords(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].Owned, snapshots[0].Determined = 1, 1
	snapshots[0].OwnedObjects = []string{"qg-other"}
	snapshots[1].Owned, snapshots[1].Determined = 1, 1
	snapshots[1].OwnedObjects = []string{"qg-two-strategies"}
	row := anomaly("qg-two-strategies")
	row.Replica = "pod-b"
	row.Strategies = []StrategyRef{{StrategyID: "8930", BusinessID: "2"}, {StrategyID: "8931", BusinessID: "2"}}
	snapshots[1].Anomalies = []Anomaly{row}
	snapshots[1].TotalAnomalies = 1
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 2, Known: true, IDs: []string{"qg-other", "qg-two-strategies"}}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	plain, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	handler := WithStrategyStanding(plain, service, func(string) StrategyLookupFacts { return StrategyLookupFacts{} }, nil, nil, nil, "pod-a", func() time.Time { return now }, 0)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/strategies?limit=1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/strategies = %d %s", response.Code, response.Body.String())
	}
	var body StrategyListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Words.State) != len(StateWords) || len(body.Words.Action) != len(ActionWords) || len(body.Words.Health) != 3 {
		t.Fatalf("words = %+v, want the whole vocabulary on the response", body.Words)
	}
	if body.Summary.Strategies != 2 || body.Summary.Lead == nil {
		t.Fatalf("summary = %+v, want the arithmetic over every line before the limit", body.Summary)
	}
	if body.Listed != 1 || body.Total != 2 || !body.Truncated {
		t.Fatalf("listed/total/truncated = %d/%d/%v, want 1 of the 2 lines the row's two strategies make, and said so", body.Listed, body.Total, body.Truncated)
	}
	if body.Strategies[0].StrategyID != "8930" || body.Strategies[0].Line == "" {
		t.Fatalf("line = %+v", body.Strategies[0])
	}
	for _, bad := range []string{"/api/strategies?state=MOSTLY_FINE", "/api/strategies?action=SHRUG", "/api/strategies?limit=0"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, bad, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400: a word outside the list is refused, not ignored", bad, response.Code)
		}
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/strategies?action=NONE", nil))
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Total != 0 || body.Action != ActionNone {
		t.Fatalf("filtered by NONE = %s", response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/strategies", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", response.Code)
	}
}
