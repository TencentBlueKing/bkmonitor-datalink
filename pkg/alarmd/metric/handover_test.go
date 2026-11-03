// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Every outcome and refusal has a zero from the start; a handover's wait is
// counted under its outcome with its duration, and the stop's own deadline is
// not a handover outcome.
func TestAHandoverDrainIsCountedUnderItsOutcome(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	if cells := testutil.CollectAndCount(r.phaseTwo.handover.drains); cells != len(observability.SlotDrainOutcomes)-2 {
		t.Fatalf("%d drain cells from start, want every outcome but the stop's two deadline ones", cells)
	}
	if cells := testutil.CollectAndCount(r.phaseTwo.handover.outputUnapplied); cells != len(ownership.RefusalReasons) {
		t.Fatalf("%d output_unapplied cells from start, want one per refusal", cells)
	}
	r.RecordHandoverDrain(observability.SlotDrainFinished, 250*time.Millisecond)
	r.RecordHandoverDrain(observability.SlotDrainIdle, 0)
	r.RecordHandoverDrain(observability.SlotDrainDeadline, time.Second)
	if got := testutil.ToFloat64(r.phaseTwo.handover.drains.WithLabelValues(observability.SlotDrainFinished)); got != 1 {
		t.Fatalf("finished = %v, want 1", got)
	}
	if cells := testutil.CollectAndCount(r.phaseTwo.handover.drains); cells != len(observability.SlotDrainOutcomes)-2 {
		t.Fatal("a stop's deadline outcome was counted as a handover")
	}
	if count := testutil.CollectAndCount(r.phaseTwo.handover.drainSeconds); count != 1 {
		t.Fatalf("drain histogram series = %d, want 1", count)
	}
}

// output_unapplied_total counts the coordinator's line, by the refusal it
// names, and nothing else.
func TestOutputUnappliedIsCountedFromItsLine(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	r.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageOutputUnapplied,
		Result: observability.ResultFailed, ReasonCode: contract.ReasonOwnershipStaleFence,
		Counts: observability.Counts{Events: 3},
	})
	r.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageStateApplied,
		Result: observability.ResultFailed, ReasonCode: contract.ReasonOwnershipStaleFence,
	})
	if got := testutil.ToFloat64(r.phaseTwo.handover.outputUnapplied.WithLabelValues(contract.ReasonOwnershipStaleFence)); got != 1 {
		t.Fatalf("output_unapplied{OWNERSHIP_STALE_FENCE} = %v, want 1 (the state_applied refusal is not one)", got)
	}
}
