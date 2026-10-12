package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// A release gate through the production cmd wiring on the state a long-running
// deployment has and a fresh fixture does not: a Segment open since before its
// output context was revised, whose first contexts and first publication's
// manifest are past their retention. After the owner restarts, four Slots in a
// row run every stage -- started, queried, evaluated, state applied, progress
// committed, completed -- and the cursor moves four steps. Every unit and
// runtime test passed on a build that stopped nearly every such group, because
// each of them built its Segments fresh.
func TestARevisedSegmentPastItsRetentionRunsEveryStageAfterARestart(t *testing.T) {
	ctx := context.Background()
	f := revisedSegmentPastRetention(t, true)
	step := execution.EvaluationTime(f.initialSchedule.Plans[0].Spec.EvaluationIntervalSeconds)
	mark := len(f.observed())
	before := f.progress(ctx)
	restartUntilFull(t, f, f.queryGroup)
	for n := 0; n < 3; n++ {
		_ = runOneSlotFull(t, f)
	}
	after := f.progress(ctx)
	if after.LastFullSlot != before.LastFullSlot+4*step || after.NextSlot != before.NextSlot+4*step {
		t.Fatalf("the cursor did not move four Slots: LastFullSlot %d -> %d, NextSlot %d -> %d",
			before.LastFullSlot, after.LastFullSlot, before.NextSlot, after.NextSlot)
	}
	stages := []observability.Stage{observability.StageSlotStarted, observability.StageQueryCompleted, observability.StageEvaluationCompleted,
		observability.StageStateApplied, observability.StageProgressCommitted, observability.StageSlotCompleted}
	counts := map[string]int{}
	for _, observation := range f.observed()[mark:] {
		for _, stage := range stages {
			if observation.Stage == stage {
				counts[string(stage)+"/"+string(observation.ReasonCode)]++
			}
		}
	}
	for _, stage := range stages {
		if counts[string(stage)+"/none"] != 4 {
			t.Fatalf("%s did not run once for each of the four Slots: %v", stage, counts)
		}
	}
	if len(counts) != len(stages) {
		t.Fatalf("a stage ended with a reason: %v", counts)
	}
	if degraded := degradedCounts(f); len(degraded) != 0 {
		t.Fatalf("the hold was degraded instead of prepared: %v", degraded)
	}
}

// While a group's hold is degraded its lookback findings change nothing, and
// say so once through the Slots' degraded line, not once per finding.
func TestAFindingForADegradedGroupIsNotLoggedAsAFailure(t *testing.T) {
	ctx := context.Background()
	var output bytes.Buffer
	f := startCutoverFixtureWith(t, nil, observability.New(observability.ComponentRuntime, &output), nil)
	_ = runOneSlotFull(t, f)
	completed := f.progress(ctx).LastCompletion
	if completed == nil {
		t.Fatal("no completed Slot to report a finding for")
	}
	if err := f.redisClient.RPush(ctx, holdRecordKey(f, f.queryGroup), "not a record").Err(); err != nil {
		t.Fatal(err)
	}
	holds := f.bundle.dependencies.ReadHolds
	holds.mu.Lock()
	group := holds.groups[f.queryGroup]
	holds.mu.Unlock()
	group.mu.Lock()
	group.prepared = execution.ScheduleSegmentFact{}
	group.mu.Unlock()
	holds.controller.Forget(f.queryGroup)
	applied := false
	holds.observation(completed.Contract, func(context.Context) error {
		applied = true
		return nil
	})
	if applied || strings.Contains(output.String(), "observation_failed") {
		t.Fatalf("a finding for a degraded group was applied (%t) or logged as a failure:\n%s", applied, output.String())
	}
	// The control: the same group's Slot does log its degradation here.
	current, err := f.production.dependencies.Catalog.ReadFrozenSchedule(ctx, f.queryGroup, f.progress(ctx).NextSlot)
	if err != nil {
		t.Fatal(err)
	}
	lease, _ := group.session.Current()
	if _, err := holds.SlotReadHold(ctx, current, f.progress(ctx).NextSlot, lease.Fence); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "degraded_"+readhold.DegradedRecordUnreadable) {
		t.Fatalf("the degraded Slot was not logged, so this log says nothing:\n%s", output.String())
	}
}
