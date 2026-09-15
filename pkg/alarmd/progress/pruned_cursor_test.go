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
		ReasonCode: execution.ReasonCode(contract.ReasonSchedulePruned), FirstSlot: 120, LastSlot: 120,
		// Uncounted rather than one. Count means Slots in every other gap, so a
		// 1 here was an unknown number of never-evaluated Slots reported as the
		// smallest non-zero amount of them -- and read as that by anything
		// adding these up. Where the cursor landed is carried instead, so the
		// extent is stated without the population being invented.
		Uncounted: true, ResumedAt: 600}
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

// Nothing is skipped over a cursor that moved, a range in flight, an
// absent Progress, a write that lost the compare-and-set or a stale owner;
// each conflict names the one fact that refused it, and a stale owner is
// the store's usual status.
func TestSkipPrunedRangeRefusesWhatItCannotProve(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	ctx := context.Background()
	skip := execution.ProgressSkipPrunedRequest{Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120, ResumeAt: 600}
	refused := func(name string, fake *controlFake, want execution.ProgressSkipRefusal, wantSlot execution.EvaluationTime) {
		t.Helper()
		result, err := mustStore(t, fake).SkipPrunedRange(ctx, skip)
		if err != nil || result.Status != execution.ProgressConflict || result.Refusal != want || result.InFlightSlot != wantSlot {
			t.Fatalf("SkipPrunedRange(%s) = (%+v, %v), want conflict refused by %s naming Slot %d", name, result, err, want, wantSlot)
		}
	}
	moved := execution.ScheduleProgress{Identity: identity, NextSlot: 180, LastFullSlot: 120}
	refused("moved cursor", &controlFake{value: mustEncode(t, moved)}, execution.SkipRefusalCursorMoved, 0)
	pending := rangeFixture(t)
	inRange := execution.ScheduleProgress{Identity: identity, NextSlot: 60, UnfinishedRange: &pending}
	refused("range in flight", &controlFake{value: mustEncode(t, inRange)}, execution.SkipRefusalRangeInFlight, 0)
	refused("absent", &controlFake{missing: true}, execution.SkipRefusalProgressMissing, 0)
	current := execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60}
	refused("lost compare-and-set", &controlFake{value: mustEncode(t, current), status: ownership.FencedCASConflict}, execution.SkipRefusalCASConflict, 0)
	// A lost compare-and-set still names the Slot the skip would have
	// discarded, so the report of a conflict that keeps happening is whole.
	projection := progressProjectionAt(120)
	inFlight := execution.ScheduleProgress{Identity: identity, NextSlot: 120, UnfinishedSlot: &projection}
	refused("lost compare-and-set with a slot in flight", &controlFake{value: mustEncode(t, inFlight), status: ownership.FencedCASConflict}, execution.SkipRefusalCASConflict, 120)
	stale := &controlFake{value: mustEncode(t, current), status: ownership.FencedCASStaleOwner}
	if result, err := mustStore(t, stale).SkipPrunedRange(ctx, skip); err != nil || result.Status != execution.ProgressStaleOwner || result.Refusal != "" {
		t.Fatalf("SkipPrunedRange(stale owner) = (%+v, %v), want stale owner without a refusal", result, err)
	}
	if _, err := mustStore(t, &controlFake{value: mustEncode(t, current)}).SkipPrunedRange(ctx,
		execution.ProgressSkipPrunedRequest{Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120, ResumeAt: 120}); err == nil {
		t.Fatal("a skip that does not move forward was accepted")
	}
}

// A Slot in flight that lies inside the pruned span goes with the skip: its
// Segment is gone, so nothing can finish it and nothing can rebuild it. The
// skip reports which Slot it discarded, and the Progress it leaves carries
// no projection.
func TestSkipPrunedRangeDiscardsASlotInFlightInsideThePrunedSpan(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	ctx := context.Background()
	projection := progressProjectionAt(120)
	current := execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull, UnfinishedSlot: &projection}
	store := mustStore(t, &controlFake{value: mustEncode(t, current)})
	skip := execution.ProgressSkipPrunedRequest{Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120, ResumeAt: 600}
	result, err := store.SkipPrunedRange(ctx, skip)
	if err != nil || result.Status != execution.ProgressCommitted || result.Refusal != "" || result.InFlightSlot != 120 {
		t.Fatalf("SkipPrunedRange() = (%+v, %v), want committed naming the discarded Slot 120", result, err)
	}
	loaded, err := store.LoadProgress(ctx, identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
	wantGap := &execution.ProgressGapSummary{Kind: execution.CompletionGapSkipped,
		ReasonCode: execution.ReasonCode(contract.ReasonSchedulePruned), FirstSlot: 120, LastSlot: 120,
		// Uncounted rather than one. Count means Slots in every other gap, so a
		// 1 here was an unknown number of never-evaluated Slots reported as the
		// smallest non-zero amount of them -- and read as that by anything
		// adding these up. Where the cursor landed is carried instead, so the
		// extent is stated without the population being invented.
		Uncounted: true, ResumedAt: 600}
	if got := loaded.Progress; got.NextSlot != 600 || got.UnfinishedSlot != nil || got.UnfinishedRange != nil || got.LastFullSlot != 0 ||
		got.CurrentOrRecentGap == nil || *got.CurrentOrRecentGap != *wantGap {
		t.Fatalf("progress after skip = %+v, want cursor 600, nothing in flight and the pruned gap %+v", got, wantGap)
	}
}
