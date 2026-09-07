// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package progress

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type controlFake struct {
	value             []byte
	missing           bool
	status            ownership.FencedCASStatus
	group             execution.QueryGroupIdentity
	name              string
	temporaryCASReads int
}

func TestBeginSlotPersistsIdempotentlyAndCompletionClearsProjection(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	projection := progressProjection()
	begin := execution.ProgressBeginRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: "q"},
		OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		Projection: projection,
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := store.BeginSlot(context.Background(), begin)
		if err != nil || result.Status != execution.ProgressCommitted {
			t.Fatalf("BeginSlot(attempt %d) = (%+v, %v)", attempt, result, err)
		}
	}
	loaded, err := store.LoadProgress(context.Background(), begin.Identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.UnfinishedSlot == nil ||
		!loaded.Progress.UnfinishedSlot.Equal(projection) {
		t.Fatalf("LoadProgress(after BeginSlot) = (%+v, %v)", loaded, err)
	}
	commit := execution.ProgressCommitRequest{
		Identity: begin.Identity, OwnerFence: begin.OwnerFence, ExpectedNextSlot: 60, Projection: projection,
		Completion: execution.SlotCompletion{Contract: progressContract(), Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess},
	}
	result, err := store.CommitProgress(context.Background(), commit)
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v)", result, err)
	}
	loaded, err = store.LoadProgress(context.Background(), begin.Identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 120 || loaded.Progress.UnfinishedSlot != nil {
		t.Fatalf("LoadProgress(after completion) = (%+v, %v)", loaded, err)
	}
}

func TestBeginSlotRejectsDifferentProjectionAndHonorsFenceOnIdempotentRetry(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	request := execution.ProgressBeginRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: "q"},
		OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		Projection: progressProjection(),
	}
	if result, err := store.BeginSlot(context.Background(), request); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(first) = (%+v, %v)", result, err)
	}
	fake.status = ownership.FencedCASStaleOwner
	if result, err := store.BeginSlot(context.Background(), request); err != nil || result.Status != execution.ProgressStaleOwner {
		t.Fatalf("BeginSlot(idempotent stale owner) = (%+v, %v)", result, err)
	}
	fake.status = ownership.FencedCASApplied
	request.Projection.DuePlanTargets.Plans[0].StrategyID = "changed"
	// The latest fenced attempt owns the unfinished Slot: its projection
	// replaces the persisted one instead of being rejected.
	if result, err := store.BeginSlot(context.Background(), request); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(different projection) = (%+v, %v), want the superseding commit", result, err)
	}
	loaded, err := store.LoadProgress(context.Background(), request.Identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.UnfinishedSlot == nil || !loaded.Progress.UnfinishedSlot.Equal(request.Projection) {
		t.Fatalf("LoadProgress(after superseding projection) = (%+v, %v)", loaded, err)
	}
}

func TestBeginSlotAcceptsTimelineProvenSuccessorAndSnapshotUnavailableAdvancesToRetiredBoundary(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	current := execution.ScheduleProgress{
		Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
	}
	fake := &controlFake{value: mustEncode(t, current)}
	store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 90, 90: 150})
	projection := progressProjectionAt(90)

	begin, err := store.BeginSlot(context.Background(), execution.ProgressBeginRequest{
		Identity: identity, OwnerFence: fence, Projection: projection,
	})
	if err != nil || begin.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(timeline successor) = (%+v, %v), want committed", begin, err)
	}
	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 90 ||
		loaded.Progress.LastFullSlot != 60 || loaded.Progress.LastCompletionKind != execution.CompletionFull ||
		loaded.Progress.UnfinishedSlot == nil || !loaded.Progress.UnfinishedSlot.Equal(projection) {
		t.Fatalf("LoadProgress(after successor BeginSlot) = (%+v, %v)", loaded, err)
	}

	commit, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 90, Projection: projection,
		Completion: execution.SlotCompletion{
			Contract: projection.Contract, Kind: execution.CompletionSnapshotUnavailable,
			Result:     observability.ResultDegraded,
			ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable),
		},
	})
	if err != nil || commit.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(snapshot unavailable) = (%+v, %v)", commit, err)
	}
	loaded, err = store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 150 ||
		loaded.Progress.LastFullSlot != 60 || loaded.Progress.UnfinishedSlot != nil ||
		loaded.Progress.CurrentOrRecentGap == nil ||
		loaded.Progress.CurrentOrRecentGap.Kind != execution.CompletionSnapshotUnavailable ||
		loaded.Progress.CurrentOrRecentGap.FirstSlot != 90 || loaded.Progress.CurrentOrRecentGap.LastSlot != 90 {
		t.Fatalf("LoadProgress(after query-free retirement) = (%+v, %v)", loaded, err)
	}
}

func TestBeginSlotRejectsUnprovenSuccessorWithoutChangingProgress(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	current := execution.ScheduleProgress{
		Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
	}
	raw := mustEncode(t, current)
	fake := &controlFake{value: raw}
	store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 90})

	result, err := store.BeginSlot(context.Background(), execution.ProgressBeginRequest{
		Identity:   identity,
		OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		Projection: progressProjectionAt(180),
	})
	if err != nil || result.Status != execution.ProgressConflict {
		t.Fatalf("BeginSlot(unproven successor) = (%+v, %v), want conflict", result, err)
	}
	if string(fake.value) != string(raw) {
		t.Fatal("unproven successor changed persisted Progress")
	}
}

func TestBeginSlotDoesNotReplaceExistingUnfinishedProjectionWithTimelineSuccessor(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	persistedProjection := progressProjectionAt(120)
	current := execution.ScheduleProgress{
		Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
		UnfinishedSlot: &persistedProjection,
	}
	raw := mustEncode(t, current)
	fake := &controlFake{value: raw}
	store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 90})

	result, err := store.BeginSlot(context.Background(), execution.ProgressBeginRequest{
		Identity:   identity,
		OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		Projection: progressProjectionAt(90),
	})
	if err != nil || result.Status != execution.ProgressConflict {
		t.Fatalf("BeginSlot(with unfinished projection) = (%+v, %v), want conflict", result, err)
	}
	if string(fake.value) != string(raw) {
		t.Fatal("successor BeginSlot replaced an existing unfinished projection")
	}
}

