// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package progress

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A pruned skip moves the cursor under the owner's fence, records the span
// as a gap, clears the anchors that pointed into the pruned past, and hands
// the cursor over to the normal begin and commit path: the first completion
// after the skip anchors continuity again.
func TestSkipPrunedRangeMovesTheCursorAndRestoresContinuity(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	current := execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull}
	fake := &controlFake{value: mustEncode(t, current)}
	store := mustStore(t, fake)
	ctx := context.Background()
	skip := execution.ProgressSkipPrunedRequest{Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120, ResumeAt: 600}
	if result, err := store.SkipPrunedRange(ctx, skip); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("SkipPrunedRange() = (%+v, %v), want committed", result, err)
	}
	loaded, err := store.LoadProgress(ctx, identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
	skipped := loaded.Progress
	wantGap := &execution.ProgressGapSummary{Kind: execution.CompletionGapSkipped,
		ReasonCode: execution.ReasonCode(contract.ReasonSchedulePruned), FirstSlot: 120, LastSlot: 120, Count: 1}
	if skipped.NextSlot != 600 || skipped.LastFullSlot != 0 || skipped.LastCompletionKind != execution.CompletionGapSkipped ||
		skipped.CurrentOrRecentGap == nil || *skipped.CurrentOrRecentGap != *wantGap || skipped.UnfinishedSlot != nil {
		t.Fatalf("progress after skip = %+v, want cursor 600 with the pruned gap %+v", skipped, wantGap)
	}
	// The cursor stands on its own: begin and commit at it do not try to
	// derive it from Slots that no longer exist.
	projection := progressProjectionAt(600)
	if result, err := store.BeginSlot(ctx, execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence, Projection: projection}); err != nil ||
		result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(600) after skip = (%+v, %v)", result, err)
	}
	commit := execution.ProgressCommitRequest{Identity: identity, OwnerFence: fence, ExpectedNextSlot: 600, Projection: projection,
		Completion: execution.SlotCompletion{Contract: projection.Contract, Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
			Result:  observability.ResultSuccess}}
	if result, err := store.CommitProgress(ctx, commit); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(600) after skip = (%+v, %v)", result, err)
	}
	loaded, err = store.LoadProgress(ctx, identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 660 || loaded.Progress.LastFullSlot != 600 {
		t.Fatalf("progress after the first completion = (%+v, %v), want cursor 660 anchored at 600", loaded.Progress, err)
	}
	if anchor, ok := loaded.Progress.ContinuityAnchor(); !ok || anchor != 600 {
		t.Fatalf("anchor after the first completion = (%d, %v), want 600", anchor, ok)
	}
}

// Nothing is skipped over a cursor that moved, a Slot in flight, an absent
// Progress or a stale owner; each is reported as the store's usual status.
func TestSkipPrunedRangeRefusesWhatItCannotProve(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	ctx := context.Background()
	skip := execution.ProgressSkipPrunedRequest{Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120, ResumeAt: 600}
	moved := execution.ScheduleProgress{Identity: identity, NextSlot: 180, LastFullSlot: 120}
	if result, err := mustStore(t, &controlFake{value: mustEncode(t, moved)}).SkipPrunedRange(ctx, skip); err != nil ||
		result.Status != execution.ProgressConflict {
		t.Fatalf("SkipPrunedRange(moved cursor) = (%+v, %v), want conflict", result, err)
	}
	projection := progressProjectionAt(120)
	inFlight := execution.ScheduleProgress{Identity: identity, NextSlot: 120, UnfinishedSlot: &projection}
	if result, err := mustStore(t, &controlFake{value: mustEncode(t, inFlight)}).SkipPrunedRange(ctx, skip); err != nil ||
		result.Status != execution.ProgressConflict {
		t.Fatalf("SkipPrunedRange(slot in flight) = (%+v, %v), want conflict", result, err)
	}
	if result, err := mustStore(t, &controlFake{missing: true}).SkipPrunedRange(ctx, skip); err != nil ||
		result.Status != execution.ProgressConflict {
		t.Fatalf("SkipPrunedRange(absent) = (%+v, %v), want conflict", result, err)
	}
	current := execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60}
	stale := &controlFake{value: mustEncode(t, current), status: ownership.FencedCASStaleOwner}
	if result, err := mustStore(t, stale).SkipPrunedRange(ctx, skip); err != nil || result.Status != execution.ProgressStaleOwner {
		t.Fatalf("SkipPrunedRange(stale owner) = (%+v, %v), want stale owner", result, err)
	}
	if _, err := mustStore(t, &controlFake{value: mustEncode(t, current)}).SkipPrunedRange(ctx,
		execution.ProgressSkipPrunedRequest{Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120, ResumeAt: 120}); err == nil {
		t.Fatal("a skip that does not move forward was accepted")
	}
}
