// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// ProgressSkipRefusal names the fact a pruned skip checked and found
// against it. The vocabulary is the observability one, so the store, the
// log line and the counter say the same word.
type ProgressSkipRefusal string

const (
	SkipRefusalProgressMissing ProgressSkipRefusal = observability.CursorRefusalProgressMissing
	SkipRefusalRangeInFlight   ProgressSkipRefusal = observability.CursorRefusalRangeInFlight
	SkipRefusalCursorMoved     ProgressSkipRefusal = observability.CursorRefusalCursorMoved
	SkipRefusalCASConflict     ProgressSkipRefusal = observability.CursorRefusalCASConflict
)

// ProgressSkipResult is what a pruned skip reports: the store's usual
// commit status, and for a conflict the one fact that refused it, so a
// conflict that keeps happening can be read instead of inferred.
type ProgressSkipResult struct {
	Status ProgressCommitStatus
	// Refusal is set only when Status is ProgressConflict.
	Refusal ProgressSkipRefusal
	// InFlightSlot is the evaluation time of the Slot found in flight, the
	// one a committed skip discarded with the pruned span. Zero when there
	// was none.
	InFlightSlot EvaluationTime
}

// ProgressSkipPrunedRequest moves a Progress cursor that points into a part
// of the Schedule timeline that has been pruned to the earliest Slot the
// timeline still holds. The skipped span is recorded as a gap of kind
// GAP_SKIPPED with reason SCHEDULE_PRUNED, bounded by the old cursor; its
// Slot count is unknown because the segments that would have counted them
// are gone, so the gap is recorded as uncounted and carries where the cursor
// resumed instead of a number.
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

// PrunedSkipGap is the gap summary a pruned skip from cursor to resumeAt
// records.
//
// Two things this has to say and one it must not. The span is real and known:
// every Slot from the old cursor up to the one the timeline still holds went
// unevaluated and will not be revisited. How many Slots that is, is not known
// and cannot become known -- the segments that would have counted them are the
// segments that were pruned.
//
// So Count is zero with Uncounted set, rather than one. Count means Slots in
// every other gap, and a 1 here was read as one Slot by everything that adds
// these up or compares them: an unknown number of object-windows that were
// never detected, reported as the smallest non-zero amount of them.
//
// resumeAt is carried as its own field rather than as LastSlot. It is not a
// Slot that was skipped -- it is the first one the timeline still holds and the
// Progress will evaluate it -- so putting it in LastSlot would both break the
// bound every other gap keeps (the last skipped Slot precedes the next one) and
// name a skipped Slot that was not skipped. Recorded separately, the extent is
// stated without the population being invented: the first skipped Slot is
// known, where the cursor landed is known, and how many lie between them is
// exactly what the pruned segments took with them.
func PrunedSkipGap(cursor, resumeAt EvaluationTime) *ProgressGapSummary {
	if resumeAt < cursor {
		resumeAt = cursor
	}
	return &ProgressGapSummary{
		Kind: CompletionGapSkipped, ReasonCode: ReasonCode(contract.ReasonSchedulePruned),
		FirstSlot: cursor, LastSlot: cursor, ResumedAt: resumeAt, Uncounted: true,
	}
}

// PlanNotActiveSkipGap is the same forward skip for Slots no Plan was due at.
//
// Same shape as the pruned skip and deliberately so: both move the cursor past
// Slots that were never evaluated and carry no completion to navigate from.
// Only the reason differs, and it has to, because a reader asking "where did
// these rounds go" gets sent to retention by one and to the active set by the
// other.
func PlanNotActiveSkipGap(cursor, resumeAt EvaluationTime) *ProgressGapSummary {
	if resumeAt < cursor {
		resumeAt = cursor
	}
	return &ProgressGapSummary{
		Kind: CompletionGapSkipped, ReasonCode: ReasonCode(contract.ReasonPlanNotActive),
		FirstSlot: cursor, LastSlot: cursor, ResumedAt: resumeAt, Uncounted: true,
	}
}

// SkippedPrunedRange reports whether the Progress currently sits on a forward
// skip: its last completion is the skip itself and no Slot has been completed
// since.
//
// Both skips count. The question this answers is navigational - is there a
// completion to anchor the next Slot on - and a skip carries none whichever
// reason it holds. Reading only the pruned reason here would have the
// plan-not-active skip anchor on a Slot that was never evaluated, and
// navigation would resume inside the stretch it just moved past.
func (progress ScheduleProgress) SkippedPrunedRange() bool {
	if progress.LastCompletionKind != CompletionGapSkipped || progress.CurrentOrRecentGap == nil ||
		progress.CurrentOrRecentGap.Kind != CompletionGapSkipped {
		return false
	}
	switch progress.CurrentOrRecentGap.ReasonCode {
	case ReasonCode(contract.ReasonSchedulePruned), ReasonCode(contract.ReasonPlanNotActive):
		return true
	default:
		return false
	}
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