// A later Schedule Segment that owns the unfinished Slot's time supersedes the
// persisted projection: the same EvaluationTime is simply re-projected.
func TestBeginSlotSupersedesUnfinishedSlotOnNewerSegmentAtSameTime(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	persisted := progressProjectionAt(120)
	current := execution.ScheduleProgress{
		Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
		UnfinishedSlot: &persisted,
	}
	fake := &controlFake{value: mustEncode(t, current)}
	store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 120, 120: 180})
	superseding := newerSegmentProjectionAt(120)

	priorUnfinished := -1
	ctx := execution.WithSlotCoverageCapture(context.Background(), &execution.SlotCoverageCapture{
		BeginCommitted: func(prior bool) {
			priorUnfinished = 0
			if prior {
				priorUnfinished = 1
			}
		},
	})
	result, err := store.BeginSlot(ctx, execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence, Projection: superseding})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(superseding same Slot) = (%+v, %v), want committed", result, err)
	}
	if priorUnfinished != 1 {
		t.Fatalf("BeginCommitted prior unfinished = %d, want the replaced projection reported as prior", priorUnfinished)
	}
	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 120 || loaded.Progress.LastFullSlot != 60 ||
		loaded.Progress.LastCompletionKind != execution.CompletionFull || loaded.Progress.CurrentOrRecentGap != nil ||
		loaded.Progress.UnfinishedSlot == nil || !loaded.Progress.UnfinishedSlot.Equal(superseding) {
		t.Fatalf("LoadProgress(after supersede) = (%+v, %v)", loaded, err)
	}
}

// A later Segment that moves the cursor abandons the unfinished Slot as one
// GAP_SKIPPED Slot and accepts only the successor proven from the folded facts.
func TestBeginSlotSupersedesUnfinishedSlotOnNewerSegmentAtLaterTime(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	gapSkipped := execution.ReasonCode(contract.ReasonGapSkipped)
	for _, test := range []struct {
		name      string
		current   execution.ScheduleProgress
		requested execution.EvaluationTime
		wantGap   execution.ProgressGapSummary
		conflict  bool
	}{
		{
			name:      "completed facts before the fold",
			current:   execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull},
			requested: 180,
			wantGap:   execution.ProgressGapSummary{Kind: execution.CompletionGapSkipped, ReasonCode: gapSkipped, FirstSlot: 120, LastSlot: 120, Count: 1},
		},
		{
			name:      "no completed facts before the fold",
			current:   execution.ScheduleProgress{Identity: identity, NextSlot: 120},
			requested: 180,
			wantGap:   execution.ProgressGapSummary{Kind: execution.CompletionGapSkipped, ReasonCode: gapSkipped, FirstSlot: 120, LastSlot: 120, Count: 1},
		},
		{
			name: "adjacent skipped gap extended",
			current: execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastCompletionKind: execution.CompletionGapSkipped,
				CurrentOrRecentGap: &execution.ProgressGapSummary{Kind: execution.CompletionGapSkipped, ReasonCode: gapSkipped, FirstSlot: 60, LastSlot: 90, Count: 2}},
			requested: 180,
			wantGap:   execution.ProgressGapSummary{Kind: execution.CompletionGapSkipped, ReasonCode: gapSkipped, FirstSlot: 60, LastSlot: 120, Count: 3},
		},
		{
			name:      "requested Slot is not the proven successor",
			current:   execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull},
			requested: 150,
			conflict:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			persisted := progressProjectionAt(120)
			current := test.current
			current.UnfinishedSlot = &persisted
			raw := mustEncode(t, current)
			fake := &controlFake{value: append([]byte(nil), raw...)}
			store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 120, 90: 120, 120: 180})
			superseding := newerSegmentProjectionAt(test.requested)
			priorUnfinished := -1
			ctx := execution.WithSlotCoverageCapture(context.Background(), &execution.SlotCoverageCapture{
				BeginCommitted: func(prior bool) {
					priorUnfinished = 0
					if prior {
						priorUnfinished = 1
					}
				},
			})
			result, err := store.BeginSlot(ctx, execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence, Projection: superseding})
			if test.conflict {
				if err != nil || result.Status != execution.ProgressConflict || string(fake.value) != string(raw) {
					t.Fatalf("BeginSlot(unproven successor) = (%+v, %v) changed=%t, want conflict without a write", result, err, string(fake.value) != string(raw))
				}
				return
			}
			if err != nil || result.Status != execution.ProgressCommitted || priorUnfinished != 0 {
				t.Fatalf("BeginSlot(superseding later Slot) = (%+v, %v) prior=%d, want committed as a fresh Slot", result, err, priorUnfinished)
			}
			loaded, err := store.LoadProgress(context.Background(), identity)
			if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != test.requested ||
				loaded.Progress.LastFullSlot != test.current.LastFullSlot ||
				loaded.Progress.LastCompletionKind != execution.CompletionGapSkipped ||
				loaded.Progress.CurrentOrRecentGap == nil || *loaded.Progress.CurrentOrRecentGap != test.wantGap ||
				loaded.Progress.UnfinishedSlot == nil || !loaded.Progress.UnfinishedSlot.Equal(superseding) {
				t.Fatalf("LoadProgress(after abandon) = (%+v, gap=%+v, %v), want NextSlot=%d gap=%+v", loaded.Progress, loaded.Progress.CurrentOrRecentGap, err, test.requested, test.wantGap)
			}
		})
	}
}

