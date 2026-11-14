// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Query Group starts its next Slot in slices by its last read's size: its
// series over a slice's (sliceSeries) or its input over half its share,
// whichever asks for more. The time a read that answered took does not
// slice it - a backend slow as a whole, every request 28-30 s whatever it
// reads, is read whole however slowly it answered - and a read that ran out
// of time slices it by that time over the target.
func TestAQueryGroupStartsInSlicesByItsSizeOrAReadThatRanOutOfTime(t *testing.T) {
	coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxRetainedBytes: 1 << 30}}
	share := coordinator.qgShareBytes()
	request := func(group string) execution.SlotExecutionRequest {
		return execution.SlotExecutionRequest{Contract: execution.FrozenExecutionContractRef{
			Slot: execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(group)}}}
	}
	leaves := func(group string) int {
		reading := coordinator.sliceReadingFor(context.Background(), request(group))
		if reading.tree == nil {
			return 1
		}
		return len(reading.tree.Pending())
	}
	dimensions := []string{"host"}
	// Each cut on host before, as a Query Group read in slices or read
	// whole and sampled is (cutOnNext).
	remember := func(group string, reading slicedReading) {
		reading.dimensions, reading.cut, reading.cutFrom = dimensions, inputslice.FixedCut("host"), observability.InputSliceCutOnRootSplit
		coordinator.slicedInput.remember(execution.QueryGroupIdentity(group), reading)
	}
	// Small, and a backend that answers every request slowly: read whole.
	remember("small-slow", slicedReading{bytes: share / 4, series: 1_000})
	// Past a slice's series, a backend answering as usual: sliced.
	remember("large", slicedReading{bytes: share / 4, series: 3 * sliceSeries})
	// Small, its last read out of time: sliced by that time.
	remember("timed-out", slicedReading{bytes: share / 4, series: 1_000, timedOutMillis: 3 * sliceQueryTargetMillis})
	// Larger than half its share: sliced by its input.
	remember("wide", slicedReading{bytes: 5 * share / 2, series: 1_000})
	if got := leaves("small-slow"); got != 1 {
		t.Errorf("small-slow: %d slices, want it read whole", got)
	}
	// The tree starts with at least the slices asked for (inputslice.New).
	for group, atLeast := range map[string]int{"large": 3, "timed-out": 3, "wide": 5} {
		if got := leaves(group); got < atLeast {
			t.Errorf("%s: %d slices, want at least %d", group, got, atLeast)
		}
	}
}

// A Query Group with a shape starts its next Slot in the shape's leaves
// while it holds, and in slices counted anew once it does not - the query
// revision it was read at changed, it was cut on other dimensions than the
// Query Group is sliced on now, or it has more leaves than a starting tree
// may - the shape dropped from the memory and counted once, by why.
func TestAShapeIsReadInWhileItHoldsAndDroppedOnceItDoesNot(t *testing.T) {
	dimensions := []string{"host"}
	keptTree := func(t *testing.T, leaves int) inputslice.Shape {
		t.Helper()
		tree, err := inputslice.New(dimensions, leaves, dimensions[0])
		if err != nil {
			t.Fatal(err)
		}
		sample := inputslice.SampleOf(map[string][]string{"host": {"abcdefgh", "ijklmnop"}})
		started := tree.Leaves()
		for _, leaf := range tree.Pending() {
			if _, err := tree.SplitKept(leaf.ID, sample); err == nil {
				break
			}
		}
		if tree.Leaves() <= started {
			t.Fatalf("no kept split of a tree of %d leaves", started)
		}
		return tree.Shape()
	}
	small := keptTree(t, 4)
	if small.IsZero() {
		t.Fatal("no shape")
	}
	large := keptTree(t, 57)
	for large.Leaves() <= inputslice.MaxLeaves-3 {
		large = keptTreeOnto(t, large)
	}
	for _, test := range []struct {
		name       string
		reading    slicedReading
		revision   execution.QueryRevision
		wantShape  bool
		wantReason string
	}{
		{"holds", slicedReading{shape: small, dimensions: dimensions, revision: "r1", slices: 4}, "r1", true, ""},
		{"another revision", slicedReading{shape: small, dimensions: dimensions, revision: "r0", slices: 4}, "r1", false,
			observability.InputSliceShapeVoidedRevision},
		{"other dimensions", slicedReading{shape: small, dimensions: []string{"host", "instance"}, revision: "r1", slices: 4}, "r1", false,
			observability.InputSliceShapeVoidedDimensions},
		{"too many leaves", slicedReading{shape: large, dimensions: dimensions, revision: "r1", slices: 4}, "r1", false,
			observability.InputSliceShapeVoidedMaxLeaves},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxRetainedBytes: 1 << 30}}
			group := execution.QueryGroupIdentity("qg")
			coordinator.slicedInput.remember(group, test.reading)
			request := execution.SlotExecutionRequest{Contract: execution.FrozenExecutionContractRef{
				Slot: execution.SlotIdentity{QueryGroup: group}, QueryRevision: test.revision}}
			reading := coordinator.sliceReadingFor(context.Background(), request)
			if test.wantShape {
				if !reading.shape.started || reading.tree == nil || reading.tree.Leaves() != test.reading.shape.Leaves() {
					t.Fatalf("started %t, want the Slot read in the shape's %d leaves", reading.shape.started, test.reading.shape.Leaves())
				}
				if reading.asked != test.reading.slices {
					t.Fatalf("asked %d, want the count the shape started in, %d", reading.asked, test.reading.slices)
				}
				return
			}
			if reading.shape.started || reading.shape.dropped != test.wantReason {
				t.Fatalf("started %t dropped %q, want the shape dropped %s", reading.shape.started, reading.shape.dropped, test.wantReason)
			}
			if !coordinator.slicedInput.lastInput(group).shape.IsZero() {
				t.Fatal("the shape that did not hold is still remembered")
			}
			again := coordinator.sliceReadingFor(context.Background(), request)
			if again.shape.started || again.shape.dropped != "" {
				t.Fatalf("the next Slot started %t dropped %q, want no shape and nothing counted again", again.shape.started, again.shape.dropped)
			}
		})
	}
}

