// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Query Group whose Progress cursor rests in a closed Schedule Segment whose
// Snapshot publication is no longer retained recovers on its own. Every
// publication closes each active Query Group's open Segment and opens a new
// one; the Snapshot objects of a publication that is no longer current expire
// with the Catalog TTL. A Query Group that fell behind by more than that TTL
// (here: nine Slots never attempted before a cutover, then the initial
// publication's Snapshot objects removed) could never freeze its cursor Slot:
// FreezeSlotContract failed with snapshot unavailable, the Slot was past its
// recovery window, no unfinished projection existed, and schedule_due reported
// BLOCKED_EXACT_SET_UNAVAILABLE for ever. The Runner must finalize every Slot
// of the unretained Segment query-free as SNAPSHOT_UNAVAILABLE without UQ,
// cross into the retained Segment and reach a FULL completion for a current
// Slot, without administrative cleanup.
func TestProductionPhaseTwoQueryGroupBehindUnretainedSegmentRecovers(t *testing.T) {
	for _, expiredRange := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired_range_enabled=%t", expiredRange), func(t *testing.T) {
			fixture := startCutoverFixture(t, func(cfg *config.Config) {
				cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled = expiredRange
			})
			ctx := context.Background()
			base, queryGroup := fixture.base, fixture.queryGroup
			catalog := fixture.production.dependencies.Catalog

			// Step 2: ten minutes pass without an attempt, then a publication
			// cuts over. The initial Segment closes with nine never-run Slots
			// behind the cursor, which still points at its second grid point.
			installCutoverStallStrategies(t, ctx, fixture.redisClient, "system.disk", 1725000600)
			stripSegmentContent(t, ctx, fixture.redisClient, productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "catalog"), queryGroup)
			boundary := execution.EvaluationTime(base + 599)
			fixture.clock.Store(int64(boundary) * 1000)
			for attempt := 0; attempt < 2; attempt++ {
				if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
					t.Fatalf("publication refresh %d error = %v", attempt+1, err)
				}
			}
			closed, err := catalog.ReadFrozenSchedule(ctx, queryGroup, execution.EvaluationTime(base))
			if err != nil || closed.Segment.End == nil || *closed.Segment.End != boundary {
				t.Fatalf("initial Segment after cutover = (%+v, %v), want End=%d", closed.Segment, err, boundary)
			}
			cursor := execution.EvaluationTime(base + 60)
			before := fixture.progress(ctx)
			if before.NextSlot != cursor || before.UnfinishedSlot != nil || !closed.Segment.Contains(cursor) {
				t.Fatalf("Progress before recovery = %+v, want the cursor at %d inside the closed Segment with nothing in flight", before, cursor)
			}

			// Step 3: the initial publication ages out of the Catalog retention:
			// its Snapshot objects are gone while the Segment still references
			// it. The catalog objects the Segment names by content age out
			// with it - nothing renews an object the current manifest does not
			// name - so the test removes them as well; otherwise the Segment
			// would still be readable by content, which is the case the
			// retention is designed to make possible, not the one under test.
			oldRevision := string(closed.Segment.Publication.SnapshotRevision)
			keys, err := fixture.redisClient.Keys(ctx, "*"+oldRevision+"*").Result()
			if err != nil || len(keys) == 0 {
				t.Fatalf("Snapshot objects of the initial publication = %v error=%v, want at least one key", keys, err)
			}
			// The cut stripped the content the Segment named (stripSegmentContent),
			// so the digests are taken from the Segment as it was first read.
			named := fixture.initialSchedule.Segment
			if named.ObjectDigest == "" || len(named.OutputContextRefs) == 0 {
				t.Fatalf("the initial Segment names no catalog object: %+v", named)
			}
			keys = append(keys, "*:qgobj:"+string(named.ObjectDigest))
			objectKeys, err := fixture.redisClient.Keys(ctx, "*:qgobj:"+string(named.ObjectDigest)).Result()
			if err != nil || len(objectKeys) != 1 {
				t.Fatalf("catalog object of the initial publication = %v error=%v, want exactly one key", objectKeys, err)
			}
			keys = append(keys[:len(keys)-1], objectKeys...)
			for _, ref := range named.OutputContextRefs {
				contextKeys, err := fixture.redisClient.Keys(ctx, "*:outctx:"+string(ref.Digest)).Result()
				if err != nil || len(contextKeys) != 1 {
					t.Fatalf("output context of the initial publication = %v error=%v, want exactly one key", contextKeys, err)
				}
				keys = append(keys, contextKeys...)
			}
			if err := fixture.redisClient.Del(ctx, keys...).Err(); err != nil {
				t.Fatal(err)
			}
			// A process that read the objects before they aged out still holds
			// them - they are immutable, so that is correct - which is not the
			// process under test: this one comes to the Segment cold.
			if err := fixture.repository.ConfigureObjectCache(1, 1); err != nil {
				t.Fatal(err)
			}
			_, freezeErr := catalog.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
				QueryGroup: queryGroup, ScheduleRevision: closed.Segment.ScheduleRevision, ScheduleSegmentStart: closed.Segment.Start,
				EvaluationTime: cursor, DuePlans: closed.DuePlanRefs(cursor),
			})
			if !errors.Is(freezeErr, controlplane.ErrSnapshotUnavailable) {
				t.Fatalf("FreezeSlotContract(cursor Slot) error = %v, want snapshot unavailable as the production precondition", freezeErr)
			}

			// Step 4: hours later the Runner drives recovery on its own.
			limits := fixture.production.dependencies.RecoveryLimits
			const staleAge = 2 * time.Hour
			if staleAge <= 2*limits.MaxReplayAge {
				t.Fatalf("stale age %s must exceed MaxReplayAge %s by far", staleAge, limits.MaxReplayAge)
			}
			fixture.clock.Store(int64(boundary)*1000 + staleAge.Milliseconds() + 17_000)
			type completionFacts struct {
				slot    int64
				kind    execution.CompletionKind
				reason  execution.ReasonCode
				uqCalls int64
				current bool
			}
			var completions []completionFacts
			currentFull := false
			const maxIterations = 1000
			for iteration := 1; iteration <= maxIterations && !currentFull; iteration++ {
				if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
					fixture.clock.Store(nextAt.UnixMilli() + 1)
				}
				at := fixture.now()
				observedBefore := len(fixture.observed())
				uqBefore := fixture.uqCalls.Load()
				result, attempted, err := fixture.runner.RunOne(ctx)
				if err != nil {
					t.Fatalf("recovery stopped: RunOne error = %v (Progress=%+v)", err, fixture.progress(ctx))
				}
				if !attempted {
					next := at.Unix() - at.Unix()%60 + 60
					fixture.clock.Store(next*1000 + 1500)
					continue
				}
				if !result.Completed {
					if result.Result == observability.ResultRetrying {
						t.Fatalf("Query Group stayed blocked instead of finalizing the unretained Segment query-free: %s (Progress=%+v)",
							result.ReasonCode, fixture.progress(ctx))
					}
					fixture.clock.Store(at.Add(time.Second).UnixMilli())
					continue
				}
				facts := completionFacts{kind: result.CompletionKind, reason: result.ReasonCode, uqCalls: fixture.uqCalls.Load() - uqBefore}
				for _, observation := range fixture.observed()[observedBefore:] {
					if observation.Stage == observability.StageSlotCompleted {
						facts.slot = observation.Trace.EvaluationTime
					}
				}
				facts.current = facts.slot+60 > at.Unix()
				completions = append(completions, facts)
				currentFull = facts.current && result.CompletionKind == execution.CompletionFull
			}
			if len(completions) == 0 {
				t.Fatalf("the Runner completed nothing in %d iterations (Progress=%+v)", maxIterations, fixture.progress(ctx))
			}

			// Step: the nine Slots of the unretained Segment complete query-free
			// as SNAPSHOT_UNAVAILABLE, in order, without a UQ call.
			unretained := 0
			for index, completion := range completions {
				if completion.slot >= int64(boundary) {
					break
				}
				wantSlot := int64(cursor) + int64(index)*60
				if completion.slot != wantSlot || completion.kind != execution.CompletionSnapshotUnavailable || completion.uqCalls != 0 {
					t.Fatalf("unretained Segment completion %d = %+v, want Slot %d finalized as SNAPSHOT_UNAVAILABLE without UQ", index, completion, wantSlot)
				}
				unretained++
			}
			if unretained != 9 {
				t.Fatalf("unretained Segment produced %d completions, want its nine Slots (completions=%+v)", unretained, completions)
			}
			if !currentFull {
				t.Fatalf("no FULL completion for a current Slot after %d completions: last %+v", len(completions), completions[len(completions)-1])
			}
			final := fixture.progress(ctx)
			if final.UnfinishedSlot != nil || final.LastFullSlot != execution.EvaluationTime(completions[len(completions)-1].slot) {
				t.Fatalf("Progress after recovery = %+v, want the current FULL Slot as LastFullSlot with nothing in flight", final)
			}
			t.Logf("recovered across the unretained Segment: %d query-free Slots, %d completions in total, current FULL at Slot %d",
				unretained, len(completions), completions[len(completions)-1].slot)
		})
	}
}