// A later Segment whose grid reaches an earlier Slot first (old 60s grid with
// an unfinished Slot at T, new 30s grid starting before T) drops the
// projection without a gap: T has not been skipped and is begun later as a
// normal Slot.
func TestBeginSlotSupersedesUnfinishedSlotOnNewerSegmentAtEarlierTime(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	const abandoned, newSegmentStart execution.EvaluationTime = 120, 89
	persisted := progressProjectionAt(abandoned)
	current := execution.ScheduleProgress{
		Identity: identity, NextSlot: abandoned, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
		UnfinishedSlot: &persisted,
	}
	raw := mustEncode(t, current)

	t.Run("successor not proven from the pre-fold facts", func(t *testing.T) {
		fake := &controlFake{value: append([]byte(nil), raw...)}
		store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 100})
		result, err := store.BeginSlot(context.Background(), execution.ProgressBeginRequest{
			Identity: identity, OwnerFence: fence, Projection: segmentProjectionAt(abandoned-30, newSegmentStart),
		})
		if err != nil || result.Status != execution.ProgressConflict || string(fake.value) != string(raw) {
			t.Fatalf("BeginSlot(unproven earlier successor) = (%+v, %v) changed=%t, want conflict without a write", result, err, string(fake.value) != string(raw))
		}
	})

	fake := &controlFake{value: append([]byte(nil), raw...)}
	store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: abandoned - 30, abandoned - 30: abandoned, abandoned: abandoned + 30})
	earlier := segmentProjectionAt(abandoned-30, newSegmentStart)
	priorUnfinished := -1
	ctx := execution.WithSlotCoverageCapture(context.Background(), &execution.SlotCoverageCapture{
		BeginCommitted: func(prior bool) {
			priorUnfinished = 0
			if prior {
				priorUnfinished = 1
			}
		},
	})
	result, err := store.BeginSlot(ctx, execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence, Projection: earlier})
	if err != nil || result.Status != execution.ProgressCommitted || priorUnfinished != 0 {
		t.Fatalf("BeginSlot(earlier successor) = (%+v, %v) prior=%d, want committed as a fresh Slot", result, err, priorUnfinished)
	}
	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != abandoned-30 || loaded.Progress.LastFullSlot != 60 ||
		loaded.Progress.LastCompletionKind != execution.CompletionFull || loaded.Progress.CurrentOrRecentGap != nil ||
		loaded.Progress.UnfinishedSlot == nil || !loaded.Progress.UnfinishedSlot.Equal(earlier) {
		t.Fatalf("LoadProgress(after earlier successor) = (%+v, %v), want NextSlot=%d without a gap", loaded.Progress, err, abandoned-30)
	}

	commit, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: abandoned - 30, Projection: earlier,
		Completion: execution.SlotCompletion{Contract: earlier.Contract, Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess},
	})
	if err != nil || commit.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(earlier successor) = (%+v, %v)", commit, err)
	}
	reached := segmentProjectionAt(abandoned, newSegmentStart)
	result, err = store.BeginSlot(context.Background(), execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence, Projection: reached})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(abandoned time reached on the new grid) = (%+v, %v), want committed", result, err)
	}
	loaded, err = store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != abandoned || loaded.Progress.LastFullSlot != abandoned-30 ||
		loaded.Progress.CurrentOrRecentGap != nil || loaded.Progress.UnfinishedSlot == nil || !loaded.Progress.UnfinishedSlot.Equal(reached) {
		t.Fatalf("LoadProgress(abandoned time as a normal Slot) = (%+v, %v), want NextSlot=%d LastFullSlot=%d without a gap", loaded.Progress, err, abandoned, abandoned-30)
	}
}

// The latest fenced attempt owns an unfinished Slot: a projection for the same
// Slot on the same Segment replaces the persisted one and the differing fields
// are observed. A view through an older Segment conflicts without a write, and
// the equal projection stays the idempotent re-begin without an observation.
func TestBeginSlotSupersedesDifferingProjectionOnSameSegment(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	for _, test := range []struct {
		name       string
		change     func(*execution.UnfinishedSlotProjection)
		wantFields string
	}{
		{name: "different deadline", change: func(p *execution.UnfinishedSlotProjection) { p.EarliestQueryDeadlineUnixMilli += 5_000 }, wantFields: "deadline"},
		{name: "different keep until", change: func(p *execution.UnfinishedSlotProjection) { p.KeepUntilUnixMilli += 90_000 }, wantFields: "keep_until"},
		{name: "pruned targets", change: func(p *execution.UnfinishedSlotProjection) { p.DuePlanTargets.Plans = p.DuePlanTargets.Plans[:1] }, wantFields: "due_plan_targets"},
		{name: "different contract", change: func(p *execution.UnfinishedSlotProjection) { p.Contract.SnapshotRevision = "s2" }, wantFields: "contract"},
		{name: "several fields", change: func(p *execution.UnfinishedSlotProjection) {
			p.Contract.QueryRevision = "query2"
			p.EarliestQueryDeadlineUnixMilli += 1_000
			p.KeepUntilUnixMilli += 1_000
		}, wantFields: "contract,deadline,keep_until"},
	} {
		t.Run(test.name, func(t *testing.T) {
			persisted := twoPlanProjectionAt(120)
			current := execution.ScheduleProgress{
				Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
				UnfinishedSlot: &persisted,
			}
			fake := &controlFake{value: mustEncode(t, current)}
			var observations []observability.Observation
			store := mustObservedStore(t, fake, mappedSlotResolver{60: 120, 120: 180}, observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observations = append(observations, observation)
			}))
			superseding := twoPlanProjectionAt(120)
			test.change(&superseding)
			priorUnfinished := -1
			ctx := execution.WithSlotCoverageCapture(context.Background(), &execution.SlotCoverageCapture{
				BeginCommitted: func(prior bool) {
					priorUnfinished = 0
					if prior {
						priorUnfinished = 1
					}
				},
			})
			result, err := store.BeginSlot(ctx, execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence, Projection: superseding})
			if err != nil || result.Status != execution.ProgressCommitted || priorUnfinished != 1 {
				t.Fatalf("BeginSlot(superseding same Segment) = (%+v, %v) prior=%d, want committed over the prior projection", result, err, priorUnfinished)
			}
			loaded, err := store.LoadProgress(context.Background(), identity)
			if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 120 || loaded.Progress.LastFullSlot != 60 ||
				loaded.Progress.CurrentOrRecentGap != nil || loaded.Progress.UnfinishedSlot == nil || !loaded.Progress.UnfinishedSlot.Equal(superseding) {
				t.Fatalf("LoadProgress(after supersede) = (%+v, %v)", loaded.Progress, err)
			}
			if len(observations) != 1 {
				t.Fatalf("observations = %+v, want exactly one superseded-projection observation", observations)
			}
			observation := observations[0]
			wantErr := "progress: unfinished Slot projection superseded by the latest fenced attempt; differing fields: " + test.wantFields
			if observation.Component != observability.ComponentProgress || observation.Stage != observability.StageProgressCommitted ||
				observation.Result != observability.ResultDegraded || observation.ReasonCode != observability.ReasonCode(contract.ReasonConfigDrift) ||
				observation.Trace.QueryGroupKey != "q" || observation.Trace.EvaluationTime != 120 ||
				observation.Trace.ScheduleSegmentStart != int64(superseding.Contract.ScheduleSegmentStart) ||
				observation.Err == nil || observation.Err.Error() != wantErr {
				t.Fatalf("superseded-projection observation = %+v (err=%v), want %q", observation, observation.Err, wantErr)
			}

			// The same Slot seen through an older Segment is not the latest view.
			raw := append([]byte(nil), fake.value...)
			older := superseding
			older.Contract.ScheduleSegmentStart = superseding.Contract.ScheduleSegmentStart - 30
			result, err = store.BeginSlot(context.Background(), execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence, Projection: older})
			if err != nil || result.Status != execution.ProgressConflict || string(fake.value) != string(raw) || len(observations) != 1 {
				t.Fatalf("BeginSlot(older Segment view) = (%+v, %v) changed=%t observations=%d, want conflict without a write", result, err, string(fake.value) != string(raw), len(observations))
			}

			// The equal projection is the idempotent re-begin.
			result, err = store.BeginSlot(context.Background(), execution.ProgressBeginRequest{Identity: identity, OwnerFence: fence, Projection: superseding})
			if err != nil || result.Status != execution.ProgressCommitted || string(fake.value) != string(raw) || len(observations) != 1 {
				t.Fatalf("BeginSlot(equal projection) = (%+v, %v) changed=%t observations=%d, want idempotent commit", result, err, string(fake.value) != string(raw), len(observations))
			}
		})
	}
}

