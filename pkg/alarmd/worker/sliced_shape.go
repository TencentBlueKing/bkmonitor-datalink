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
	"errors"
	"slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// shapeTree is the tree a Slot starts in from its Query Group's shape, nil
// when it has none or the shape no longer holds: the query revision it was
// read at changed, it cuts on other dimensions than the Query Group is
// sliced on now, it has more leaves than a starting tree may
// (inputslice.MaxLeaves-3, as sliceReadingFor bounds a count), or its leaves
// are not a partition of every value (inputslice.FromShape). A shape that
// does not hold is dropped from the memory as it is found, and counted once,
// by why, on the Slot that found it; the Slot starts in slices counted as
// though there had been none. A Slot read in a shape is not cut further
// before it is read for the Query Group's size (slicesBySeries, its bytes):
// a Query Group grown since is caught by its slices split for their size as
// they stream, and by a slice split for its time once a round.
func (coordinator *SlotExecutionCoordinator) shapeTree(request execution.SlotExecutionRequest, last slicedReading, reading *sliceReading) *inputslice.Tree {
	if last.shape.IsZero() {
		return nil
	}
	dropped := ""
	switch {
	case last.revision != request.Contract.QueryRevision:
		dropped = observability.InputSliceShapeVoidedRevision
	case !slices.Equal(last.shape.Dimensions(), last.dimensions):
		dropped = observability.InputSliceShapeVoidedDimensions
	case last.shape.Leaves() > inputslice.MaxLeaves-3:
		dropped = observability.InputSliceShapeVoidedMaxLeaves
	default:
		tree, err := inputslice.FromShape(last.dimensions, last.shape)
		if err == nil {
			reading.shape.started = true
			if last.seriesMicros > 0 {
				reading.leafPlanned = make(map[string]uint64, len(last.leafSeries))
				for digest, series := range last.leafSeries {
					reading.leafPlanned[digest] = series * last.seriesMicros / 1000
				}
			}
			return tree
		}
		dropped = observability.InputSliceShapeRefusedNotAPartition
		var refused *inputslice.ShapeRefusedError
		if errors.As(err, &refused) {
			dropped = shapeRefusals[refused.Reason]
		}
	}
	reading.shape.dropped = dropped
	coordinator.slicedInput.dropShape(request.Contract.Slot.QueryGroup)
	return nil
}

// shapeRefusals is the outcome each reason FromShape refuses a shape for is
// counted as. A shape cut on other dimensions is found before it is built
// (shapeTree), and counted as voided.
var shapeRefusals = map[string]string{
	inputslice.ShapeRefusedDimensions:    observability.InputSliceShapeVoidedDimensions,
	inputslice.ShapeRefusedNotALeaf:      observability.InputSliceShapeRefusedNotALeaf,
	inputslice.ShapeRefusedNotAPartition: observability.InputSliceShapeRefusedNotAPartition,
	inputslice.ShapeRefusedMaxLeaves:     observability.InputSliceShapeRefusedMaxLeaves,
}

// shapeAfter is the shape a completed Slot leaves its Query Group, and the
// quick rounds it has had. A Slot that split a slice for its time keeps that
// split, its quick rounds starting again. A Slot read in a shape and not
// split so keeps it as it was - a slice split for its size is the round's,
// and is asked again from its bytes - and after sliceDecayRounds rounds in a
// row that read every leaf its last kept split made whole and under half a
// slice's target, that split is taken back: its leaves are one again
// (inputslice.Shape.Merged), the leaves of the splits before it kept, so
// the slice that ran out of time is not read whole again while it is still
// slow. Merging a shape's every kept split back leaves the Query Group
// counted in slices, as before its first. A Slot read whole, or that is no
// longer sliceable on the dimensions its shape cut (sliceable false, or
// cut on others), leaves none.
func (coordinator *SlotExecutionCoordinator) shapeAfter(reading *sliceReading, before slicedReading, current []string, sliceable bool) (inputslice.Shape, int) {
	if reading.tree == nil || reading.unsliceable {
		return inputslice.Shape{}, 0
	}
	shape := reading.tree.Shape()
	if shape.IsZero() {
		return inputslice.Shape{}, 0
	}
	if !sliceable || !slices.Equal(current, shape.Dimensions()) {
		reading.shape.dropped = observability.InputSliceShapeVoidedDimensions
		return inputslice.Shape{}, 0
	}
	if reading.timeoutSplit {
		reading.shape.kept = true
		return shape, 0
	}
	quick := true
	for _, leaf := range reading.tree.LastKept() {
		took, read := reading.leafMillis[leaf]
		quick = quick && read && took < sliceQueryTargetMillis/2
	}
	if !quick {
		return shape, 0
	}
	if rounds := before.shapeQuickRounds + 1; rounds < sliceDecayRounds {
		return shape, rounds
	}
	reading.shape.merged = true
	return shape.Merged(), 0
}

