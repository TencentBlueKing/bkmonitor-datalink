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
	"fmt"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// The strategy drawer: one strategy as the admin page opens it. Its
// conclusion is the board's row, read from the same computation; what it
// should detect is the redacted configuration the catalog froze for it
// (include=config), written here as words; what it does detect is its
// objects counted by punctuality. It carries no query text, raw
// configuration, query group, replica or revision: the page shows it as it
// is.

// PunctualityWords is the page's word for each punctuality.
var PunctualityWords = map[Punctuality]string{
	PunctualityOnTime: "按时", PunctualityLate: "迟到", PunctualityOverdue: "超期",
	PunctualityNotStarted: "还没开始", PunctualityNotDetecting: "没有在检测", PunctualityUnknown: "未知",
}

// ViewObjects is a strategy's detection objects counted by punctuality; the
// page says "检测对象", never query group.
type ViewObjects struct {
	Total         int                 `json:"total"`
	ByPunctuality map[Punctuality]int `json:"by_punctuality"`
}

// StrategyViewActual is what the diagnosis knows of one strategy, from the
// board's computation: its row, its objects by punctuality, and the check its
// words were decided under.
type StrategyViewActual struct {
	GeneratedAt   time.Time   `json:"generated_at"`
	MaxAgeSeconds int         `json:"max_age_seconds"`
	Row           BoardRow    `json:"row"`
	Objects       ViewObjects `json:"objects"`
	Check         Check       `json:"check,omitempty"`
	// LateWaits is the late and overdue objects grouped by what they waited
	// on (strategyLateWaits).
	LateWaits []LateWaitGroup `json:"late_waits,omitempty"`
	// StoreOutages is the strategy's windows lost to this deployment's own
	// store outages, with the interval it speaks for.
	StoreOutages StoreOutageSummary `json:"store_outages"`
}

// boardViewFacts is what the drawer reads of a strategy beyond its row.
type boardViewFacts struct {
	objects   ViewObjects
	check     Check
	lateWaits []LateWaitGroup
}

// viewFactsOf counts the decided strategy's objects by punctuality, each read
// from its own replica as the row's punctuality is.
func viewFactsOf(decided DiagnosisRow, due *DueView, waits *lateWaitIndex) boardViewFacts {
	facts := boardViewFacts{objects: ViewObjects{ByPunctuality: map[Punctuality]int{}}, check: decided.Check,
		lateWaits: strategyLateWaits(decided, due, waits)}
	for _, punctuality := range Punctualities {
		facts.objects.ByPunctuality[punctuality] = 0
	}
	for _, plan := range decided.Plans {
		if plan.QueryGroup == "" {
			continue
		}
		facts.objects.Total++
		facts.objects.ByPunctuality[due.objectPunctuality(plan.QueryGroup, plan.Replica)]++
	}
	return facts
}

// view is one strategy of this computation, false when it is not in the
// universe.
func (index *boardIndex) view(strategyID string, maxAge time.Duration) (StrategyViewActual, bool) {
	for _, row := range index.rows {
		if row.StrategyID != strategyID {
			continue
		}
		facts := index.views[strategyID]
		outages, lost := index.outages[strategyID]
		if !lost {
			outages = index.noOutage
		}
		if outages.State == "" {
			outages = unknownStoreOutage()
		}
		return StrategyViewActual{GeneratedAt: index.at, MaxAgeSeconds: int(maxAge / time.Second), Row: row,
			Objects: facts.objects, Check: facts.check, LateWaits: facts.lateWaits, StoreOutages: outages}, true
	}
	return StrategyViewActual{}, false
}

// ExpectedDetection is what the strategy is configured to detect, in words.
// Known is false when the configuration was not read; UnknownWord then says
// why, and Items is empty.
type ExpectedDetection struct {
	Known       bool           `json:"known"`
	UnknownWord string         `json:"unknown_word,omitempty"`
	Items       []ExpectedItem `json:"items"`
}

// ExpectedItem is one monitored item's configuration, in words. Two plans of
// one strategy with the same words are one item.
type ExpectedItem struct {
	Interval      string   `json:"interval"`
	DataSource    string   `json:"data_source"`
	Algorithms    []string `json:"algorithms"`
	NoData        string   `json:"no_data"`
	Target        string   `json:"target"`
	EffectiveTime string   `json:"effective_time"`
}

// The words of a strategy whose configuration is not read.
const (
	ExpectedNotAccepted = "策略没有被接纳，没有可检测的配置"
	ExpectedNotRead     = "检测配置暂时读不到"
)