// twoPlanProjectionAt is a projection whose due Plan targets can lose one Plan
// while keeping the frozen digest, as a pruned re-freeze would.
func twoPlanProjectionAt(evaluationTime execution.EvaluationTime) execution.UnfinishedSlotProjection {
	projection := progressProjectionAt(evaluationTime)
	projection.DuePlanTargets.Plans = append(projection.DuePlanTargets.Plans,
		execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy-2"})
	return projection
}

func mustObservedStore(t *testing.T, control ControlStore, slots ContinuousSlotResolver, observer observability.Observer) *Store {
	t.Helper()
	store, err := NewStore(StoreOptions{
		Prefix: "alarmd", Control: control, Slots: slots, Observer: observer,
		Now: func() time.Time { return time.Unix(1, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// newerSegmentProjectionAt is the projection a Worker freezes for the same
// Query Group after a cutover: a later Segment under a new publication.
func newerSegmentProjectionAt(evaluationTime execution.EvaluationTime) execution.UnfinishedSlotProjection {
	return segmentProjectionAt(evaluationTime, 119)
}

func segmentProjectionAt(evaluationTime, segmentStart execution.EvaluationTime) execution.UnfinishedSlotProjection {
	projection := progressProjectionAt(evaluationTime)
	projection.Contract.ScheduleSegmentStart = segmentStart
	projection.Contract.SnapshotRevision = "s2"
	projection.Contract.QueryRevision = "query2"
	return projection
}

func TestCommitProgressUsesExplicitNextSlotAndFence(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	ref := progressContract()
	request := execution.ProgressCommitRequest{
		Identity:         execution.ProgressIdentity{QueryGroup: "q"},
		OwnerFence:       execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		ExpectedNextSlot: 60,
		Completion: execution.SlotCompletion{Contract: ref, Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess},
	}
	result, err := store.CommitProgress(context.Background(), request)
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v)", result, err)
	}
	loaded, err := store.LoadProgress(context.Background(), request.Identity)
	if err != nil || loaded.Progress.NextSlot != 120 || loaded.Progress.LastFullSlot != 60 {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
}

func TestCommitProgressAdvancesColdStartHistoryWarmingUntilFull(t *testing.T) {
	fake := &controlFake{missing: true}
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	commitWarming := func(store *Store, slot execution.EvaluationTime) {
		t.Helper()
		result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
			Identity: identity, OwnerFence: fence, ExpectedNextSlot: slot,
			Completion: execution.SlotCompletion{
				Contract: progressContractAt(slot), Kind: execution.CompletionPartialGap,
				Primary: &execution.PrimaryInputFact{
					Completeness: execution.CompletenessFull,
					DataState:    execution.DataStateData,
				},
				Result:     observability.ResultDegraded,
				ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
			},
		})
		if err != nil || result.Status != execution.ProgressCommitted {
			t.Fatalf("CommitProgress(warming %d) = (%+v, %v)", slot, result, err)
		}
	}

	first := mustStore(t, fake)
	commitWarming(first, 60)
	loaded, err := first.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress(first warming) = (%+v, %v)", loaded, err)
	}
	assertWarmingProgress(t, *loaded.Progress, 120, 60, 60, 1)

	// Re-open the Store over the same persisted value to prove a process restart
	// continues from the next Slot instead of replaying the first warming Slot.
	restarted := mustStore(t, fake)
	commitWarming(restarted, 120)
	loaded, err = restarted.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress(second warming) = (%+v, %v)", loaded, err)
	}
	assertWarmingProgress(t, *loaded.Progress, 180, 60, 120, 2)

	result, err := restarted.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 180,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(180), Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{
				Completeness: execution.CompletenessFull,
				DataState:    execution.DataStateData,
			},
			Result: observability.ResultSuccess,
		},
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(full) = (%+v, %v)", result, err)
	}
	loaded, err = restarted.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 240 ||
		loaded.Progress.LastFullSlot != 180 || loaded.Progress.LastCompletionKind != execution.CompletionFull {
		t.Fatalf("LoadProgress(full) = (%+v, %v)", loaded, err)
	}
	assertWarmingGap(t, loaded.Progress.CurrentOrRecentGap, 60, 120, 2)
}

