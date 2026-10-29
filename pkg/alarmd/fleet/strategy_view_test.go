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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A strategy's drawer counts its objects by punctuality, each from its own
// replica as the row's punctuality is, and carries the check its words were
// decided under; a strategy outside the universe is not found.
func TestAStrategyViewCountsItsObjectsByPunctuality(t *testing.T) {
	universe, rows, due, census := boardFixture(true)
	decided := rows["2"]
	decided.Check = CheckSlotsOverdue
	rows["2"] = decided
	index := buildBoard("c1", time.Unix(10_000, 0), "digest-1", universe, func(id string) DiagnosisRow { return rows[id] }, nil, due, census, nil, nil)
	want := map[string]map[Punctuality]int{
		"1": {PunctualityOverdue: 1, PunctualityOnTime: 1},
		"2": {PunctualityLate: 1, PunctualityNotStarted: 1},
		"5": {},
		"6": {PunctualityUnknown: 1},
		"7": {PunctualityOverdue: 1, PunctualityUnknown: 1},
	}
	for id, counts := range want {
		view, found := index.view(id, DiagnosisSummaryFreshFor)
		if !found || view.Row.StrategyID != id || view.MaxAgeSeconds != int(DiagnosisSummaryFreshFor/time.Second) {
			t.Fatalf("strategy %s: found %v view %+v", id, found, view)
		}
		total := 0
		for _, punctuality := range Punctualities {
			if view.Objects.ByPunctuality[punctuality] != counts[punctuality] {
				t.Errorf("strategy %s %s = %d, want %d", id, punctuality, view.Objects.ByPunctuality[punctuality], counts[punctuality])
			}
			total += counts[punctuality]
		}
		if view.Objects.Total != total || len(view.Objects.ByPunctuality) != len(Punctualities) {
			t.Errorf("strategy %s total %d over %d cells, want %d over every punctuality", id, view.Objects.Total, len(view.Objects.ByPunctuality), total)
		}
	}
	if view, _ := index.view("2", DiagnosisSummaryFreshFor); view.Check != CheckSlotsOverdue {
		t.Errorf("strategy 2 check %q, want %q", view.Check, CheckSlotsOverdue)
	}
	if _, found := index.view("nope", DiagnosisSummaryFreshFor); found {
		t.Error("a strategy outside the universe was found")
	}
}

func viewStanding() *StrategyStanding {
	one := uint32(1)
	return &StrategyStanding{StrategyID: "77", Plans: []StrategyPlanStanding{
		{Config: &StrategyPlanConfigs{Redacted: true, Items: []StrategyPlanConfig{{
			PlanID: "plan-secret-id", StateGeneration: "gen-r9",
			Schedule: StrategyScheduleConfig{IntervalSeconds: 60},
			Query: StrategyQueryConfig{SourceSemantics: []string{"bk_monitor/time_series"},
				Clauses: []StrategyQueryClause{{TableID: "system.cpu_summary", Field: "usage"}}, PromQL: &StrategyPromQLConfig{}},
			Target: StrategyTargetConfig{Kind: TargetKindScope, Scope: &StrategyTargetScopeInfo{Groups: 1, Conditions: []StrategyTargetScopeCondition{
				{Group: 0, Field: "HOST", Method: "EQ", Keys: 3}, {Group: 0, Field: "TOPO_NODE", Method: "NEQ", Keys: 1}}}},
			NoData: &StrategyNoDataConfig{Continuous: 5, Level: 2},
			Levels: []StrategyLevelConfig{
				{LevelID: 1, Connector: "and", Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}, {Type: "SimpleRingRatio"}}, Trigger: StrategyTypedPlanConfig{WindowSize: &one}},
				{LevelID: 3, Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}}},
			},
			EffectiveTimeConfigured: true,
		}}}},
		// A second plan of the same item, on another shard: one item.
		{Config: &StrategyPlanConfigs{Redacted: true, Items: []StrategyPlanConfig{{
			PlanID: "plan-secret-id-2", Schedule: StrategyScheduleConfig{IntervalSeconds: 60},
			Query: StrategyQueryConfig{SourceSemantics: []string{"bk_monitor/time_series"}},
			Target: StrategyTargetConfig{Kind: TargetKindScope, Scope: &StrategyTargetScopeInfo{Groups: 1, Conditions: []StrategyTargetScopeCondition{
				{Group: 0, Field: "HOST", Method: "EQ", Keys: 3}, {Group: 0, Field: "TOPO_NODE", Method: "NEQ", Keys: 1}}}},
			NoData: &StrategyNoDataConfig{Continuous: 5, Level: 2},
			Levels: []StrategyLevelConfig{
				{LevelID: 1, Connector: "and", Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}, {Type: "SimpleRingRatio"}}},
				{LevelID: 3, Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}}},
			},
			EffectiveTimeConfigured: true,
		}}}},
	}}
}

