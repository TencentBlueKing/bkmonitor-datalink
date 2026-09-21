// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// resumedSeriesResult resumes completion bookkeeping from already committed
// State. It is not a new evaluation: State does not retain the original record
// outcomes or window counts, so neither business verdicts nor HistoryCoverage
// are manufactured here. UNKNOWN entries describe persisted protection only.
func resumedSeriesResult(header execution.InternalExecutionHeader, due execution.DuePlan, view execution.RuntimeStateView, gaps execution.GapLoadResult) (execution.EvaluationResult, error) {
	version, err := execution.BuildApplyVersion(header.Contract, due.StateApplyEpoch)
	if err != nil {
		return execution.EvaluationResult{}, err
	}
	if view.Identity.Plan != due.Identity || view.Identity.StateGeneration != due.StateGeneration ||
		view.VersionComparison != execution.ApplyVersionEqual || execution.CompareApplyVersion(view.PersistedApplyVersion, version) != execution.ApplyVersionEqual {
		return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed State identity or ApplyVersion differs from Slot")
	}
	if len(view.Levels) == 0 {
		return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed State has no persisted Level facts")
	}
	result := execution.EvaluationResult{Contract: header.Contract, Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone}
	plan := execution.PlanEvaluationResult{Plan: due.Identity, Disposition: execution.PlanDecided, ReasonCode: observability.ReasonNone}
	for _, level := range view.Levels {
		reason := execution.ReasonCode("")
		switch level.HistoryCompleteness {
		case execution.HistoryFull:
		case execution.HistoryWarming, execution.HistoryGapped:
			reason = level.GapReasonCode
			if reason == "" {
				return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed incomplete Level has no reason")
			}
		default:
			return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed Level has unsupported completeness")
		}
		if guard := view.SeriesGuard; guard != nil {
			if guard.Status != execution.HistoryWarming && guard.Status != execution.HistoryGapped {
				return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed series guard has unsupported completeness")
			}
			if guard.ReasonCode == "" {
				return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed series guard has no reason")
			}
			if reason == "" {
				reason = guard.ReasonCode
			}
		}
		if reason != "" {
			plan.Disposition = execution.PlanDecidedDegraded
			if plan.ReasonCode == observability.ReasonNone {
				plan.ReasonCode = reason
			}
			plan.LevelOutcomes = append(plan.LevelOutcomes, execution.LevelOutcome{Plan: due.Identity, LevelID: level.LevelID, SeriesIdentityDigest: view.Identity.SeriesIdentityDigest, Outcome: execution.LevelOutcomeUnknown, ReasonCode: reason})
			if result.Result == observability.ResultSuccess {
				result.Result = observability.ResultDegraded
				result.ReasonCode = reason
			}
		}
	}
	gap, found := gaps.Find(execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration})
	if found && gap.Status != execution.GapMissing {
		if gap.Status != execution.GapFound && gap.Status != execution.GapClearedTombstone {
			return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed Plan gap is not readable: %s", gap.Status)
		}
		switch execution.CompareApplyVersion(gap.PersistedApplyVersion, version) {
		case execution.ApplyVersionPersistedNewer:
			return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed Plan gap is newer than Slot")
		case execution.ApplyVersionEqual:
			if gap.LastScheduleRevision != due.ScheduleRevision {
				return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed same-Slot Plan gap has a different schedule")
			}
			// This Slot already committed its gap update; do not count it twice.
		case execution.ApplyVersionPersistedOlder:
			// The original recovery decision was not persisted with State.
			// Its inputs cannot be reconstructed from post-Slot history, so
			// leave protection intact until a later evaluated Slot advances it.
		default:
			return execution.EvaluationResult{}, fmt.Errorf("alarmd worker: resumed Plan gap version is not comparable")
		}
	}
	result.Plans = []execution.PlanEvaluationResult{plan}
	return result, nil
}
