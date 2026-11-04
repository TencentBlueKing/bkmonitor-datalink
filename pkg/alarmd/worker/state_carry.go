// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// observeCarries reports one batch's carry outcomes, one observation per
// outcome that any series met.
func (stream *streamedExecution) observeCarries(ctx context.Context, outcomes map[string]int) {
	for _, outcome := range SeriesCarryOutcomes {
		count := outcomes[outcome]
		if count == 0 {
			continue
		}
		stream.coordinator.emitObservation(ctx, observability.Observation{
			Component: observability.ComponentState, Stage: observability.StageStateCarried,
			Operation: observability.Operation(stream.request.Operation), Result: observability.ResultSuccess,
			StateCarry: &observability.StateCarryFacts{Scope: "series", Result: outcome, Count: count},
		})
	}
}

// Series carry outcomes, one per evaluation of a series that has no record of
// its own yet under a Plan whose activation carries history from the
// generation it moved from: a series is counted on each evaluation until its
// first record under the new generation is written, so the counts are
// evaluations, not series.
const (
	SeriesCarryCarried = "carried"
	// SeriesCarryCarriedAcrossDetect is a series carried with results its
	// Level's previous detection decided: the edit changed how the Level
	// detects, and the results stand as decided, as Python's do.
	SeriesCarryCarriedAcrossDetect = "carried_across_detect"
	SeriesCarryNothingKept         = "nothing_kept"
	// SeriesCarrySkippedActiveGuard is a series whose previous record held a
	// series guard. SeriesCarrySkippedGuardWarming and
	// SeriesCarrySkippedGuardGapped are one whose carried Level a guard held
	// WARMING, or GAPPED, there - a reason other than the window's own holes:
	// three different reasons a reader acts on differently, so each is named.
	SeriesCarrySkippedActiveGuard  = "skipped_active_guard"
	SeriesCarrySkippedGuardWarming = "skipped_guard_warming"
	SeriesCarrySkippedGuardGapped  = "skipped_guard_gapped"
	SeriesCarryOldMissing          = "old_missing"
)

// SeriesCarryOutcomes are the outcomes in the order a reader groups them.
var SeriesCarryOutcomes = []string{SeriesCarryCarried, SeriesCarryCarriedAcrossDetect, SeriesCarryNothingKept,
	SeriesCarrySkippedActiveGuard, SeriesCarrySkippedGuardWarming, SeriesCarrySkippedGuardGapped, SeriesCarryOldMissing}

// carryFrom is the generation a due Plan's series are also read under when
// they have no record of their own.
func carryFrom(due execution.DuePlan) execution.StateGeneration {
	if due.StateCarry == nil || due.StateCarry.From == due.StateGeneration {
		return ""
	}
	return due.StateCarry.From
}

