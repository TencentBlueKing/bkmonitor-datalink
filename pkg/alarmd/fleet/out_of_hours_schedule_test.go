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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Whatever the round at a hole's minute did, a hole the schedule puts outside
// the strategy's effective time reads EFFECTIVE_TIME_INACTIVE and a window of
// such holes is under no line, the strategy detecting with nothing to do.
// The same holes in hours keep their round's word and the window keeps a
// line. Every way a hole is read is a case: answered without the series,
// answered empty -- the out-of-hours round that suppresses no Level and so
// carries no reason -- not whole, given up without a query, recorded
// without its answer, rolled out of memory, before this process, held by a
// line, and a point the Level could not use.
func TestEveryRoundAHoleCanCarryReadsOutOfHoursFromTheSchedule(t *testing.T) {
	const minute = int64(1_791_500_000)
	round := func(kind, reason string, answer primaryAnswer, inferred bool) []roundMark {
		return []roundMark{{end: minute, kind: roundWordTable.of(kind), reason: roundWordTable.of(reason), answer: answer, endInferred: inferred}}
	}
	for _, tc := range []struct {
		name     string
		rounds   []roundMark
		since    int64
		held     int64
		unusable bool
		cause    HoleCause
		reason   string
	}{
		{"answered without the series, its Level suppressed", round("COMPLETED_WITH_UNAVAILABLE", contract.ReasonEffectiveTimeInactive, primaryWholeWithData, false),
			0, 0, false, HoleAnsweredWithoutSeries, contract.ReasonEffectiveTimeInactive},
		{"answered without the series, the guard's word", round("COMPLETED_WITH_UNAVAILABLE", contract.ReasonHistoryGapped, primaryWholeWithData, false),
			0, 0, false, HoleAnsweredWithoutSeries, contract.ReasonHistoryGapped},
		{"answered without the series, warming", round("COMPLETED_WITH_UNAVAILABLE", contract.ReasonHistoryWarming, primaryWholeWithData, false),
			0, 0, false, HoleAnsweredWithoutSeries, contract.ReasonHistoryWarming},
		{"answered empty, no Level to suppress, no reason", round("FULL_EMPTY_COMPLETED", "", primaryWholeEmpty, true),
			0, 0, false, HoleAnsweredEmpty, ""},
		{"partial", round("COMPLETED_WITH_PARTIAL_GAP", contract.ReasonQueryPartial, primaryNotWhole, false),
			0, 0, false, HoleInputIncomplete, contract.ReasonQueryPartial},
		{"unavailable", round("COMPLETED_WITH_UNAVAILABLE", contract.ReasonQueryUnavailable, primaryNotWhole, false),
			0, 0, false, HoleInputIncomplete, contract.ReasonQueryUnavailable},
		{"given up without a query", round("GAP_SKIPPED", contract.ReasonGapSkipped, primaryUnrecorded, false),
			0, 0, false, HoleInputIncomplete, contract.ReasonGapSkipped},
		{"snapshot unavailable without a query", round("SNAPSHOT_UNAVAILABLE", contract.ReasonSnapshotUnavailable, primaryUnrecorded, false),
			0, 0, false, HoleInputIncomplete, contract.ReasonSnapshotUnavailable},
		{"recorded without its answer", round("FULL_COMPLETED", "", primaryUnrecorded, false),
			0, 0, false, HolePrimaryUnrecorded, ""},
		{"rolled out of memory", nil, minute - 600, 0, false, HoleNotInMemory, ""},
		{"before this process", nil, minute + 60, 0, false, HoleBeforeThisProcess, ""},
		{"held by a line", nil, minute - 600, minute, false, HoleHeldByLine, ""},
		{"a point the Level could not use", round("FULL_COMPLETED", "", primaryWholeWithData, false),
			0, 0, true, HolePointUnusable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, outOfHours := range []bool{true, false} {
				fact := observability.HistoryWindowFact{Strategy: "13", Business: "2", Series: "series-a", Level: 3,
					Valid: 3, Required: 5, End: minute + 60}
				if tc.unusable {
					fact.Unusable, fact.UnusableTotal = []int64{minute}, 1
				} else {
					fact.Missing, fact.MissingTotal = []int64{minute}, 1
				}
				if outOfHours {
					fact.Inactive = []int64{minute}
				}
				rows := windowRows(tc.rounds, &observability.HistoryCoverageFacts{Levels: 1, Short: 1, Windows: []observability.HistoryWindowFact{fact}},
					tc.since, tc.held)
				if len(rows) != 1 || len(rows[0].Holes) != 1 {
					t.Fatalf("rows=%+v, want one window with its one hole", rows)
				}
				hole := rows[0].Holes[0]
				if hole.Cause != tc.cause {
					t.Fatalf("cause %s, want %s", hole.Cause, tc.cause)
				}
				row := Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_GAPPED",
					Coverage: &HistoryCoverage{Levels: 1, Short: 1, WorstValid: 3, WorstRequired: 5, ShortRounds: 9, Windows: rows}}
				check, under, _ := checkOf(row, ScheduleOnTime)
				if outOfHours {
					if !hole.OutsideEffectiveTime || hole.Reason != contract.ReasonEffectiveTimeInactive {
						t.Fatalf("out of hours: hole=%+v, want outside its effective time and EFFECTIVE_TIME_INACTIVE", hole)
					}
					if check != "" || under {
						t.Fatalf("out of hours: the row is under %s, want no line: detecting, nothing to do", check)
					}
					continue
				}
				if hole.OutsideEffectiveTime || hole.Reason != tc.reason {
					t.Fatalf("in hours: hole=%+v, want its round's word %q and not outside", hole, tc.reason)
				}
				if check == "" || !under {
					t.Fatal("in hours: the row left its line")
				}
			}
		})
	}
}

// A window with one hole the schedule could not answer for among holes it
// put out of hours keeps its line: unknown is not outside.
func TestOneHoleTheScheduleCouldNotAnswerForKeepsTheWindowInItsLine(t *testing.T) {
	const minute = int64(1_791_500_000)
	var rounds []roundMark
	var missing []int64
	for step := int64(0); step < 5; step++ {
		at := minute + step*60
		rounds = append(rounds, roundMark{end: at, kind: roundWordTable.of("FULL_EMPTY_COMPLETED"), answer: primaryWholeEmpty})
		missing = append(missing, at)
	}
	fact := observability.HistoryWindowFact{Strategy: "13", Series: "series-a", Level: 3, Valid: 1, Required: 6, End: minute + 300,
		Missing: missing, MissingTotal: 5, Inactive: missing[:4]}
	rows := windowRows(rounds, &observability.HistoryCoverageFacts{Levels: 1, Short: 1, Windows: []observability.HistoryWindowFact{fact}}, 0, 0)
	row := Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_GAPPED",
		Coverage: &HistoryCoverage{Levels: 1, Short: 1, WorstValid: 1, WorstRequired: 6, ShortRounds: 9, Windows: rows}}
	if check, under, _ := checkOf(row, ScheduleOnTime); check == "" || !under {
		t.Fatal("a window with one hole the schedule did not answer for left its line")
	}
	fact.Inactive = missing
	rows = windowRows(rounds, &observability.HistoryCoverageFacts{Levels: 1, Short: 1, Windows: []observability.HistoryWindowFact{fact}}, 0, 0)
	row.Coverage.Windows = rows
	if check, under, _ := checkOf(row, ScheduleOnTime); check != "" || under {
		t.Fatalf("every hole out of hours: the row is under %s, want no line", check)
	}
}