// keptTreeOnto is shape with one more slice split for its time.
func keptTreeOnto(t *testing.T, shape inputslice.Shape) inputslice.Shape {
	t.Helper()
	tree, err := inputslice.FromShape(shape.Dimensions(), shape)
	if err != nil {
		t.Fatal(err)
	}
	sample := inputslice.SampleOf(map[string][]string{"host": {"abcdefgh", "ijklmnop"}})
	for _, leaf := range tree.Pending() {
		if _, err := tree.SplitKept(leaf.ID, sample); err == nil {
			return tree.Shape()
		}
	}
	t.Fatal("no leaf of the shape could be split")
	return inputslice.Shape{}
}

// validSlotRequest is a live Slot of group at evaluation that passes
// SlotExecutionRequest.Validate, as the scheduler sends one.
func validSlotRequest(t *testing.T, group string, evaluation execution.EvaluationTime) execution.SlotExecutionRequest {
	t.Helper()
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(group), EvaluationTime: evaluation},
		SnapshotRevision: "snapshot-v1", QueryRevision: "r1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: evaluation, DuePlanSetDigest: "due-set-v1",
	}
	request := execution.SlotExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
		DuePlanTargets: execution.FrozenDuePlanTargets{DuePlanSetDigest: contractRef.DuePlanSetDigest,
			Plans: []execution.PlanKey{{PlanIdentity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: group}}}},
		EarliestQueryDeadlineUnixMilli: int64(evaluation)*1000 + 1_000,
		RecoveryUntilUnixMilli:         int64(evaluation)*1000 + 601_000,
		KeepUntilUnixMilli:             int64(evaluation)*1000 + 677_000,
		OwnerFence:                     execution.OwnerFence{QueryGroup: contractRef.Slot.QueryGroup, OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1"},
		ExpectedNextSlot:               evaluation,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("the Slot request does not validate: %v", err)
	}
	return request
}

// A Query Group not started on this replica for ten of its periods, and ten
// minutes at least, has its slice memory and its shape dropped by a Slot
// that starts, counted as evicted_idle by the next Slot that completes:
// started again, it is read as a Query Group never read here. Its period is
// the scheduler's FollowingSlot less its evaluation time; a Slot that gives
// none keeps the last one known, and a Query Group whose period was never
// known is never dropped - how often it is read is not known.
func TestAnIdleQueryGroupsSliceMemoryIsDropped(t *testing.T) {
	tree, err := inputslice.New([]string{"host"}, 4, "host")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.SplitKept(tree.Pending()[1].ID, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1_700_000_000, 0)
	now := start
	coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxRetainedBytes: 1 << 30},
		ports: Ports{Now: func() time.Time { return now }}}
	startSlot := func(group string, period time.Duration) *sliceReading {
		evaluation := execution.EvaluationTime(now.Unix())
		ctx := context.Background()
		if period > 0 {
			ctx = execution.WithFollowingSlot(ctx, evaluation+execution.EvaluationTime(period/time.Second))
		}
		return coordinator.sliceReadingFor(ctx, validSlotRequest(t, group, evaluation))
	}
	remembered := slicedReading{shape: tree.Shape(), dimensions: []string{"host"}, revision: "r1", slices: 4, series: 3 * sliceSeries}
	// An hour's period, bound at ten hours; one whose period is not known.
	for group, period := range map[string]time.Duration{"hourly": time.Hour, "unknown": 0} {
		startSlot(group, period)
		coordinator.slicedInput.remember(execution.QueryGroupIdentity(group), remembered)
	}
	// The hourly Query Group's next Slot gives no FollowingSlot: its hour is
	// kept.
	now = start.Add(time.Minute)
	startSlot("hourly", 0)
	coordinator.slicedInput.remember("hourly", remembered)
	busyAt := func(at time.Duration) {
		now = start.Add(time.Minute + at)
		startSlot("busy", time.Minute)
	}
	busyAt(59 * time.Minute)
	if evicted := coordinator.slicedInput.takeEvicted(); evicted != 0 {
		t.Fatalf("%d evicted after 59 minutes, want none within ten hours", evicted)
	}
	busyAt(10*time.Hour + time.Second)
	if evicted := coordinator.slicedInput.takeEvicted(); evicted != 1 {
		t.Fatalf("%d evicted past ten hours, want the hourly Query Group alone", evicted)
	}
	if last := coordinator.slicedInput.lastInput("hourly"); !last.shape.IsZero() || last.series != 0 {
		t.Fatalf("hourly: memory %+v kept after it was idle, want none", last)
	}
	if last := coordinator.slicedInput.lastInput("unknown"); last.shape.IsZero() {
		t.Fatal("the Query Group whose period was never known was dropped")
	}
	if reading := startSlot("hourly", time.Hour); reading.shape.started || reading.tree != nil {
		t.Fatalf("hourly started again in a shape (%t) or slices, want it read as never read here", reading.shape.started)
	}
}

