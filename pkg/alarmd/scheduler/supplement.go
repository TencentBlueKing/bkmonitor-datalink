// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

var (
	// ErrSupplementFlightBusy is a supplement that did not run because the
	// Query Group's own Slot was executing: a supplement never waits behind
	// one, and never makes one wait behind it longer than it runs.
	ErrSupplementFlightBusy = errors.New("alarmd scheduler: the Query Group's Slot is executing")
	// ErrSupplementContractExpired is a supplement of a Slot past its keep
	// boundary, or whose Segment's content is no longer held: its contract
	// cannot be frozen again as it was, and nothing may be written for it.
	ErrSupplementContractExpired = errors.New("alarmd scheduler: the Slot's contract is no longer kept")
	// ErrSupplementUnsupported is a Runner whose Slot source cannot freeze a
	// completed Slot again.
	ErrSupplementUnsupported = errors.New("alarmd scheduler: the Slot source does not freeze completed Slots")
	// ErrSupplementOvertaken is a supplement whose guard, asked once the
	// flight was held, said the Query Group has moved past the moment the
	// supplement was for (WithSupplementGuard): nothing was frozen or written.
	ErrSupplementOvertaken = errors.New("alarmd scheduler: the Query Group moved on before the supplement held its flight")
)

type supplementGuardKey struct{}

// WithSupplementGuard is ctx carrying a guard a supplement asks once it
// holds its Query Group's flight and before it freezes anything: false
// means the Query Group has moved past what the supplement was for, and it
// gives the flight back having written nothing (ErrSupplementOvertaken).
// Asked under the flight, the answer holds until the supplement ends: no
// Slot of the group can start while it is held.
func WithSupplementGuard(ctx context.Context, guard func() bool) context.Context {
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, supplementGuardKey{}, guard)
}

// SupplementGuardOf is the guard ctx carries, nil when none.
func SupplementGuardOf(ctx context.Context) func() bool {
	guard, _ := ctx.Value(supplementGuardKey{}).(func() bool)
	return guard
}

// SupplementSlotSource freezes a completed Slot again, for a supplement.
type SupplementSlotSource interface {
	FreezeSupplement(ctx context.Context, at execution.EvaluationTime, readHoldMillis int64) (FrozenSlot, error)
}

// ObservedContractFreezer is the catalog read that freezes a Slot's
// contract from its Segment's retained content alone. A catalog that offers
// it is asked with it for a supplement: a completed Slot whose content is
// gone is past being supplemented, and rebuilding the content from the
// published catalog for one past Slot is a fleet-wide read.
type ObservedContractFreezer interface {
	FreezeObservedSlotContract(context.Context, execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error)
}

// FreezeSupplement freezes the Slot at at again as Next froze it: the
// Schedule Segment that covers it, its due Plans, the contract the catalog
// freezes for them, the boundaries every Slot carries, and the content and
// fence it runs under now. The Slot need not be the next one - it is a
// completed one - and it is not classified for recovery: a supplement is
// not a replay of it.
//
// readHoldMillis is the read hold the Slot was frozen with when it ran: the
// contract is frozen with it again, so it is the contract the Slot ran under
// and its keep-until the one that Slot had.
func (source *ProductionSlotSource) FreezeSupplement(ctx context.Context, at execution.EvaluationTime, readHoldMillis int64) (FrozenSlot, error) {
	if source == nil || at <= 0 {
		return FrozenSlot{}, errors.New("alarmd scheduler: a supplement freezes one Slot of its Query Group")
	}
	now := source.now()
	assignment, fence, err := source.currentOwnership(ctx, now)
	if err != nil {
		return FrozenSlot{}, err
	}
	schedule, err := source.catalog.ReadFrozenSchedule(ctx, source.queryGroup, at)
	if err != nil {
		return FrozenSlot{}, supplementFreezeError(err)
	}
	if err := source.validateSchedule(schedule, at); err != nil {
		return FrozenSlot{}, err
	}
	duePlans := schedule.DuePlanRefs(at)
	if len(duePlans) == 0 {
		return FrozenSlot{}, ErrProgressOffSchedule
	}
	request := execution.FreezeSlotContractRequest{
		QueryGroup: source.queryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: at, DuePlans: duePlans,
		ReadHoldMillis: readHoldMillis,
	}
	freeze := source.catalog.FreezeSlotContract
	if observed, ok := source.catalog.(ObservedContractFreezer); ok {
		freeze = observed.FreezeObservedSlotContract
	}
	fact, err := freeze(ctx, request)
	if err != nil {
		return FrozenSlot{}, supplementFreezeError(err)
	}
	if err := fact.Validate(request); err != nil {
		return FrozenSlot{}, fmt.Errorf("%w: %v", ErrSlotContractDrift, err)
	}
	if fact.Contract.SnapshotRevision != schedule.Segment.Publication.SnapshotRevision ||
		fact.Contract.QueryRevision != schedule.Segment.QueryRevision {
		return FrozenSlot{}, ErrSlotContractDrift
	}
	targets, queryDeadline, err := frozenSlotExecutionFacts(fact)
	if err != nil {
		return FrozenSlot{}, err
	}
	recoveryUntil, keepUntil, err := source.recoveryBoundaries(queryDeadline)
	if err != nil {
		return FrozenSlot{}, err
	}
	if now.UnixMilli() >= keepUntil {
		return FrozenSlot{}, ErrSupplementContractExpired
	}
	slot := FrozenSlot{
		Contract:                       fact.Contract,
		DuePlanTargets:                 targets.Clone(),
		EarliestQueryDeadlineUnixMilli: queryDeadline,
		RecoveryUntilUnixMilli:         recoveryUntil,
		KeepUntilUnixMilli:             keepUntil,
		Dispatch: SlotDispatchContext{Operation: execution.OperationSupplement, OwnerFence: fence,
			AssignmentGeneration: assignment.AssignmentGeneration, ContentScope: declaredContentScope(schedule.Segment)},
		ExpectedNextSlot: at,
	}
	if err := slot.Validate(source.queryGroup); err != nil {
		return FrozenSlot{}, err
	}
	return slot, nil
}