// configRefusalWords says why a plan's configuration was not read.
var configRefusalWords = map[string]string{
	ConfigLoaderNotWired:   "本进程没有接上配置读取",
	ConfigObjectUnreadable: "配置存储没有应答",
	ConfigObjectMissing:    "发布的配置在存储里找不到",
	ConfigObjectCorrupt:    "存储里的配置已损坏",
	ConfigPlanNotInObject:  "配置里没有这条策略",
	ConfigNoObjectDigest:   "还没有发布配置",
}

// sourceWords names a data source kind.
var sourceWords = map[string]string{
	"bk_monitor/time_series":    "监控平台时序数据",
	"bk_monitor/log":            "监控平台日志关键字",
	"bk_data/time_series":       "计算平台时序数据",
	"bk_log_search/time_series": "日志平台时序数据",
	"bk_log_search/log":         "日志平台日志",
	"custom/time_series":        "自定义指标",
	"custom/event":              "自定义事件",
	"prometheus/time_series":    "Prometheus 指标",
}

// algorithmWords names a detection algorithm.
var algorithmWords = map[string]string{
	"Threshold": "静态阈值", "SimpleRingRatio": "简易环比", "SimpleYearRound": "简易同比",
	"AdvancedRingRatio": "高级环比", "AdvancedYearRound": "高级同比", "RingRatioAmplitude": "环比振幅",
	"YearRoundAmplitude": "同比振幅", "YearRoundRange": "同比区间", "OsRestart": "主机重启", "ProcPort": "进程端口",
}

// levelWords names an alert level.
var levelWords = map[uint32]string{1: "致命", 2: "预警", 3: "提醒"}

// targetFieldWords and targetRuleWords name what a target is made of.
var targetFieldWords = map[string]string{
	"TOPO_NODE": "拓扑节点", "HOST": "主机", "DYNAMIC_GROUP": "动态分组", "SERVICE_INSTANCE": "服务实例", "OBJECT_MODEL_INST": "模型实例",
}

var targetRuleWords = map[string]string{
	"host_id": "主机", "ip_cloud": "主机", "model_inst_id": "模型实例",
	"k8s_cluster": "K8s 集群", "k8s_node": "K8s 节点", "k8s_workload": "K8s 工作负载",
}

// ExpectedOf writes the strategy's configuration, read with include=config,
// as words. A strategy with no plan is not accepted; one whose plans were
// all refused says the first refusal.
func ExpectedOf(standing *StrategyStanding) ExpectedDetection {
	expected := ExpectedDetection{Items: []ExpectedItem{}}
	if standing == nil {
		expected.UnknownWord = ExpectedNotRead
		return expected
	}
	if len(standing.Plans) == 0 {
		expected.UnknownWord = ExpectedNotAccepted
		return expected
	}
	seen := map[string]bool{}
	refusal := ""
	for _, plan := range standing.Plans {
		if plan.Config == nil {
			continue
		}
		if plan.Config.Refusal != "" {
			if refusal == "" {
				refusal = plan.Config.Refusal
			}
			continue
		}
		for _, config := range plan.Config.Items {
			item := expectedItemOf(config)
			key := strings.Join([]string{item.Interval, item.DataSource, strings.Join(item.Algorithms, "|"), item.NoData, item.Target, item.EffectiveTime}, "\n")
			if !seen[key] {
				seen[key] = true
				expected.Items = append(expected.Items, item)
			}
		}
	}
	if len(expected.Items) > 0 {
		expected.Known = true
		return expected
	}
	expected.UnknownWord = ExpectedNotRead
	if word, ok := configRefusalWords[refusal]; ok {
		expected.UnknownWord += "：" + word
	}
	return expected
}

func expectedItemOf(config StrategyPlanConfig) ExpectedItem {
	item := ExpectedItem{Interval: intervalWord(config.Schedule.IntervalSeconds), DataSource: sourceWord(config.Query.SourceSemantics),
		Algorithms: []string{}, NoData: "不检测无数据", Target: targetWord(config.Target), EffectiveTime: "全天生效"}
	for _, level := range config.Levels {
		names := make([]string, 0, len(level.Algorithms))
		for _, algorithm := range level.Algorithms {
			name, ok := algorithmWords[algorithm.Type]
			if !ok {
				name = "其他算法"
			}
			names = append(names, name)
		}
		joiner := "、"
		switch strings.ToLower(level.Connector) {
		case "and":
			joiner = " 且 "
		case "or":
			joiner = " 或 "
		}
		item.Algorithms = append(item.Algorithms, levelWord(level.LevelID)+"："+strings.Join(names, joiner))
	}
	if config.NoData != nil {
		item.NoData = fmt.Sprintf("连续 %d 个周期无数据时告警（%s）", config.NoData.Continuous, levelWord(config.NoData.Level))
	}
	if config.EffectiveTimeConfigured {
		item.EffectiveTime = "只在配置的生效时段内检测"
	}
	return item
}

