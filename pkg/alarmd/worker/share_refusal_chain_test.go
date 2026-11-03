// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

type chainSession struct{ fence execution.OwnerFence }

func (session chainSession) ValidateCurrent(context.Context, time.Time) (execution.OwnerFence, error) {
	return session.fence, nil
}
func (chainSession) Deadline() time.Time     { return time.Time{} }
func (chainSession) NoteContentScope(string) {}
func (session chainSession) ValidateCurrentWithAssignment(context.Context, time.Time) (execution.OwnerFence, ownership.AssignmentRecord, error) {
	return session.fence, ownership.AssignmentRecord{}, nil
}

// chainSource hands out its Slots in order and moves on when the
// coordinator commits Progress for the current one.
type chainSource struct {
	slots    []scheduler.FrozenSlot
	progress func() int
}

func (source *chainSource) Next(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, scheduler.SlotDueFacts, error) {
	index := source.progress()
	if index >= len(source.slots) {
		return scheduler.FrozenSlot{}, false, scheduler.SlotDueFacts{}, nil
	}
	return source.slots[index], true, scheduler.SlotDueFacts{}, nil
}

func chainSlot(evaluation execution.EvaluationTime, snapshot execution.SnapshotRevision) scheduler.FrozenSlot {
	request := slotRequest(execution.OperationNormal)
	request.Contract.Slot.EvaluationTime = evaluation
	request.Contract.SnapshotRevision = snapshot
	request.ExpectedNextSlot = evaluation
	request.EarliestQueryDeadlineUnixMilli = int64(evaluation)*1000 + 1_000
	request.RecoveryUntilUnixMilli = int64(evaluation)*1000 + 601_000
	request.KeepUntilUnixMilli = int64(evaluation)*1000 + 677_000
	return scheduler.FrozenSlot{Contract: request.Contract, DuePlanTargets: request.DuePlanTargets,
		Dispatch:                       scheduler.SlotDispatchContext{Operation: execution.OperationNormal, OwnerFence: request.OwnerFence, AssignmentGeneration: 1},
		EarliestQueryDeadlineUnixMilli: request.EarliestQueryDeadlineUnixMilli, RecoveryUntilUnixMilli: request.RecoveryUntilUnixMilli,
		KeepUntilUnixMilli: request.KeepUntilUnixMilli, ExpectedNextSlot: evaluation}
}

// The whole chain, with the real coordinator: its retained pool is sized so
// one Slot fits the pool and not its share. The Runner sends the Slot to the
// provider twice, is refused for the share both times, and the next round
// finalizes it without a query as GAP_SKIPPED, cause RESOURCE_SHARE_EXCEEDED.
// The group's next Slot is sent once, refused once, and finalized the same
// way. The recording provider's answer is bound to one evaluation time, so
// the next Slot here is a second frozen contract of the group at that time (a
// re-frozen Snapshot); to the Runner it is another Slot all the same - its
// share key is the group, the phase and the share, not the evaluation time.
func TestTheShareRefusalChainGivesTheSlotUpAndTheNextAfterOneRead(t *testing.T) {
	calibration := newFixtureWithBudget(t, worker.ProvisionalBudget{
		MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10,
	})
	measured, err := calibration.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !measured.Completed || measured.Usage.RetainedBytes == 0 {
		t.Fatalf("calibration Execute() = %+v, %v", measured, err)
	}
	// The pool holds the whole Slot; its share - half - does not.
	fixture := newFixtureWithBudget(t, worker.ProvisionalBudget{
		MaxSeries: 100, MaxRetainedBytes: measured.Usage.RetainedBytes, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10,
	})
	first := slotRequest(execution.OperationNormal).Contract.Slot.EvaluationTime
	source := &chainSource{slots: []scheduler.FrozenSlot{chainSlot(first, "snapshot-v1"), chainSlot(first, "snapshot-v2")},
		progress: func() int { return countSteps(*fixture.ports.trace, "progress_commit") }}
	now := func() time.Time { return time.UnixMilli(int64(first)*1000 + 500) }
	runner, err := scheduler.NewRunner("query-group", chainSession{fence: source.slots[0].Dispatch.OwnerFence}, source,
		fixture.coordinator, scheduler.NewFlightCoordinator(), now)
	if err != nil {
		t.Fatal(err)
	}
	count := func(name string) int {
		total := 0
		for _, step := range *fixture.ports.trace {
			if step == name {
				total++
			}
		}
		return total
	}
	sends := func() int { return count("query") }
	round := func(step string) (execution.SlotExecutionResult, error) {
		t.Helper()
		result, attempted, err := runner.RunOne(context.Background())
		if !attempted {
			t.Fatalf("%s: not attempted", step)
		}
		return result, err
	}
	refused := func(step string) {
		t.Helper()
		if _, err := round(step); err == nil {
			t.Fatalf("%s: not refused", step)
		} else if _, deterministic := execution.ShareRefusalOf(err); !deterministic {
			t.Fatalf("%s: refused, but not as a deterministic share refusal: %v", step, err)
		}
	}
	gap := func(step string) {
		t.Helper()
		result, err := round(step)
		if err != nil || !result.Completed || result.CompletionKind != execution.CompletionGapSkipped ||
			result.QueryFreeCause != execution.CauseResourceShareExceeded {
			t.Fatalf("%s: result=%+v err=%v, want GAP_SKIPPED for the share", step, result, err)
		}
	}

	refused("Slot 1, first send")
	refused("Slot 1, second send")
	if got := sends(); got != 2 {
		t.Fatalf("provider sends for Slot 1 = %d, want 2", got)
	}
	gap("Slot 1, finalization")
	if got := sends(); got != 2 {
		t.Fatalf("the finalization sent a query: sends = %d", got)
	}
	refused("Slot 2, first send")
	gap("Slot 2, finalization")
	if got := sends(); got != 3 {
		t.Fatalf("provider sends after Slot 2 = %d, want 3: one read for the group's next Slot", got)
	}
	if commits := count("progress_commit"); commits != 2 {
		t.Fatalf("progress commits = %d, want both Slots finalized", commits)
	}
}

func countSteps(trace []string, name string) int {
	total := 0
	for _, step := range trace {
		if step == name {
			total++
		}
	}
	return total
}
