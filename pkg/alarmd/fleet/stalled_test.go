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
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A fold whose objects' rounds end but whose way out has not moved reads as
// stalled, not recovering. Two completing objects with the same guard word:
// one whose guard count has been 0 of 9 for StalledRounds rounds, one whose
// count rose last round. The fold with the first is STALLED and counts it;
// the fold with only the second is RECOVERING. A guard that has reached its
// requirement is a release not yet happened, not a stall; and a stalled
// object beside a failing one leaves the fold BLOCKED -- failing outranks.
func TestAFoldWhoseWayOutHasNotMovedReadsAsStalledNotRecovering(t *testing.T) {
	fresh := now.Add(-time.Minute)
	completing := func(qg string, guard GapGuard, coverage *HistoryCoverage) Anomaly {
		return Anomaly{QueryGroup: qg, Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "CONFIG_DRIFT",
			ReasonLastAt: fresh, Coverage: coverage, Guards: []GapGuard{guard}}
	}
	shortWindow := &HistoryCoverage{Levels: 5, Short: 1, WorstValid: 6, WorstRequired: 9, Guarded: 1}
	stuck := GapGuard{Scope: "plan", Status: "GAPPED", Reason: "CONFIG_DRIFT", Required: 9, Observed: 0, Progress: "none",
		UnchangedRounds: StalledRounds, Rounds: StalledRounds + 1}
	moving := GapGuard{Scope: "plan", Status: "WARMING", Reason: "CONFIG_DRIFT", Required: 9, Observed: 4, Progress: "partial",
		UnchangedRounds: 0, Rounds: 5}
	tooEarly := stuck
	tooEarly.UnchangedRounds = StalledRounds - 1
	reached := GapGuard{Scope: "plan", Status: "WARMING", Reason: "CONFIG_DRIFT", Required: 9, Observed: 9, Progress: "ready",
		UnchangedRounds: StalledRounds + 5, Rounds: 20}

	groupOf := func(rows []Anomaly) CheckGroup {
		t.Helper()
		Attribute(rows, now)
		for _, report := range ReportChecks([][]Anomaly{rows}, nil, nil, now) {
			if report.Code != CheckWindowUndecided {
				continue
			}
			if len(report.Groups) != 1 {
				t.Fatalf("groups = %+v, want the one fold", report.Groups)
			}
			return report.Groups[0]
		}
		t.Fatalf("no %s report for %+v", CheckWindowUndecided, rows)
		return CheckGroup{}
	}

	if group := groupOf([]Anomaly{completing("qg-stuck", stuck, shortWindow), completing("qg-moving", moving, shortWindow)}); group.Recovery != RecoveryStalled || group.Stalled != 1 || group.CompletingNow != 2 {
		t.Fatalf("fold with a stuck guard = %s stalled %d completing %d, want STALLED, 1 of 2", group.Recovery, group.Stalled, group.CompletingNow)
	}
	if group := groupOf([]Anomaly{completing("qg-moving", moving, shortWindow)}); group.Recovery != RecoveryRecovering || group.Stalled != 0 {
		t.Fatalf("fold with a moving guard = %s stalled %d, want RECOVERING and none stalled", group.Recovery, group.Stalled)
	}
	if group := groupOf([]Anomaly{completing("qg-early", tooEarly, shortWindow)}); group.Recovery != RecoveryRecovering {
		t.Fatalf("a guard unchanged for %d rounds reads %s, want RECOVERING: under the bound it is not a run", StalledRounds-1, group.Recovery)
	}
	if group := groupOf([]Anomaly{completing("qg-reached", reached, shortWindow)}); group.Recovery != RecoveryRecovering {
		t.Fatalf("a guard at its requirement reads %s, want RECOVERING: a release not yet happened is not a stall", group.Recovery)
	}
	// A short window whose worst valid count has not moved is the other way
	// to stall, with no guard on the row at all: a series whose window has
	// been short for longer than it is long (the data-missing line), flat for
	// StalledRounds, reads stalled; the same window whose count moved last
	// round reads recovering.
	windowRow := func(qg string, unchanged uint32) Anomaly {
		return Anomaly{QueryGroup: qg, Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "HISTORY_GAPPED",
			ReasonLastAt: fresh, Coverage: &HistoryCoverage{Levels: 5, Short: 1, WorstValid: 6, WorstRequired: 9, ShortRounds: 12, UnchangedRounds: unchanged}}
	}
	dataMissing := func(rows []Anomaly) CheckGroup {
		t.Helper()
		Attribute(rows, now)
		for _, report := range ReportChecks([][]Anomaly{rows}, nil, nil, now) {
			if report.Code == CheckSeriesDataMissing && len(report.Groups) == 1 {
				return report.Groups[0]
			}
		}
		t.Fatalf("no %s fold for %+v", CheckSeriesDataMissing, rows)
		return CheckGroup{}
	}
	if group := dataMissing([]Anomaly{windowRow("qg-flat", StalledRounds)}); group.Recovery != RecoveryStalled || group.Stalled != 1 {
		t.Fatalf("a flat short window reads %s stalled %d, want STALLED, 1", group.Recovery, group.Stalled)
	}
	if group := dataMissing([]Anomaly{windowRow("qg-filling", 0)}); group.Recovery != RecoveryRecovering || group.Stalled != 0 {
		t.Fatalf("a filling short window reads %s stalled %d, want RECOVERING, 0", group.Recovery, group.Stalled)
	}
	// Failing outranks stalled, on the counts the reading is decided from.
	if got := recoveryOf(&CheckGroup{FailingNow: 1, Stalled: 1, CompletingNow: 2}, false); got != RecoveryBlocked {
		t.Fatalf("a fold with a failing object and a stalled one reads %s, want BLOCKED", got)
	}
	if got := recoveryOf(&CheckGroup{Stalled: 1, CompletingNow: 2}, false); got != RecoveryStalled {
		t.Fatalf("a fold with a stalled object and no failing one reads %s, want STALLED", got)
	}
}

