// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// What a series keeps from the generation its Plan moved from: every result
// of a carried Level, as decided - one its Level's previous detection decided
// is written under the fingerprint the Plan now compiles to and counted
// carried_across_detect, as Python keeps its check results across an edit
// - nothing of a Level not carried, and nothing from a record
// a guard still held; a window's own holes come along.
func TestASeriesKeepsTheCarriedLevelsResultsFromThePreviousGeneration(t *testing.T) {
	plan := internalCompiledPlan(t, "7", 5, 60)
	levels := plan.Levels().Copy()
	if len(levels) == 0 {
		t.Fatal("test Plan has no Level")
	}
	level := levels[0].Definition().LevelID
	fingerprint := levels[0].Fingerprints().Detect
	due := execution.DuePlan{CompiledPlan: plan, StateGeneration: "new",
		StateCarry: &execution.StateCarry{From: "old", Levels: []uint32{level}}}
	pointAs := func(sourceTime int64, fingerprint string, result execution.LevelFactResult) execution.StateHistoryPoint {
		return execution.StateHistoryPoint{RecordID: "r", SourceTime: sourceTime, Levels: []execution.StateLevelFact{
			{LevelID: level, DetectFingerprint: fingerprint, Result: result},
		}}
	}
	point := func(sourceTime int64, fingerprint string) execution.StateHistoryPoint {
		return pointAs(sourceTime, fingerprint, execution.LevelFactAnomalous)
	}
	previous := func(history []execution.StateHistoryPoint, completeness execution.HistoryCompleteness, guard *execution.StateGuardFact) *execution.RuntimeStateView {
		return &execution.RuntimeStateView{Status: execution.StateFoundReady, History: history, SeriesGuard: guard,
			Levels: []execution.RuntimeLevelStateView{{LevelID: level, HistoryCompleteness: completeness}}}
	}
	missing := func(carried *execution.RuntimeStateView) execution.RuntimeStateView {
		return execution.RuntimeStateView{Status: execution.StateMissingWarming, Carried: carried}
	}

	t.Run("the same detection is kept as it is", func(t *testing.T) {
		view, outcome := carryHistory(due, missing(previous([]execution.StateHistoryPoint{
			point(60, fingerprint), point(120, fingerprint),
		}, execution.HistoryFull, nil)))
		if outcome != SeriesCarryCarried || len(view.History) != 2 || view.History[0].SourceTime != 60 || view.History[1].SourceTime != 120 {
			t.Fatalf("outcome=%s history=%+v, want both points carried", outcome, view.History)
		}
		if view.Carried != nil || view.Status != execution.StateMissingWarming || len(view.Levels) != 0 || view.BlobRevision != 0 {
			t.Fatalf("view=%+v, want the missing record the write creates, with the history and nothing else", view)
		}
	})

	t.Run("results another detection decided are kept as decided, under the fingerprint now", func(t *testing.T) {
		view, outcome := carryHistory(due, missing(previous([]execution.StateHistoryPoint{
			point(60, fingerprint), pointAs(120, "the threshold before the edit", execution.LevelFactNormal),
			point(180, "the threshold before the edit"),
		}, execution.HistoryFull, nil)))
		if outcome != SeriesCarryCarriedAcrossDetect || len(view.History) != 3 {
			t.Fatalf("outcome=%s history=%+v, want all three points carried across the detection edit", outcome, view.History)
		}
		want := []execution.LevelFactResult{execution.LevelFactAnomalous, execution.LevelFactNormal, execution.LevelFactAnomalous}
		for index, point := range view.History {
			if fact := point.Levels[0]; fact.DetectFingerprint != fingerprint || fact.Result != want[index] {
				t.Fatalf("point %d carries %+v, want result %s under the fingerprint now", index, fact, want[index])
			}
		}
	})

	t.Run("a Level the activation does not carry keeps nothing", func(t *testing.T) {
		other := previous([]execution.StateHistoryPoint{{RecordID: "r", SourceTime: 60, Levels: []execution.StateLevelFact{
			{LevelID: level + 1, DetectFingerprint: fingerprint, Result: execution.LevelFactAnomalous},
		}}}, execution.HistoryFull, nil)
		view, outcome := carryHistory(due, missing(other))
		if outcome != SeriesCarryNothingKept || len(view.History) != 0 {
			t.Fatalf("outcome=%s history=%+v, want nothing kept: no carried Level had a result", outcome, view.History)
		}
	})

	t.Run("a record a guard still held is not carried, and says why", func(t *testing.T) {
		held := func(completeness execution.HistoryCompleteness, reason execution.ReasonCode) *execution.RuntimeStateView {
			carried := previous([]execution.StateHistoryPoint{point(60, fingerprint)}, completeness, nil)
			carried.Levels[0].GapReasonCode = reason
			return carried
		}
		guard := execution.ReasonCode("PRIMARY_INPUT_PARTIAL")
		for name, test := range map[string]struct {
			carried *execution.RuntimeStateView
			want    string
		}{
			"series guard": {previous([]execution.StateHistoryPoint{point(60, fingerprint)}, execution.HistoryFull,
				&execution.StateGuardFact{Status: execution.HistoryGapped}), SeriesCarrySkippedActiveGuard},
			"a guard held the Level WARMING": {held(execution.HistoryWarming, guard), SeriesCarrySkippedGuardWarming},
			"a guard held the Level GAPPED":  {held(execution.HistoryGapped, guard), SeriesCarrySkippedGuardGapped},
			// A Plan back after a stretch it was not active guards that
			// stretch, which is not its own activation's drift.
			"PLAN_REACTIVATED WARMING": {held(execution.HistoryWarming, execution.ReasonCode(contract.ReasonPlanReactivated)), SeriesCarrySkippedGuardWarming},
			// No reason is not the window's own: the record is read as guarded.
			"GAPPED with no reason": {held(execution.HistoryGapped, ""), SeriesCarrySkippedGuardGapped},
		} {
			view, outcome := carryHistory(due, missing(test.carried))
			if outcome != test.want || len(view.History) != 0 {
				t.Errorf("%s: outcome=%s history=%+v, want %s and the series to start from nothing", name, outcome, view.History, test.want)
			}
		}
	})

	// A window that had not filled yet, or had lost a point, when the edit
	// landed: its holes are absent points and come along absent, so the
	// series keeps what it had decided.
	t.Run("a Level WARMING or GAPPED for holes in its points is carried", func(t *testing.T) {
		for name, test := range map[string]struct {
			completeness execution.HistoryCompleteness
			reason       string
		}{
			"WARMING": {execution.HistoryWarming, contract.ReasonHistoryWarming},
			"GAPPED":  {execution.HistoryGapped, contract.ReasonHistoryGapped},
			// Its activation's own drift guard, until the trigger and recovery
			// windows converge, on the first edit and on a later one inside it.
			"WARMING under its activation's drift": {execution.HistoryWarming, contract.ReasonConfigDrift},
			"GAPPED under its activation's drift":  {execution.HistoryGapped, contract.ReasonConfigDrift},
		} {
			carried := previous([]execution.StateHistoryPoint{point(120, "the threshold before the edit"), point(180, fingerprint)}, test.completeness, nil)
			carried.Levels[0].GapReasonCode = execution.ReasonCode(test.reason)
			view, outcome := carryHistory(due, missing(carried))
			if outcome != SeriesCarryCarriedAcrossDetect || len(view.History) != 2 {
				t.Errorf("%s: outcome=%s history=%+v, want both points carried across the detection edit", name, outcome, view.History)
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
