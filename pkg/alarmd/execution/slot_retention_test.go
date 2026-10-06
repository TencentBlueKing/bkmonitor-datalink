// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"errors"
	"testing"
	"time"
)

func retentionSchedule(t *testing.T, start, end EvaluationTime) FrozenQueryGroupSchedule {
	t.Helper()
	segment := ScheduleSegmentFact{QueryGroup: "qg", Publication: SnapshotPublicationRef{SnapshotRevision: "rev", PublicationEpoch: 1},
		QueryRevision: "query", ScheduleRevision: "schedule", Start: start}
	if end > 0 {
		segment.End = &end
	}
	return FrozenQueryGroupSchedule{Segment: segment, Plans: []FrozenPlanSchedule{
		{Identity: PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "1"}, ScheduleRevision: "a", Spec: ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC"}},
		{Identity: PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "2"}, ScheduleRevision: "b", Spec: ScheduleSpec{EvaluationIntervalSeconds: 300, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 600}},
	}}
}

func TestSegmentKeepUntilIsTheLatestSlotKeepUntilTheSchedulerWouldDerive(t *testing.T) {
	retention := SlotRetention{QueryReserve: 5 * time.Second, MaxReplayAge: 10 * time.Minute, TerminalDelay: 30 * time.Second}
	schedule := retentionSchedule(t, 600, 900)
	keepUntil, err := SegmentKeepUntilUnixMilli(schedule, retention)
	if err != nil {
		t.Fatal(err)
	}
	// Slots at 600 (both Plans), 660, 720, 780, 840 (the 60 s Plan). At 600
	// the 60 s Plan's deadline (655 s) is the earlier one; at 840 it is
	// 895 s, the latest of the Segment, so the Segment is read until
	// 895 s + 10 min + 30 s.
	want := int64((840 + 60 - 5 + 600 + 30)) * 1000
	if keepUntil != want {
		t.Fatalf("keepUntil=%d want %d", keepUntil, want)
	}
	// Each Slot's value is the scheduler's own derivation, taken through the
	// same two functions.
	deadline, err := SlotQueryDeadlineUnixMilli(schedule, 840, retention.QueryReserve)
	if err != nil {
		t.Fatal(err)
	}
	_, slotKeepUntil, err := SlotRecoveryBoundaries(deadline, retention.MaxReplayAge, retention.TerminalDelay)
	if err != nil || slotKeepUntil != want {
		t.Fatalf("slot keepUntil=(%d,%v) want %d", slotKeepUntil, err, want)
	}
}

func TestSegmentKeepUntilRefusesOpenSegmentsAndInvalidRetention(t *testing.T) {
	retention := SlotRetention{QueryReserve: 5 * time.Second, MaxReplayAge: 10 * time.Minute, TerminalDelay: 30 * time.Second}
	if _, err := SegmentKeepUntilUnixMilli(retentionSchedule(t, 600, 0), retention); err == nil {
		t.Fatal("an open Segment has no keep-until instant")
	}
	for _, invalid := range []SlotRetention{{}, {QueryReserve: time.Second}, {QueryReserve: time.Second, MaxReplayAge: time.Second}} {
		if _, err := SegmentKeepUntilUnixMilli(retentionSchedule(t, 600, 900), invalid); !errors.Is(err, ErrSlotRetentionInvalid) {
			t.Fatalf("retention %+v: err=%v", invalid, err)
		}
	}
	if _, err := SlotQueryDeadlineUnixMilli(retentionSchedule(t, 600, 900), 630, 5*time.Second); !errors.Is(err, ErrSlotDeadlineDrift) {
		t.Fatalf("a time no Plan is aligned at must have no deadline: %v", err)
	}
	if _, err := SlotQueryDeadlineUnixMilli(retentionSchedule(t, 600, 900), 600, 2*time.Minute); !errors.Is(err, ErrSlotDeadlineDrift) {
		t.Fatalf("a reserve that consumes the whole completion offset must be refused: %v", err)
	}
}
