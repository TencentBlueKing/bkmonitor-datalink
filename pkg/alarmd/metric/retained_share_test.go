// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The counter moves on completed Slots at or past the threshold of their
// share and on nothing else: one byte under it, a failed Slot, a completion
// that carried no share, and a row of another stage all leave it alone.
func TestTheShareApproachCounterCountsCompletedSlotsFromTheThreshold(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	counter := recorder.phaseTwo.retainedShareApproaching
	if got := testutil.ToFloat64(counter); got != 0 {
		t.Fatalf("before anything happened = %v, want a series at zero", got)
	}
	const share = 1_000_000
	at := uint64(share * observability.RetainedShareApproachPercent / 100)
	observe := func(stage observability.Stage, retained, share uint64, err error) {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: stage, Result: observability.ResultSuccess, Err: err,
			SlotBudgetUsage: &observability.SlotBudgetUsageFacts{RetainedBytes: retained, RetainedShareBytes: share},
		})
	}
	observe(observability.StageSlotCompleted, at-1, share, nil)
	if got := testutil.ToFloat64(counter); got != 0 {
		t.Fatalf("one byte under the threshold counted: %v", got)
	}
	observe(observability.StageSlotCompleted, at, share, nil)
	observe(observability.StageSlotCompleted, share, share, nil)
	observe(observability.StageSlotCompleted, share, share, errors.New("failed"))
	observe(observability.StageSlotCompleted, share, 0, nil)
	observe(observability.StageStateApplied, share, share, nil)
	if got := testutil.ToFloat64(counter); got != 2 {
		t.Fatalf("state_retained_share_approaching_slots_total = %v, want 2: the Slot at the threshold and the "+
			"one at the share, and none of the failed, share-less or other-stage rows", got)
	}
}

// A Slot given up because the share refused it is past the threshold and is
// counted, once per Slot given up; a Slot skipped for anything else is not.
// Without this an object refused on every Slot, which completes none, left
// the counter at zero.
func TestTheShareApproachCounterCountsSlotsGivenUpAtTheShare(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	counter := recorder.phaseTwo.retainedShareApproaching
	share := &observability.SlotSkipShareFacts{Phase: "normal_output", OwnUsedBytes: 530 << 20, LimitBytes: 512 << 20, Refusals: 2}
	for _, facts := range []observability.SlotSkippedFacts{
		{Path: observability.SlotSkippedQueryFree, Reason: "GAP_SKIPPED", Cause: observability.CompletionCauseResourceShareExceeded, SlotCount: 1, Share: share},
		{Path: observability.SlotSkippedQueryFree, Reason: "GAP_SKIPPED", Cause: observability.CompletionCauseResourceShareExceeded, SlotCount: 1, Share: share},
		{Path: observability.SlotSkippedQueryFree, Reason: "GAP_SKIPPED", Cause: "EXPIRED_REPLAY", SlotCount: 1},
	} {
		observability.ObserveSlotSkipped(context.Background(), recorder, observability.TraceFields{QueryGroupKey: "qg-101"},
			observability.OperationWrite, facts)
	}
	if got := testutil.ToFloat64(counter); got != 2 {
		t.Fatalf("state_retained_share_approaching_slots_total = %v, want 2: the two Slots given up at the share", got)
	}
}

// The counter reads the bytes alone: a Slot applied in windows at its share
// counts, as one applied whole does. Only the fleet line leaves the windowed
// Slot out.
func TestTheShareApproachCounterCountsASlotAppliedInWindows(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	counter := recorder.phaseTwo.retainedShareApproaching
	const share = 1_000_000
	for _, windows := range []uint64{0, 4} {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
			Result: observability.ResultSuccess,
			SlotBudgetUsage: &observability.SlotBudgetUsageFacts{RetainedBytes: share * 99 / 100,
				RetainedShareBytes: share, Windows: windows},
		})
	}
	if got := testutil.ToFloat64(counter); got != 2 {
		t.Fatalf("state_retained_share_approaching_slots_total = %v, want 2: the Slot applied whole and the "+
			"one applied in four windows", got)
	}
}

// A completed Slot read in slices counts its slices landed and let go, once
// as a sliced Slot, its overturned first-seen writes and how its reads
// ahead ended; a Slot read whole counts no slice.
func TestACompletedSlotCountsItsSlicesAndHowItsReadsAheadEnded(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	m := recorder.phaseTwo
	for _, usage := range []observability.SlotBudgetUsageFacts{
		{InputSlices: 1},
		{InputSlices: 4, InputSlicesDiscarded: 1, FirstSeenUnseenSlices: 1, FirstSeenUnseenSeries: 30,
			InputSlicesReadAhead: 2, InputSlicesReadAheadYielded: 1},
	} {
		usage := usage
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
			Result: observability.ResultSuccess, SlotBudgetUsage: &usage,
		})
	}
	for name, want := range map[string]float64{
		"sliced":     1,
		"landed":     4,
		"discarded":  1,
		"slices":     1,
		"series":     30,
		"replayed":   2,
		"yielded":    1,
		"other_leaf": 0,
		"failed":     0,
	} {
		var got float64
		switch name {
		case "sliced":
			got = testutil.ToFloat64(m.slicedSlots)
		case "landed", "discarded":
			got = testutil.ToFloat64(m.inputSlices.WithLabelValues(name))
		case "slices", "series":
			got = testutil.ToFloat64(m.firstSeenUnseen.WithLabelValues(name))
		default:
			got = testutil.ToFloat64(m.sliceReadAhead.WithLabelValues(name))
		}
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}
