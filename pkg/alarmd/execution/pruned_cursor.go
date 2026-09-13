// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// ProgressSkipPrunedRequest moves a Progress cursor that points into a part
// of the Schedule timeline that has been pruned to the earliest Slot the
// timeline still holds. The skipped span is recorded as a gap of kind
// GAP_SKIPPED with reason SCHEDULE_PRUNED, bounded by the old cursor; its
// Slot count is unknown because the segments that would have counted them
// are gone, so the gap carries a count of one skip and the span in time is
// ResumeAt minus ExpectedNextSlot.
type ProgressSkipPrunedRequest struct {
	Identity         ProgressIdentity
	OwnerFence       OwnerFence
	ExpectedNextSlot EvaluationTime
	ResumeAt         EvaluationTime
}

func (request ProgressSkipPrunedRequest) Validate() error {
	if request.Identity.QueryGroup == "" {
		return errors.New("alarmd execution: complete Progress identity is required")
	}
	if request.OwnerFence.QueryGroup != request.Identity.QueryGroup || request.OwnerFence.OwnerID == "" ||
		request.OwnerFence.OwnerEpoch == 0 || request.OwnerFence.LeaseToken == "" {
		return errors.New("alarmd execution: owner fence does not name the Progress owner")
	}
	if request.ExpectedNextSlot <= 0 || request.ResumeAt <= request.ExpectedNextSlot {
		return errors.New("alarmd execution: a pruned skip must resume after the cursor it skips from")
	}
	return nil
}

// PrunedSkipGap is the gap summary a pruned skip from cursor records.
func PrunedSkipGap(cursor EvaluationTime) *ProgressGapSummary {
	return &ProgressGapSummary{
		Kind: CompletionGapSkipped, ReasonCode: ReasonCode(contract.ReasonSchedulePruned),
		FirstSlot: cursor, LastSlot: cursor, Count: 1,
	}
}

// SkippedPrunedRange reports whether the Progress currently sits on a
// pruned skip: its last completion is the skip itself and no Slot has been
// completed since.
func (progress ScheduleProgress) SkippedPrunedRange() bool {
	return progress.LastCompletionKind == CompletionGapSkipped && progress.CurrentOrRecentGap != nil &&
		progress.CurrentOrRecentGap.Kind == CompletionGapSkipped &&
		progress.CurrentOrRecentGap.ReasonCode == ReasonCode(contract.ReasonSchedulePruned)
}

// ContinuityAnchor is the last Slot whose completion the Progress carries,
// from which the next continuous Slot is derived; ok is false when there is
// none and the persisted NextSlot stands on its own. A pruned skip has no
// anchor: the Slots before its cursor no longer exist on the timeline, so
// deriving a successor from them can only fail, and the cursor the skip set
// is authoritative until the first completion after it.
func (progress ScheduleProgress) ContinuityAnchor() (EvaluationTime, bool) {
	if progress.SkippedPrunedRange() {
		return 0, false
	}
	completed := progress.LastFullSlot
	if progress.CurrentOrRecentGap != nil && progress.CurrentOrRecentGap.LastSlot > completed {
		completed = progress.CurrentOrRecentGap.LastSlot
	}
	return completed, completed > 0
}