func TestCommitProgressAdvancesAndRestoresConsecutiveGapSkippedSlots(t *testing.T) {
	fake := &controlFake{missing: true}
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	commitGapSkipped := func(store *Store, slot execution.EvaluationTime) {
		t.Helper()
		result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
			Identity: identity, OwnerFence: fence, ExpectedNextSlot: slot,
			Completion: execution.SlotCompletion{
				Contract: progressContractAt(slot), Kind: execution.CompletionGapSkipped,
				Result:     observability.ResultDegraded,
				ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
			},
		})
		if err != nil || result.Status != execution.ProgressCommitted {
			t.Fatalf("CommitProgress(gap skipped %d) = (%+v, %v)", slot, result, err)
		}
	}

	first := mustStore(t, fake)
	commitGapSkipped(first, 60)
	loaded, err := first.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress(first gap skipped) = (%+v, %v)", loaded, err)
	}
	assertGapSkippedProgress(t, *loaded.Progress, 120, 60, 60, 1)

	// Re-open over the persisted value to prove startup can continue past an
	// expired Slot without replaying it or requiring an in-memory cursor.
	restarted := mustStore(t, fake)
	commitGapSkipped(restarted, 120)
	loaded, err = restarted.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress(second gap skipped) = (%+v, %v)", loaded, err)
	}
	assertGapSkippedProgress(t, *loaded.Progress, 180, 60, 120, 2)
}

func TestCommitProgressAdvancesFullDataGuardedByPreviousGapSkipped(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 60,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(60), Kind: execution.CompletionGapSkipped,
			Result:     observability.ResultDegraded,
			ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
		},
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(gap skipped) = (%+v, %v)", result, err)
	}

	// A following live Slot can have FULL+DATA input while its Level outcome is
	// still guarded by the durable GAP_SKIPPED marker. Completion derivation
	// intentionally preserves that as COMPLETED_WITH_UNAVAILABLE without
	// replacing the original query-free gap episode.
	result, err = store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(120), Kind: execution.CompletionUnavailable,
			Primary: &execution.PrimaryInputFact{
				Completeness: execution.CompletenessFull,
				DataState:    execution.DataStateData,
			},
			Result:     observability.ResultDegraded,
			ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
		},
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(full data guarded by gap skipped) = (%+v, %v)", result, err)
	}

	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
	progress := *loaded.Progress
	if progress.NextSlot != 180 || progress.LastFullSlot != 120 ||
		progress.LastCompletionKind != execution.CompletionUnavailable {
		t.Fatalf("guarded full-data Progress = %+v", progress)
	}
	// The guarded live Slot must not rewrite the earlier query-free gap episode.
	gap := progress.CurrentOrRecentGap
	if gap == nil || gap.Kind != execution.CompletionGapSkipped ||
		gap.ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) ||
		gap.FirstSlot != 60 || gap.LastSlot != 60 || gap.Count != 1 || gap.NextProbeAt != nil {
		t.Fatalf("preserved gap-skipped summary = %+v", gap)
	}
}

func TestCommitProgressAdvancesContractValidUnavailableInG2(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	for _, slot := range []execution.EvaluationTime{60, 120} {
		request := execution.ProgressCommitRequest{
			Identity: identity, OwnerFence: fence, ExpectedNextSlot: slot,
			Completion: execution.SlotCompletion{
				Contract: progressContractAt(slot), Kind: execution.CompletionUnavailable,
				Primary: &execution.PrimaryInputFact{
					Completeness: execution.CompletenessUnavailable,
					DataState:    execution.DataStateUnknown,
				},
				Result:     observability.ResultDegraded,
				ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable),
			},
		}
		if err := request.Validate(); err != nil {
			t.Fatalf("G2 UNAVAILABLE contract at %d is invalid: %v", slot, err)
		}
		result, err := store.CommitProgress(context.Background(), request)
		if err != nil || result.Status != execution.ProgressCommitted {
			t.Fatalf("CommitProgress(unavailable %d) = (%+v, %v)", slot, result, err)
		}
	}

	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
	progress := *loaded.Progress
	if progress.NextSlot != 180 || progress.LastFullSlot != 0 ||
		progress.LastCompletionKind != execution.CompletionUnavailable {
		t.Fatalf("UNAVAILABLE Progress = %+v", progress)
	}
	gap := progress.CurrentOrRecentGap
	if gap == nil || gap.Kind != execution.CompletionUnavailable ||
		gap.ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) ||
		gap.FirstSlot != 60 || gap.LastSlot != 120 || gap.Count != 2 || gap.NextProbeAt != nil {
		t.Fatalf("UNAVAILABLE gap summary = %+v", gap)
	}
}

func TestCommitProgressPersistsAndFoldsSnapshotUnavailableAcrossRestart(t *testing.T) {
	fake := &controlFake{missing: true}
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	commit := func(t *testing.T, store *Store, slot execution.EvaluationTime) {
		t.Helper()
		result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
			Identity: identity, OwnerFence: fence, ExpectedNextSlot: slot,
			Completion: execution.SlotCompletion{
				Contract: progressContractAt(slot), Kind: execution.CompletionSnapshotUnavailable,
				Result:     observability.ResultDegraded,
				ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable),
			},
		})
		if err != nil || result.Status != execution.ProgressCommitted {
			t.Fatalf("CommitProgress(snapshot unavailable %d) = (%+v, %v)", slot, result, err)
		}
	}

	commit(t, mustStore(t, fake), 60)
	restarted := mustStore(t, fake)
	commit(t, restarted, 120)

	loaded, err := restarted.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
	progress := *loaded.Progress
	if progress.NextSlot != 180 || progress.LastFullSlot != 0 ||
		progress.LastCompletionKind != execution.CompletionSnapshotUnavailable {
		t.Fatalf("SNAPSHOT_UNAVAILABLE Progress = %+v", progress)
	}
	gap := progress.CurrentOrRecentGap
	if gap == nil || gap.Kind != execution.CompletionSnapshotUnavailable ||
		gap.ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) ||
		gap.FirstSlot != 60 || gap.LastSlot != 120 || gap.Count != 2 || gap.NextProbeAt != nil {
		t.Fatalf("SNAPSHOT_UNAVAILABLE gap summary = %+v", gap)
	}
}

