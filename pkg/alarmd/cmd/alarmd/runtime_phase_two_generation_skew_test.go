// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// previousFormulaLeader is a Control Leader of the binary that ran before a
// release moved the state generation formula: the same source, planner,
// repository and Progress the running bundle uses, with state semantics that
// derive another generation from every Plan. What it publishes is what a
// Worker of the new binary finds in Redis after the rolling restart.
type previousFormulaLeader struct {
	reconciler *controlplane.SourceReconciler
	activator  *controlplane.ScheduleActivationReconciler
	source     controlplane.StrategySource
	planner    controlplane.PrimaryQueryCompiler
}

func newPreviousFormulaLeader(t *testing.T, fixture *cutoverStallFixture) *previousFormulaLeader {
	t.Helper()
	control := fixture.bundle.dependencies.Control.(*productionPhaseTwoControl).dependencies
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), fixture.cfg.CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	runtimeSemantics, err := state.RuntimeStateSemantics()
	if err != nil {
		t.Fatal(err)
	}
	// The identity schema digest is one of the inputs the hash closes over;
	// moving it moves every generation the way a new closure formula does.
	semantics := strategy.StateSemantics{
		StateSchemaVersion:          runtimeSemantics.StateSchemaVersion,
		CodecSemanticsVersion:       runtimeSemantics.CodecSemanticsVersion,
		IdentitySchemaDigest:        strings.Repeat("4", 64),
		SourceTimeSemanticsVersion:  runtimeSemantics.SourceTimeSemanticsVersion,
		HistoryCellSemanticsVersion: runtimeSemantics.HistoryCellSemanticsVersion,
	}
	reconciler, err := controlplane.NewSourceReconciler(fixture.repository, compiler, semantics, phaseTwoCatalogRetentionValidator(fixture.cfg))
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.ConfigureOutputProtocol(fixture.cfg.OutputProtocol()); err != nil {
		t.Fatal(err)
	}
	activator, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		fixture.repository, compiler, semantics, fixture.progressStore, fixture.now,
	)
	if err != nil {
		t.Fatal(err)
	}
	return &previousFormulaLeader{reconciler: reconciler, activator: activator, source: control.Source, planner: control.Planner}
}

// publish confirms and publishes the source as the previous binary would
// have and cuts every Segment over to the publication at the fixture clock.
func (leader *previousFormulaLeader) publish(t *testing.T, ctx context.Context) controlplane.ActivationState {
	t.Helper()
	if result, err := leader.reconciler.Refresh(ctx, leader.source, leader.planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("previous formula pending = (%+v, %v)", result, err)
	}
	published, err := leader.reconciler.Refresh(ctx, leader.source, leader.planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("previous formula publish = (%+v, %v)", published, err)
	}
	activation, err := leader.activator.Ensure(ctx, published.Publication)
	if err != nil {
		t.Fatalf("previous formula cutover: %v", err)
	}
	return activation
}

