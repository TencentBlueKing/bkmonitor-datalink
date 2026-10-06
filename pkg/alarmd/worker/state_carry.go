// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"

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

// Series carry outcomes, one per series that had no record of its own under
// a Plan whose activation carries history from the generation it moved from.
const (
	SeriesCarryCarried            = "carried"
	SeriesCarryNothingKept        = "nothing_kept"
	SeriesCarrySkippedActiveGuard = "skipped_active_guard"
	SeriesCarryOldMissing         = "old_missing"
)

// SeriesCarryOutcomes are the outcomes in the order a reader groups them.
var SeriesCarryOutcomes = []string{SeriesCarryCarried, SeriesCarryNothingKept, SeriesCarrySkippedActiveGuard,
	SeriesCarryOldMissing}

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
// activation carries and whose detect fingerprint is the one the Plan now
// compiles to: the same detection on the same series, which is a fact under
// either generation. The trigger and the recovery are summarized from those
// results on every record, so the Plan reads them with its new parameters the
// way it would have read them had the new contract always run. A point left
// with no result is dropped. What is kept goes into the view's history and
// nowhere else: the view stays the missing record the write creates, with
// the new Level contract, so nothing loaded has to be validated against the
// old one.
//
// A series whose previous record still guarded something - a series guard,
// or a carried Level that was not FULL - is not carried at all. A guard
// stands for holes the points do not show, and taking the points without it
// could let the new generation conclude where the old one would not. Such a
// series starts from nothing, which is WARMING, as a new series does.
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
	for _, level := range due.CompiledPlan.Levels() {
		id := level.Definition().LevelID
		for _, carriedLevel := range due.StateCarry.Levels {
			if carriedLevel == id {
				fingerprints[id] = level.Fingerprints().Detect
			}
		}
	}
	for _, level := range carried.Levels {
		if _, kept := fingerprints[level.LevelID]; kept && level.HistoryCompleteness != execution.HistoryFull {
			return view, SeriesCarrySkippedActiveGuard
		}
	}
	history := make([]execution.StateHistoryPoint, 0, len(carried.History))
	for _, point := range carried.History {
		facts := make([]execution.StateLevelFact, 0, len(point.Levels))
		for _, fact := range point.Levels {
			if fingerprint, kept := fingerprints[fact.LevelID]; kept && fingerprint == fact.DetectFingerprint {
				facts = append(facts, fact)
			}
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
	return view, SeriesCarryCarried
}