// What a strategy should detect is its frozen configuration written as
// words: interval, data source, algorithms by level, no-data, target and
// effective time. Plans with the same words are one item.
func TestExpectedDetectionIsTheFrozenConfigInWords(t *testing.T) {
	got := ExpectedOf(viewStanding())
	want := ExpectedDetection{Known: true, Items: []ExpectedItem{{
		Interval: "每 1 分钟", DataSource: "监控平台时序数据",
		Algorithms: []string{"致命：静态阈值 且 简易环比", "提醒：静态阈值"},
		NoData:     "连续 5 个周期无数据时告警（预警）", Target: "包含 3 个主机，排除 1 个拓扑节点",
		EffectiveTime: "只在配置的生效时段内检测",
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected = %+v\nwant %+v", got, want)
	}
	plain := StrategyPlanConfig{Schedule: StrategyScheduleConfig{IntervalSeconds: 90}, Target: StrategyTargetConfig{Kind: TargetKindNone},
		Levels: []StrategyLevelConfig{{LevelID: 2, Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}}}}}
	if item := expectedItemOf(plain); item.Interval != "每 90 秒" || item.DataSource != "监控平台时序数据" || item.NoData != "不检测无数据" ||
		item.Target != "不限定目标（全部）" || item.EffectiveTime != "全天生效" || !reflect.DeepEqual(item.Algorithms, []string{"预警：静态阈值"}) {
		t.Fatalf("plain item %+v", item)
	}
	planned := StrategyTargetConfig{Kind: TargetKindPlan, Plan: &StrategyTargetPlanConfig{Rule: "k8s_workload", StaticMembers: 4, DynamicGroups: 2, ExcludeMembers: 1}}
	if word := targetWord(planned); word != "按K8s 工作负载：4 个，2 个动态分组，排除 1 个" {
		t.Fatalf("planned target %q", word)
	}
}

// A configuration the page cannot have is said in words, never as an empty
// list that reads "detects nothing": not accepted, refused with why, or not
// read.
func TestExpectedDetectionSaysWhyItIsNotKnown(t *testing.T) {
	cases := map[string]struct {
		standing *StrategyStanding
		want     string
	}{
		"no read":      {nil, ExpectedNotRead},
		"not accepted": {&StrategyStanding{Plans: []StrategyPlanStanding{}}, ExpectedNotAccepted},
		"refused": {&StrategyStanding{Plans: []StrategyPlanStanding{{Config: &StrategyPlanConfigs{Refusal: ConfigObjectUnreadable}}}},
			ExpectedNotRead + "：配置存储没有应答"},
		"no config asked": {&StrategyStanding{Plans: []StrategyPlanStanding{{}}}, ExpectedNotRead},
	}
	for name, c := range cases {
		got := ExpectedOf(c.standing)
		if got.Known || got.UnknownWord != c.want || got.Items == nil || len(got.Items) != 0 {
			t.Errorf("%s: %+v, want unknown %q with an empty list", name, got, c.want)
		}
	}
	for _, refusal := range ConfigRefusals {
		if configRefusalWords[refusal] == "" {
			t.Errorf("refusal %s has no word", refusal)
		}
	}
}

