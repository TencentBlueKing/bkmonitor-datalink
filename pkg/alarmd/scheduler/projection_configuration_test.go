// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
)

// The unfinished projection of one frozen Slot is not a pure function of the
// frozen facts: KeepUntil adds the process's post-recovery terminal delay to
// the recovery boundary. Two Workers that freeze the same Slot in the same
// Segment with different delays therefore disagree on the projection, and the
// Worker that takes over after a restart must still be able to begin the Slot.
func TestProductionSlotSourceProjectionFollowsTerminalDelayConfiguration(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	at := time.Unix(200, 0)
	freeze := func(terminalDelay time.Duration, epoch uint64) execution.SlotExecutionRequest {
		t.Helper()
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
		source, err := NewProductionSlotSource("query-group-1", "worker-1",
			&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
			&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(epoch)}}, catalog,
			&fakeProgressReader{result: missingProgress(), catalog: catalog}, func() time.Time { return at },
			WithRecoveryLimits(testRecoveryLimits()), WithPostRecoveryTerminalDelay(terminalDelay), WithQueryDeadlineReserve(5*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		slot, due, err := source.Next(context.Background(), "query-group-1")
		if err != nil || !due {
			t.Fatalf("Next(terminal delay %s) = (%+v, %t, %v), want a due Slot", terminalDelay, slot, due, err)
		}
		return execution.SlotExecutionRequest{
			Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
			EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli,
			RecoveryUntilUnixMilli:         slot.RecoveryUntilUnixMilli, KeepUntilUnixMilli: slot.KeepUntilUnixMilli,
			Operation: slot.Dispatch.Operation, AttemptNo: 1, OwnerFence: slot.Dispatch.OwnerFence, ExpectedNextSlot: slot.ExpectedNextSlot,
		}
	}
	first := freeze(time.Minute, 7)
	second := freeze(2*time.Minute, 8)
	before, after := first.UnfinishedProjection(), second.UnfinishedProjection()
	if before.Contract != after.Contract || !before.DuePlanTargets.Equal(after.DuePlanTargets) ||
		before.EarliestQueryDeadlineUnixMilli != after.EarliestQueryDeadlineUnixMilli {
		t.Fatalf("frozen facts differ between the two Workers: %+v vs %+v", before, after)
	}
	if after.KeepUntilUnixMilli-before.KeepUntilUnixMilli != time.Minute.Milliseconds() || before.Equal(after) {
		t.Fatalf("KeepUntil %d -> %d, want exactly the terminal delay difference and unequal projections", before.KeepUntilUnixMilli, after.KeepUntilUnixMilli)
	}

	// The first Worker begins the Slot and disappears; the second Worker's
	// attempt supersedes the persisted projection and names the changed field.
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	var observations []observability.Observation
	store, err := progress.NewStore(progress.StoreOptions{
		Prefix: "alarmd", Control: &slotProgressControlStore{missing: true}, Slots: slotProgressResolver{catalog: catalog},
		Now: func() time.Time { return at },
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := execution.ProgressIdentity{QueryGroup: "query-group-1"}
	if result, err := store.BeginSlot(context.Background(), execution.ProgressBeginRequest{
		Identity: identity, OwnerFence: first.OwnerFence, Projection: before,
	}); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(first Worker) = (%+v, %v)", result, err)
	}
	result, err := store.BeginSlot(context.Background(), execution.ProgressBeginRequest{
		Identity: identity, OwnerFence: second.OwnerFence, Projection: after,
	})
	if err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(second Worker) = (%+v, %v), want the superseding commit", result, err)
	}
	loaded, err := store.LoadProgress(context.Background(), identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.UnfinishedSlot == nil || !loaded.Progress.UnfinishedSlot.Equal(after) {
		t.Fatalf("LoadProgress(after takeover) = (%+v, %v), want the second Worker's projection", loaded.Progress, err)
	}
	if len(observations) != 1 || observations[0].Err == nil || !strings.HasSuffix(observations[0].Err.Error(), "differing fields: keep_until") {
		t.Fatalf("superseded-projection observations = %+v, want one naming keep_until only", observations)
	}
}