// carryHistory turns what the previous generation held for a series with no
// record of its own into the history the new generation starts from.
//
// Only detection results are kept, and only a result whose Level the
// activation carries. Each is the anomaly or normal its point was decided as
// when it arrived, and stands as decided, under whichever detection the
// Level ran then: a threshold or algorithm edit keeps the results the old
// detection decided, as Python's check results do (bk-monitor
// keys them by strategy, item, dimensions and level, and no edit clears
// them). A result decided by another detection is written under the Level's
// detect fingerprint now - the window keeps one fingerprint a Level - and
// the series is counted carried_across_detect. The trigger and the recovery
// are summarized from those results on every record, so the Plan reads them
// with its new parameters. A point left with no result is dropped. What is kept goes into the view's history and
// nowhere else: the view stays the missing record the write creates, with
// the new Level contract, so nothing loaded has to be validated against the
// old one.
//
// A series whose previous record still guarded something - a series guard,
// or a carried Level a guard held WARMING or GAPPED - is not carried at all.
// A guard stands for holes the points do not show, and taking the points
// without it could let the new generation conclude where the old one would
// not. Such a series starts from nothing, which is WARMING, as a new series
// does. A Level WARMING or GAPPED for its window's own holes
// (HISTORY_WARMING, HISTORY_GAPPED) is carried: the holes are absent points,
// they come along as absent, and the new window reads the same completeness
// from them. Skipping it started over every series whose window had not yet
// filled when the edit landed, or had lost one point in it, and so a
// threshold edit on such a series waited a whole window more to conclude.
// A Level its activation's own drift guard holds WARMING or
// GAPPED (CONFIG_DRIFT, until its trigger and recovery windows converge) is
// carried too: the positions that guard covers are before the Plan ran under
// that activation, and the drift Slot that wrote no point - absent points
// all, which come along as absent - and the new activation has its own
// guard. Skipping it started over every series edited within that stretch
// of a new or rewarmed Plan, again on each later edit inside it, where
// Python keeps the results whatever came before. Any other
// guard still skips. Two guard paths also write HISTORY_GAPPED, and both are
// safe to carry: the missing-input guard stores its point UNAVAILABLE, a
// hole in the window that comes along as one; the supplement's replay of a
// cleared gap marker writes it once the gap has cleared, and the carrying
// activation warms up a full Slot first anyway. A new guard that writes
// either reason is carried here too: give it its own reason if its holes
// are not in the points.
func carryHistory(due execution.DuePlan, view execution.RuntimeStateView) (execution.RuntimeStateView, string) {
	carried := view.Carried
	view.Carried = nil
	if carryFrom(due) == "" || view.Status != execution.StateMissingWarming {
		return view, ""
	}
	if carried == nil {
		return view, SeriesCarryOldMissing
	}
	if carried.SeriesGuard != nil {
		return view, SeriesCarrySkippedActiveGuard
	}
	fingerprints := make(map[uint32]string)
	for _, level := range due.CompiledPlan.Levels().All() {
		id := level.Definition().LevelID
		for _, carriedLevel := range due.StateCarry.Levels {
			if carriedLevel == id {
				fingerprints[id] = level.Fingerprints().Detect
			}
		}
	}
	for _, level := range carried.Levels {
		if _, kept := fingerprints[level.LevelID]; !kept || level.HistoryCompleteness == execution.HistoryFull || holesInThePoints(level) {
			continue
		}
		if level.HistoryCompleteness == execution.HistoryWarming {
			return view, SeriesCarrySkippedGuardWarming
		}
		return view, SeriesCarrySkippedGuardGapped
	}
	history := make([]execution.StateHistoryPoint, 0, len(carried.History))
	acrossDetect := false
	for _, point := range carried.History {
		facts := make([]execution.StateLevelFact, 0, len(point.Levels))
		for _, fact := range point.Levels {
			fingerprint, kept := fingerprints[fact.LevelID]
			if !kept {
				continue
			}
			if fact.DetectFingerprint != fingerprint {
				fact.DetectFingerprint, acrossDetect = fingerprint, true
			}
			facts = append(facts, fact)
		}
		if len(facts) == 0 {
			continue
		}
		history = append(history, execution.StateHistoryPoint{RecordID: point.RecordID, SourceTime: point.SourceTime, Levels: facts})
	}
	if len(history) == 0 {
		return view, SeriesCarryNothingKept
	}
	view.History = history
	if acrossDetect {
		return view, SeriesCarryCarriedAcrossDetect
	}
	return view, SeriesCarryCarried
}

// holesInThePoints reports whether a Level is WARMING or GAPPED for a reason
// whose holes are absent points in its window: its window's own holes, the
// reasons the evaluator writes when no guard decided it (which the
// missing-input guard and the supplement's replay of a cleared gap also
// write, see carryHistory), or its activation's own drift guard.
func holesInThePoints(level execution.RuntimeLevelStateView) bool {
	switch level.GapReasonCode {
	case execution.ReasonCode(contract.ReasonHistoryWarming):
		return level.HistoryCompleteness == execution.HistoryWarming
	case execution.ReasonCode(contract.ReasonHistoryGapped):
		return level.HistoryCompleteness == execution.HistoryGapped
	case execution.ReasonCode(contract.ReasonConfigDrift):
		return level.HistoryCompleteness == execution.HistoryWarming || level.HistoryCompleteness == execution.HistoryGapped
	}
	return false
}
