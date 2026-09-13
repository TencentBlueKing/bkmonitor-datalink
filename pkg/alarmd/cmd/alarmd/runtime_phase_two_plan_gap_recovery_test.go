// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// supersededPlanScheduleRevision has the shape of a Plan schedule revision but
// belongs to no compiled Plan: it stands for the revision a marker was written
// under before the Plan's schedule changed.
const supersededPlanScheduleRevision = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

// planGapMarkerKey locates the persisted Plan gap marker of the fixture's
// Query Group and returns the identity it is keyed by.
func (fixture *cutoverStallFixture) planGapMarkerKey(ctx context.Context) (string, execution.PlanGapIdentity) {
	fixture.t.Helper()
	activation, err := fixture.repository.LoadActivation(ctx)
	if err != nil {
		fixture.t.Fatal(err)
	}
	for _, record := range activation.Plans {
		if record.Fact.Plan.StrategyID != "1001" {
			continue
		}
		identity := execution.PlanGapIdentity{Plan: record.Fact.Plan, StateGeneration: record.Fact.Selected.StateGeneration}
		key, keyErr := state.PlanGapKeyV2(fixture.cfg.Redis.StatePrefix, identity)
		if keyErr != nil {
			fixture.t.Fatal(keyErr)
		}
		return key, identity
	}
	fixture.t.Fatal("strategy 1001 has no activated Plan")
	return "", execution.PlanGapIdentity{}
}

// readPlanGapMarker returns the persisted marker as a decoded envelope, or
// false when no marker is persisted.
func (fixture *cutoverStallFixture) readPlanGapMarker(ctx context.Context) (map[string]any, bool) {
	fixture.t.Helper()
	key, _ := fixture.planGapMarkerKey(ctx)
	raw, err := fixture.redisClient.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var envelope map[string]any
	if unmarshalErr := json.Unmarshal(raw, &envelope); unmarshalErr != nil {
		fixture.t.Fatalf("persisted Plan gap marker is not decodable: %v", unmarshalErr)
	}
	return envelope, true
}

// rewritePlanGapMarker renames the reason and scope of every persisted gap
// scope and, when superseded is set, rolls the marker's schedule revision back
// to a revision no compiled Plan carries: exactly what a Plan schedule change
// leaves behind. It reports whether a marker was there to rewrite.
func (fixture *cutoverStallFixture) rewritePlanGapMarker(
	ctx context.Context,
	reason string,
	levelScope bool,
	superseded bool,
) bool {
	fixture.t.Helper()
	envelope, found := fixture.readPlanGapMarker(ctx)
	if !found {
		return false
	}
	scopes, ok := envelope["scopes"].([]any)
	if !ok || len(scopes) == 0 {
		return false
	}
	for _, entry := range scopes {
		scope, entryOK := entry.(map[string]any)
		if !entryOK {
			fixture.t.Fatalf("persisted gap scope has an unexpected shape: %#v", entry)
		}
		scope["ReasonCode"] = reason
	}
	if levelScope {
		// One Level-scoped gap, the shape completionGapMutation writes for an
		// incomplete per-consumer binding. Collapsing keeps the scope set
		// unique: a Plan-wide scope reopened by a query-free finalization
		// would otherwise duplicate the Level scope.
		first, _ := scopes[0].(map[string]any)
		first["Scope"] = map[string]any{"LevelID": 1, "HasLevel": true}
		envelope["scopes"] = []any{first}
	}
	if superseded {
		envelope["schedule_revision"] = supersededPlanScheduleRevision
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		fixture.t.Fatal(err)
	}
	key, _ := fixture.planGapMarkerKey(ctx)
	if setErr := fixture.redisClient.Set(ctx, key, encoded, 0).Err(); setErr != nil {
		fixture.t.Fatal(setErr)
	}
	return true
}

