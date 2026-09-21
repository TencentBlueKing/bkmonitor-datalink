// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func (stream *streamedExecution) retainTargets(ctx context.Context, count int, targets any) error {
	return stream.retainTargetBytes(ctx, count, retainedObjectBytes(targets))
}

func (stream *streamedExecution) retainTargetBytes(ctx context.Context, count int, size uint64) error {
	if uint64(count) > stream.coordinator.slotBudget().MaxGapMutations {
		err := &provisionalBudgetExceededError{budget: observability.CapacityBudgetGapMutations}
		stream.coordinator.observeCapacityRejection(ctx, stream.request.Operation, err.budget, err)
		return err
	}
	// Covers target copies, the selected-Plan index and mutation lookup tables.
	// No Dataset or compiled Plan graph is passed into this measurement.
	retained := 4 * size
	if err := stream.reserveProvisionalAt(ctx, 0, retained, stream.reservationPhase("normal_gap")); err != nil {
		return err
	}
	stream.retained += retained
	return nil
}

func (stream *streamedExecution) loadGapFacts(ctx context.Context, request execution.GapLoadRequest) (execution.GapLoadResult, error) {
	err := stream.coordinator.ports.GapGuard.LoadGapsInto(ctx, request, func(snapshot execution.GapGuardSnapshot) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Only one decoded value exists outside the shared retained budget.
		// The Store bounds that value and emits before reading the next key.
		retained := 2 * retainedObjectBytes(snapshot)
		reservations := &stream.coordinator.reservations
		reservations.mu.Lock()
		var err error
		if reservations.gapFacts >= stream.coordinator.budget.MaxGapMutations {
			err = budgetRejection(observability.CapacityBudgetGapMutations, stream.reservationPhase("normal_gap"), reservations.gapFacts, 1, stream.coordinator.budget.MaxGapMutations, stream.ownReservation(stream.gapFacts))
		} else if retained > stream.coordinator.budget.MaxRetainedBytes-reservations.retainedBytes {
			err = budgetRejection(observability.CapacityBudgetRetainedBytes, stream.reservationPhase("normal_gap"), reservations.retainedBytes, retained, stream.coordinator.budget.MaxRetainedBytes, stream.ownBudget(observability.CapacityBudgetRetainedBytes))
		} else {
			reservations.gapFacts++
			reservations.retainedBytes += retained
		}
		reservations.mu.Unlock()
		if err != nil {
			var exceeded *provisionalBudgetExceededError
			if errors.As(err, &exceeded) {
				stream.coordinator.observeCapacityRejection(ctx, stream.request.Operation, exceeded.budget, err)
			}
			return err
		}
		stream.gapFacts++
		stream.retained += retained
		stream.gaps.Items = append(stream.gaps.Items, snapshot)
		return nil
	})
	if err != nil {
		return execution.GapLoadResult{}, err
	}
	if err := execution.ValidateGapLoad(request, stream.gaps); err != nil {
		return execution.GapLoadResult{}, err
	}
	return stream.gaps, nil
}

func (stream *streamedExecution) retainGapMutation(ctx context.Context, mutation execution.PlanGapMutation) error {
	retained := 2 * retainedObjectBytes(mutation)
	delta := effectCounts{gaps: 1}
	if err := stream.coordinator.acquireEffects(delta, retained, stream, stream.reservationPhase("normal_gap")); err != nil {
		var exceeded *provisionalBudgetExceededError
		if errors.As(err, &exceeded) {
			stream.coordinator.observeCapacityRejection(ctx, stream.request.Operation, exceeded.budget, err)
		}
		return err
	}
	stream.effects.gaps++
	stream.retained += retained
	return nil
}

// observeGapProgress reports how far every marker this round read has got
// toward releasing, at the moment it was read.
//
// Reported off the load rather than anywhere later because the load is where
// the marker is: a round that goes on to fail for some other reason has still
// stood under the guard, and a guard holding a strategy down is a fact about
// that strategy whether or not the round that found it finished.
//
// Every scope of every snapshot, with no check on the snapshot's status. That
// is not an oversight and it is not the same as trusting an unreadable marker:
// the store gives a snapshot scopes only when it decoded one and
// ValidateGapLoad accepted it, and every other outcome -- missing, cleared to
// a tombstone, unreadable, refused -- arrives with none. So a Plan with no
// marker and a cleared one both report nothing, which is the right silence:
// there is no guard to be making progress. A status check here would read as a
// rule about which markers are reported while never being able to exclude one.
//
// What is not silent is a held scope on a round that read it, every round,
// whether or not the numbers moved.
//
// Reported under the state component, beside the gap load and the gap commit.
// It was evaluation, which is where the guard has its effect but not where it
// lives: a reader looking for what a guard is doing filters by component, and
// finds gap_loaded and gap_guard_committed under state with this one missing
// from between them.
func (stream *streamedExecution) observeGapProgress(ctx context.Context) {
	for _, snapshot := range stream.gaps.Items {
		stream.observeSnapshotProgress(ctx, snapshot)
	}
}

func (stream *streamedExecution) observeSnapshotProgress(ctx context.Context, snapshot execution.GapGuardSnapshot) {
	for _, scope := range snapshot.Scopes {
		name := "plan"
		if scope.Scope.HasLevel {
			name = strconv.FormatUint(uint64(scope.Scope.LevelID), 10)
		}
		stream.coordinator.emitObservation(ctx, observability.Observation{
			Component: observability.ComponentState, Stage: observability.StageGapGuardProgress,
			Operation: observability.Operation(stream.request.Operation),
			Direction: observability.DirectionInternal, Result: observability.ResultSuccess,
			Trace: observability.TraceFields{
				StrategyID: snapshot.Identity.Plan.StrategyID,
				BusinessID: snapshot.Identity.Plan.BusinessID,
			},
			GapProgress: &observability.GapProgressFacts{
				Scope: name, Status: string(scope.Status), Reason: string(scope.ReasonCode),
				Required: scope.RequiredFullSlots, Observed: scope.ObservedFullSlots,
				Progress: contract.GapScopeProgress(scope.ObservedFullSlots, scope.RequiredFullSlots),
			},
		})
	}
}
