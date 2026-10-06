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
	"sort"
	"strings"
)

// What a withheld reason means and what to do about it, decided here per
// reason and read by the page per group.
//
// The line for strategies this deployment cannot run used to carry one
// sentence for every reason under it -- "snapshot retention or completion
// offset too small, a deployment parameter" -- chosen by the line and not
// by the reason. On a deployment whose five strategies were withheld for a
// target model this build could not resolve, the page told the operator to
// change a deployment parameter; no parameter would have helped, and the
// facts under the sentence said so in a word the page had no words for. A
// reason word is decided where it is produced; the sentence that goes with
// it is decided here, once, and a reason this table does not know is said
// to be unknown rather than folded into whichever cause the line assumed.

// WithheldKind is who can act on a withheld reason. Closed: the page's
// wording table is held to this list.
type WithheldKind string

const (
	// WithheldDeploymentParameter: the strategy asks more than this
	// deployment is configured to keep or reserve; a deployment parameter,
	// raised to the value the strategy needs, admits it on the next round.
	WithheldDeploymentParameter WithheldKind = "DEPLOYMENT_PARAMETER"
	// WithheldBuildCapability: this build does not evaluate what the
	// strategy is written with; no parameter changes that, a build does.
	WithheldBuildCapability WithheldKind = "BUILD_CAPABILITY"
	// WithheldWriterAhead: the source wrote a document shape this build does
	// not read yet -- the writer went first.
	WithheldWriterAhead WithheldKind = "WRITER_AHEAD"
	// WithheldStrategyDefinition: the strategy as written exceeds a compile
	// guardrail of this deployment -- levels per Plan, algorithms per level,
	// Plan bytes, trigger compute. The guardrail is a value in the
	// deployment's limits and has a default, but it is a guardrail against a
	// runaway definition and not a working ceiling: the next step is to
	// shrink the strategy, and raising the limit is the strategy owner's
	// case to make, not the first move.
	WithheldStrategyDefinition WithheldKind = "STRATEGY_DEFINITION"
	// WithheldUnknownReason: a reason word this table does not know. The
	// page says so; it does not guess a cause.
	WithheldUnknownReason WithheldKind = "UNKNOWN_REASON"
)

// WithheldKinds is the closed list.
var WithheldKinds = []WithheldKind{WithheldDeploymentParameter, WithheldBuildCapability, WithheldWriterAhead, WithheldStrategyDefinition, WithheldUnknownReason}

// WithheldReasonWords is one reason's meaning: who acts, what happened, and
// the next step, in the words the page shows.
type WithheldReasonWords struct {
	Kind WithheldKind `json:"kind"`
	What string       `json:"what"`
	Next string       `json:"next"`
	// Action is set on the reasons under CONFIG_NORMALIZED, whose line
	// holds strategies that are detecting: whether the reason asks the
	// strategy to change what it wrote, or asks nothing. The line takes the
	// most demanding of its reasons, so a line of ignored priorities alone
	// does not ask anybody to act.
	Action ActionWord `json:"action,omitempty"`
}

