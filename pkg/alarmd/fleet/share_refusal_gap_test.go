// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Slots given up for their share read as one capacity defect of the object:
// one row, filed under RESOURCE_SHARE_EXCEEDED on the strategy's line - the
// same line the refusal itself lands on - with the refusal's own bytes
// against the share and how many Slots it has cost, not abandoned detection
// and not a silent stall. A round that completes any other way ends it.
func TestSlotsGivenUpForTheShareAreOneNamedCapacityRow(t *testing.T) {
	const group = "qg-share-101"
	d := newDefectTracker(t, group)
	trace := func(slot int64) observability.TraceFields {
		return observability.TraceFields{QueryGroupKey: group, StrategyID: "101", BusinessID: "2", EvaluationTime: slot}
	}
	giveUp := func(slot int64) {
		d.tick()
		d.tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
			ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: observability.CompletionCauseResourceShareExceeded,
			ProgressCompletionReason: "GAP_SKIPPED", Trace: trace(slot),
		})
		observability.ObserveSlotSkipped(context.Background(), d.tracker, trace(slot), observability.OperationRetry,
			observability.SlotSkippedFacts{Path: observability.SlotSkippedQueryFree, FirstSlot: slot, LastSlot: slot, SlotCount: 1,
				Reason: "GAP_SKIPPED", Cause: observability.CompletionCauseResourceShareExceeded,
				Share: &observability.SlotSkipShareFacts{Phase: "normal_output", OwnUsedBytes: 530 << 20,
					RequestedBytes: 4 << 20, LimitBytes: 512 << 20, Refusals: 2}})
	}
	rows := func() []Anomaly {
		all := d.tracker.Anomalies()
		Attribute(all, d.at.Now())
		var mine []Anomaly
		for _, candidate := range all {
			if candidate.QueryGroup == group {
				mine = append(mine, candidate)
			}
		}
		return mine
	}

	giveUp(100)
	giveUp(160)
	giveUp(220)
	listed := rows()
	if len(listed) != 1 {
		t.Fatalf("rows = %d, want one row for the object however many Slots it gave up", len(listed))
	}
	row := listed[0]
	if row.Cause != observability.CompletionCauseResourceShareExceeded || row.CauseReason != observability.CompletionCauseResourceShareExceeded {
		t.Fatalf("row cause = %q/%q, want the share named", row.Cause, row.CauseReason)
	}
	if row.Finding.Check != CheckPlanUnevaluable {
		t.Fatalf("row is under %s, want %s: the strategy has to be sharded", row.Finding.Check, CheckPlanUnevaluable)
	}
	share := row.ShareRefusal
	if share == nil || share.OwnUsedBytes != 530<<20 || share.LimitBytes != 512<<20 || share.Phase != "normal_output" ||
		share.Refusals != 2 || share.Slots != 3 || share.FirstSlot != 100 || share.LastSlot != 220 {
		t.Fatalf("share refusal = %+v, want own 530 MiB against 512 MiB over three Slots 100..220", share)
	}
	if blocked, known := failureFacets["RESOURCE_SHARE_EXCEEDED"]; !known || blocked.class != ClassCapacity {
		t.Fatalf("blocked map = %+v %t, want a capacity class", blocked, known)
	}

	// The strategy was sharded: the next round completes whole and the row
	// carries no share refusal.
	d.tick()
	d.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		ProgressCompletionKind: "FULL_COMPLETED", Trace: trace(280),
	})
	for _, after := range rows() {
		if after.ShareRefusal != nil {
			t.Fatalf("a whole round left the share refusal on the row: %+v", after.ShareRefusal)
		}
	}
}

// A run that ends takes its share refusal with it (a row is listed from
// the third degraded round of a run, hence three Slots after the restart): the next run of Slots
// given up for the share counts from its own first Slot and time, not from
// the run before.
func TestANewRunOfShareSkipsCountsFromItsOwnStart(t *testing.T) {
	const group = "qg-share-102"
	d := newDefectTracker(t, group)
	trace := func(slot int64) observability.TraceFields {
		return observability.TraceFields{QueryGroupKey: group, StrategyID: "102", BusinessID: "2", EvaluationTime: slot}
	}
	giveUp := func(slot int64) {
		d.tick()
		d.tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
			ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: observability.CompletionCauseResourceShareExceeded,
			ProgressCompletionReason: "GAP_SKIPPED", Trace: trace(slot),
		})
		observability.ObserveSlotSkipped(context.Background(), d.tracker, trace(slot), observability.OperationRetry,
			observability.SlotSkippedFacts{Path: observability.SlotSkippedQueryFree, FirstSlot: slot, LastSlot: slot, SlotCount: 1,
				Reason: "GAP_SKIPPED", Cause: observability.CompletionCauseResourceShareExceeded,
				Share: &observability.SlotSkipShareFacts{Phase: "normal_output", OwnUsedBytes: 530 << 20, LimitBytes: 512 << 20, Refusals: 1}})
	}
	giveUp(100)
	giveUp(160)
	d.tick()
	d.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		ProgressCompletionKind: "FULL_COMPLETED", Trace: trace(220),
	})
	d.tick()
	restartedAt := d.at.Now()
	giveUp(280)
	giveUp(340)
	giveUp(400)
	var share *ShareRefusalFacts
	for _, row := range d.tracker.Anomalies() {
		if row.QueryGroup == group {
			share = row.ShareRefusal
		}
	}
	if share == nil || share.Slots != 3 || share.FirstSlot != 280 || share.Since.Before(restartedAt) {
		t.Fatalf("share refusal = %+v, want three Slots from 280 since the new run", share)
	}

	// resetRun itself clears it, whichever path ends the run.
	d.tracker.mu.Lock()
	state := d.tracker.groups[group]
	state.shareRefusal = &ShareRefusalFacts{Slots: 5, FirstSlot: 100}
	d.tracker.resetRun(state)
	cleared := state.shareRefusal == nil
	d.tracker.mu.Unlock()
	if !cleared {
		t.Fatal("resetRun kept the share refusal of the run that ended")
	}
}
