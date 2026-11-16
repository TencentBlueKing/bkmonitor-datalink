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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A strategy's drawer counts its objects by punctuality, each from its own
// replica as the row's punctuality is, and carries the check its words were
// decided under; a strategy outside the universe is not found.
func TestAStrategyViewCountsItsObjectsByPunctuality(t *testing.T) {
	universe, rows, due, census := boardFixture(true)
	decided := rows["2"]
	decided.Check = CheckSlotsOverdue
	rows["2"] = decided
	index := buildBoard("c1", time.Unix(10_000, 0), "digest-1", universe, func(id string) DiagnosisRow { return rows[id] }, nil, due, census, nil)
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

// viewRecovery is a level's recovery plan as the catalog writes it.
func viewRecovery(config string) StrategyTypedPlanConfig {
	return typedPlanConfigOf(contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(config)})
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
				{LevelID: 1, Connector: "and", Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}, {Type: "SimpleRingRatio"}}, Trigger: StrategyTypedPlanConfig{WindowSize: &one}, Recovery: viewRecovery(`{"enabled":true,"consecutive_windows":5}`)},
				{LevelID: 3, Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}}, Recovery: viewRecovery(`{"enabled":false,"consecutive_windows":0}`)},
			},
			DataUnit: "percent", DataUnitBasis: controlplane.DataUnitBasisQueryUnit,
			RawUnitLevels:           []controlplane.DataUnitRawLevel{{LevelID: 3, Reason: controlplane.ReasonUnitPrefixOutsideStepsReadAsSmallest, Unit: "percent", Prefix: "Gi"}},
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
				{LevelID: 1, Connector: "and", Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}, {Type: "SimpleRingRatio"}}, Recovery: viewRecovery(`{"enabled":true,"consecutive_windows":5}`)},
				{LevelID: 3, Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}}, Recovery: viewRecovery(`{"enabled":false,"consecutive_windows":0}`)},
			},
			DataUnit: "percent", DataUnitBasis: controlplane.DataUnitBasisQueryUnit,
			RawUnitLevels:           []controlplane.DataUnitRawLevel{{LevelID: 3, Reason: controlplane.ReasonUnitPrefixOutsideStepsReadAsSmallest, Unit: "percent", Prefix: "Gi"}},
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
		DataUnit: "percent",
		Comparison: []string{"按指标单位 percent 比较，表达式没有声明单位，取查询的单位；阈值没选单位时按最小单位",
			"提醒的阈值单位 Gi 和数据单位 percent 不是同一类，按没选单位处理，阈值按最小阶读"},
		Algorithms: []string{"致命：静态阈值 且 简易环比", "提醒：静态阈值"},
		Recovery:   []string{"致命：连续 5 个周期不满足触发条件时恢复告警（约 5 分钟）", "提醒：不自动恢复"},
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