func intervalWord(seconds int64) string {
	switch {
	case seconds <= 0:
		return "未知"
	case seconds%60 == 0:
		return fmt.Sprintf("每 %d 分钟", seconds/60)
	default:
		return fmt.Sprintf("每 %d 秒", seconds)
	}
}

func sourceWord(semantics []string) string {
	if len(semantics) == 0 {
		return sourceWords[controlplane.LegacyTimeSeriesSemantics]
	}
	names, seen := []string{}, map[string]bool{}
	for _, semantic := range semantics {
		name, ok := sourceWords[semantic]
		if !ok {
			name = "其他数据源"
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return strings.Join(names, "、")
}

func levelWord(level uint32) string {
	if word, ok := levelWords[level]; ok {
		return word
	}
	return "其他级别"
}

func targetWord(target StrategyTargetConfig) string {
	switch target.Kind {
	case TargetKindScope:
		if target.Scope == nil {
			return "限定了目标范围"
		}
		parts := []string{}
		for _, condition := range target.Scope.Conditions {
			field, ok := targetFieldWords[condition.Field]
			if !ok {
				field = "其他目标"
			}
			verb := "包含"
			if condition.Method == "NEQ" {
				verb = "排除"
			}
			parts = append(parts, fmt.Sprintf("%s %d 个%s", verb, condition.Keys, field))
		}
		if len(parts) == 0 {
			return "限定了目标范围"
		}
		return strings.Join(parts, "，")
	case TargetKindPlan:
		if target.Plan == nil {
			return "限定了目标范围"
		}
		rule, ok := targetRuleWords[target.Plan.Rule]
		if !ok {
			rule = "其他目标"
		}
		parts := []string{}
		if count := target.Plan.StaticMembers; count > 0 {
			parts = append(parts, fmt.Sprintf("%d 个", count))
		}
		if count := target.Plan.DynamicGroups; count > 0 {
			parts = append(parts, fmt.Sprintf("%d 个动态分组", count))
		}
		if count := target.Plan.DynamicTopologies; count > 0 {
			parts = append(parts, fmt.Sprintf("%d 个拓扑节点", count))
		}
		if count := target.Plan.ExcludeMembers + target.Plan.ExcludeHosts; count > 0 {
			parts = append(parts, fmt.Sprintf("排除 %d 个", count))
		}
		if len(parts) == 0 {
			return "按" + rule
		}
		return "按" + rule + "：" + strings.Join(parts, "，")
	default:
		return "不限定目标（全部）"
	}
}

// ActualDetection is what the strategy does detect: how punctual, its objects
// by punctuality, and its open alerts on alarmd's copy.
type ActualDetection struct {
	Punctuality     Punctuality `json:"punctuality"`
	PunctualityWord string      `json:"punctuality_word"`
	Objects         ViewObjects `json:"objects"`
	OpenAlerts      *BoardCount `json:"open_alerts,omitempty"`
	// LateWaits is what the late and overdue objects waited on, grouped.
	LateWaits []LateWaitGroup `json:"late_waits,omitempty"`
}

// StrategyView is the drawer: conclusion, what it should detect, what it
// does, and the check with its knowledge-base anchor.
type StrategyView struct {
	GeneratedAt   time.Time         `json:"generated_at"`
	MaxAgeSeconds int               `json:"max_age_seconds"`
	Conclusion    BoardRow          `json:"conclusion"`
	Expected      ExpectedDetection `json:"expected"`
	Actual        ActualDetection   `json:"actual"`
	// StoreOutages is the windows this strategy lost to this deployment's
	// own store outages (StoreOutageSummary).
	StoreOutages StoreOutageSummary `json:"store_outages"`
	Check        Check              `json:"check,omitempty"`
	KBAnchor     string             `json:"kb_anchor"`
}

// ComposeStrategyView joins the diagnosis's view of a strategy with its
// configuration read.
func ComposeStrategyView(actual StrategyViewActual, standing *StrategyStanding) StrategyView {
	return StrategyView{GeneratedAt: actual.GeneratedAt, MaxAgeSeconds: actual.MaxAgeSeconds, Conclusion: actual.Row,
		Expected: ExpectedOf(standing),
		Actual: ActualDetection{Punctuality: actual.Row.Punctuality, PunctualityWord: PunctualityWords[actual.Row.Punctuality],
			Objects: actual.Objects, OpenAlerts: actual.Row.OpenAlerts, LateWaits: actual.LateWaits},
		StoreOutages: actual.StoreOutages, Check: actual.Check, KBAnchor: actual.Row.KBAnchor}
}