func TestCommitProgressPersistsTerminalAndRestoresContinuousCursor(t *testing.T) {
	fake := &controlFake{missing: true}
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}

	store := mustStore(t, fake)
	result, err := store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 60,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(60), Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{
				Completeness: execution.CompletenessFull,
				DataState:    execution.DataStateData,
			},
			Result: observability.ResultSuccess,
		},
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(full) = (%+v, %v)", result, err)
	}

	result, err = store.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 120,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(120), Kind: execution.CompletionTerminal,
			Primary: &execution.PrimaryInputFact{
				Completeness: execution.CompletenessPartial,
				DataState:    execution.DataStateData,
			},
			Result:     observability.ResultTerminal,
			ReasonCode: execution.ReasonCode(contract.ReasonRecordInvalid),
		},
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(terminal) = (%+v, %v)", result, err)
	}

	// Re-open over the persisted value to prove the terminal Slot remains a
	// bounded completion cursor instead of being replayed after restart.
	restarted := mustStore(t, fake)
	result, err = restarted.CommitProgress(context.Background(), execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: fence, ExpectedNextSlot: 180,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(180), Kind: execution.CompletionTerminal,
			Primary: &execution.PrimaryInputFact{
				Completeness: execution.CompletenessPartial,
				DataState:    execution.DataStateData,
			},
			Result:     observability.ResultTerminal,
			ReasonCode: execution.ReasonCode(contract.ReasonRecordInvalid),
		},
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress(terminal after restart) = (%+v, %v)", result, err)
	}

	loaded, err := restarted.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil {
		t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
	}
	progress := *loaded.Progress
	if progress.NextSlot != 240 || progress.LastFullSlot != 60 ||
		progress.LastCompletionKind != execution.CompletionTerminal {
		t.Fatalf("terminal Progress = %+v", progress)
	}
	gap := progress.CurrentOrRecentGap
	if gap == nil || gap.Kind != execution.CompletionTerminal ||
		gap.ReasonCode != execution.ReasonCode(contract.ReasonRecordInvalid) ||
		gap.FirstSlot != 120 || gap.LastSlot != 180 || gap.Count != 2 || gap.NextProbeAt != nil {
		t.Fatalf("terminal summary = %+v", gap)
	}
}

func assertWarmingProgress(
	t *testing.T,
	progress execution.ScheduleProgress,
	nextSlot, firstGap, lastGap execution.EvaluationTime,
	count uint32,
) {
	t.Helper()
	if progress.NextSlot != nextSlot || progress.LastFullSlot != lastGap ||
		progress.LastCompletionKind != execution.CompletionPartialGap {
		t.Fatalf("warming Progress = %+v", progress)
	}
	assertWarmingGap(t, progress.CurrentOrRecentGap, firstGap, lastGap, count)
}

func assertWarmingGap(
	t *testing.T,
	gap *execution.ProgressGapSummary,
	first, last execution.EvaluationTime,
	count uint32,
) {
	t.Helper()
	if gap == nil || gap.Kind != execution.CompletionPartialGap ||
		gap.ReasonCode != execution.ReasonCode(contract.ReasonHistoryWarming) ||
		gap.FirstSlot != first || gap.LastSlot != last || gap.Count != count || gap.NextProbeAt != nil {
		t.Fatalf("warming gap = %+v", gap)
	}
}

func assertGapSkippedProgress(
	t *testing.T,
	progress execution.ScheduleProgress,
	nextSlot, firstGap, lastGap execution.EvaluationTime,
	count uint32,
) {
	t.Helper()
	if progress.NextSlot != nextSlot || progress.LastFullSlot != 0 ||
		progress.LastCompletionKind != execution.CompletionGapSkipped {
		t.Fatalf("gap-skipped Progress = %+v", progress)
	}
	gap := progress.CurrentOrRecentGap
	if gap == nil || gap.Kind != execution.CompletionGapSkipped ||
		gap.ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) ||
		gap.FirstSlot != firstGap || gap.LastSlot != lastGap || gap.Count != count || gap.NextProbeAt != nil {
		t.Fatalf("gap-skipped summary = %+v", gap)
	}
}

func TestLoadProgressRejectsCorruptValueWithoutTreatingItAsMissing(t *testing.T) {
	store := mustStore(t, &controlFake{value: []byte("not-json")})
	result, err := store.LoadProgress(context.Background(), execution.ProgressIdentity{QueryGroup: "q"})
	if err == nil || result.Status == execution.ProgressMissing {
		t.Fatalf("LoadProgress(corrupt) = (%+v, %v)", result, err)
	}
	if _, ok := err.(*DeterministicInvalidError); !ok {
		t.Fatalf("error type = %T", err)
	}
}