// A persisted Plan gap marker recovers on data Slots whatever opened it and
// whatever Plan schedule revision it was written under. The marker is rewritten
// in place before every attempt of the recovery drive, so the first current
// data Slot always meets it; from then on the Query Group must reach a FULL
// completion within a bounded number of data Slots.
//
// Before the fix a marker whose schedule revision no longer matched the Plan
// was neither warmed nor cleared: no writer refreshes that revision while the
// data is FULL, so every Level under the marker stayed UNKNOWN with the
// marker's reason and the Query Group completed COMPLETED_WITH_UNAVAILABLE for
// ever.
func TestProductionPhaseTwoPlanGapMarkerRecoversUnderEveryReasonAndScheduleRevision(t *testing.T) {
	for _, variant := range []struct {
		reason        string
		triggerWindow int
		levelScope    bool
		superseded    bool
	}{
		{reason: contract.ReasonGapSkipped, triggerWindow: 1},
		{reason: contract.ReasonExecutionBudgetExhausted, triggerWindow: 1},
		{reason: contract.ReasonGapSkipped, triggerWindow: 1, superseded: true},
		{reason: contract.ReasonExecutionBudgetExhausted, triggerWindow: 1, superseded: true},
		{reason: contract.ReasonExecutionBudgetExhausted, triggerWindow: 1, levelScope: true, superseded: true},
		// Two required FULL Slots: the stale marker restarts its warmup on the
		// first data Slot and clears on the second, instead of clearing at once.
		{reason: contract.ReasonExecutionBudgetExhausted, triggerWindow: 2, superseded: true},
	} {
		name := fmt.Sprintf("reason=%s/level_scope=%t/superseded_schedule=%t/trigger_window=%d",
			variant.reason, variant.levelScope, variant.superseded, variant.triggerWindow)
		t.Run(name, func(t *testing.T) {
			previousWindow := cutoverStallTriggerWindow
			cutoverStallTriggerWindow = variant.triggerWindow
			t.Cleanup(func() { cutoverStallTriggerWindow = previousWindow })
			fixture := newCutoverStalledFixture(t, func(cfg *config.Config) {
				cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled = true
			})
			ctx := context.Background()
			requiredFullSlots := fixture.requiredFullSlots(ctx)

			drive := fixture.driveRecovery(ctx, driveOptions{
				rewriteReason: variant.reason, rewriteLevelScope: variant.levelScope,
				rewriteSuperseded: variant.superseded, maxCurrentDataSlots: requiredFullSlots + 3,
			})
			t.Logf("%s: rewrote the marker %d times; current data Slots %d (RequiredFullSlots=%d); completions:\n  %s",
				name, drive.rewrites, drive.currentDataSlots, requiredFullSlots, strings.Join(drive.path, "\n  "))
			if drive.firstCurrentFull == 0 {
				t.Fatalf("no FULL completion for a current Slot after %d current data Slots: the marker never recovered", drive.currentDataSlots)
			}
			if drive.rewrites == 0 {
				t.Fatal("no Plan gap marker was ever persisted, so the recovery was never exercised")
			}
			// The restart of a marker left behind by a schedule change is named
			// in the flow; an ordinary warmup Slot is not.
			if variant.superseded && drive.scheduleRestarts == 0 {
				t.Fatal("recovering a marker under a superseded Plan schedule revision was not observed")
			}
			if !variant.superseded && drive.scheduleRestarts != 0 {
				t.Fatalf("an ordinary warmup Slot was observed as a schedule restart %d times", drive.scheduleRestarts)
			}
			if envelope, found := fixture.readPlanGapMarker(ctx); found {
				if scopes, _ := envelope["scopes"].([]any); len(scopes) != 0 {
					t.Fatalf("the marker still holds active scopes after a FULL Slot: %#v", envelope)
				}
			}
		})
	}
}

// A marker is not cleared while the data is genuinely incomplete: the UQ answer
// is partial on every current Slot, so the PRIMARY input never becomes FULL and
// the Query Group keeps its guard instead of reaching FULL.
func TestProductionPhaseTwoPlanGapMarkerSurvivesPartialData(t *testing.T) {
	fixture := newCutoverStalledFixture(t, func(cfg *config.Config) {
		cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled = true
	})
	ctx := context.Background()
	requiredFullSlots := fixture.requiredFullSlots(ctx)
	// Every query answered after the backlog is drained is partial.
	fixture.uqPartial = func(int64) bool { return true }

	drive := fixture.driveRecovery(ctx, driveOptions{
		rewriteReason: contract.ReasonExecutionBudgetExhausted, rewriteSuperseded: true,
		maxCurrentDataSlots: requiredFullSlots + 3,
	})
	t.Logf("partial data: current data Slots %d (RequiredFullSlots=%d); completions:\n  %s",
		drive.currentDataSlots, requiredFullSlots, strings.Join(drive.path, "\n  "))
	if drive.firstCurrentFull != 0 {
		t.Fatalf("a Slot whose PRIMARY input was PARTIAL completed FULL at attempt %d", drive.firstCurrentFull)
	}
	if drive.currentDataSlots == 0 {
		t.Fatal("no current data Slot was executed, so the guard was never exercised")
	}
	envelope, found := fixture.readPlanGapMarker(ctx)
	if !found {
		t.Fatal("the Plan gap marker was removed although the data stayed PARTIAL")
	}
	scopes, _ := envelope["scopes"].([]any)
	if len(scopes) == 0 {
		t.Fatalf("the Plan gap marker was cleared although the data stayed PARTIAL: %#v", envelope)
	}
}

