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
	"time"
)

// A strategy alarmd holds at the read hold limit, its data measured landing
// past it, is read before its data is all there every round: its line and
// its diagnosis say so under READ_BEFORE_COMPLETE - detecting, its results
// not to be taken as they stand, the strategy's time_delay to move - with
// the arrival age, the limit and the time_delay that would read it whole;
// and it counts as a running RESULT_UNTRUSTED strategy. The same hold under
// its limit stays READ_HELD and detecting.
func TestAStrategyHeldAtTheLimitIsReadBeforeItsDataIsComplete(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	for _, atLimit := range []bool{true, false} {
		t.Run(map[bool]string{true: "at the limit", false: "under it"}[atLimit], func(t *testing.T) {
			hold := ReadHoldFacts{Millis: 600_000, BaseMillis: 600_000, ArrivalAgeMillis: 2_176_000, LimitMillis: 600_000, AtLimit: atLimit,
				HeldSince: now.Add(-time.Hour).Unix(), DelaySeconds: 60, SuggestedDelaySeconds: 2160, SettlingWaitSeconds: 30}
			if !atLimit {
				hold.LimitMillis = 3_600_000
			}
			view, lookup := heldView(t, now, hold)
			check, state := CheckReadHeld, StateDetecting
			if atLimit {
				check, state = CheckReadBeforeComplete, StateResultUntrusted
			}
			row := diagnoseStrategy("4101", lookup, newDiagnosisContext(&view, "pod-a", now))
			if row.Verdict != state || row.Action != ActionStrategyEdit || row.Check != check {
				t.Fatalf("diagnosis %s %s under %s, want %s, the strategy's to edit, under %s", row.Verdict, row.Action, row.Check, state, check)
			}
			if row.TimeDelayAdvice == nil || row.TimeDelayAdvice.SuggestedDelaySeconds != 2160 {
				t.Fatalf("advice %+v, want a time_delay of 2160 s", row.TimeDelayAdvice)
			}
			counted := false
			for _, report := range ReportChecks(nil, nil, &view, now) {
				counted = counted || (report.Code == check && report.LineCount() == 1)
			}
			if !counted {
				t.Fatalf("checks %+v, want a %s line of the one object", ReportChecks(nil, nil, &view, now), check)
			}
			var line *StrategyLine
			for _, candidate := range StrategyLines(&view, now) {
				if candidate.StrategyID == "4101" {
					line = &candidate
				}
			}
			if line == nil || line.Standing.State != state {
				t.Fatalf("line %+v, want a %s line", line, state)
			}
			words := []string{"最晚约在窗口结束后 2176 秒到齐", "超过 alarmd 最多推后的 600 秒", "每轮在数据到齐前就已读取判定", "time_delay 改为 2160 秒"}
			if !atLimit {
				words = []string{"最晚约在窗口结束后 2176 秒到齐", "自动推后 600 秒", "time_delay 改为 2160 秒"}
			}
			for _, word := range words {
				if !strings.Contains(line.Line, word) {
					t.Fatalf("line %q, want it to say %q", line.Line, word)
				}
			}
			snapshot := Snapshot{Replica: "pod-a", TakenAt: now, Owned: 1, Determined: 1, OwnedObjects: []string{"qg-held"},
				ReadHolds: map[string]ReadHoldFacts{"qg-held": hold}, ReadHeld: view.ReadHeld,
				EvaluatingStrategies: []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}, EvaluatingStrategiesKnown: true}
			if running := SummaryOf(snapshot, []string{"qg-held"}, 0).Part.RunningStrategies; running[state] != 1 {
				t.Fatalf("running %v, want the held strategy counted %s", running, state)
			}
		})
	}
}
