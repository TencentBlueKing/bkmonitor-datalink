package progress

import (
	"context"
	"errors"
	"math"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func (store *Store) BeginRange(ctx context.Context, request execution.ExpiredRangeRequest) (execution.ExpiredRangeResult, error) {
	return store.changeRange(ctx, request, false)
}

func (store *Store) CommitRange(ctx context.Context, request execution.ExpiredRangeRequest) (execution.ExpiredRangeResult, error) {
	return store.changeRange(ctx, request, true)
}

func (store *Store) changeRange(ctx context.Context, request execution.ExpiredRangeRequest, commit bool) (execution.ExpiredRangeResult, error) {
	if err := ctx.Err(); err != nil {
		return execution.ExpiredRangeResult{}, err
	}
	if err := request.Validate(); err != nil {
		return execution.ExpiredRangeResult{}, err
	}
	p := request.Projection
	kind, reason := p.CompletionKind(), p.CompletionReason()
	identity := execution.ProgressIdentity{QueryGroup: p.First.Contract.Slot.QueryGroup}
	name, err := store.namespace(identity)
	if err != nil {
		return execution.ExpiredRangeResult{}, err
	}
	raw, missing, err := store.options.Control.ReadControl(ctx, identity.QueryGroup, name)
	if err != nil {
		return rangeIO(), nil
	}
	// A range only starts from an actually persisted NextSlot; cold starts
	// retain the existing single Slot path.
	if missing {
		return execution.ExpiredRangeResult{Status: execution.ProgressConflict}, nil
	}
	current, err := decode(raw)
	if err != nil {
		return execution.ExpiredRangeResult{}, &DeterministicInvalidError{Err: err}
	}
	if current.Identity != identity || current.UnfinishedSlot != nil {
		return execution.ExpiredRangeResult{Status: execution.ProgressConflict}, nil
	}
	if current.UnfinishedRange == nil && current.NextSlot >= p.Next {
		return execution.ExpiredRangeResult{Status: execution.ProgressCommitted, AlreadyCommitted: true}, nil
	}
	if current.NextSlot != p.First.Contract.Slot.EvaluationTime ||
		(current.UnfinishedRange != nil && !current.UnfinishedRange.Equal(p)) ||
		(commit && current.UnfinishedRange == nil) {
		return execution.ExpiredRangeResult{Status: execution.ProgressConflict}, nil
	}
	// Recheck the actual successor on both Begin and Commit. A later cutover
	// cannot silently turn the stored proof into a different range.
	next, err := store.options.Slots.NextSlotAfter(ctx, identity.QueryGroup, p.Last.Contract.Slot.EvaluationTime)
	if err != nil {
		return execution.ExpiredRangeResult{}, err
	}
	if next != p.Next {
		return execution.ExpiredRangeResult{Status: execution.ProgressConflict}, nil
	}
	if prior := current.CurrentOrRecentGap; prior != nil && current.LastCompletionKind == kind &&
		prior.Kind == kind && prior.ReasonCode == reason && prior.Count > math.MaxUint32-p.Count {
		continuous, readErr := store.options.Slots.NextSlotAfter(ctx, identity.QueryGroup, prior.LastSlot)
		if readErr != nil {
			return execution.ExpiredRangeResult{}, readErr
		}
		if continuous == p.First.Contract.Slot.EvaluationTime {
			return execution.ExpiredRangeResult{}, errors.New("progress: expired range recent Gap count overflow before Begin")
		}
	}
	if commit {
		gap := &execution.ProgressGapSummary{Kind: kind,
			ReasonCode: reason,
			FirstSlot:  p.First.Contract.Slot.EvaluationTime, LastSlot: p.Last.Contract.Slot.EvaluationTime, Count: p.Count}
		prior := current.CurrentOrRecentGap
		if current.LastCompletionKind == gap.Kind && prior != nil && prior.Kind == gap.Kind && prior.ReasonCode == gap.ReasonCode {
			continuous, err := store.options.Slots.NextSlotAfter(ctx, identity.QueryGroup, prior.LastSlot)
			if err != nil {
				return execution.ExpiredRangeResult{}, err
			}
			if continuous == gap.FirstSlot {
				if prior.Count > math.MaxUint32-p.Count {
					return execution.ExpiredRangeResult{}, errors.New("progress: expired range recent Gap count overflow")
				}
				gap.FirstSlot, gap.Count = prior.FirstSlot, prior.Count+p.Count
				gap.NextProbeAt = prior.NextProbeAt
			}
		}
		current.NextSlot, current.LastCompletionKind = p.Next, gap.Kind
		current.CurrentOrRecentGap, current.UnfinishedRange = gap, nil
	} else {
		copy := p.Clone()
		current.UnfinishedRange = &copy
	}
	value, err := encode(current)
	if err != nil {
		return execution.ExpiredRangeResult{}, err
	}
	status, err := store.options.Control.FencedCompareAndSet(ctx, ownership.FencedCASRequest{
		Fence: request.OwnerFence, At: store.options.Now(), Namespace: name,
		Expected: raw, Value: value, TTL: 0,
	})
	if err != nil {
		return rangeIO(), nil
	}
	switch status {
	case ownership.FencedCASApplied:
		return execution.ExpiredRangeResult{Status: execution.ProgressCommitted}, nil
	case ownership.FencedCASConflict:
		return execution.ExpiredRangeResult{Status: execution.ProgressConflict}, nil
	case ownership.FencedCASStaleOwner:
		return execution.ExpiredRangeResult{Status: execution.ProgressStaleOwner}, nil
	default:
		return rangeIO(), nil
	}
}

func rangeIO() execution.ExpiredRangeResult {
	return execution.ExpiredRangeResult{Status: execution.ProgressRetryableIO, ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable)}
}

var _ execution.ExpiredRangeStore = (*Store)(nil)