// A Query Group read every ten seconds is kept ten minutes idle, not its ten
// periods (100 s): the floor bounds how short an idle drops a Query Group
// read often. Both times are a sweep's, each past the minute sweeps keep
// apart.
func TestAQueryGroupOfAShortPeriodIsKeptForTheIdleFloor(t *testing.T) {
	var trees sliceTrees
	start := time.Unix(1_700_000_000, 0)
	trees.start("often", start, 10*time.Second)
	trees.remember("often", slicedReading{series: 1})
	trees.start("busy", start.Add(2*time.Minute), time.Minute)
	if evicted := trees.takeEvicted(); evicted != 0 || trees.lastInput("often").series == 0 {
		t.Fatalf("%d evicted two minutes idle, past ten periods of ten seconds; want none within ten minutes", evicted)
	}
	trees.start("busy", start.Add(10*time.Minute+time.Second), time.Minute)
	if evicted := trees.takeEvicted(); evicted != 1 || trees.lastInput("often").series != 0 {
		t.Fatalf("%d evicted past ten minutes idle, want the ten-second Query Group", evicted)
	}
}

// The Query Groups dropped as idle are counted by the next Slot that
// completes, however many Slots started between.
func TestIdleEvictionsAreCountedByTheNextSlotThatCompletes(t *testing.T) {
	var trees sliceTrees
	start := time.Unix(1_700_000_000, 0)
	trees.start("idle", start, time.Minute)
	trees.remember("idle", slicedReading{series: 1})
	trees.start("busy", start.Add(11*time.Minute), time.Minute)
	trees.start("busy", start.Add(13*time.Minute), time.Minute)
	if evicted := trees.takeEvicted(); evicted != 1 {
		t.Fatalf("%d counted, want the one dropped", evicted)
	}
	if evicted := trees.takeEvicted(); evicted != 0 {
		t.Fatalf("%d counted again, want it counted once", evicted)
	}
}

// A shape's last kept split is merged back only after rounds that read each
// of its leaves whole and quickly: a leaf split for its size in the round
// was not read whole, and the round is no quick one.
func TestARoundThatSplitALeafOfTheLastKeptSplitForItsSizeIsNoQuickOne(t *testing.T) {
	dimensions := []string{"host"}
	tree, err := inputslice.New(dimensions, 4, dimensions[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.SplitKept(tree.Pending()[1].ID, nil); err != nil {
		t.Fatal(err)
	}
	coordinator := &SlotExecutionCoordinator{}
	read := func(splitOne bool) (*sliceReading, inputslice.Shape) {
		shaped, err := inputslice.FromShape(dimensions, tree.Shape())
		if err != nil {
			t.Fatal(err)
		}
		reading := &sliceReading{tree: shaped, dimensions: dimensions}
		for index, leaf := range shaped.LastKept() {
			if splitOne && index == 0 {
				if _, err := shaped.Split(leaf, nil); err != nil {
					t.Fatal(err)
				}
				continue
			}
			reading.noteLeafRead(inputslice.Leaf{ID: leaf}, 1_000)
		}
		return reading, tree.Shape()
	}
	before := slicedReading{shapeQuickRounds: sliceDecayRounds - 1}
	reading, shape := read(false)
	if merged, rounds := coordinator.shapeAfter(reading, before, dimensions, true); !reading.shape.merged || merged.Leaves() >= shape.Leaves() || rounds != 0 {
		t.Fatalf("every leaf whole and quick on the third round: merged %t, want the split taken back", reading.shape.merged)
	}
	reading, shape = read(true)
	if kept, rounds := coordinator.shapeAfter(reading, before, dimensions, true); reading.shape.merged || kept.Leaves() != shape.Leaves() || rounds != 0 {
		t.Fatalf("a leaf split for its size: merged %t rounds %d, want the shape kept and no quick round", reading.shape.merged, rounds)
	}
}
