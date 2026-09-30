// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func slotCompletion(group string, retained uint64) Observation {
	return Observation{Stage: StageSlotCompleted, Result: ResultSuccess, Duration: time.Millisecond, DurationKnown: true,
		Trace: TraceFields{QueryGroupKey: group, EvaluationTime: 590}, SlotBudgetUsage: &SlotBudgetUsageFacts{RetainedBytes: retained}}
}

// The census holds a reading for every Query Group that completed a Slot;
// the summary holds readings for the Query Groups its roster admitted. On a
// replica owning more than the roster's capacity the two answer differently,
// and the heartbeat has to carry the census: a Leader that never hears of a
// Query Group cannot place it, and a replica whose unread objects never
// clear is never a destination.
func TestTheCensusHoldsEveryQueryGroupTheRosterDoesNot(t *testing.T) {
	now := time.Unix(600, 0)
	clock := func() time.Time { return now }
	summary := NewCostSummary(CostSummaryOptions{ProcessID: "process-a", Window: time.Minute, GroupCapacity: 4, PlanCapacity: 8, MetadataBytes: 4096, TopN: 2, Now: clock})
	census := NewRetainedPeakCensus(time.Minute, clock)
	groups := make([]CostGroup, 0, 10)
	for i := 0; i < 10; i++ {
		groups = append(groups, CostGroup{QueryGroupKey: fmt.Sprintf("qg-%02d", i), QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r"})
	}
	summary.Reconcile(groups, true)
	for i := 0; i < 10; i++ {
		o := slotCompletion(fmt.Sprintf("qg-%02d", i), uint64(1000+i))
		summary.Observe(context.Background(), o)
		census.Observe(context.Background(), o)
	}
	if got := len(summary.RetainedPeaks()); got != 4 {
		t.Fatalf("the summary with a roster of four reads %d Query Groups; this test needs the roster to be the smaller answer", got)
	}
	peaks := census.RetainedPeaks()
	if len(peaks) != 10 {
		t.Fatalf("the census reads %d of ten Query Groups", len(peaks))
	}
	for i, peak := range peaks {
		if peak.QueryGroupKey != fmt.Sprintf("qg-%02d", i) || peak.RetainedBytesPeak != uint64(1000+i) || peak.ComputeWallNS != 0 {
			t.Fatalf("reading %d = %+v", i, peak)
		}
	}
	// For a Query Group both hold, the two read the same number the same way:
	// the larger of the two windows.
	now = now.Add(time.Minute)
	o := slotCompletion("qg-00", 500)
	summary.Observe(context.Background(), o)
	census.Observe(context.Background(), o)
	if summary.RetainedPeaks()[0].RetainedBytesPeak != 1000 || census.RetainedPeaks()[0].RetainedBytesPeak != 1000 {
		t.Fatalf("summary %d census %d, want both to keep the previous window's larger peak", summary.RetainedPeaks()[0].RetainedBytesPeak, census.RetainedPeaks()[0].RetainedBytesPeak)
	}
	now = now.Add(2 * time.Minute)
	census.Observe(context.Background(), slotCompletion("qg-00", 300))
	if got := census.RetainedPeaks()[0].RetainedBytesPeak; got != 300 {
		t.Fatalf("two windows later the census still reads %d, want the peak to have aged out to 300", got)
	}
}

// The compute wall is the evaluation and state wall, as the summary takes
// it, and a Query Group with a wall but no peak is still a reading.
func TestTheCensusTakesTheComputeWallAsTheSummaryDoes(t *testing.T) {
	now := time.Unix(600, 0)
	census := NewRetainedPeakCensus(time.Minute, func() time.Time { return now })
	for _, stage := range []Stage{StageEvaluationCompleted, StageStateApplied, StageStatePreflight} {
		census.Observe(context.Background(), Observation{Stage: stage, Duration: 2 * time.Millisecond, DurationKnown: true, Trace: TraceFields{QueryGroupKey: "qg-1"}})
	}
	// Query and run wall are not compute, and an unknown duration is counted
	// as unknown rather than as zero.
	census.Observe(context.Background(), Observation{Stage: StageQueryCompleted, Duration: time.Second, DurationKnown: true, Trace: TraceFields{QueryGroupKey: "qg-1"}})
	census.Observe(context.Background(), Observation{Stage: StageEvaluationCompleted, Trace: TraceFields{QueryGroupKey: "qg-1"}})
	peaks := census.RetainedPeaks()
	if len(peaks) != 1 || peaks[0].ComputeWallNS != int64(6*time.Millisecond) || peaks[0].ComputeWallUnknown != 1 || peaks[0].RetainedBytesPeak != 0 {
		t.Fatalf("peaks = %+v, want 6ms of compute wall, one unknown, no peak", peaks)
	}
}

// The roster prunes the census: a Query Group the replica let go is not its
// reading to report. And a Query Group with neither peak nor wall is not a
// reading at all.
func TestTheRosterPrunesTheCensus(t *testing.T) {
	now := time.Unix(600, 0)
	census := NewRetainedPeakCensus(time.Minute, func() time.Time { return now })
	census.Observe(context.Background(), slotCompletion("qg-1", 10))
	census.Observe(context.Background(), slotCompletion("qg-2", 20))
	census.Observe(context.Background(), Observation{Stage: StageSlotCompleted, Trace: TraceFields{QueryGroupKey: "qg-empty"}})
	if got := len(census.RetainedPeaks()); got != 2 {
		t.Fatalf("%d readings, want two: the Query Group with no peak and no wall is not one", got)
	}
	census.Retain([]string{"qg-2"})
	peaks := census.RetainedPeaks()
	if len(peaks) != 1 || peaks[0].QueryGroupKey != "qg-2" {
		t.Fatalf("after the roster kept qg-2 the census reads %+v", peaks)
	}
}

// A Query Group that stopped completing Slots ages out of its two windows on
// the read, exactly as the summary rotates it on Publish: the window just
// past is kept as the previous one, anything older is gone, and a group with
// nothing in either window is not a reading. Before this the census rotated
// only when a group was observed, so an idle group reported the last thing it
// did for as long as it was owned - a sum that read high on every replica
// with an idle object, in the direction of one more move.
func TestAnIdleQueryGroupAgesOutOfTheCensusOnRead(t *testing.T) {
	now := time.Unix(600, 0)
	census := NewRetainedPeakCensus(time.Minute, func() time.Time { return now })
	census.Observe(context.Background(), slotCompletion("qg-1", 900))
	now = now.Add(time.Minute)
	census.Observe(context.Background(), slotCompletion("qg-1", 100))
	// One window later: the 900 is the previous window's, still read.
	now = now.Add(time.Minute)
	if peaks := census.RetainedPeaks(); len(peaks) != 1 || peaks[0].RetainedBytesPeak != 100 {
		t.Fatalf("one window after the last Slot the census reads %+v, want the last window's 100 as the previous window", peaks)
	}
	// Two windows later: both windows are past, nothing to read.
	now = now.Add(time.Minute)
	if peaks := census.RetainedPeaks(); len(peaks) != 0 {
		t.Fatalf("two windows after the last Slot the census still reads %+v", peaks)
	}
	// And a group that skips a window loses its previous window: the
	// cross-window branch, not the adjacent one.
	census.Observe(context.Background(), slotCompletion("qg-1", 500))
	now = now.Add(2 * time.Minute)
	census.Observe(context.Background(), slotCompletion("qg-1", 50))
	if peaks := census.RetainedPeaks(); len(peaks) != 1 || peaks[0].RetainedBytesPeak != 50 {
		t.Fatalf("after a skipped window the census reads %+v, want only the current window's 50", peaks)
	}
}

// A pool refusal counts as the object's peak only for the two refusals the
// summary counts - the pool full and this object over its share - and never
// for a Slot refused for its own size, which says nothing about the pool.
func TestOnlyThePoolsOwnRefusalsCountAsAPeak(t *testing.T) {
	now := time.Unix(600, 0)
	census := NewRetainedPeakCensus(time.Minute, func() time.Time { return now })
	own := uint64(300)
	refusal := func(reason string) Observation {
		return Observation{Stage: StageResourceHard, Component: ComponentResource, CapacityBudget: CapacityBudgetRetainedBytes,
			ReasonCode: ReasonCode(reason), Trace: TraceFields{QueryGroupKey: "qg-" + reason},
			CapacityRejection: &CapacityRejectionFacts{OwnUsed: &own, Requested: 200}}
	}
	for _, reason := range []string{contract.ReasonResourceHardStop, contract.ReasonQGBudgetShareExceeded, contract.ReasonSlotBudgetExceeded} {
		census.Observe(context.Background(), refusal(reason))
	}
	peaks := census.RetainedPeaks()
	if len(peaks) != 2 {
		t.Fatalf("peaks = %+v, want the two pool refusals and not the Slot's own", peaks)
	}
	for _, peak := range peaks {
		if peak.RetainedBytesPeak != 500 {
			t.Fatalf("a pool refusal reads %+v, want own plus requested", peak)
		}
	}
}

// The census is bounded far above any owned count, and an observation past
// the bound is counted as overflow rather than admitted: a replica no roster
// has pruned cannot grow it without limit, and the count says so.
func TestTheCensusIsBoundedAndCountsWhatItDrops(t *testing.T) {
	now := time.Unix(600, 0)
	census := NewRetainedPeakCensus(time.Minute, func() time.Time { return now })
	for i := 0; i < RetainedPeakCensusMaxGroups+3; i++ {
		census.Observe(context.Background(), slotCompletion(fmt.Sprintf("qg-%d", i), 1))
	}
	if got := census.Overflow(); got != 3 {
		t.Fatalf("overflow = %d, want the three past the bound", got)
	}
	if got := len(census.RetainedPeaks()); got != RetainedPeakCensusMaxGroups {
		t.Fatalf("the census holds %d groups, want the bound", got)
	}
}