// The row carries how many series each Plan was bound to, from the Plan's
// evaluation lines, and puts the count beside the Plan's held guard: a guard
// at 0 of N on a Plan bound to no series reads as having nothing to warm on,
// which its rounds and its unchanged count cannot say. A Plan the round bound
// series to says its number; a Plan no line has spoken for is unknown, not
// zero.
func TestTheRowPutsEachPlansBoundSeriesBesideItsGuard(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-input"})
	evaluated := func(strategy string, matched int, slot int64) {
		tracker.Observe(ctx, observability.Observation{
			Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
			Result: observability.ResultSuccess, Direction: observability.DirectionInternal,
			Trace:             observability.TraceFields{StrategyID: strategy, BusinessID: "2", EvaluationTime: slot},
			PlanSeriesMatched: &matched,
		})
	}
	// Two Plans held by the same guard word: one bound to no series, one to
	// three; a third Plan held with no evaluation line yet.
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(600 + 60*round)
		evaluated("s-none", 0, slot)
		evaluated("s-three", 3, slot)
		for _, strategy := range []string{"s-none", "s-three", "s-unspoken"} {
			guardProgress(ctx, tracker, strategy, "plan", "GAPPED", "CONFIG_DRIFT", 9, 0)
		}
		degradedRound(ctx, tracker, slot)
	}
	rows := anyColumn(tracker)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the one object", rows)
	}
	row := rows[0]
	lastSlot := int64(600 + 60*(DefaultDegradedRounds-1))
	if len(row.PlanSeries) != 2 || row.PlanSeries[0].Plan.StrategyID != "s-none" || row.PlanSeries[0].Matched != 0 ||
		row.PlanSeries[1].Plan.StrategyID != "s-three" || row.PlanSeries[1].Matched != 3 || row.PlanSeries[0].EvaluationTime != lastSlot {
		t.Fatalf("plan series = %+v, want s-none 0 and s-three 3 at slot %d, smallest first", row.PlanSeries, lastSlot)
	}
	byPlan := map[string]GapGuard{}
	for _, guard := range row.Guards {
		byPlan[guard.Plan.StrategyID] = guard
	}
	if g := byPlan["s-none"]; !g.SeriesMatchedKnown || g.SeriesMatched != 0 {
		t.Fatalf("guard of the Plan bound to nothing = %+v, want series matched known and 0", g)
	}
	if g := byPlan["s-three"]; !g.SeriesMatchedKnown || g.SeriesMatched != 3 {
		t.Fatalf("guard of the Plan bound to three = %+v, want 3", g)
	}
	if g := byPlan["s-unspoken"]; g.SeriesMatchedKnown || g.SeriesMatched != 0 {
		t.Fatalf("guard of the Plan no line spoke for = %+v, want unknown, not zero", g)
	}
	// A later round binds series to the first Plan: its number is replaced,
	// not kept.
	at.at = at.at.Add(time.Minute)
	evaluated("s-none", 2, lastSlot+60)
	row = anyColumn(tracker)[0]
	if row.PlanSeries[0].Matched != 2 || row.PlanSeries[0].EvaluationTime != lastSlot+60 || !row.PlanSeries[0].LastSeenAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after a round that bound two, the entry = %+v, want 2 at slot %d", row.PlanSeries[0], lastSlot+60)
	}
}
