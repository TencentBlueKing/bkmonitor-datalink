package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type rangeCommittedCountKey struct{}

func (coordinator *SlotExecutionCoordinator) executeExpiredRange(ctx context.Context, request execution.SlotExecutionRequest) (result execution.SlotExecutionResult, err error) {
	var committed uint32
	ctx = context.WithValue(ctx, rangeCommittedCountKey{}, &committed)
	defer func() {
		outcome := "retrying"
		if err != nil {
			outcome = "error"
		} else if result.Completed {
			outcome = "committed"
		} else if result.ReasonCode == execution.ReasonBlockedExactSetUnavailable {
			outcome = "blocked"
		}
		func() {
			defer func() { _ = recover() }()
			coordinator.ports.Observer.Observe(ctx, observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageExpiredRangeReturned, Result: observability.ResultSuccess, ExpiredRange: &observability.ExpiredRangeFacts{Result: outcome, CommittedSlots: committed, ReasonCode: observability.ReasonCode(request.ExpiredRange.CompletionReason())}})
		}()
		p := request.ExpiredRange
		observability.EmitTargetFlow(ctx, "expired_range_returned", observability.TraceFields{}, observability.TargetFlowFacts{Decision: outcome, RangeFirst: int64(p.First.Contract.Slot.EvaluationTime), RangeLast: int64(p.Last.Contract.Slot.EvaluationTime), RangeCount: p.Count, RangeDigest: p.Digest, NextSlot: int64(p.Next), Completed: result.Completed})
	}()
	owner := &streamedExecution{coordinator: coordinator, request: request}
	defer owner.releaseProvisional()
	if err := owner.retainTargets(ctx, len(request.DuePlanTargets.Plans), request.ExpiredRange); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	store, ok := coordinator.ports.Progress.(execution.ExpiredRangeStore)
	if !ok {
		return execution.SlotExecutionResult{}, errors.New("alarmd worker: range-aware Progress Store is required")
	}
	begin, err := store.BeginRange(ctx, execution.ExpiredRangeRequest{OwnerFence: request.OwnerFence, Projection: *request.ExpiredRange})
	if err != nil {
		return activationRetry(execution.ReasonBlockedExactSetUnavailable), nil
	}
	if begin.Status != execution.ProgressCommitted {
		reason := begin.ReasonCode
		if reason == "" {
			reason = execution.ReasonCode(contract.ReasonProgressBeginRejected)
		}
		return activationRetry(reason), nil
	}
	if begin.AlreadyCommitted {
		return expiredRangeCompletedFor(request), nil
	}
	// The durable proof, not a new Snapshot read or a larger current floor,
	// selects the original tail. Dynamic activation/admission remain in the
	// existing query-free finalizer and are re-read before Guard and Progress.
	mode := execution.FinalizationSnapshotUnavailable
	if request.ExpiredRange.CompletionKind() == execution.CompletionGapSkipped {
		mode = execution.FinalizationGapSkipped
	}
	finalization := execution.QueryFreeFinalization{Contract: request.Contract,
		Mode: mode, ReasonCode: request.ExpiredRange.CompletionReason(),
		Targets: request.DuePlanTargets.Clone()}
	if err := finalization.Validate(request); err != nil {
		return execution.SlotExecutionResult{}, err
	}
	return coordinator.executeQueryFreeFinalization(ctx, request, finalization)
}

func (coordinator *SlotExecutionCoordinator) commitExpiredRange(ctx context.Context, request execution.SlotExecutionRequest, completion execution.SlotCompletion) (execution.SlotExecutionResult, error) {
	if completion.Kind != request.ExpiredRange.CompletionKind() || completion.ReasonCode != request.ExpiredRange.CompletionReason() || completion.Contract != request.Contract {
		return execution.SlotExecutionResult{}, errors.New("alarmd worker: range completion differs from proof")
	}
	store, ok := coordinator.ports.Progress.(execution.ExpiredRangeStore)
	if !ok {
		return execution.SlotExecutionResult{}, errors.New("alarmd worker: range-aware Progress Store is required")
	}
	result, err := store.CommitRange(ctx, execution.ExpiredRangeRequest{OwnerFence: request.OwnerFence, Projection: *request.ExpiredRange})
	if err != nil {
		return execution.SlotExecutionResult{}, err
	}
	if result.Status != execution.ProgressCommitted {
		return execution.SlotExecutionResult{}, fmt.Errorf("alarmd worker: range Progress not committed: %s", result.Status)
	}
	if !result.AlreadyCommitted {
		if count, ok := ctx.Value(rangeCommittedCountKey{}).(*uint32); ok {
			*count = request.ExpiredRange.Count
		}
	}
	return expiredRangeCompletedFor(request), nil
}

func expiredRangeCompletedFor(request execution.SlotExecutionRequest) execution.SlotExecutionResult {
	return execution.SlotExecutionResult{Completed: true, CompletionKind: request.ExpiredRange.CompletionKind(),
		Result: observability.ResultDegraded, ReasonCode: request.ExpiredRange.CompletionReason()}
}