// TestProductionPhaseTwoWorkerResumesAcrossAStateGenerationFormulaMove: the
// shape of the release that moved the state compatibility hash to
// strategy-state-input-closure-v2. The Leader of the previous binary
// published every Segment under its formula; the Workers then restarted on
// the new binary and found, in every open Segment, Plans whose stored
// generation the new binary does not derive. The Leader of the new binary
// later cut over on its own formula, but only ahead of the Progress cursor,
// which still rested in the previous Leader's Segment; nothing could pass a
// Slot that would not freeze, and by then those Slots were long past their
// recovery window. From exactly that state - no Redis touched - the Query
// Group must freeze the old Slots under the generation their records name,
// finalize them as expired, reach the new Leader's Segment and warm up to a
// FULL completion for a current Slot within a bounded number of attempts.
func TestProductionPhaseTwoWorkerResumesAcrossAStateGenerationFormulaMove(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	base := fixture.base
	limits := fixture.production.dependencies.RecoveryLimits
	const staleAge = 2 * time.Hour
	if staleAge <= 2*limits.MaxReplayAge {
		t.Fatalf("stale age %s must exceed MaxReplayAge %s by far", staleAge, limits.MaxReplayAge)
	}
	currentGeneration := activationGenerationOf(t, mustLoadActivation(t, ctx, fixture).Plans, "1001")

	// The previous binary's Leader publishes and cuts over just before the
	// second grid point, so that grid point is the first Slot of a Segment
	// whose content carries the previous formula's generation.
	fixture.clock.Store((base + 59) * 1000)
	previous := newPreviousFormulaLeader(t, fixture)
	previousActivation := previous.publish(t, ctx)
	previousGeneration := activationGenerationOf(t, previousActivation.Plans, "1001")
	if previousGeneration == currentGeneration {
		t.Fatalf("the previous formula must derive another generation than the running binary: %s", currentGeneration)
	}
	previousSegment, err := fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(base+60))
	if err != nil || previousSegment.Segment.ObjectDigest == "" || previousSegment.Segment.ObjectDigest == fixture.initialSchedule.Segment.ObjectDigest {
		t.Fatalf("the previous formula's cutover must open a Segment naming new content: %+v err=%v", previousSegment.Segment, err)
	}

	// Hours pass with the Worker unable to freeze anything, then the new
	// binary's Leader cuts over on its own formula, ahead of the cursor.
	fixture.clock.Store((base+59)*1000 + staleAge.Milliseconds())
	for attempt := 0; attempt < 2; attempt++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("current formula refresh %d error = %v", attempt+1, err)
		}
	}
	if generation := activationGenerationOf(t, mustLoadActivation(t, ctx, fixture).Plans, "1001"); generation != currentGeneration {
		t.Fatalf("the running binary's cutover must publish its own generation %s, got %s", currentGeneration, generation)
	}
	if cursor := fixture.progress(ctx); cursor.NextSlot != execution.EvaluationTime(base+60) {
		t.Fatalf("the cursor must still rest on the previous formula's Segment: %+v", cursor)
	}
	requiredFullSlots := fixture.requiredFullSlots(ctx)

	// Drive the Runner from that state. Every attempt that reports retrying
	// is the stall; the Query Group must instead drain the old Segments and
	// complete a current Slot FULL.
	fixture.clock.Store(fixture.now().Add(17 * time.Second).UnixMilli())
	observedBefore := len(fixture.observed())
	var expired, drifted int
	var full bool
	attempts := 0
	for iteration := 1; iteration <= 1000 && !full; iteration++ {
		if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		at := fixture.now()
		result, attempted, err := fixture.runner.RunOne(ctx)
		if err != nil {
			t.Fatalf("attempt %d RunOne error = %v (Progress=%+v)", attempts+1, err, fixture.progress(ctx))
		}
		if !attempted {
			next := at.Unix() - at.Unix()%60 + 60
			fixture.clock.Store(next*1000 + 1500)
			continue
		}
		attempts++
		if !result.Completed {
			if result.Result == observability.ResultRetrying {
				t.Fatalf("attempt %d returned retrying %s: the Worker is stuck on the previous formula's Segment (Progress=%+v)", attempts, result.ReasonCode, fixture.progress(ctx))
			}
			fixture.clock.Store(at.Add(time.Second).UnixMilli())
			continue
		}
		switch {
		case result.CompletionKind == execution.CompletionGapSkipped || result.CompletionKind == execution.CompletionUnavailable:
			expired++
		case result.ReasonCode == execution.ReasonCode(contract.ReasonConfigDrift):
			drifted++
		}
		if result.CompletionKind == execution.CompletionFull && at.Unix()-int64(fixture.progress(ctx).LastFullSlot) < 120 {
			full = true
		}
	}
	if !full {
		t.Fatalf("no current FULL completion within %d attempts: expired=%d drifted=%d Progress=%+v", attempts, expired, drifted, fixture.progress(ctx))
	}
	if expired == 0 {
		t.Fatalf("the Slots left in the previous formula's Segment must be finalized as expired, none were (attempts=%d)", attempts)
	}
	t.Logf("resumed after %d attempts: expired=%d drifted=%d required full Slots=%d", attempts, expired, drifted, requiredFullSlots)

	// The path went through formula skew, never through a record the object
	// does not vouch for.
	var formula, record int
	for _, observation := range fixture.observed()[observedBefore:] {
		if facts := observation.StateGenerationSkew; facts != nil {
			switch facts.Kind {
			case "formula":
				formula++
			case "record":
				record++
			}
		}
	}
	if formula == 0 || record != 0 {
		t.Fatalf("state generation skew reported formula=%d record=%d, want formula skew only", formula, record)
	}
}

func mustLoadActivation(t *testing.T, ctx context.Context, fixture *cutoverStallFixture) controlplane.ActivationState {
	t.Helper()
	activation, err := fixture.repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return activation
}
