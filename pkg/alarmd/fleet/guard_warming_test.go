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
	"strings"
	"testing"
)

// Every hole cause is counted in exactly one group, so the groups add up to
// the minutes a window misses: a cause added to the closed list and left out
// of the groups would drop its minutes from both the strategy line and the
// guard reading.
func TestEveryHoleCauseIsInOneGroup(t *testing.T) {
	one := map[HoleCause]WindowHoleCounts{
		HoleAnsweredWithoutSeries: {AnsweredWithoutSeries: 1},
		HoleAnsweredEmpty:         {AnsweredEmpty: 1},
		HoleInputIncomplete:       {InputIncomplete: 1},
		HolePointUnusable:         {Unusable: 1},
		HolePrimaryUnrecorded:     {PrimaryUnrecorded: 1},
		HoleNotInMemory:           {NotInMemory: 1},
		HoleBeforeThisProcess:     {BeforeThisProcess: 1},
		HoleHeldByLine:            {HeldByLine: 1},
	}
	if len(one) != len(HoleCauses) {
		t.Fatalf("the test names %d causes, the closed list has %d", len(one), len(HoleCauses))
	}
	for _, cause := range HoleCauses {
		counts, named := one[cause]
		if !named {
			t.Fatalf("hole cause %s is not named here", cause)
		}
		if total := holeGroupsOf([]WindowRow{{HolesBy: counts}}, nil).Total(); total != 1 {
			t.Errorf("one %s hole is %d minutes across the groups, want 1", cause, total)
		}
	}
}

func guardWarmingRow(windows ...WindowRow) Anomaly {
	return Anomaly{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", Cause: "GAP_GUARD_WARMING",
		CauseReason: "GAP_GUARD_WARMING", Coverage: &HistoryCoverage{Levels: 33, Short: 15, Guarded: 15, Windows: windows}}
}

// A row filed under GAP_GUARD_WARMING says what the guard waits for and
// withholds, and whose its named guarded windows' missing minutes are; a
// short window no guard holds is not counted, and the reasons the guards
// were opened with are named once each.
func TestAWarmingGuardSaysItWithholdsOnlyNormalAndWhoseItsMinutesAre(t *testing.T) {
	row := guardWarmingRow(
		WindowRow{Guarded: true, GuardReason: "QUERY_UNAVAILABLE", MissingTotal: 5,
			HolesBy: WindowHoleCounts{AnsweredWithoutSeries: 3, AnsweredEmpty: 1, InputIncomplete: 1}},
		WindowRow{Guarded: true, GuardReason: "QUERY_UNAVAILABLE", MissingTotal: 4,
			HolesBy: WindowHoleCounts{AnsweredWithoutSeries: 2, NotInMemory: 1, BeforeThisProcess: 1}},
		WindowRow{Guarded: true, GuardReason: "QUERY_TIMEOUT", MissingTotal: 2, UnusableTotal: 1,
			HolesBy: WindowHoleCounts{HeldByLine: 1, Unusable: 1}},
		WindowRow{MissingTotal: 7, HolesBy: WindowHoleCounts{InputIncomplete: 7}},
	)
	reading := GuardWarmingOf(row)
	if reading == nil {
		t.Fatal("a row filed under GAP_GUARD_WARMING had no reading")
	}
	want := HoleGroups{Data: 6, Incomplete: 1, Unusable: 1, Unknown: 2, Before: 1}
	if reading.Minutes != want || reading.Listed != 3 || reading.Guarded != 15 {
		t.Fatalf("reading = %+v, want minutes %+v over 3 of 15 guarded windows", reading, want)
	}
	if strings.Join(reading.OpenedBy, ",") != "QUERY_UNAVAILABLE,QUERY_TIMEOUT" {
		t.Fatalf("opened by %v, want each guard reason once in the order met", reading.OpenedBy)
	}
	for _, words := range []string{"只挡 NORMAL", "ABNORMAL 和 RECOVERY 照常判", "窗口重新完整一次才收",
		"QUERY_UNAVAILABLE、QUERY_TIMEOUT", "列出的 3/15 个守卫窗口共缺 11 分钟",
		"6 分钟查询正常返回、序列不在结果里", "1 分钟本侧没查全", "1 分钟记录检测用不了", "2 分钟说不出是谁的",
		"1 分钟早于本副本接手这个对象"} {
		if !strings.Contains(reading.Line, words) {
			t.Errorf("line %q does not say %q", reading.Line, words)
		}
	}
}

// Either field naming the cause is enough, and a row under any other cause
// has no reading.
func TestOnlyARoundFiledUnderTheGuardIsReadAsWarming(t *testing.T) {
	byCause := Anomaly{Cause: "GAP_GUARD_WARMING", CauseReason: "QUERY_UNAVAILABLE"}
	byReason := Anomaly{CauseReason: "GAP_GUARD_WARMING"}
	if GuardWarmingOf(byCause) == nil || GuardWarmingOf(byReason) == nil {
		t.Fatal("a round filed under GAP_GUARD_WARMING by one of its two fields had no reading")
	}
	for _, other := range []Anomaly{{CauseReason: "HISTORY_GAPPED"}, {Cause: "LEVEL_OUTCOME_UNKNOWN", ReasonCode: "GAP_GUARD_WARMING"}, {}} {
		if reading := GuardWarmingOf(other); reading != nil {
			t.Errorf("%+v read as warming: %+v", other, reading)
		}
	}
}

// A row that names no guarded window says so instead of a count of zero.
func TestAWarmingRowWithNoGuardedWindowNamedSaysItCannotTell(t *testing.T) {
	reading := GuardWarmingOf(guardWarmingRow(WindowRow{MissingTotal: 3, HolesBy: WindowHoleCounts{AnsweredWithoutSeries: 3}}))
	if reading == nil || reading.Listed != 0 || reading.Minutes.Total() != 0 || !strings.Contains(reading.Line, "缺的分钟归谁读不出") {
		t.Fatalf("reading = %+v", reading)
	}
	if strings.Contains(reading.Line, "共缺") {
		t.Fatalf("line %q counts minutes it did not read", reading.Line)
	}
}