// withheldReasonWords is the table, over the reasons the control plane
// attaches to the UNSUPPORTED_PHASE2_CAPABILITY disposition. A reason
// produced and not listed here reaches the page as unknown, which a test
// against the control plane's literals keeps from lasting a release.
var withheldReasonWords = map[string]WithheldReasonWords{
	// The one reason under CONFIG_NORMALIZED: not withheld at all. The
	// words say what was read instead of what was written, and that the
	// strategy detects more than it asked for until the range is fixed.
	"EFFECTIVE_TIME_RANGE_INVALID": {Kind: WithheldStrategyDefinition,
		What:   "生效时间段的开始或结束时间格式不合法，已按平台自己的读法读：开始坏读作 00:00、结束坏读作 23:59——策略在检测，但检测的时段比配置写的宽，不是被扣住",
		Next:   "策略负责人把该时间段改成 HH:MM；改好后下一轮刷新按写的时段检测，这一行消失",
		Action: ActionStrategyEdit},
	// The second reason under CONFIG_NORMALIZED, also not withheld: the
	// strategy runs, without the arbitration the platform applies between
	// the strategies of one priority group.
	"PRIORITY_IGNORED": {Kind: WithheldStrategyDefinition,
		What:   "策略配了优先级分组。优先级是平台告警链路在同组策略之间做的抑制：同一目标上只留优先级最高的那条。这里按独立策略检测，不做这层抑制，所以同组的低优先级策略也会在同一目标上告警——策略在检测，不是被扣住",
		Next:   "不需要处理。平台那边可能仍会按优先级关掉低优先级的告警，那是平台的规则，不是这里的错关",
		Action: ActionNone},
	// Under CONFIG_NORMALIZED as well: the level runs, on another level's
	// trigger, the way the platform runs it.
	"LEVEL_TRIGGER_BORROWED": {Kind: WithheldStrategyDefinition,
		What:   "这一级别的算法没有配同级别的触发条件，已按平台的读法借用策略里第一条触发条件（次数、窗口、生效时间）来检测，恢复按平台默认的 5 个周期——策略在检测，不是被扣住",
		Next:   "策略负责人把触发条件配到算法所在的级别上；改好后下一轮刷新按写的条件检测，这一行消失",
		Action: ActionStrategyEdit},
	"SNAPSHOT_RETENTION_INSUFFICIENT": {Kind: WithheldStrategyDefinition,
		What: "策略的评估周期太长：冻结的 Slot 要读的内容和按序列状态，需要保留得比状态存储的上限还久（样本里有要求值和上限）",
		Next: "缩短该策略的评估周期，下一轮刷新自动接受；调部署参数没有用"},
	"COMPLETION_OFFSET_BELOW_RESERVE": {Kind: WithheldDeploymentParameter,
		What: "策略要的完成偏移小于本部署的预留",
		Next: "把完成偏移预留调到策略要求的值，下一轮刷新自动接受"},
	"EFFECTIVE_TIME_SCHEMA_UNSUPPORTED": {Kind: WithheldBuildCapability,
		What: "策略生效时间的快照是更新版本写的，本构建读不了它，所以整条策略不检测",
		Next: "等能读该版本的构建；改策略配置或部署参数都没有用"},
	"ALGORITHM_NOT_MIGRATED": {Kind: WithheldBuildCapability,
		What: "该检测算法还没迁到 Go 侧，本构建不评估它",
		Next: "等带该算法的构建；改部署参数没有用"},
	"QUERY_BK_DATA_LOCAL_TIME_NOT_MIGRATED": {Kind: WithheldBuildCapability,
		What: "该查询的本地时间字段还没迁到 Go 侧",
		Next: "等带该字段的构建；改部署参数没有用"},
	"EVALUATION_STEP_INCONSISTENT": {Kind: WithheldBuildCapability,
		What: "这条策略编出来的调度周期、评估间隔和触发步长三者不一致。三者今天应当相等，不一致说明本构建有缺陷；按这样的调度去跑，触发窗口的计数会错，所以整条策略不检测",
		Next: "交 alarmd 修构建；改策略配置或部署参数都没有用"},
	"UNSUPPORTED_TARGET_SCOPE": {Kind: WithheldBuildCapability,
		What: "策略的目标范围写法本构建不支持",
		Next: "等支持该目标范围的构建；改部署参数没有用"},
	"UNSUPPORTED_TARGET_SCOPE_UNRESOLVABLE": {Kind: WithheldBuildCapability,
		What: "策略的目标范围本构建解析不了",
		Next: "等能解析它的构建；改部署参数没有用"},
	"UNSUPPORTED_TARGET_VALUE_SHAPE": {Kind: WithheldBuildCapability,
		What: "策略目标值的写法本构建读不出键",
		Next: "等能读该值形状的构建；改部署参数没有用"},
	"UNSUPPORTED_MULTI_ITEM_STRATEGY": {Kind: WithheldBuildCapability,
		What: "多 item 的策略本构建不支持",
		Next: "等支持多 item 的构建；改部署参数没有用"},
	"QUERY_SOURCE_NOT_MIGRATED": {Kind: WithheldBuildCapability,
		What: "该数据源的查询还没迁到 Go 侧",
		Next: "等带该数据源的构建；改部署参数没有用"},
	"QUERY_MIXED_PROMQL_NOT_MIGRATED": {Kind: WithheldBuildCapability,
		What: "混合 PromQL 的查询还没迁到 Go 侧",
		Next: "等带它的构建；改部署参数没有用"},
	"QUERY_CMDB_LEVEL_BYPASSES_UQ": {Kind: WithheldBuildCapability,
		What: "按 CMDB 层级聚合的查询绕过了统一查询，本构建不支持",
		Next: "等支持该聚合的构建；改部署参数没有用"},
	"EXPRESSION_FUNCTION_NOT_MIGRATED": {Kind: WithheldBuildCapability,
		What: "表达式里的函数还没迁到 Go 侧",
		Next: "等带该函数的构建；改部署参数没有用"},
	"QUERY_FUNCTION_NOT_MIGRATED": {Kind: WithheldBuildCapability,
		What: "查询里的函数还没迁到 Go 侧",
		Next: "等带该函数的构建；改部署参数没有用"},
	"UNSUPPORTED_TARGET_PLAN": {Kind: WithheldWriterAhead,
		What: "target_plan 里 field_path 指到的字段本解码器读不了（缺字段、多字段、类型不对、未知的 version 或 rule），整条策略具名拒绝、不回退旧 target、不保留旧 Plan；本构建解释 target_plan，只是拒这一个字段",
		Next: "写入方按协议核该字段；若是本构建还没有的新字段，等解释它的构建"},
	// The runtime compiler's terminals, filed under the same disposition by
	// controlplane.CompilerTerminalDisposition. ALGORITHM_UNSUPPORTED and
	// ALGORITHM_NOT_MIGRATED are two producers and two words: the first is
	// the runtime compiler meeting an algorithm it has no evaluator for, the
	// second the legacy compiler meeting one not yet ported.
	"ALGORITHM_UNSUPPORTED": {Kind: WithheldBuildCapability,
		What: "运行时编译器没有这个检测算法的评估器",
		Next: "等带该算法的构建；改部署参数没有用"},
	"PLAN_BUDGET_EXCEEDED": {Kind: WithheldStrategyDefinition,
		What: "策略编译出的 Plan 超出本部署的护栏（层级数 limits.compiler.max_levels_per_plan、Plan 字节数 max_plan_bytes、触发计算量 limits.trigger.max_compute_cost；field_path 指到超的那一处）",
		Next: "先收策略（减层级、减触发计算），护栏是防失控的不是工作上限；确有必要再由策略侧提出抬对应的 limits 值"},
	"LEVEL_BUDGET_EXCEEDED": {Kind: WithheldStrategyDefinition,
		What: "某一层级的算法数超出本部署的护栏（limits.compiler.max_algorithms_per_level；field_path 指到 level.detect_plan.algorithms）",
		Next: "先收该层级的算法数，护栏是防失控的不是工作上限；确有必要再由策略侧提出抬 max_algorithms_per_level"},
	"TARGET_PLAN_EMPTY": {Kind: WithheldWriterAhead,
		What: "写入方给的 target_plan 既没有静态目标也没有动态引用，永远匹配不到任何东西",
		Next: "写入方核这条策略的目标计划；alarmd 与部署参数都不用改"},
	"TARGET_PLAN_MISSING": {Kind: WithheldWriterAhead,
		What: "item 的目标是选择文档但没有配套的 target_plan",
		Next: "写入方补 target_plan；alarmd 与部署参数都不用改"},
	"DYNAMIC_GROUP_SOURCE_UNCONFIGURED": {Kind: WithheldDeploymentParameter,
		What: "策略的目标计划引用了动态分组，而本部署没有渲染动态分组缓存前缀",
		Next: "给部署配上动态分组缓存前缀，下一轮刷新自动接受"},
}

