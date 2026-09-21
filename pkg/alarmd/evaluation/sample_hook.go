// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

// SetSeriesSampler is startup wiring, before this evaluator is shared with
// workers. Selections can subsequently change through sampler.Select.
func (e *Evaluator) SetSeriesSampler(sampler *observability.SeriesSampler) { e.samples = sampler }

func (e *Evaluator) reserveSeriesSample(ctx context.Context, header execution.InternalExecutionHeader, due execution.DuePlan, inputs []execution.SeriesEvaluationInputRequest, record execution.RecordView, view execution.RuntimeStateView) *observability.SeriesSampleReservation {
	first := inputs[0]
	r := e.samples.TryReserve(ctx, observability.SeriesSampleCandidate{
		QueryGroup: string(header.Contract.Slot.QueryGroup), TenantID: due.Identity.TenantID,
		BusinessID: due.Identity.BusinessID, StrategyID: due.Identity.StrategyID,
		StateGeneration: string(due.StateGeneration), PlanScheduleRevision: string(due.ScheduleRevision),
		SeriesDigest: string(first.SeriesIdentity), SeriesKind: string(first.Kind), Slot: int64(header.Contract.Slot.EvaluationTime),
	})
	if r == nil {
		return nil
	}
	s := &r.Sample
	s.ExecutionID, s.SnapshotRevision, s.QueryRevision = header.ExecutionID, string(header.Contract.SnapshotRevision), string(header.Contract.QueryRevision)
	s.ScheduleRevision, s.StateStatus = string(header.Contract.ScheduleRevision), string(view.Status)
	s.RecordID, s.SourceTime = record.RecordID(), record.SourceTime()
	for _, level := range due.CompiledPlan.Levels() {
		l := r.AddLevel(level.Definition().LevelID)
		if l == nil {
			continue
		}
		l.TriggerRequired, l.TriggerWindow = level.Trigger().RequiredAnomalies, level.Trigger().WindowSize
		l.RecoveryEnabled, l.RecoveryRequired = level.Recovery().Enabled, level.Recovery().ConsecutiveWindows
		l.StateDisposition = "not_evaluated"
		for _, input := range inputs {
			if input.Consumer.LevelID != l.LevelID {
				continue
			}
			l.InputCompletion = "FULL"
			for _, binding := range input.Inputs {
				if binding.Completeness == execution.CompletenessUnavailable {
					l.InputCompletion = "UNAVAILABLE"
					break
				}
				if binding.Completeness != execution.CompletenessFull {
					l.InputCompletion = "PARTIAL"
				}
			}
		}
	}
	return r
}

func captureSampleFacts(r *observability.SeriesSampleReservation, facts []detect.LevelFact, projected []detect.ProjectedValue) {
	if r == nil {
		return
	}
	for _, f := range facts {
		l := r.Level(f.Definition.LevelID)
		if l == nil {
			continue
		}
		l.DetectResult, l.DetectReason, l.PredicateFingerprint = f.Result, f.ReasonCode, f.Evidence.PredicateDigest
		if f.Evidence.HasMatchedAlgorithm {
			ordinal := f.Evidence.MatchedAlgorithmOrdinal
			l.MatchedAlgorithmOrdinal = &ordinal
		}
		if f.Evidence.HasMatchedGroup {
			ordinal := f.Evidence.MatchedGroupOrdinal
			l.MatchedGroupOrdinal = &ordinal
		}
		if f.Evidence.HasProjectedValue && int(f.Evidence.ProjectedValueOrdinal) < len(projected) {
			v := projected[f.Evidence.ProjectedValueOrdinal]
			if v.Available {
				l.NormalizedScalar, l.ScalarStatus = v.CanonicalDecimal, "available"
			} else {
				l.ScalarStatus = "unavailable"
			}
		}
	}
}

func captureSampleDecision(r *observability.SeriesSampleReservation, result trigger.EvaluationResultV2) {
	if r == nil {
		return
	}
	r.Sample.RecoveryHeld, r.Sample.RecoveryCause, r.Sample.OpenAlertGate = result.RecoveryGate.Held, result.RecoveryGate.Cause, result.RecoveryGate.OpenAlertGate
	if result.TriggerEvent != nil {
		r.Sample.EventID = result.TriggerEvent.EventID
	}
	for _, outcome := range result.LevelOutcomes {
		l := r.Level(outcome.LevelID)
		if l == nil {
			continue
		}
		l.StateDisposition = outcome.StateDisposition
		if w := outcome.DecisionWindow; w != nil {
			l.DecisionStatus = "evaluated"
			observed, misses := w.Trigger.ObservedAnomalies, w.Recovery.ObservedConsecutiveMisses
			l.TriggerObserved, l.RecoveryObserved = &observed, &misses
		}
	}
}

func finishSeriesSample(r *observability.SeriesSampleReservation, result recordResult) {
	if r == nil {
		return
	}
	for _, outcome := range result.outcomes {
		if l := r.Level(outcome.LevelID); l != nil {
			l.Outcome, l.Reason = string(outcome.Outcome), string(outcome.ReasonCode)
		}
	}
	r.Commit()
}