func (fixture *cutoverStallFixture) requiredFullSlots(ctx context.Context) uint32 {
	fixture.t.Helper()
	activation, err := fixture.repository.LoadActivation(ctx)
	if err != nil {
		fixture.t.Fatal(err)
	}
	for _, record := range activation.Plans {
		if record.Fact.Plan.StrategyID == "1001" && record.Fact.Selected.RequiredFullSlots > 0 {
			return record.Fact.Selected.RequiredFullSlots
		}
	}
	fixture.t.Fatal("strategy 1001 activation has no RequiredFullSlots")
	return 0
}

type driveOptions struct {
	rewriteReason       string
	rewriteLevelScope   bool
	rewriteSuperseded   bool
	maxCurrentDataSlots uint32
}

type driveResult struct {
	rewrites         int
	currentDataSlots uint32
	firstCurrentFull int
	// scheduleRestarts counts the observations that named a marker recovered
	// under a Plan schedule revision it was not written under.
	scheduleRestarts int
	path             []string
}

// driveRecovery runs the Query Group's Runner from the stalled cutover until it
// completes a current Slot FULL, rewriting the persisted Plan gap marker before
// every attempt so that the marker the data Slots meet always carries the
// requested reason, scope and schedule revision.
func (fixture *cutoverStallFixture) driveRecovery(ctx context.Context, options driveOptions) driveResult {
	t := fixture.t
	t.Helper()
	limits := fixture.production.dependencies.RecoveryLimits
	const staleAge = 2 * time.Hour
	if staleAge <= 2*limits.MaxReplayAge {
		t.Fatalf("stale age %s must exceed MaxReplayAge %s by far", staleAge, limits.MaxReplayAge)
	}
	fixture.clock.Store(int64(fixture.firstNewSlot)*1000 + staleAge.Milliseconds() + 17_000)

	var result driveResult
	attempts := 0
	const maxIterations = 1000
	for iteration := 1; iteration <= maxIterations && result.firstCurrentFull == 0; iteration++ {
		if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		// The marker is rewritten only until the first current data Slot has
		// run: that Slot meets the requested reason, scope and schedule
		// revision, and from there the marker evolves on its own.
		if result.currentDataSlots == 0 &&
			fixture.rewritePlanGapMarker(ctx, options.rewriteReason, options.rewriteLevelScope, options.rewriteSuperseded) {
			result.rewrites++
		}
		at := fixture.now()
		before := len(fixture.observed())
		uqBefore := fixture.uqCalls.Load()
		outcome, attempted, err := fixture.runner.RunOne(ctx)
		if err != nil {
			t.Fatalf("recovery stopped: attempt %d RunOne error = %v (Progress=%+v)", attempts+1, err, fixture.progress(ctx))
		}
		if !attempted {
			next := at.Unix() - at.Unix()%60 + 60
			fixture.clock.Store(next*1000 + 1500)
			continue
		}
		attempts++
		if !outcome.Completed {
			if outcome.Result == observability.ResultRetrying {
				t.Fatalf("recovery stopped: attempt %d returned retrying %s (Progress=%+v)", attempts, outcome.ReasonCode, fixture.progress(ctx))
			}
			fixture.clock.Store(at.Add(time.Second).UnixMilli())
			continue
		}
		slot := int64(0)
		for _, observation := range fixture.observed()[before:] {
			if observation.Stage == observability.StageSlotCompleted {
				slot = observation.Trace.EvaluationTime
			}
			if observation.Stage == observability.StageGapGuardCommitted && observation.Result == observability.ResultResumed {
				result.scheduleRestarts++
			}
		}
		queries := fixture.uqCalls.Load() - uqBefore
		current := slot+60 > at.Unix()
		if current && queries > 0 {
			result.currentDataSlots++
			result.path = append(result.path, fmt.Sprintf("attempt %d Slot %d: %s/%s uq=%d",
				attempts, slot, outcome.CompletionKind, outcome.ReasonCode, queries))
		}
		if outcome.CompletionKind == execution.CompletionFull && current {
			result.firstCurrentFull = attempts
		}
		if result.currentDataSlots > options.maxCurrentDataSlots {
			break
		}
	}
	return result
}
