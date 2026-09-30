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

// shortRound completes a round whose worst window is short by the given
// count, under a guard, the way a window with holes in it reports.
func shortRound(ctx context.Context, tracker *Tracker, slot int64, valid, required uint32) {
	tracker.Observe(ctx, observability.Observation{
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
		ProgressCompletionReason: "GAP_SKIPPED",
		HistoryCoverage:          &observability.HistoryCoverageFacts{Levels: 249, Short: 249, WorstValid: valid, WorstRequired: required, Guarded: 249},
		Trace:                    observability.TraceFields{StrategyID: "4101", EvaluationTime: slot},
	})
}

// The flat count and the no-progress count are two counters because they
// answer two questions. A window whose worst valid count falls every round
// is being emptied; one whose count is flat has a fixed set of holes sliding
// through. Both read "no progress" for as many rounds; only the flat count
// tells them apart -- and on a live object the no-progress count read 115 for
// both hypotheses while the flat count decided between them.
func TestAFlatShortCountIsCountedApartFromAFallingOne(t *testing.T) {
	at := &clock{at: now}
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-flat"})
	flat := newTracker(t, at)
	falling := newTracker(t, at)
	for round := 0; round < 5; round++ {
		shortRound(ctx, flat, int64(600+60*round), 1466, 1469)
		shortRound(ctx, falling, int64(600+60*round), uint32(1466-round), 1469)
		at.at = at.at.Add(time.Minute)
	}
	flatRow := anyColumn(flat)
	fallingRow := anyColumn(falling)
	if len(flatRow) != 1 || len(fallingRow) != 1 {
		t.Fatalf("rows = %d / %d, want the one object on each", len(flatRow), len(fallingRow))
	}
	// Four comparisons after the first round, all "not risen" on both.
	if flatRow[0].Coverage.NoProgressRounds != 4 || fallingRow[0].Coverage.NoProgressRounds != 4 {
		t.Fatalf("no-progress = %d / %d, want 4 on both: the counter cannot tell them apart",
			flatRow[0].Coverage.NoProgressRounds, fallingRow[0].Coverage.NoProgressRounds)
	}
	if flatRow[0].Coverage.UnchangedRounds != 4 {
		t.Fatalf("flat window's unchanged rounds = %d, want 4", flatRow[0].Coverage.UnchangedRounds)
	}
	if fallingRow[0].Coverage.UnchangedRounds != 0 {
		t.Fatalf("falling window's unchanged rounds = %d, want 0: the count moved every round", fallingRow[0].Coverage.UnchangedRounds)
	}
	// A round with nothing short ends the run, like every other run counter
	// -- read on the round itself, which is still listed when the guard holds
	// a full window: a stale flat count on a row whose window is full would
	// project a fill for a window with nothing to fill.
	flat.Observe(ctx, observability.Observation{
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
		ProgressCompletionReason: "GAP_SKIPPED",
		HistoryCoverage:          &observability.HistoryCoverageFacts{Levels: 249, Short: 0, Guarded: 249},
		Trace:                    observability.TraceFields{StrategyID: "4101", EvaluationTime: 900}})
	rows := anyColumn(flat)
	if len(rows) != 1 || rows[0].Coverage == nil {
		t.Fatalf("rows after the full round = %+v, want the object still listed under its guard with coverage", rows)
	}
	if rows[0].Coverage.UnchangedRounds != 0 || rows[0].Coverage.NoProgressRounds != 0 {
		t.Fatalf("after a round with nothing short the counts = flat %d / no-progress %d, want 0 / 0",
			rows[0].Coverage.UnchangedRounds, rows[0].Coverage.NoProgressRounds)
	}
}

// The projection reads the flat run and nothing else. Flat for k rounds and
// k shorter than the window: holes sliding, out within required − k rounds,
// stated as the latest they leave. Flat for at least the window's length:
// not sliding -- a hole that old has left -- and no time is promised.
// Too few flat rounds, a window under an hour, nothing short, or no period:
// nothing to project from.
func TestTheFillProjectionReadsTheFlatRunAndSaysWhichReadingItSupports(t *testing.T) {
	row := func(valid, required, unchanged uint32, short uint32) Anomaly {
		return Anomaly{Coverage: &HistoryCoverage{
			Levels: 249, Short: short, WorstValid: valid, WorstRequired: required, Guarded: 249,
			UnchangedRounds: unchanged, Measure: CoverageValidMeasure,
		}}
	}
	// 3 holes in a 1469-position window, flat for 115 of 60-second rounds:
	// the newest hole is at least 115 rounds old and leaves within 1354.
	fill := WindowFillOf(row(1466, 1469, 115, 249), 60, now)
	if fill == nil {
		t.Fatal("a flat short window projected nothing")
	}
	wantBy := now.Add(time.Duration(1469-115) * time.Minute)
	if fill.Holes != 3 || fill.Required != 1469 || fill.PeriodSeconds != 60 || fill.SpanSeconds != 1469*60 ||
		fill.UnchangedRounds != 115 || !fill.Sliding || fill.LatestFillBy == nil || !fill.LatestFillBy.Equal(wantBy) ||
		!fill.AnomaliesStillFire || fill.Measure != CoverageValidMeasure {
		t.Fatalf("fill = %+v (by %v), want 3 holes of 1469 at 60 s, sliding, latest by %v", fill, fill.LatestFillBy, wantBy)
	}
	// The same 3 missing for 12 rounds in a 9-position window at 600 s: a
	// hole 12 rounds old has left a 9-position window, so these are not
	// holes sliding. No time promised.
	steady := WindowFillOf(row(6, 9, 12, 3), 600, now)
	if steady == nil || steady.Sliding || steady.LatestFillBy != nil || steady.Holes != 3 || steady.UnchangedRounds != 12 {
		t.Fatalf("a window flat for longer than it is long = %+v, want not sliding and no time", steady)
	}
	// Flat for exactly the window's length is the boundary: the oldest
	// possible hole has just left, so still not sliding.
	if edge := WindowFillOf(row(6, 9, 9, 3), 600, now); edge == nil || edge.Sliding {
		t.Fatalf("flat for exactly the window = %+v, want not sliding", edge)
	}
	for name, testCase := range map[string]struct {
		anomaly Anomaly
		period  int64
	}{
		"too few flat rounds":      {row(1466, 1469, 2, 249), 60},
		"a window under an hour":   {row(6, 9, 12, 3), 60},
		"nothing short":            {row(1469, 1469, 12, 0), 60},
		"valid not below required": {row(9, 9, 12, 3), 600},
		"no period":                {row(1466, 1469, 115, 249), 0},
		"no coverage":              {Anomaly{}, 60},
	} {
		if fill := WindowFillOf(testCase.anomaly, testCase.period, now); fill != nil {
			t.Fatalf("%s projected %+v, want nothing", name, fill)
		}
	}
	// Exactly the steady-rounds bound and exactly an hour both project.
	if fill := WindowFillOf(row(6, 60, WindowFillSteadyRounds, 3), 60, now); fill == nil {
		t.Fatal("a window of exactly an hour flat for exactly the steady bound projected nothing")
	}
}