func TestProgressIdentityMismatchIsDeterministicInvalidAndNotOverwritten(t *testing.T) {
	requested := execution.ProgressIdentity{QueryGroup: "q"}
	other := execution.ProgressIdentity{QueryGroup: "other"}
	raw := mustEncode(t, execution.ScheduleProgress{Identity: other, NextSlot: 60})
	fake := &controlFake{value: append([]byte(nil), raw...)}
	store := mustStore(t, fake)
	if _, err := store.LoadProgress(context.Background(), requested); err == nil {
		t.Fatal("LoadProgress accepted mismatched identity")
	}
	request := execution.ProgressCommitRequest{Identity: requested, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"}, ExpectedNextSlot: 60, Completion: execution.SlotCompletion{Contract: progressContract(), Kind: execution.CompletionFull, Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess}}
	if _, err := store.CommitProgress(context.Background(), request); err == nil {
		t.Fatal("CommitProgress accepted mismatched identity")
	}
	if string(fake.value) != string(raw) {
		t.Fatal("mismatched Progress was overwritten")
	}
}

func TestCommitProgressRepairsFullCompletionWithoutFallingBackToOlderGap(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	raw := mustEncode(t, execution.ScheduleProgress{
		Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
		CurrentOrRecentGap: &execution.ProgressGapSummary{
			Kind: execution.CompletionPartialGap, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
			FirstSlot: 30, LastSlot: 30, Count: 1,
		},
	})
	fake := &controlFake{value: raw}
	store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 90, 90: 180})
	request := execution.ProgressCommitRequest{
		Identity: identity, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"},
		ExpectedNextSlot: 90,
		Completion: execution.SlotCompletion{
			Contract: progressContractAt(90), Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
			Result:  observability.ResultSuccess,
		},
	}
	result, err := store.CommitProgress(context.Background(), request)
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v), want stale next repaired", result, err)
	}
	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != 180 {
		t.Fatalf("LoadProgress() = (%+v, %v), want next Slot 180", loaded, err)
	}
}

func TestCommitProgressAcceptsTimelineProvenGridJumpForEveryNonFullCompletion(t *testing.T) {
	for _, test := range []struct {
		name     string
		kind     execution.CompletionKind
		reason   execution.ReasonCode
		oldNext  execution.EvaluationTime
		newFirst execution.EvaluationTime
	}{
		{name: "partial gap after reactivation", kind: execution.CompletionPartialGap,
			reason: execution.ReasonCode(contract.ReasonHistoryWarming), oldNext: 90, newFirst: 180},
		{name: "unavailable after reactivation", kind: execution.CompletionUnavailable,
			reason: execution.ReasonCode(contract.ReasonQueryUnavailable), oldNext: 90, newFirst: 180},
		{name: "gap skipped after reactivation", kind: execution.CompletionGapSkipped,
			reason: execution.ReasonCode(contract.ReasonGapSkipped), oldNext: 90, newFirst: 180},
		{name: "partial gap after ordinary cutover", kind: execution.CompletionPartialGap,
			reason: execution.ReasonCode(contract.ReasonHistoryWarming), oldNext: 120, newFirst: 90},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity := execution.ProgressIdentity{QueryGroup: "q"}
			current := execution.ScheduleProgress{
				Identity: identity, NextSlot: test.oldNext, LastCompletionKind: test.kind,
				CurrentOrRecentGap: &execution.ProgressGapSummary{
					Kind: test.kind, ReasonCode: test.reason, FirstSlot: 60, LastSlot: 60, Count: 1,
				},
			}
			fake := &controlFake{value: mustEncode(t, current)}
			store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: test.newFirst, test.newFirst: test.newFirst + 60})
			request := progressRequestForKind(test.newFirst, test.kind, test.reason)

			result, err := store.CommitProgress(context.Background(), request)
			if err != nil || result.Status != execution.ProgressCommitted {
				t.Fatalf("CommitProgress() = (%+v, %v), want timeline-proven grid jump", result, err)
			}
			loaded, err := store.LoadProgress(context.Background(), identity)
			if err != nil || loaded.Progress == nil || loaded.Progress.NextSlot != test.newFirst+60 {
				t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
			}
		})
	}
}

func TestCommitProgressRejectsJumpPastTimelineNextSlot(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	current := execution.ScheduleProgress{
		Identity: identity, NextSlot: 120, LastCompletionKind: execution.CompletionPartialGap,
		CurrentOrRecentGap: &execution.ProgressGapSummary{
			Kind: execution.CompletionPartialGap, ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming),
			FirstSlot: 60, LastSlot: 60, Count: 1,
		},
	}
	raw := mustEncode(t, current)
	fake := &controlFake{value: raw}
	store := mustStoreWithSlots(t, fake, mappedSlotResolver{60: 90, 180: 240})

	result, err := store.CommitProgress(context.Background(), progressRequestForKind(
		180, execution.CompletionPartialGap, execution.ReasonCode(contract.ReasonHistoryWarming)))
	if err != nil || result.Status != execution.ProgressConflict {
		t.Fatalf("CommitProgress(illegal jump) = (%+v, %v), want conflict", result, err)
	}
	if string(fake.value) != string(raw) {
		t.Fatal("illegal old-Segment jump changed persisted Progress")
	}
}