// supplementFreezeError names a Slot whose Segment or content is no longer
// kept as expired; every other failure is returned as it came.
func supplementFreezeError(err error) error {
	if errors.Is(err, controlplane.ErrScheduleUnavailable) || errors.Is(err, controlplane.ErrCatalogObjectUnavailable) ||
		errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		return fmt.Errorf("%w: %v", ErrSupplementContractExpired, err)
	}
	return err
}

// Supplement runs a supplement of the completed Slot at at over the series
// of scope, under the Query Group's flight and its current fence.
//
// The flight is tried, never waited for: a Slot of the Query Group that is
// executing makes this ErrSupplementFlightBusy at once, and the caller
// decides whether to try again. Held, it keeps the Query Group's next Slot
// from running while the supplement writes the same series - the one race
// a supplement could lose to - for as long as the supplement runs. The
// read it runs on is the caller's, carried on ctx.
func (runner *Runner) Supplement(
	ctx context.Context,
	at execution.EvaluationTime,
	readHoldMillis int64,
	scope execution.SupplementScope,
) (execution.SupplementFacts, error) {
	if runner == nil {
		return execution.SupplementFacts{}, errors.New("alarmd scheduler: initialized Runner is required")
	}
	source, supported := runner.source.(SupplementSlotSource)
	if !supported {
		return execution.SupplementFacts{}, ErrSupplementUnsupported
	}
	release, acquired := runner.flights.TrySupplement(runner.queryGroup)
	if !acquired {
		return execution.SupplementFacts{}, ErrSupplementFlightBusy
	}
	defer release()
	if guard := SupplementGuardOf(ctx); guard != nil && !guard() {
		return execution.SupplementFacts{}, ErrSupplementOvertaken
	}
	slot, err := source.FreezeSupplement(ctx, at, readHoldMillis)
	if err != nil {
		return execution.SupplementFacts{}, err
	}
	fence, err := runner.session.ValidateCurrent(ctx, runner.now())
	if err != nil {
		return execution.SupplementFacts{}, err
	}
	if fence != slot.Dispatch.OwnerFence {
		return execution.SupplementFacts{}, ErrSlotOwnershipChanged
	}
	request := execution.SlotExecutionRequest{
		Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli,
		RecoveryUntilUnixMilli:         slot.RecoveryUntilUnixMilli,
		KeepUntilUnixMilli:             slot.KeepUntilUnixMilli,
		Operation:                      execution.OperationSupplement, AttemptNo: 1, OwnerFence: fence,
		ExpectedNextSlot: slot.ExpectedNextSlot, ContentScope: slot.Dispatch.ContentScope, Supplement: &scope,
	}
	runner.session.NoteContentScope(slot.Dispatch.ContentScope)
	// Not on the Runner's stage marker: a supplement is not a scheduled
	// execution the watchdog watches, and its frozen Slot's deadline - long
	// past, since it supplements a Slot already completed - is not a deadline
	// it runs under. The marker stays clear while it holds the flight.
	result, err := runner.executor.Execute(execution.ContextWithLeaseAuthority(ctx, runner.session), request)
	if err != nil {
		return execution.SupplementFacts{}, err
	}
	if result.Supplement == nil {
		return execution.SupplementFacts{}, errors.New("alarmd scheduler: a supplement returned no facts")
	}
	return *result.Supplement, nil
}