// Every algorithm, source, target field and rule alarmd knows has a word; a
// kind it does not know reads "其他", never its code.
func TestExpectedDetectionHasAWordForEveryKnownKind(t *testing.T) {
	for _, kind := range []string{"Threshold", "SimpleRingRatio", "OsRestart", "ProcPort", "SimpleYearRound", "AdvancedRingRatio",
		"AdvancedYearRound", "RingRatioAmplitude", "YearRoundAmplitude", "YearRoundRange"} {
		if algorithmWords[kind] == "" {
			t.Errorf("algorithm %s has no word", kind)
		}
	}
	for _, rule := range []string{"host_id", "model_inst_id", "k8s_cluster", "k8s_node", "k8s_workload", "ip_cloud"} {
		if targetRuleWords[rule] == "" {
			t.Errorf("target rule %s has no word", rule)
		}
	}
	for _, field := range []string{"TOPO_NODE", "HOST", "DYNAMIC_GROUP", "SERVICE_INSTANCE", "OBJECT_MODEL_INST"} {
		if targetFieldWords[field] == "" {
			t.Errorf("target field %s has no word", field)
		}
	}
	for _, punctuality := range Punctualities {
		if PunctualityWords[punctuality] == "" {
			t.Errorf("punctuality %s has no word", punctuality)
		}
	}
	odd := StrategyPlanConfig{Schedule: StrategyScheduleConfig{IntervalSeconds: 60}, Query: StrategyQueryConfig{SourceSemantics: []string{"mystery/source"}},
		Target: StrategyTargetConfig{Kind: TargetKindPlan, Plan: &StrategyTargetPlanConfig{Rule: "mystery_rule"}},
		Levels: []StrategyLevelConfig{{LevelID: 9, Algorithms: []StrategyAlgorithmInfo{{Type: "MysteryAlgo"}}}}}
	encoded, _ := json.Marshal(expectedItemOf(odd))
	for _, code := range []string{"mystery", "Mystery"} {
		if strings.Contains(string(encoded), code) {
			t.Errorf("an unknown kind is written by its code: %s", encoded)
		}
	}
	if !strings.Contains(string(encoded), "其他数据源") || !strings.Contains(string(encoded), "其他级别：其他算法") || !strings.Contains(string(encoded), "按其他目标") {
		t.Errorf("unknown kinds %s", encoded)
	}
}

// The drawer carries words and counts only: no query text, table, plan,
// revision, query group or replica, and decodes strictly into its type.
func TestStrategyViewCarriesNoDeveloperCoordinates(t *testing.T) {
	index := boardOf(t, false)
	actual, _ := index.view("1", DiagnosisSummaryFreshFor)
	actual.Row.OpenAlerts = &BoardCount{Value: 3, AtLeast: true}
	view := ComposeStrategyView(actual, viewStanding())
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var strict StrategyView
	if err := decoder.Decode(&strict); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	for _, forbidden := range []string{"plan-secret-id", "gen-r9", "system.cpu_summary", "usage", "promql", "table", "plan_id",
		"query_group", "qg-", "replica", "revision", "r1", "Threshold", "bk_monitor/time_series", "查询组", "副本", "修订"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("the drawer carries %q: %s", forbidden, encoded)
		}
	}
	if regexp.MustCompile(`[0-9a-f]{64}`).Match(encoded) {
		t.Error("the drawer carries a 64-hex identity")
	}
	if view.Actual.Punctuality != PunctualityOverdue || view.Actual.PunctualityWord != "超期" || view.Actual.Objects.Total != 2 ||
		view.KBAnchor != actual.Row.KBAnchor || view.Conclusion.Line == "" || !view.Expected.Known ||
		!reflect.DeepEqual(view.Actual.OpenAlerts, &BoardCount{Value: 3, AtLeast: true}) || view.MaxAgeSeconds != actual.MaxAgeSeconds {
		t.Fatalf("view %+v", view)
	}
}

// view=1 answers one strategy from the board's computation: the same read as
// the board while fresh, not found for a strategy outside the universe, and
// refused for anything but its id.
func TestTheStrategyViewIsReadFromTheBoardsComputation(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"77", "78"}
	if code, _, _ := rig.board(t, "board=1"); code != http.StatusOK {
		t.Fatalf("board status %d", code)
	}
	w := httptest.NewRecorder()
	rig.handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/diagnose?view=1&strategy_id=77", nil))
	var view StrategyViewActual
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Row.StrategyID != "77" {
		t.Fatalf("view status %d body %s", w.Code, w.Body.String())
	}
	if rig.universeReads != 1 {
		t.Fatalf("universe read %d times for a board and a view within the freshness, want once", rig.universeReads)
	}
	w = httptest.NewRecorder()
	rig.handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/diagnose?view=1&strategy_id=999", nil))
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "STRATEGY_NOT_IN_UNIVERSE") {
		t.Fatalf("unknown strategy: %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"view=1", "view=true&strategy_id=77", "view=1&strategy_id=77&board=1",
		"view=1&strategy_id=77&summary=1", "view=1&strategy_id=77&limit=5", "view=1&strategy_id=77&state=DETECTING"} {
		w = httptest.NewRecorder()
		rig.handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/diagnose?"+query, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want refused", query, w.Code)
		}
	}
}
