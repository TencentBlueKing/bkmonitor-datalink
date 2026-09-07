// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Query Group whose persisted unfinished projection sits on the old
// Segment at the first Slot of the new Segment, hours past MaxReplayAge,
// recovers on its own once the newer Segment supersedes the projection: the
// stale Slot is finalized query-free through the expiry path, the backlog is
// drained by the expired-range path or one Slot at a time, the gap episode
// is warmed through RequiredFullSlots data Slots, and the group reaches a
// FULL completion for a current Slot within a bounded number of Runner
// attempts. No administrative cleanup is involved.
func TestProductionPhaseTwoCutoverStalledGroupRecoversPastReplayAge(t *testing.T) {
	for _, expiredRange := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired_range_enabled=%t", expiredRange), func(t *testing.T) {
			fixture := newCutoverStalledFixture(t, func(cfg *config.Config) {
				cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled = expiredRange
			})
			ctx := context.Background()
			limits := fixture.production.dependencies.RecoveryLimits
			const staleAge = 2 * time.Hour
			if staleAge <= 2*limits.MaxReplayAge {
				t.Fatalf("stale age %s must exceed MaxReplayAge %s by far", staleAge, limits.MaxReplayAge)
			}
			activation, err := fixture.repository.LoadActivation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			requiredFullSlots := uint32(0)
			for _, record := range activation.Plans {
				if record.Fact.Plan.StrategyID == "1001" {
					requiredFullSlots = record.Fact.Selected.RequiredFullSlots
				}
			}
			if requiredFullSlots == 0 {
				t.Fatalf("strategy 1001 activation has no RequiredFullSlots: %+v", activation.Plans)
			}

			// The Slot begun under the old Segment is now hours old and many
			// later Slots are due.
			fixture.clock.Store(int64(fixture.firstNewSlot)*1000 + staleAge.Milliseconds() + 17_000)
			uqCallsBefore := fixture.uqCalls.Load()
			observedBefore := len(fixture.observed())

			type completionFacts struct {
				attempt   int
				at        int64
				slot      int64
				current   bool
				kind      execution.CompletionKind
				reason    execution.ReasonCode
				result    observability.Result
				uqCalls   int64
				rangeFact *observability.ExpiredRangeFacts
				next      execution.EvaluationTime
			}
			var completions []completionFacts
			attempts, currentDataSlots, firstCurrentFull := 0, uint32(0), 0
			const maxIterations = 1000
			for iteration := 1; iteration <= maxIterations && firstCurrentFull == 0; iteration++ {
				if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
					fixture.clock.Store(nextAt.UnixMilli() + 1)
				}
				at := fixture.now()
				before := len(fixture.observed())
				uqBefore := fixture.uqCalls.Load()
				result, attempted, err := fixture.runner.RunOne(ctx)
				if err != nil {
					t.Fatalf("recovery stopped: attempt %d RunOne error = %v (Progress=%+v)", attempts+1, err, fixture.progress(ctx))
				}
				if !attempted {
					// Nothing is due: the next grid point arrives, as it would for a
					// ticking Runner, with readiness already satisfied.
					next := at.Unix() - at.Unix()%60 + 60
					fixture.clock.Store(next*1000 + 1500)
					continue
				}
				attempts++
				if !result.Completed {
					if result.Result == observability.ResultRetrying {
						t.Fatalf("recovery stopped: attempt %d returned retrying %s (Progress=%+v)", attempts, result.ReasonCode, fixture.progress(ctx))
					}
					// A readiness deferral; the next tick retries.
					fixture.clock.Store(at.Add(time.Second).UnixMilli())
					continue
				}
				facts := completionFacts{attempt: attempts, at: at.Unix(), kind: result.CompletionKind, reason: result.ReasonCode,
					result: result.Result, uqCalls: fixture.uqCalls.Load() - uqBefore, next: fixture.progress(ctx).NextSlot}
				for _, observation := range fixture.observed()[before:] {
					switch observation.Stage {
					case observability.StageSlotCompleted:
						facts.slot = observation.Trace.EvaluationTime
					case observability.StageExpiredRangeReturned:
						if observation.ExpiredRange != nil {
							rangeFacts := *observation.ExpiredRange
							facts.rangeFact = &rangeFacts
						}
					}
				}
				facts.current = facts.slot+60 > at.Unix()
				completions = append(completions, facts)
				if facts.current && facts.uqCalls > 0 {
					currentDataSlots++
				}
				if facts.uqCalls > 0 {
					var stages []string
					for _, observation := range fixture.observed()[before:] {
						switch observation.Stage {
						case observability.StageGapGuardCommitted, observability.StageStateApplied, observability.StageStatePreflight, observability.StageGapLoaded:
							stages = append(stages, fmt.Sprintf("%s(%s/%s)", observation.Stage, observation.Result, observation.ReasonCode))
						case observability.StageEvaluationCompleted:
							var algorithms []string
							for _, fact := range observation.AlgorithmEvaluations {
								algorithms = append(algorithms, fmt.Sprintf("level%d:%s/%s", fact.Provenance.LevelID, fact.Result, fact.ReasonCode))
							}
							stages = append(stages, fmt.Sprintf("%s(%s/%s %s)", observation.Stage, observation.Result, observation.ReasonCode, strings.Join(algorithms, ",")))
						}
					}
					t.Logf("data Slot %d at attempt %d (current=%t): %s/%s; stages: %s", facts.slot, attempts, facts.current, facts.kind, facts.reason, strings.Join(stages, " "))
				}
				if result.CompletionKind == execution.CompletionFull && facts.current {
					firstCurrentFull = attempts
				}
				if currentDataSlots > requiredFullSlots+3 {
					// Bounded: the guard needs RequiredFullSlots data Slots to warm.
					break
				}
			}
			if len(completions) == 0 {
				t.Fatalf("recovery stopped: the Runner completed nothing in %d iterations (Progress=%+v)", maxIterations, fixture.progress(ctx))
			}

			// Step: the stale Slot is superseded and finalized query-free.
			stale := completions[0]
			if stale.slot != int64(fixture.firstNewSlot) || stale.uqCalls != 0 ||
				(stale.kind != execution.CompletionSnapshotUnavailable && stale.kind != execution.CompletionGapSkipped) ||
				stale.next != fixture.firstNewSlot+60 {
				t.Fatalf("recovery stopped at the stale Slot: first completion = %+v, want Slot %d finalized query-free with the cursor at %d", stale, fixture.firstNewSlot, fixture.firstNewSlot+60)
			}
			for _, observation := range fixture.observed()[observedBefore:] {
				switch observation.ReasonCode {
				case observability.ReasonCode(contract.ReasonBlockedExactSetUnavailable),
					observability.ReasonCode(contract.ReasonProgressBeginFailed), observability.ReasonCode(contract.ReasonProgressBeginRejected):
					t.Fatalf("recovery stopped: BeginSlot did not commit, observed %s at stage %s: %v", observation.ReasonCode, observation.Stage, observation.Err)
				}
			}

			// Step: the cursor is current and the current Slots are executed
			// with FULL PRIMARY data (LastFullSlot follows every current Slot).
			final := completions[len(completions)-1]
			progress := fixture.progress(ctx)
			if !final.current || final.uqCalls == 0 || progress.LastFullSlot != execution.EvaluationTime(final.slot) ||
				progress.NextSlot != execution.EvaluationTime(final.slot)+60 || progress.UnfinishedSlot != nil || currentDataSlots == 0 {
				t.Fatalf("recovery stopped before the cursor was current: last completion = %+v, Progress = %+v, current data Slots = %d", final, progress, currentDataSlots)
			}

			// Report the observed path.
			histogram := make(map[string]int)
			var ranges []string
			var currentPath []string
			queryFreeUQCalls := int64(0)
			backlogAttempts := 0
			for _, completion := range completions {
				key := string(completion.kind) + "/" + string(completion.reason)
				histogram[key]++
				if completion.kind != execution.CompletionFull && completion.uqCalls == 0 {
					queryFreeUQCalls += completion.uqCalls
				}
				if completion.rangeFact != nil {
					ranges = append(ranges, fmt.Sprintf("attempt %d: range first Slot %d committed %d Slots as %s reason %s, cursor -> %d",
						completion.attempt, completion.slot, completion.rangeFact.CommittedSlots, completion.kind, completion.rangeFact.ReasonCode, completion.next))
				}
				if completion.current {
					currentPath = append(currentPath, fmt.Sprintf("attempt %d Slot %d: %s/%s uq=%d", completion.attempt, completion.slot, completion.kind, completion.reason, completion.uqCalls))
				} else {
					backlogAttempts = completion.attempt
				}
			}
			if queryFreeUQCalls != 0 {
				t.Fatalf("query-free finalizations issued %d UQ queries", queryFreeUQCalls)
			}
			t.Logf("stale Slot %d: attempt %d finalized as %s reason %s result %s without UQ, cursor -> %d",
				stale.slot, stale.attempt, stale.kind, stale.reason, stale.result, stale.next)
			if len(ranges) > 0 {
				t.Logf("expired ranges:\n  %s", strings.Join(ranges, "\n  "))
			} else {
				t.Logf("no expired range was used (expired_range_enabled=%t)", expiredRange)
			}
			t.Logf("backlog drained by attempt %d; current Slots:\n  %s", backlogAttempts, strings.Join(currentPath, "\n  "))
			t.Logf("completions by kind/reason: %v; total completions %d; RequiredFullSlots=%d; current data Slots %d; first current FULL at attempt %d (0 = none); UQ calls during recovery %d",
				histogram, len(completions), requiredFullSlots, currentDataSlots, firstCurrentFull, fixture.uqCalls.Load()-uqCallsBefore)

			// Step: a FULL completion for a current Slot. The clearing data Slot
			// persists the level history as GAPPED under the still-gapped guard;
			// on the next Slot the loaded history already forms the required full
			// window, so the GAPPED Level converges to FULL exactly as WARMING does
			// (evaluation/evaluator.go guardConvergenceAllowed) and the Slot
			// completes FULL instead of COMPLETED_WITH_UNAVAILABLE for ever.
			if firstCurrentFull == 0 {
				t.Fatalf("no FULL completion for a current Slot after %d current data Slots (RequiredFullSlots=%d): last completion %s/%s at Slot %d",
					currentDataSlots, requiredFullSlots, final.kind, final.reason, final.slot)
			}
		})
	}
}