// keepTimedOutSplit splits the slice whose read ran out of time and failed
// the Slot, when the provider said it ran out (timedOutByProvider: a read
// alarmd's deadline ended is the window's) and its time is put on its size
// (timedOutForSize, as a slice is split and read again for it), by the sample of what it delivered before it
// ran out, the fixed alphabet with none: whether it was not split - the
// cooldown held, the window left was too short - or was split and a child of
// it ran out too. The split is the tree's alone; the Slot is failing, and
// what its stream holds is let go with it.
//
// Judged under the cooldown as a split now is (cooldownHeld): while it holds,
// a slice with no other slice read before it is not put on its size, and a
// Query Group failing so is not made a level deeper each round. Before it
// holds - the scheduler holds it after unavailableThreshold (3) queries in
// a row unavailable - a Slot that fails so splits its shape twice at most:
// the slice split and read again (splitsOnTimeout), and the slice the Slot
// then fails on, split here - another leaf, as children are read after the
// leaves made before them. Three such Slots are six splits of at most
// classesPerSplit+1 (5) leaves: 24 leaves more, and a shape past a starting
// tree's leaves (inputslice.MaxLeaves-3) is dropped.
func (stream *streamedExecution) keepTimedOutSplit(leaf inputslice.Leaf) {
	reading := stream.slices
	if reading.tree == nil || reading.unsliceable || !reading.timedOutByProvider ||
		!reading.timedOutForSize(reading.timedOutTook, stream.cooldownHeld()) {
		return
	}
	if _, err := reading.tree.SplitKept(leaf.ID, reading.sample); err == nil {
		reading.keptOnFailure = true
	}
}

// shapeOnFailure is the shape a Slot that failed with a slice split for its
// size leaves its Query Group: its tree's, held to FromShape as every shape
// read in is, none when it does not hold.
func (coordinator *SlotExecutionCoordinator) shapeOnFailure(reading *sliceReading) inputslice.Shape {
	shape := reading.tree.Shape()
	if _, err := inputslice.FromShape(shape.Dimensions(), shape); err != nil {
		return inputslice.Shape{}
	}
	return shape
}

// forget takes everything the memory holds of a Query Group out of it - its
// shape, cut, slice count and times - so its next Slot reads it whole.
func (trees *sliceTrees) forget(group execution.QueryGroupIdentity) {
	trees.mu.Lock()
	defer trees.mu.Unlock()
	delete(trees.input, group)
}

// dropShape takes a Query Group's shape out of the memory, what else it
// holds kept.
func (trees *sliceTrees) dropShape(group execution.QueryGroupIdentity) {
	trees.mu.Lock()
	defer trees.mu.Unlock()
	if reading, ok := trees.input[group]; ok {
		reading.shape, reading.shapeQuickRounds = inputslice.Shape{}, 0
		trees.input[group] = reading
	}
}

// sliceShapeOutcomes is this Slot's shape usage as the counts of
// observability.InputSliceShapeOutcomes, in that order.
func (stream *streamedExecution) sliceShapeOutcomes() [len(observability.InputSliceShapeOutcomes)]uint64 {
	var counts [len(observability.InputSliceShapeOutcomes)]uint64
	if !stream.slicing() {
		return counts
	}
	usage := stream.slices.shape
	for index, outcome := range observability.InputSliceShapeOutcomes {
		if outcome == observability.InputSliceShapeEvictedIdle {
			counts[index] = stream.coordinator.slicedInput.takeEvicted()
			continue
		}
		switch {
		case outcome == observability.InputSliceShapeStarted && usage.started,
			outcome == observability.InputSliceShapeKept && usage.kept,
			outcome == observability.InputSliceShapeMerged && usage.merged,
			outcome == usage.dropped:
			counts[index] = 1
		}
	}
	return counts
}