// A level's recovery is said with its window and how the alert ends
// (status_setter close or recovery), in periods and as a duration. A -nodata
// setter adds its own end, which R does not count: (R+T+1) periods since the
// group's last real point, T the trigger window (the product specs
// alarmd-recovery-and-missing-data.md §1, the recovery-nodata and close-nodata
// rows; alarmd-strategy-capabilities.md §2.3, §2.4). Here R=3, T=5 and the
// period is 60 s: 3 minutes, and 9 periods, 9 minutes. A plan the projection
// could not read in full is unknown. A Plan written to close and one written
// to recover are two items.
func TestExpectedDetectionSaysEachLevelsRecovery(t *testing.T) {
	trigger := typedPlanConfigOf(contract.TypedPlanV1{Type: "N_OF_M", Version: 1,
		Config: json.RawMessage(`{"window_size":5,"required_anomalies":2,"step_seconds":60}`)})
	for config, want := range map[string]string{
		`{"enabled":true,"consecutive_windows":3}`:                                                 "预警：连续 3 个周期不满足触发条件时恢复告警（约 3 分钟）",
		`{"enabled":true,"consecutive_windows":3,"end":"close"}`:                                   "预警：连续 3 个周期不满足触发条件时关闭告警（约 3 分钟）",
		`{"enabled":true,"consecutive_windows":3,"missing_data":"evaluate_as_zero"}`:               "预警：连续 3 个周期不满足触发条件时恢复告警（约 3 分钟）；无数据满 9 个周期（约 9 分钟）也结束",
		`{"enabled":true,"consecutive_windows":3,"end":"close","missing_data":"evaluate_as_zero"}`: "预警：连续 3 个周期不满足触发条件时关闭告警（约 3 分钟）；无数据满 9 个周期（约 9 分钟）也结束",
		`{"enabled":false,"consecutive_windows":0}`:                                                "预警：不自动恢复",
		`{"consecutive_windows":3}`:                                                                "预警：未知",
		`{"enabled":true,"consecutive_windows":3,"end":"archive"}`:                                 "预警：未知",
		`{"enabled":true,"consecutive_windows":3,"missing_data":"skip"}`:                           "预警：未知",
		`{"enabled":true,"consecutive_windows":3,"by":"host"}`:                                     "预警：未知",
		`[1]`: "预警：未知",
	} {
		plan := StrategyPlanConfig{Schedule: StrategyScheduleConfig{IntervalSeconds: 60}, Target: StrategyTargetConfig{Kind: TargetKindNone},
			Levels: []StrategyLevelConfig{{LevelID: 2, Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}}, Trigger: trigger, Recovery: viewRecovery(config)}}}
		if item := expectedItemOf(plan); !reflect.DeepEqual(item.Recovery, []string{want}) {
			t.Errorf("%s: recovery %q, want %q", config, item.Recovery, want)
		}
	}
	// A zero count that is no observation (an event count, an ungrouped log
	// count; alarmd-recovery-and-missing-data.md §3.1, §3.2) does not recover
	// on R quiet periods: only a count above zero that does not trigger
	// recovers, and without -nodata the words say a quiet group stays open
	// and name the setter that would end it. Off stays off.
	for config, want := range map[string]string{
		`{"enabled":true,"consecutive_windows":3}`:                                                 "预警：安静不恢复：只在来了大于 0、不满足触发的计数时恢复告警，且此前连续 3 个周期不满足触发条件（约 3 分钟）；可配 recovery-nodata",
		`{"enabled":true,"consecutive_windows":3,"end":"close"}`:                                   "预警：安静不关闭：只在来了大于 0、不满足触发的计数时关闭告警，且此前连续 3 个周期不满足触发条件（约 3 分钟）；可配 close-nodata",
		`{"enabled":true,"consecutive_windows":3,"missing_data":"evaluate_as_zero"}`:               "预警：只在来了大于 0、不满足触发的计数时恢复告警，且此前连续 3 个周期不满足触发条件（约 3 分钟）；无数据满 9 个周期（约 9 分钟）也结束",
		`{"enabled":true,"consecutive_windows":3,"end":"close","missing_data":"evaluate_as_zero"}`: "预警：只在来了大于 0、不满足触发的计数时关闭告警，且此前连续 3 个周期不满足触发条件（约 3 分钟）；无数据满 9 个周期（约 9 分钟）也结束",
		`{"enabled":false,"consecutive_windows":0}`:                                                "预警：不自动恢复",
		`{"consecutive_windows":3}`:                                                                "预警：未知",
	} {
		plan := StrategyPlanConfig{Schedule: StrategyScheduleConfig{IntervalSeconds: 60}, Target: StrategyTargetConfig{Kind: TargetKindNone},
			Levels: []StrategyLevelConfig{{LevelID: 2, Algorithms: []StrategyAlgorithmInfo{{Type: "Threshold"}}, Trigger: trigger,
				Recovery: viewRecovery(config), ZeroCountIsNoObservation: true}}}
		if item := expectedItemOf(plan); !reflect.DeepEqual(item.Recovery, []string{want}) {
			t.Errorf("zero count %s: recovery %q, want %q", config, item.Recovery, want)
		}
	}
	// -nodata with no trigger window known has no (R+T+1) to say.
	noTrigger := StrategyPlanConfig{Schedule: StrategyScheduleConfig{IntervalSeconds: 60}, Levels: []StrategyLevelConfig{{LevelID: 2,
		Recovery: viewRecovery(`{"enabled":true,"consecutive_windows":3,"missing_data":"evaluate_as_zero"}`)}}}
	if item := expectedItemOf(noTrigger); !reflect.DeepEqual(item.Recovery, []string{"预警：未知"}) {
		t.Errorf("-nodata without a trigger window: %q, want unknown", item.Recovery)
	}
	resolve, closing := viewStanding(), viewStanding()
	closing.Plans[1].Config.Items[0].Levels[0].Recovery = viewRecovery(`{"enabled":true,"consecutive_windows":5,"end":"close"}`)
	if got := len(ExpectedOf(resolve).Items); got != 1 {
		t.Fatalf("two Plans with one recovery are %d items, want 1", got)
	}
	if got := len(ExpectedOf(closing).Items); got != 2 {
		t.Fatalf("a Plan that closes and one that recovers are %d items, want 2", got)
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
		view.MaxAgeSeconds != actual.MaxAgeSeconds {
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