func progressRequestForKind(
	slot execution.EvaluationTime,
	kind execution.CompletionKind,
	reason execution.ReasonCode,
) execution.ProgressCommitRequest {
	completion := execution.SlotCompletion{
		Contract: progressContractAt(slot), Kind: kind, Result: observability.ResultDegraded, ReasonCode: reason,
	}
	if kind != execution.CompletionGapSkipped {
		completion.Primary = &execution.PrimaryInputFact{Completeness: execution.CompletenessPartial, DataState: execution.DataStateData}
		if kind == execution.CompletionUnavailable {
			completion.Primary = &execution.PrimaryInputFact{Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown}
		}
	}
	return execution.ProgressCommitRequest{
		Identity:         execution.ProgressIdentity{QueryGroup: "q"},
		OwnerFence:       execution.OwnerFence{QueryGroup: "q", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"},
		ExpectedNextSlot: slot, Completion: completion,
	}
}

func TestCommitProgressMapsFencedCASStatuses(t *testing.T) {
	for _, test := range []struct {
		cas  ownership.FencedCASStatus
		want execution.ProgressCommitStatus
	}{
		{ownership.FencedCASConflict, execution.ProgressConflict},
		{ownership.FencedCASStaleOwner, execution.ProgressStaleOwner},
	} {
		fake := &controlFake{missing: true, status: test.cas}
		store := mustStore(t, fake)
		request := execution.ProgressCommitRequest{Identity: execution.ProgressIdentity{QueryGroup: "q"}, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"}, ExpectedNextSlot: 60, Completion: execution.SlotCompletion{Contract: progressContract(), Kind: execution.CompletionFull, Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess}}
		result, err := store.CommitProgress(context.Background(), request)
		if err != nil || result.Status != test.want {
			t.Fatalf("CommitProgress(%s) = (%+v, %v)", test.cas, result, err)
		}
		if !fake.missing || fake.value != nil {
			t.Fatalf("CommitProgress(%s) mutated rejected fake value: missing=%t value=%q", test.cas, fake.missing, fake.value)
		}
	}
}

func (fake *controlFake) ReadControl(_ context.Context, group execution.QueryGroupIdentity, name string) ([]byte, bool, error) {
	fake.group, fake.name = group, name
	return append([]byte(nil), fake.value...), fake.missing, nil
}
func (fake *controlFake) ReadControlForTemporaryLegacyDrainingCAS(_ context.Context, group execution.QueryGroupIdentity, name string) (ownership.TemporaryLegacyDrainingCASRead, error) {
	fake.group, fake.name = group, name
	fake.temporaryCASReads++
	return ownership.TemporaryLegacyDrainingCASRead{RedisKey: "redis:progress:key", Raw: append([]byte(nil), fake.value...), Missing: fake.missing}, nil
}
func (fake *controlFake) FencedCompareAndSet(_ context.Context, request ownership.FencedCASRequest) (ownership.FencedCASStatus, error) {
	status := fake.status
	if status == "" {
		status = ownership.FencedCASApplied
	}
	if status == ownership.FencedCASApplied {
		fake.value, fake.missing = append([]byte(nil), request.Value...), false
	}
	return status, nil
}

func TestLoadProgressForTemporaryLegacyDrainingCASReturnsExactRawReadWithoutMutation(t *testing.T) {
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	persisted := execution.ScheduleProgress{
		Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
	}
	raw := mustEncode(t, persisted)
	fake := &controlFake{value: append([]byte(nil), raw...)}
	store := mustStore(t, fake)

	result, err := store.LoadProgressForTemporaryLegacyDrainingCAS(context.Background(), identity)
	if err != nil || result.RedisKey != "redis:progress:key" || !reflect.DeepEqual(result.Raw, raw) ||
		result.Load.Status != execution.ProgressFound || result.Load.Progress == nil || *result.Load.Progress != persisted {
		t.Fatalf("LoadProgressForTemporaryLegacyDrainingCAS()=(%#v,%v)", result, err)
	}
	if !reflect.DeepEqual(fake.value, raw) {
		t.Fatalf("LoadProgressForTemporaryLegacyDrainingCAS() changed raw Progress: got=%q want=%q", fake.value, raw)
	}
}

func TestLoadProgressDistinguishesMissingAndFound(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	missing, err := store.LoadProgress(context.Background(), identity)
	if err != nil || missing.Status != execution.ProgressMissing {
		t.Fatalf("missing = (%+v, %v)", missing, err)
	}
	if fake.group != "q" || fake.name != "alarmd:progress" {
		t.Fatalf("Progress control location = (%q, %q)", fake.group, fake.name)
	}
	if fake.temporaryCASReads != 0 {
		t.Fatalf("normal LoadProgress used temporary CAS read %d times", fake.temporaryCASReads)
	}
	fake.value = mustEncode(t, execution.ScheduleProgress{Identity: identity, NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull})
	fake.missing = false
	found, err := store.LoadProgress(context.Background(), identity)
	if err != nil || found.Status != execution.ProgressFound || found.Progress.NextSlot != 120 {
		t.Fatalf("found = (%+v, %v)", found, err)
	}
}

func progressContract() execution.FrozenExecutionContractRef {
	return progressContractAt(60)
}

func progressContractAt(evaluationTime execution.EvaluationTime) execution.FrozenExecutionContractRef {
	return execution.FrozenExecutionContractRef{
		Slot: execution.SlotIdentity{QueryGroup: "q", EvaluationTime: evaluationTime}, SnapshotRevision: "s", QueryRevision: "query",
		ScheduleRevision: "r", ScheduleSegmentStart: 60, DuePlanSetDigest: "plans",
	}
}

func progressProjection() execution.UnfinishedSlotProjection {
	return progressProjectionAt(60)
}

func progressProjectionAt(evaluationTime execution.EvaluationTime) execution.UnfinishedSlotProjection {
	contractRef := progressContractAt(evaluationTime)
	return execution.UnfinishedSlotProjection{
		Contract: contractRef,
		DuePlanTargets: execution.FrozenDuePlanTargets{
			DuePlanSetDigest: contractRef.DuePlanSetDigest,
			Plans:            []execution.PlanIdentity{{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy"}},
		},
		EarliestQueryDeadlineUnixMilli: int64(evaluationTime)*1000 + 1_000,
		KeepUntilUnixMilli:             int64(evaluationTime)*1000 + 601_000,
	}
}

func mustStore(t *testing.T, control ControlStore) *Store {
	t.Helper()
	return mustStoreWithSlots(t, control, fixedSlotResolver{interval: 60})
}

func mustStoreWithSlots(t *testing.T, control ControlStore, slots ContinuousSlotResolver) *Store {
	t.Helper()
	store, err := NewStore(StoreOptions{
		Prefix: "alarmd", Control: control, Slots: slots,
		Now: func() time.Time { return time.Unix(1, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type mappedSlotResolver map[execution.EvaluationTime]execution.EvaluationTime

func (resolver mappedSlotResolver) NextSlotAfter(
	_ context.Context,
	_ execution.QueryGroupIdentity,
	completed execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	return resolver[completed], nil
}

type fixedSlotResolver struct {
	interval execution.EvaluationTime
}

func (resolver fixedSlotResolver) NextSlotAfter(
	_ context.Context,
	_ execution.QueryGroupIdentity,
	completed execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	return completed + resolver.interval, nil
}

func mustEncode(t *testing.T, value execution.ScheduleProgress) []byte {
	t.Helper()
	raw, err := encode(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
