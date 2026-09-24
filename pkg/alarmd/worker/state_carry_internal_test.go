// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// What a series keeps from the generation its Plan moved from: results of
// the carried Levels under the detect fingerprint the Plan now compiles to,
// and nothing from a record that still guarded holes.
func TestASeriesKeepsOnlyTheSameFactsFromThePreviousGeneration(t *testing.T) {
	plan := internalCompiledPlan(t, "7", 5, 60)
	levels := plan.Levels()
	if len(levels) == 0 {
		t.Fatal("test Plan has no Level")
	}
	level := levels[0].Definition().LevelID
	fingerprint := levels[0].Fingerprints().Detect
	due := execution.DuePlan{CompiledPlan: plan, StateGeneration: "new",
		StateCarry: &execution.StateCarry{From: "old", Levels: []uint32{level}}}
	point := func(sourceTime int64, fingerprint string) execution.StateHistoryPoint {
		return execution.StateHistoryPoint{RecordID: "r", SourceTime: sourceTime, Levels: []execution.StateLevelFact{
			{LevelID: level, DetectFingerprint: fingerprint, Result: execution.LevelFactAnomalous},
		}}
	}
	previous := func(history []execution.StateHistoryPoint, completeness execution.HistoryCompleteness, guard *execution.StateGuardFact) *execution.RuntimeStateView {
		return &execution.RuntimeStateView{Status: execution.StateFoundReady, History: history, SeriesGuard: guard,
			Levels: []execution.RuntimeLevelStateView{{LevelID: level, HistoryCompleteness: completeness}}}
	}
	missing := func(carried *execution.RuntimeStateView) execution.RuntimeStateView {
		return execution.RuntimeStateView{Status: execution.StateMissingWarming, Carried: carried}
	}

	t.Run("the same detection is kept, another is dropped", func(t *testing.T) {
		view, outcome := carryHistory(due, missing(previous([]execution.StateHistoryPoint{
			point(60, fingerprint), point(120, "another detection"), point(180, fingerprint),
		}, execution.HistoryFull, nil)))
		if outcome != SeriesCarryCarried || len(view.History) != 2 || view.History[0].SourceTime != 60 || view.History[1].SourceTime != 180 {
			t.Fatalf("outcome=%s history=%+v, want the two points of the same detection", outcome, view.History)
		}
		if view.Carried != nil || view.Status != execution.StateMissingWarming || len(view.Levels) != 0 || view.BlobRevision != 0 {
			t.Fatalf("view=%+v, want the missing record the write creates, with the history and nothing else", view)
		}
	})

	t.Run("a Level named carried whose points were fingerprinted otherwise keeps nothing", func(t *testing.T) {
		view, outcome := carryHistory(due, missing(previous([]execution.StateHistoryPoint{point(60, "old formula")}, execution.HistoryFull, nil)))
		if outcome != SeriesCarryNothingKept || len(view.History) != 0 {
			t.Fatalf("outcome=%s history=%+v, want nothing kept: the Level warms up on its window", outcome, view.History)
		}
	})

	t.Run("a record that still guarded holes is not carried", func(t *testing.T) {
		for name, carried := range map[string]*execution.RuntimeStateView{
			"series guard":  previous([]execution.StateHistoryPoint{point(60, fingerprint)}, execution.HistoryFull, &execution.StateGuardFact{Status: execution.HistoryGapped}),
			"GAPPED Level":  previous([]execution.StateHistoryPoint{point(60, fingerprint)}, execution.HistoryGapped, nil),
			"WARMING Level": previous([]execution.StateHistoryPoint{point(60, fingerprint)}, execution.HistoryWarming, nil),
		} {
			view, outcome := carryHistory(due, missing(carried))
			if outcome != SeriesCarrySkippedActiveGuard || len(view.History) != 0 {
				t.Errorf("%s: outcome=%s history=%+v, want the series to start from nothing", name, outcome, view.History)
			}
		}
	})

	t.Run("nothing under the previous generation", func(t *testing.T) {
		if _, outcome := carryHistory(due, missing(nil)); outcome != SeriesCarryOldMissing {
			t.Fatalf("outcome=%s", outcome)
		}
	})

	t.Run("a Plan that carries nothing, or a series with its own record, is untouched", func(t *testing.T) {
		carried := previous([]execution.StateHistoryPoint{point(60, fingerprint)}, execution.HistoryFull, nil)
		plain := due
		plain.StateCarry = nil
		if view, outcome := carryHistory(plain, missing(carried)); outcome != "" || len(view.History) != 0 || view.Carried != nil {
			t.Fatalf("no carry: outcome=%s view=%+v", outcome, view)
		}
		found := execution.RuntimeStateView{Status: execution.StateFoundReady, History: []execution.StateHistoryPoint{point(300, fingerprint)}, Carried: carried}
		if view, outcome := carryHistory(due, found); outcome != "" || len(view.History) != 1 || view.History[0].SourceTime != 300 {
			t.Fatalf("own record: outcome=%s view=%+v, want its own history only", outcome, view)
		}
	})
}
