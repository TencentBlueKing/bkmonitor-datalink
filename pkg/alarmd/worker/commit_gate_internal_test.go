// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// declinedContext is the context of an execution the watchdog has already
// let go as hung, before it began to commit.
func declinedContext(t *testing.T) context.Context {
	t.Helper()
	marker := &execution.StageMarker{}
	marker.Begin(execution.SlotStageEvaluate)
	marker.Extend(time.UnixMilli(1_000), time.UnixMilli(1_000))
	if _, _, declined := marker.Decline(time.UnixMilli(1_000).Add(time.Hour), time.Minute); !declined {
		t.Fatal("setup: the execution was not declined")
	}
	return execution.WithStageMarker(context.Background(), marker)
}

// countingNoDataStore counts the memory writes that reach the store.
type countingNoDataStore struct {
	*refusingNoDataStore
	applies int
}

func (store *countingNoDataStore) ApplyNoData(ctx context.Context, request execution.NoDataApplyRequest) (execution.NoDataApplyResult, error) {
	store.applies++
	return store.refusingNoDataStore.ApplyNoData(ctx, request)
}

// An execution the watchdog let go writes nothing of its commit, whichever
// write it reaches first: no output a next holder would send again, no State
// without the output it follows, no Progress past a Slot it did not finish.
// Its lease is being released; the next holder runs the Slot.
func TestADeclinedExecutionWritesNothingOfItsCommit(t *testing.T) {
	t.Run("output first", func(t *testing.T) {
		f := newPlanIsolationFixture(t, nil)
		_, err := f.coordinator.finalizePrepared(declinedContext(t), f.request, f.header, f.bindings, f.loaded, f.evaluated)
		if !errors.Is(err, execution.ErrExecutionDeclined) || len(f.ports.eventAttempts) != 0 ||
			len(f.base.stateApplied) != 0 || f.base.progressCommits != 0 {
			t.Fatalf("error %v, events %v, State %v, Progress %d; want declined and nothing written",
				err, f.ports.eventAttempts, f.base.stateApplied, f.base.progressCommits)
		}
	})
	t.Run("State first, nothing to send", func(t *testing.T) {
		f := newPlanIsolationFixture(t, nil)
		for plan := range f.evaluated.Plans {
			for state := range f.evaluated.Plans[plan].StateResults {
				f.evaluated.Plans[plan].StateResults[state].Events = nil
			}
		}
		_, err := f.coordinator.finalizePrepared(declinedContext(t), f.request, f.header, f.bindings, f.loaded, f.evaluated)
		if !errors.Is(err, execution.ErrExecutionDeclined) || len(f.ports.eventAttempts) != 0 ||
			len(f.base.stateApplied) != 0 || f.base.progressCommits != 0 {
			t.Fatalf("error %v, events %v, State %v, Progress %d; want declined and nothing written",
				err, f.ports.eventAttempts, f.base.stateApplied, f.base.progressCommits)
		}
	})
	t.Run("Progress alone", func(t *testing.T) {
		f := newPlanIsolationFixture(t, nil)
		_, err := f.coordinator.commitProgress(declinedContext(t), f.request, execution.SlotCompletion{}, execution.CompletionAttribution{})
		if !errors.Is(err, execution.ErrExecutionDeclined) || f.base.progressCommits != 0 {
			t.Fatalf("error %v, Progress %d; want declined and nothing written", err, f.base.progressCommits)
		}
	})
	t.Run("gap marks first", func(t *testing.T) {
		f := newPlanIsolationFixture(t, nil)
		marks := []execution.PlanGapMutation{{Identity: execution.PlanGapIdentity{Plan: f.healthyState.Plan, StateGeneration: f.healthyState.StateGeneration}}}
		err := f.coordinator.applyGap(declinedContext(t), f.request.Operation, f.request.Contract, marks,
			execution.GenerationRetention{Unknown: true}, GapSiteBeforeEvents)
		if !errors.Is(err, execution.ErrExecutionDeclined) || len(f.base.guards) != 0 {
			t.Fatalf("error %v, gap marks written %v; want declined and nothing written", err, f.base.guards)
		}
	})
	t.Run("no-data memory", func(t *testing.T) {
		store := &countingNoDataStore{refusingNoDataStore: &refusingNoDataStore{}}
		coordinator, _ := noDataRefusalFixture(store.refusingNoDataStore)
		coordinator.ports.NoData = store
		err := coordinator.applyNoDataMemory(declinedContext(t), execution.SlotExecutionRequest{Operation: execution.OperationNormal},
			refusedMemoryDue(t), []execution.PlanNoDataMutation{refusedMemoryMutation(t)})
		if !errors.Is(err, execution.ErrExecutionDeclined) || store.applies != 0 {
			t.Fatalf("error %v, memory writes %d; want declined and nothing written", err, store.applies)
		}
	})
}

// decliningEvents tries to decline the execution while its output is being
// written, as a watchdog tick landing then would.
type decliningEvents struct {
	*planFailurePorts
	marker  *execution.StageMarker
	claimed bool
}

func (events *decliningEvents) WriteBatch(ctx context.Context, batch []contract.TriggerEventV1) error {
	if _, _, claimed := events.marker.Decline(time.UnixMilli(1_000).Add(24*time.Hour), time.Minute); claimed {
		events.claimed = true
	}
	return events.planFailurePorts.WriteBatch(ctx, batch)
}

// The output write itself is inside the commit: a watchdog that finds the
// execution there, however far past its deadline, does not claim it, and the
// State and Progress after the output land.
func TestAnExecutionWritingItsOutputIsNotDeclinedAndItsStateLands(t *testing.T) {
	f := newPlanIsolationFixture(t, nil)
	// The Slot's due Plans and frozen boundaries, which the Progress commit
	// checks against its contract; the fixture's other cases stop before it.
	evaluatedAt := int64(f.request.Contract.Slot.EvaluationTime) * 1000
	f.request.EarliestQueryDeadlineUnixMilli = evaluatedAt + 55_000
	f.request.RecoveryUntilUnixMilli = evaluatedAt + 600_000
	f.request.KeepUntilUnixMilli = evaluatedAt + 3_600_000
	f.request.DuePlanTargets = execution.FrozenDuePlanTargets{DuePlanSetDigest: f.request.Contract.DuePlanSetDigest}
	for _, due := range f.header.DuePlans {
		f.request.DuePlanTargets.Plans = append(f.request.DuePlanTargets.Plans, execution.PlanKey{PlanIdentity: due.Identity})
	}
	marker := &execution.StageMarker{}
	marker.Begin(execution.SlotStageEvaluate)
	marker.Extend(time.UnixMilli(1_000), time.UnixMilli(1_000))
	events := &decliningEvents{planFailurePorts: f.ports, marker: marker}
	f.coordinator.ports.Events = events
	// The fixture's Progress store counts the commit and then refuses it;
	// what matters here is that the commit reached it.
	_, err := f.coordinator.finalizePrepared(execution.WithStageMarker(context.Background(), marker),
		f.request, f.header, f.bindings, f.loaded, f.evaluated)
	if errors.Is(err, execution.ErrExecutionDeclined) || events.claimed {
		t.Fatalf("finalize error %v, declined during the output write %t; want it to commit", err, events.claimed)
	}
	if len(f.ports.eventAttempts) != 2 || len(f.base.stateApplied) != 2 || f.base.progressCommits != 1 {
		t.Fatalf("events %v, State %v, Progress %d; want both Plans' output and State and the Progress commit",
			f.ports.eventAttempts, f.base.stateApplied, f.base.progressCommits)
	}
}