// WithheldWordsOf is the words for a reason, or the unknown entry naming
// the reason as it was written.
func WithheldWordsOf(reason string) WithheldReasonWords {
	if words, known := withheldReasonWords[reason]; known {
		return words
	}
	return WithheldReasonWords{Kind: WithheldUnknownReason,
		What: "原因 " + reason + "：本构建的页面还没有这个原因的说明",
		Next: "按原因词查该构建的变更说明；别按别的原因的处理办法改参数"}
}

// KnownWithheldReasons lists the reasons this table explains, sorted, for
// the test that holds it to what the control plane produces.
func KnownWithheldReasons() []string {
	reasons := make([]string, 0, len(withheldReasonWords))
	for reason := range withheldReasonWords {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	return reasons
}

// capabilityLine is the sentence for the line of strategies this
// deployment cannot run: how many, and by kind of cause -- not one cause
// for every reason. Kinds are named in the order of the closed list, and
// only the ones present.
func capabilityLine(strategies int, groups []CheckGroup) string {
	byKind := map[WithheldKind]int{}
	for _, group := range groups {
		if group.Words != nil {
			byKind[group.Words.Kind] += group.Strategies
		}
	}
	parts := make([]string, 0, len(WithheldKinds))
	for _, kind := range WithheldKinds {
		count := byKind[kind]
		if count == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d 条", withheldKindWords[kind], count))
	}
	line := fmt.Sprintf("%d 条策略这个部署跑不了（%d 种原因）", strategies, len(groups))
	if len(parts) > 0 {
		line += "——" + strings.Join(parts, "、") + "；处理办法按原因组看"
	}
	return line
}

// withheldKindWords is each kind in the words of the line.
var withheldKindWords = map[WithheldKind]string{
	WithheldDeploymentParameter: "部署参数不够",
	WithheldBuildCapability:     "本构建不支持",
	WithheldWriterAhead:         "写入方超前于本构建",
	WithheldStrategyDefinition:  "策略定义超出护栏",
	WithheldUnknownReason:       "原因待查",
}
