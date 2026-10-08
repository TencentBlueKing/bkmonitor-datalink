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
	"slices"
	"strings"

	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// HoleGroups is missing minutes by whose they are, in the groups the
// strategy line names them in (evidenceClause): the data's, this side's
// incomplete reads, records the detection could not use, minutes nobody can
// say, and minutes before this replica took the object.
type HoleGroups struct {
	// Data is the minutes whose query answered whole without the series, or
	// with no record at all.
	Data uint32 `json:"data"`
	// Incomplete is the minutes this side did not see whole.
	Incomplete uint32 `json:"incomplete"`
	// Unusable is the minutes whose record the Level could not use.
	Unusable uint32 `json:"unusable"`
	// Unknown is the minutes whose round is not on record, not remembered,
	// or let go for the memory line.
	Unknown uint32 `json:"unknown"`
	// Before is the minutes before this replica took the object.
	Before uint32 `json:"before"`
}

// Total is every minute in the groups.
func (groups HoleGroups) Total() uint32 {
	return groups.Data + groups.Incomplete + groups.Unusable + groups.Unknown + groups.Before
}

// holeGroupsOf adds up the windows' holes by group, over the windows keep
// takes; nil keep takes them all. Every hole cause is in one group
// (TestEveryHoleCauseIsInOneGroup), so the groups add up to the windows'
// missing minutes.
func holeGroupsOf(windows []WindowRow, keep func(WindowRow) bool) HoleGroups {
	var groups HoleGroups
	for _, window := range windows {
		if keep != nil && !keep(window) {
			continue
		}
		groups.Data += window.HolesBy.AnsweredWithoutSeries + window.HolesBy.AnsweredEmpty
		groups.Incomplete += window.HolesBy.InputIncomplete
		groups.Unusable += window.HolesBy.Unusable
		groups.Unknown += window.HolesBy.NotInMemory + window.HolesBy.PrimaryUnrecorded + window.HolesBy.HeldByLine
		groups.Before += window.HolesBy.BeforeThisProcess
	}
	return groups
}

// GuardWarming is what an object whose round was filed under
// GAP_GUARD_WARMING leaves out of the word.
//
// A guard an earlier gap opened is let go only on a window that is whole
// again (execution.PlanGapRecoveryMutation), so on a series that keeps
// missing whole minutes it stays for as long as the series keeps missing
// them. And it withholds only NORMAL, which needs a whole window whether or
// not a guard holds it: ABNORMAL is decided from the anomalies in the window
// and RECOVERY steps over minutes nobody observed, so the series still alerts
// and recovers. Read as "the guard is stuck, nothing is detected", the word
// sent a reader through the evaluator and the window reader for 47 strategies
// on two deployments. What is left to ask is whose the missing minutes are,
// which the guarded windows' holes answer.
type GuardWarming struct {
	Line string `json:"line"`
	// Guarded is how many windows the round's guards held, and Listed how
	// many of them the row names; the minutes are the named ones', added
	// across windows.
	Guarded int `json:"guarded"`
	Listed  int `json:"listed"`
	// OpenedBy is the reasons the named windows' guards were raised with.
	OpenedBy []string   `json:"opened_by,omitempty"`
	Minutes  HoleGroups `json:"minutes"`
}

// GuardWarmingOf reads the row's guarded windows; nil when the row's round
// was not filed under GAP_GUARD_WARMING.
func GuardWarmingOf(anomaly Anomaly) *GuardWarming {
	warming := string(model.CauseGapGuardWarming)
	if anomaly.Cause != warming && anomaly.CauseReason != warming {
		return nil
	}
	reading := &GuardWarming{}
	if coverage := anomaly.Coverage; coverage != nil {
		reading.Guarded = int(coverage.Guarded)
		guarded := func(window WindowRow) bool { return window.Guarded }
		for _, window := range coverage.Windows {
			if !window.Guarded {
				continue
			}
			reading.Listed++
			if window.GuardReason != "" && !slices.Contains(reading.OpenedBy, window.GuardReason) {
				reading.OpenedBy = append(reading.OpenedBy, window.GuardReason)
			}
		}
		reading.Minutes = holeGroupsOf(coverage.Windows, guarded)
	}
	reading.Line = guardWarmingLine(*reading)
	return reading
}

// guardWarmingLine is the reading as one sentence: what the guard waits for
// and withholds, then whose the named windows' missing minutes are.
func guardWarmingLine(reading GuardWarming) string {
	opened := "更早一轮的缺口"
	if len(reading.OpenedBy) > 0 {
		opened = "更早一轮（" + strings.Join(reading.OpenedBy, "、") + "）的缺口"
	}
	line := "GAP_GUARD_WARMING：" + opened + "打开了守卫，窗口重新完整一次才收；序列一直缺分钟，守卫就一直在。" +
		"它只挡 NORMAL（判正常本来就要完整窗口），ABNORMAL 和 RECOVERY 照常判，告警和恢复不受影响"
	if reading.Listed == 0 {
		return line + "；本行没有列出带守卫的窗口，缺的分钟归谁读不出"
	}
	minutes := reading.Minutes
	parts := []string{}
	for _, part := range []struct {
		n    uint32
		text string
	}{
		{minutes.Data, "查询正常返回、序列不在结果里"},
		{minutes.Incomplete, "本侧没查全"},
		{minutes.Unusable, "记录检测用不了"},
		{minutes.Unknown, "说不出是谁的"},
		{minutes.Before, "早于本副本接手这个对象"},
	} {
		if part.n > 0 {
			parts = append(parts, fmt.Sprintf("%d 分钟%s", part.n, part.text))
		}
	}
	line += fmt.Sprintf("；列出的 %d/%d 个守卫窗口共缺 %d 分钟（各窗口相加）", reading.Listed, reading.Guarded, minutes.Total())
	if len(parts) > 0 {
		line += "：" + strings.Join(parts, "，")
	}
	return line
}
