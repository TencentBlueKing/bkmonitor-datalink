// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group whose Progress cursor rests in a closed Schedule Segment whose
// Snapshot publication is no longer retained (Catalog TTL) could never freeze
// that Slot: FreezeSlotContract failed with snapshot unavailable, the Slot was
// past its recovery window, no unfinished projection existed, and the source
// returned BLOCKED_EXACT_SET_UNAVAILABLE on every attempt for ever. Past the
// recovery window the worker finalizes such a Slot query-free without the
// Snapshot, so the source must hand it over built from the persisted Segment
// facts. Within the recovery window the freeze failure stays a retry.
func TestProductionSlotSourceFinalizesExpiredSlotQueryFreeWhenSegmentSnapshotIsUnavailable(t *testing.T) {
	end := execution.EvaluationTime(600)
	closed := schedulerSchedule(t, 60, 60, &end, "snapshot-expired", 1)
	open := schedulerSchedule(t, 60, 600, nil, "snapshot-current", 2)
	freezeErr := &controlplane.FreezeSlotContractError{Class: controlplane.FreezeSlotFailureSnapshotRead, Err: controlplane.ErrSnapshotUnavailable}
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{closed, open}, freezeErr: freezeErr}
	limits := testRecoveryLimits()
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.Unix(100_000, 0), limits)

	slot, due, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next(expired Slot in unretained Segment) = (%+v, %t, %v), want a query-free Slot", slot, due, err)
	}
	if len(catalog.requests) != 1 || catalog.requests[0].EvaluationTime != 120 {
		t.Fatalf("FreezeSlotContract requests = %+v, want one attempt for Slot 120", catalog.requests)
	}
	contractRef := slot.Contract
	if contractRef.Slot.EvaluationTime != 120 || contractRef.SnapshotRevision != "snapshot-expired" ||
		contractRef.QueryRevision != closed.Segment.QueryRevision || contractRef.ScheduleRevision != closed.Segment.ScheduleRevision ||
		contractRef.ScheduleSegmentStart != 60 || len(contractRef.DuePlanSetDigest) != 64 {
		t.Fatalf("contract = %+v, want the persisted Segment facts of Slot 120", contractRef)
	}
	if slot.ExpectedNextSlot != 120 || slot.Dispatch.Operation != execution.OperationNormal ||
		slot.Recovery.Disposition != ReplayExpired || slot.ExpiredRange != nil {
		t.Fatalf("slot dispatch=%+v recovery=%+v range=%v, want an expired normal Slot", slot.Dispatch, slot.Recovery, slot.ExpiredRange)
	}
	if !slot.DuePlanTargets.Equal(execution.FrozenDuePlanTargets{DuePlanSetDigest: contractRef.DuePlanSetDigest, Plans: []execution.PlanIdentity{planIdentity("1")}}) {
		t.Fatalf("due Plan targets = %+v, want the Segment's due Plan", slot.DuePlanTargets)
	}
	if err := slot.Validate("query-group-1"); err != nil {
		t.Fatalf("query-free Slot does not validate: %v", err)
	}
	request := execution.SlotExecutionRequest{
		Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli, RecoveryUntilUnixMilli: slot.RecoveryUntilUnixMilli,
		KeepUntilUnixMilli: slot.KeepUntilUnixMilli, ReplayExpired: true, Operation: slot.Dispatch.Operation, AttemptNo: 1,
		OwnerFence: slot.Dispatch.OwnerFence, ExpectedNextSlot: slot.ExpectedNextSlot,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("the runner request built from the query-free Slot does not validate: %v", err)
	}

	again, _, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || again.Contract != slot.Contract {
		t.Fatalf("second attempt contract = %+v error=%v, want the identical contract", again.Contract, err)
	}

	// Within the recovery window the freeze failure keeps its retry semantics
	// so a transient Snapshot read problem never skips a live Slot.
	live := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.Unix(200, 0), limits)
	_, due, _, err = live.Next(context.Background(), "query-group-1")
	var retry *SourceRetryError
	if due || !errors.As(err, &retry) || fmt.Sprintf("%T", retry.Err) != "*scheduler.slotFreezeSnapshotUnavailableFailure" {
		t.Fatalf("Next(live Slot, snapshot unavailable) due=%t error=%v, want a retry with the snapshot-unavailable class", due, err)
	}
}
