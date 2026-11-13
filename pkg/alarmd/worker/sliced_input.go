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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot whose input does not fit its share is read in slices (decision-020
// batch 8 §7.2-7.5): every physical query of the Slot is run once a slice
// with the slice's conditions, the PRIMARY and its dependencies alike, so a
// series is read whole in exactly one slice and needs no join. A slice is
// evaluated, written and let go before the next one is read: its windows land
// as any window does, its inputs are dropped, its series stay in the present
// sets; the Slot's no-data judgement, its completion-only Plans and its tail
// come after the last slice.
//
// A slice whose input passes its limit while it streams is let go there and
// split by the next character from the end of its values (inputslice), the
// children read in its place in the same attempt; the slices already written
// are not touched, so the slices stay disjoint. The first slice is the whole
// Query Group unless this replica's last reading of it says more are needed.

// errSliceOverflow is a slice's input past its limit while it streams: the
// slice is let go and split, not the Slot refused. The access layer reads it
// too (execution.ErrSliceOverflow): what the lookback captured of the slice
// is discarded.
var errSliceOverflow = execution.ErrSliceOverflow

// errSliceDimensionsGone is a Slot started in slices on dimensions its
// queries no longer all slice on (execution.InternalExecutionHeader
// .SliceableDimensions): the tree is let go at Begin, before any query is
// sent, and the Slot is read whole (sliceReading.forgetDimensions).
var errSliceDimensionsGone = errors.New("alarmd worker: input slice dimensions no longer sliceable")

// dimensionsGone reports whether the tree this read started in cuts on a
// dimension the header's queries do not all slice on: inputslice.New holds
// its caller to every dimension being a raw group-by label of every
// physical query, and the dimensions came from the memory of a read before.
func (reading *sliceReading) dimensionsGone(header execution.InternalExecutionHeader) bool {
	if reading.tree == nil {
		return false
	}
	sliceable, err := header.SliceableDimensions()
	if err != nil {
		return true
	}
	for _, dimension := range reading.dimensions {
		if !slices.Contains(sliceable, dimension) {
			return true
		}
	}
	return false
}

// forgetDimensions lets go a tree on dimensions gone: the Slot is read
// whole, its sample choosing what the next is cut on, and a shape it was
// started in is dropped as cut on other dimensions. The size its memory
// holds stays.
func (reading *sliceReading) forgetDimensions() {
	if reading.shape.started {
		reading.shape.started, reading.shape.dropped = false, observability.InputSliceShapeVoidedDimensions
	}
	reading.tree, reading.dimensions, reading.cutFrom, reading.deferred, reading.asked = nil, nil, "", nil, 1
	reading.starting = nil
}

// sliceSampleValues bounds the values a slice keeps of each dimension it may
// be split on, the sample its split classes are drawn from.
const sliceSampleValues = 4096

// sliceReading is the Slot read slice by slice.
type sliceReading struct {
	tree *inputslice.Tree
	// dimensions are the ones the tree splits on, in the order it takes them.
	dimensions []string
	leaf       inputslice.Leaf
	// index is how many slices have completed; last is whether the slice
	// being read is the last one pending.
	index int
	last  bool
	// input is what the slice being read holds of the input phase, largest
	// the most one of its series did, and sample its values on the
	// dimensions.
	input, largest uint64
	// series is the series the slice being read reserved of the pool, and
	// read the input every slice that landed held, in all; seriesRead the
	// series every slice that landed held, in all. timedOutTook is how long
	// the slice that ran out of time took: when the provider said it ran out
	// (timedOutByProvider, a providerTimeoutError), counted at no less than
	// twice a slice's target, the backend's own limit; when alarmd's deadline
	// ended it, as long as it took.
	series, read, seriesRead, timedOutTook uint64
	sample                                 inputslice.Sample
	seen                                   map[string]map[string]int
	// unsliceable is a Slot found, at its first overflow, to have nothing it
	// can be sliced on: it is read whole and judged by its share alone, as
	// before slicing.
	unsliceable bool
	// unsupported is why it cannot be sliced (execution.InputSliceReason*).
	unsupported string
	// leaveInput ends the input phase of a slice after the first.
	leaveInput func()
	// landed is how many series of each Plan the slices written so far held,
	// and landedSlices how many of those slices held any.
	landed, landedSlices map[execution.PlanIdentity]uint64
	// sliceSeries is the series of each Plan the slice being read holds,
	// counted when its series are prepared: a window cut halfway through the
	// slice lets go of the series it wrote (dropWindowInputs), and what is
	// left at the slice's end is not what it held.
	sliceSeries map[execution.PlanIdentity]uint64
	// unseenSlices and unseenSeries are the slices, and their series, a Plan
	// judged first-seen wrote before a later slice found it had run.
	unseenSlices, unseenSeries uint64
	// queryMillis is the time this Slot's slices' queries took on their
	// critical path so far (slowestQueryMillis, summed over the slices read).
	queryMillis uint64
	// sliceMillis is each slice's read so far, and plannedSliceMillis what
	// the last Slot's reading gives each of this Slot's first slices; a slice
	// that runs out of time is put on its size against them
	// (timedOutForSize). timeoutSplit is set once a slice that ran out of
	// time was split: a Slot splits so once.
	sliceMillis        []uint64
	plannedSliceMillis uint64
	timeoutSplit       bool
	// asked is how many slices this read was started in (sliceReadingFor):
	// what it remembers it was read in, not the slices its tree landed - a
	// tree holds at least the slices asked, and a slice split while it
	// streamed is the input's to ask again from its bytes.
	asked int
	// leafMillis is each slice's read by its leaf, for the decay of a shape
	// (shapeDecay). shape is how this read's start and end came to the
	// Query Group's shape, its usage (observability.InputSliceShapeOutcomes).
	leafMillis map[int]uint64
	shape      sliceShapeUsage
	// leafSeries is the series each slice read held, by its digest, and
	// leafPlanned what each slice of a Slot read in a shape is expected to
	// take, by its digest (shapeTree): the series that slice held the round
	// before at the time a series took then.
	leafSeries  map[string]uint64
	leafPlanned map[string]uint64
	// keptOnFailure is set once the slice of a Slot that failed for running
	// out of time was split for its size into the tree (keepTimedOutSplit).
	keptOnFailure bool
	// timedOutByProvider is set when the read that ran out of time ran out by
	// the provider's word (providerTimeoutError): the only time out put on a
	// slice's size. One alarmd's deadline ended is the window's, not the
	// slice's.
	timedOutByProvider bool
	// unknownSize is a Query Group with no reading on this replica: its size
	// is not known. learnedMillis is the time its whole read took when the
	// provider timed it out (providerTimeout): what the next Slot is sliced by.
	unknownSize   bool
	learnedMillis uint64
	// wholeTimedOut is a read whole the provider timed out: it delivered
	// nothing, its size is the read before's, and with no sample its next
	// read is cut on the first dimension (cutOnNext).
	wholeTimedOut bool
	// readSlices is the slices that completed, and targetMissingSlices those
	// of them every physical query of which the provider answered with its
	// target missing (targetMissingCompletion): a read all of whose slices
	// were is no reading of the Query Group's size, and the memory of it is
	// let go (forget).
	readSlices, targetMissingSlices int
	// cutFrom is where the dimension this read's starting slices were cut
	// on came from (observability.InputSliceCutOnSources), empty for a read
	// not started in slices counted. unsampled is a read whole found to have
	// nothing it can be sliced on, which samples nothing.
	cutFrom   string
	unsampled bool
	// deferred is the slices, by digest, the provider timed out the Slot
	// before and that completed degraded: read after the others, so that
	// the slices before them are what their time is judged against
	// (timedOutForSize). degraded is this read's own, for the next, and
	// deferredRead how many slices read last for it landed.
	deferred, degraded map[string]bool
	deferredRead       uint64
	// starting is the leaves this read started in from its memory - counted
	// on the cut a read before chose, or in its shape - and startDiscarded
	// whether one of them was let go for its size: the cut remembered did not
	// fit the input, as a sample that saw only the start of the stream may
	// not (its values' last characters sorted slowest).
	starting       map[int]bool
	startDiscarded bool
}

// sliceShapeUsage is what one Slot did with its Query Group's shape: read
// in it, kept a split in it, merged a split of it back, or dropped it, why
// (one of voided and refused at most, the words of
// observability.InputSliceShapeOutcomes).
type sliceShapeUsage struct {
	started, kept, merged bool
	dropped               string
}

func (reading *sliceReading) noteLeafRead(leaf inputslice.Leaf, took uint64) {
	if reading.leafMillis == nil {
		reading.leafMillis, reading.leafSeries = map[int]uint64{}, map[string]uint64{}
	}
	reading.leafMillis[leaf.ID] = took
	reading.leafSeries[leaf.Slice.Digest()] = reading.series
}

// plannedLeafMillis is what the slice being read of a Slot read in a shape
// is expected to take: its series the round before at the time a series
// took then, a slice's target when that is not known - a leaf the round
// before did not read whole.
func (reading *sliceReading) plannedLeafMillis() uint64 {
	if planned, known := reading.leafPlanned[reading.leaf.Slice.Digest()]; known {
		return planned
	}
	return sliceQueryTargetMillis
}

// slicing reports whether this Slot is read in slices.
func (stream *streamedExecution) slicing() bool { return stream.slices != nil }

// readInSlices reports whether this Slot's input is being read as more than
// one slice: a Slot read whole, its one read the Query Group, is not, and
// what its completion says of a Plan without series is the completion-only
// path's to decide.
func (stream *streamedExecution) readInSlices() bool {
	return stream.slicing() && stream.slices.tree != nil
}

// sliceLimit is what the slice being read may hold of the input phase: half
// of what its share leaves beside everything else the Slot holds, the other
// half for the windows that write it.
func (stream *streamedExecution) sliceLimit() uint64 {
	share := stream.coordinator.qgShareBytes()
	if share == 0 {
		return ^uint64(0)
	}
	others := stream.retainedTotal() - min(stream.retainedTotal(), stream.slices.input)
	if others >= share {
		return 0
	}
	return (share - others) / 2
}

// admitSliceInput judges one series batch of retained bytes against the
// slice's limit before it is reserved. A batch that takes the slice past its
// limit lets the slice go (errSliceOverflow) - unless one series the slice
// has seen alone does not fit the limit, which no split mends: the Slot is
// then refused by its share there (QG_BUDGET_SHARE_EXCEEDED, 7.3), with what
// it holds on the row, rather than split until K_max and named for its
// values, or read on whole into the half of the share its windows need. A
// Slot with nothing to slice on is read whole and judged by its share alone,
// as before slicing.
func (stream *streamedExecution) admitSliceInput(ctx context.Context, batch execution.SeriesExecutionBatch, retained uint64) error {
	if !stream.slicing() {
		return nil
	}
	if stream.slices.unsliceable {
		return nil
	}
	reading := stream.slices
	reading.largest = max(reading.largest, retained)
	limit := stream.sliceLimit()
	if reading.input+retained <= limit {
		return nil
	}
	if reading.tree == nil {
		if _, err := stream.sliceDimensions(); err != nil {
			reading.unsliceable = true
			reading.unsupported, _ = execution.InputSliceUnsupportedOf(err)
			return nil
		}
	}
	if reading.largest > limit {
		coordinator := stream.coordinator
		err := shareRejection(stream.reservationPhase("normal_input"), stream.retainedTotal(), retained,
			coordinator.qgShareBytes(), stream.ownBudgetUsage(coordinator.budget))
		coordinator.observeCapacityRejection(ctx, stream.request.Operation, observability.CapacityBudgetRetainedBytes, err)
		return err
	}
	stream.sampleSlice(batch)
	return errSliceOverflow
}

// sliceDimensions is what the Slot's physical queries can be sliced on, by
// their frozen facts, the PRIMARY requirements' first so the order is the
// Plan's own group-by (execution.InternalExecutionHeader.SliceableDimensions).
func (stream *streamedExecution) sliceDimensions() ([]string, error) {
	return stream.header.SliceableDimensions()
}

// noteSliceInput counts a batch reserved for the slice being read.
func (stream *streamedExecution) noteSliceInput(batch execution.SeriesExecutionBatch, retained uint64) {
	if !stream.slicing() || stream.slices.unsliceable {
		return
	}
	stream.slices.input += retained
	stream.slices.series += batch.Delivery.Series
	stream.sampleSlice(batch)
}

// sampleSlice keeps a batch's values on the slice's dimensions, from its
// PRIMARY Dataset: a dependency's series are handed on without dimensions.
func (stream *streamedExecution) sampleSlice(batch execution.SeriesExecutionBatch) {
	reading := stream.slices
	feedsPrimary := false
	for _, binding := range batch.Inputs {
		feedsPrimary = feedsPrimary || binding.Role == execution.InputRolePrimary
	}
	record, ok := batch.Dataset.Record(0)
	if !feedsPrimary || !ok {
		return
	}
	if len(reading.dimensions) == 0 {
		// A read whole samples too: the dimension its sample splits most
		// evenly is what its next read, in slices, is cut on
		// (rememberSlotInput), and what its own first split cuts.
		if reading.unsampled {
			return
		}
		sliceable, err := stream.sliceDimensions()
		if err != nil {
			reading.unsampled = true
			return
		}
		reading.dimensions = sliceable
	}
	dimensions := record.Dimensions()
	if reading.sample == nil {
		reading.sample = make(inputslice.Sample, len(reading.dimensions))
		reading.seen = make(map[string]map[string]int, len(reading.dimensions))
	}
	// Each value once, counted for every series sampled that carries it -
	// a split is judged by the series its parts hold - the values past
	// sliceSampleValues left out.
	for _, dimension := range reading.dimensions {
		value := sliceValue(dimensions[dimension])
		seen := reading.seen[dimension]
		if seen == nil {
			seen = make(map[string]int)
			reading.seen[dimension] = seen
		}
		if at, dup := seen[value]; dup {
			reading.sample[dimension][at].Series++
			continue
		}
		if len(reading.sample[dimension]) >= sliceSampleValues {
			continue
		}
		seen[value] = len(reading.sample[dimension])
		reading.sample[dimension] = append(reading.sample[dimension], inputslice.SampledValue{Value: value, Series: 1})
	}
}

// sliceValue is a dimension's value as a label matcher reads it: the string
// a JSON string holds, a missing or null dimension the empty value, and any
// other scalar its own text.
func sliceValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return string(raw)
}

// beginSlice readies the stream for the next slice's read: the streamed
// state of the slice before it has been dropped as its windows landed, and
// what is left of it is the Slot's.
func (stream *streamedExecution) beginSlice(leaf inputslice.Leaf, last bool) {
	reading := stream.slices
	reading.leaf, reading.last = leaf, last
	reading.input, reading.largest, reading.series, reading.sample, reading.seen = 0, 0, 0, nil, nil
	reading.sliceSeries = nil
	if reading.index > 0 {
		reading.leaveInput = stream.enter(slotPhaseInput)
	}
}

// abandonSlice lets go everything the slice being read streamed, for its
// children to read again, and returns the sample it kept.
func (stream *streamedExecution) abandonSlice() inputslice.Sample {
	reading := stream.slices
	if reading.leaveInput != nil {
		reading.leaveInput()
		reading.leaveInput = nil
	}
	stream.unretainInput(reading.input)
	stream.coordinator.releaseProvisional(reading.series, 0)
	stream.series -= min(stream.series, reading.series)
	stream.resetSliceStream()
	sample := reading.sample
	reading.input, reading.series, reading.sample, reading.seen = 0, 0, nil, nil
	reading.sliceSeries = nil
	return sample
}

// resetSliceStream clears the per-slice streamed state: the next slice's
// series arrive into empty maps, Begin having run once for the Slot.
func (stream *streamedExecution) resetSliceStream() {
	stream.streamed = make(map[streamedInputKey]streamedInput)
	stream.shared = sharedBindings{}
	stream.planSeries = make(map[execution.PlanIdentity]map[execution.SeriesIdentityDigest]struct{})
	stream.inputHolds = nil
	stream.delivered = nil
	stream.hostOverlays = nil
}

// foldSliceCompletion folds one slice's completion into the Slot's: each
// physical query at the worst completeness any slice answered it with, its
// deliveries summed.
func foldSliceCompletion(slot, slice execution.QueryExecutionCompletion) (execution.QueryExecutionCompletion, error) {
	if len(slot.PhysicalQueries) == 0 {
		return slice, nil
	}
	index := make(map[execution.PhysicalQueryDigest]int, len(slot.PhysicalQueries))
	for position, query := range slot.PhysicalQueries {
		index[query.PhysicalQuery] = position
	}
	for _, query := range slice.PhysicalQueries {
		position, found := index[query.PhysicalQuery]
		if !found {
			slot.PhysicalQueries = append(slot.PhysicalQueries, query)
			continue
		}
		folded := &slot.PhysicalQueries[position]
		// A slice that held none of the query's series delivered nothing:
		// its zero delivery adds nothing to fold, and is not a delivery
		// AccumulateSeriesDelivery takes.
		delivery := folded.Delivery
		if query.Delivery.Series != 0 {
			var err error
			if delivery, err = execution.AccumulateSeriesDelivery(folded.Delivery, query.Delivery); err != nil {
				return execution.QueryExecutionCompletion{}, fmt.Errorf("alarmd worker: fold slice delivery: %w", err)
			}
		}
		if completenessRank(query.Completeness) > completenessRank(folded.Completeness) {
			*folded = query
		}
		folded.Delivery = delivery
	}
	slot.CompletionBindings = slice.CompletionBindings
	slot.AllRequiredCompleted = slot.AllRequiredCompleted && slice.AllRequiredCompleted
	if slot.InPlaceWait == nil {
		slot.InPlaceWait = slice.InPlaceWait
	}
	return slot, nil
}

func completenessRank(completeness execution.Completeness) int {
	switch completeness {
	case execution.CompletenessUnavailable:
		return 2
	case execution.CompletenessPartial:
		return 1
	default:
		return 0
	}
}

// sliceTrees keeps, by Query Group, the input bytes this replica read of it
// on its last completed Slot: the reading the next Slot's first slices are
// counted from.
//
// A Query Group not started on this replica for sliceMemoryIdlePeriods of
// its periods, and no less than sliceMemoryIdleFloor, is dropped from it
// with its shape: a strategy deleted, a Query Group moved to another
// replica, and the memory only ever growing are all ended so, with no word
// from the scheduler. The Slots that start sweep it, at most once
// sliceMemorySweepEvery; evicted is how many it dropped not yet counted,
// the next Slot to complete counting them.
type sliceTrees struct {
	mu      sync.Mutex
	input   map[execution.QueryGroupIdentity]slicedReading
	started map[execution.QueryGroupIdentity]sliceStart
	swept   time.Time
	evicted uint64
}

// sliceStart is when a Query Group last started a Slot on this replica, and
// its period as last known: the Slot after it, by the scheduler's
// FollowingSlot, less its own evaluation time. A Query Group whose period
// was never known is never dropped: how often it is read is not known, so
// it cannot be judged idle - its entries stay, as every Query Group's did
// before, and a Slot of a live Query Group gives its FollowingSlot.
type sliceStart struct {
	at     time.Time
	period time.Duration
}

const (
	// sliceMemoryIdlePeriods is how many of its periods a Query Group's
	// memory is kept without a Slot started, and sliceMemoryIdleFloor the
	// least it is kept for, whatever its period.
	sliceMemoryIdlePeriods = 10
	sliceMemoryIdleFloor   = 10 * time.Minute
	// sliceMemorySweepEvery is how often the Slots that start look for the
	// idle.
	sliceMemorySweepEvery = time.Minute
)

// start notes group's Slot started at now, its period period (zero when
// not known, its last known one kept), and drops every other Query Group
// idle past its bound.
func (trees *sliceTrees) start(group execution.QueryGroupIdentity, now time.Time, period time.Duration) {
	trees.mu.Lock()
	defer trees.mu.Unlock()
	if trees.started == nil {
		trees.started = make(map[execution.QueryGroupIdentity]sliceStart)
	}
	if period <= 0 {
		period = trees.started[group].period
	}
	trees.started[group] = sliceStart{at: now, period: period}
	if !trees.swept.IsZero() && now.Sub(trees.swept) < sliceMemorySweepEvery {
		return
	}
	trees.swept = now
	for other, last := range trees.started {
		if last.period <= 0 || now.Sub(last.at) <= max(sliceMemoryIdlePeriods*last.period, sliceMemoryIdleFloor) {
			continue
		}
		delete(trees.started, other)
		if _, remembered := trees.input[other]; remembered {
			delete(trees.input, other)
			trees.evicted++
		}
	}
}

// takeEvicted is the Query Groups dropped as idle not yet counted, now
// counted: taken as a Slot's usage is made, at the exits that report it
// (budgetUsage), so an exit that reports none leaves them for the next.
func (trees *sliceTrees) takeEvicted() uint64 {
	trees.mu.Lock()
	defer trees.mu.Unlock()
	evicted := trees.evicted
	trees.evicted = 0
	return evicted
}

// slotPeriod is the Query Group's period as its Slot's run gives it: the
// scheduler's FollowingSlot (execution.WithFollowingSlot) less this Slot's
// evaluation time, zero when it gives none.
func slotPeriod(ctx context.Context, request execution.SlotExecutionRequest) time.Duration {
	evaluation, next := request.Contract.Slot.EvaluationTime, execution.FollowingSlotOf(ctx)
	if next <= evaluation {
		return 0
	}
	return time.Duration(next-evaluation) * time.Second
}

// slicedReading is one Query Group's last read on this replica: its input
// bytes and series, the dimensions it is sliced on, and for a read that ran
// out of time the time it took on its critical path (timedOutMillis), zero
// for one that completed. slices is how many slices the next read starts in
// at least - what the reads before it learned, a read out of time among
// them - and quickRounds how many completed reads in a row took under half a
// slice's target on every slice, after sliceDecayRounds of which it is one
// fewer.
//
// shape is the leaves a slice that ran out of time for its size was split
// into, and the starting slices it was split among
// (inputslice.Tree.Shape): the next read starts in them, not in slices
// counted anew - a tree started in a count is cut by the fixed alphabet,
// shallowest first, and would hold that slice whole again, to run out of
// time and be split once a round. revision is the Query Group's query
// revision the shape was read at, and shapeQuickRounds how many completed
// reads in a row read every leaf of its last kept split whole under half a
// slice's target, after sliceDecayRounds of which that split is taken back.
// A shape lives in this process only: lost on a restart, the Query Group
// runs out of time once more and is split so again.
type slicedReading struct {
	bytes            uint64
	series           uint64
	dimensions       []string
	timedOutMillis   uint64
	slices           int
	quickRounds      int
	shape            inputslice.Shape
	revision         execution.QueryRevision
	shapeQuickRounds int
	// leafSeries is the series each slice of the last completed read held,
	// by its digest, and seriesMicros the time a series of it took on its
	// critical path: what the next Slot read in the shape plans each slice
	// to take.
	leafSeries   map[string]uint64
	seriesMicros uint64
	// cut is how the next read's starting slices are cut - the dimension
	// and the classes of its root (inputslice.Cut) - and cutFrom where it
	// came from (observability.InputSliceCutOnSources): the cut the last
	// read's tree had its root split by, or the one the sample of the last
	// read, read whole, split most evenly; the zero Cut when no read chose.
	cut     inputslice.Cut
	cutFrom string
	// deferred is the slices, by digest, the provider timed out in the last
	// read and that completed degraded (sliceReading.deferred). Lost with
	// this memory on a restart: such a Query Group degrades a round more.
	deferred map[string]bool
}

// sliceDecayRounds is how many completed reads in a row, every slice under
// half a slice's target, a Query Group's learned slices are kept before it
// starts in one fewer: a Query Group sliced for a read that ran out of time
// is not read whole again the round after its slices answered - it would run
// out again, every other round - and slices it no longer needs go one at a
// time. Three, as the query cooldown counts its rounds
// (scheduler.unavailableThreshold): three rounds is what this module takes
// as a Query Group's way of reading, not one round's chance.
const sliceDecayRounds = 3

func (trees *sliceTrees) lastInput(group execution.QueryGroupIdentity) slicedReading {
	trees.mu.Lock()
	defer trees.mu.Unlock()
	return trees.input[group]
}

func (trees *sliceTrees) remember(group execution.QueryGroupIdentity, reading slicedReading) {
	trees.mu.Lock()
	defer trees.mu.Unlock()
	if trees.input == nil {
		trees.input = make(map[execution.QueryGroupIdentity]slicedReading)
	}
	reading.dimensions = append([]string(nil), reading.dimensions...)
	trees.input[group] = reading
}

// sliceSeries is how many series one slice of a Query Group is cut to read
// before it is read (decision-020 throughput design 3.1, way 6): the series
// UQ answers in sliceQueryTargetMillis at the rate the line read of it,
// 16.5 s at P95 for the largest Query Group's 96,000 series - 0.172 ms a
// series, 87,000 series in 15 s. A Query Group whose last read held more
// starts its next Slot in as many slices as its series over this. The
// series, not the time a read took: a backend slow as a whole is as slow for
// a slice as for the Query Group, and slicing for its time would read the
// same slow answer once a slice, one after another. Read again when the line
// reads UQ otherwise.
const sliceSeries = 87_000

// slicesBySeries is how many slices a read of that many series asks for.
func slicesBySeries(series uint64) int {
	return int((series + sliceSeries - 1) / sliceSeries)
}

// sliceQueryTargetMillis is the time one slice's query is cut to take: half
// the 30 s a provider's backend gives one request (UQ's VictoriaMetrics
// timeout, its default). A Query Group whose last read ran out of time - and
// only then: a read that answered is within the backend's limit, however
// slow - starts its next Slot in as many slices as that time over this
// target, a read that ran out counted at no less than twice it
// (decision-020 throughput design 3.1, way 6).
const sliceQueryTargetMillis = 15_000

// slicesByTime is how many slices the time of a read's queries asks for at
// sliceQueryTargetMillis each.
func slicesByTime(queryMillis uint64) int {
	return int((queryMillis + sliceQueryTargetMillis - 1) / sliceQueryTargetMillis)
}

// sliceTimeoutError is a Slot's query failure after one of its slices ran
// out of time, carrying how that slice ended for the query line
// (observability.InputSliceTimeoutOutcomes).
type sliceTimeoutError struct {
	err     error
	outcome string
}

func (failure *sliceTimeoutError) Error() string { return failure.err.Error() }
func (failure *sliceTimeoutError) Unwrap() error { return failure.err }

// sliceTimeoutsSplit is the slices of this Slot split for running out of
// time, for a Slot that completed: one or none.
func (stream *streamedExecution) sliceTimeoutsSplit() uint64 {
	if stream.slicing() && stream.slices.timeoutSplit {
		return 1
	}
	return 0
}

// splitsOnTimeout says whether the slice the provider timed out
// (providerTimeoutError), after took milliseconds - its query's time, at
// least the backend's 30 s - is split and its children read in its place. A
// read alarmd's deadline ended is never asked: the window was short, not the
// slice large. A slice runs out of
// time more often because the backend is slow than because the slice is
// large, and splitting one sends two requests more to a slow backend: so only
// when all hold - the cooldown does not hold, the time is put on the slice's
// size (timedOutForSize), the window left reads its children at the Slot's
// width, and the Slot has not split so before.
func (stream *streamedExecution) splitsOnTimeout(ctx context.Context, took uint64) bool {
	reading := stream.slices
	if reading == nil || reading.unsliceable || reading.timeoutSplit || stream.cooldownHeld() {
		return false
	}
	if !reading.timedOutForSize(took, stream.cooldownHeld()) {
		return false
	}
	// The children read what the slice did, at least that slice's time,
	// read width at a time beside the slices left.
	if deadline, bounded := ctx.Deadline(); bounded {
		width := uint64(max(stream.coordinator.sliceReadWidth(), 1))
		estimate := max(took, reading.plannedSliceMillis)
		if time.Until(deadline) < time.Duration(estimate/width)*time.Millisecond {
			return false
		}
	}
	return true
}

// timedOutForSize says whether a slice's read that ran out of time after
// took milliseconds is put on its size: it took well over the slices read
// before it in the Slot (twice their median), or with none read before it,
// the last Slot's reading gives each slice more than sliceQueryTargetMillis.
// Neither, and the backend is as likely slow as the slice large.
//
// A slice of a Slot read in a shape with no slice read before it is the one
// reading there is: its time is put on its size against its plan only while
// the query cooldown does not hold (cooldown). A backend slow as a whole
// times a first read out at the 30 s it is counted at whatever its plan,
// and the cooldown is there for that backend; a Query Group in it is not
// split deeper on one read's word.
func (reading *sliceReading) timedOutForSize(took uint64, cooldown bool) bool {
	if len(reading.sliceMillis) > 0 {
		sorted := append([]uint64(nil), reading.sliceMillis...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		return took >= 2*sorted[len(sorted)/2]
	}
	if reading.shape.started {
		if cooldown {
			return false
		}
		// A slice of a shape is the first read of the Slot: put on its size
		// when it took twice what it was planned to, as a slice is against
		// the median of those before it.
		return took >= 2*reading.plannedLeafMillis()
	}
	return reading.plannedSliceMillis > sliceQueryTargetMillis
}

// cooldownHeld reports whether the Query Group's query cooldown holds as
// this Slot runs (SlotExecutionRequest.QueryCooldownHeld): what a slice out
// of time is judged under, whether to split it now (splitsOnTimeout) and
// whether to split it into the tree when the Slot fails (keepTimedOutSplit).
func (stream *streamedExecution) cooldownHeld() bool { return stream.request.QueryCooldownHeld }

// providerTimeoutError is a slice's read the provider answered as out of
// time - UQ's 504 or 408 before the query's deadline (providerTimedOut) -
// whose time is put on the slice's size (providerTimeout): the read is let
// go and the slice split, or the Slot fails as a read out of time does. took
// is its query's time, at no less than the backend's 30 s.
type providerTimeoutError struct {
	took    uint64
	request *execution.RequestFacts
}

func (*providerTimeoutError) Error() string {
	return "alarmd worker: the provider answered the slice's query as out of time"
}

func (*providerTimeoutError) QueryFailure() (string, string) {
	return observability.QueryFailureCategoryProviderTransport, contract.ReasonQueryTimeout
}

func (err *providerTimeoutError) QueryFailureRequest() *execution.RequestFacts { return err.request }

// providerTimedOut is the time a read's query took when the provider itself
// answered it as out of time, and its request: a physical query completed
// UNAVAILABLE whose attempt was answered 504 or 408, or whose request says
// provider_status, before the query's deadline. A query alarmd's deadline
// ended - deadline_passed, transport_class_timeout, or an answer at or past
// the deadline - is none: the provider never said so. Only an answer like
// this comes back as a completion; every timeout that comes back as an
// error is the deadline's (sliceReadTimedOut).
func providerTimedOut(completion execution.QueryExecutionCompletion) (uint64, *execution.RequestFacts, bool) {
	for _, query := range completion.PhysicalQueries {
		if query.Completeness != execution.CompletenessUnavailable {
			continue
		}
		for _, attempt := range query.RouteFacts.Attempts {
			status, answered := attempt.HTTPStatus, attempt.Request
			if answered != nil && answered.AnswerStatus != 0 {
				status = answered.AnswerStatus
			}
			said := status == http.StatusGatewayTimeout || status == http.StatusRequestTimeout ||
				answered != nil && answered.TimeoutSource == observability.QueryTimeoutSourceProviderStatus
			if !said || answered != nil && answered.DeadlineUnixMilli > 0 && answered.AnsweredUnixMilli >= answered.DeadlineUnixMilli {
				continue
			}
			return query.Stats.QueryMillis, answered, true
		}
	}
	return 0, nil, false
}

// providerTimeout is how a read the provider timed out (providerTimedOut,
// after took) goes on: a providerTimeoutError when its time is put on the
// slice's size - as a slice is (timedOutForSize), the cooldown not holding -
// for the Slot to split the slice or fail; nil when it is not, the read
// completing UNAVAILABLE as it came and the Slot degraded. A Query Group of
// unknown size read whole and timed out so, the cooldown not holding, is
// read in slices next (learnedMillis): the backend's limit over a slice's
// target, two slices, the time being all it says of the size.
func (stream *streamedExecution) providerTimeout(took uint64, answered *execution.RequestFacts) error {
	reading := stream.slices
	took = max(took, 2*sliceQueryTargetMillis)
	if reading != nil && reading.tree == nil {
		// What happened, whatever it is put on: under the cooldown too.
		reading.wholeTimedOut = true
	}
	if reading == nil || reading.unsliceable || stream.cooldownHeld() {
		return nil
	}
	if reading.tree == nil {
		if reading.unknownSize {
			reading.learnedMillis = took
		}
		return nil
	}
	if !reading.timedOutForSize(took, false) {
		return nil
	}
	return &providerTimeoutError{took: took, request: answered}
}

// timedOutTook is how long a read that ran out of time took, and whether
// the provider said it ran out: a providerTimeoutError's time, at least the
// backend's 30 s; any other's as long as it took (elapsed), alarmd's
// deadline having ended it - a read ahead failed past the deadline and read
// again takes nothing, and is no 30 s read.
func timedOutTook(err error, elapsed time.Duration) (uint64, bool) {
	var provider *providerTimeoutError
	if errors.As(err, &provider) {
		return provider.took, true
	}
	return uint64(elapsed.Milliseconds()), false
}

// sliceReadTimedOut says whether a slice's read ended because it ran out of
// time: its deadline passed, or the query failed as a timeout or as a
// delivery past its deadline - every one alarmd's deadline - or the provider
// timed it out and its time is put on its size (providerTimeoutError).
func sliceReadTimedOut(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var diagnostic interface{ QueryFailure() (string, string) }
	if errors.As(err, &diagnostic) {
		_, code := diagnostic.QueryFailure()
		return code == contract.ReasonQueryTimeout || code == contract.ReasonQueryDeliveryTimeout
	}
	return false
}

// slowestQueryMillis is the time a slice's read took on its critical path:
// its physical queries run side by side, so the slowest of them.
func slowestQueryMillis(completion execution.QueryExecutionCompletion) uint64 {
	var slowest uint64
	for _, query := range completion.PhysicalQueries {
		slowest = max(slowest, query.Stats.QueryMillis)
	}
	return slowest
}

// landSlice writes what is left of a slice before the last, as a window of
// its own, and lets its inputs go: the next slice is read into a Slot that
// holds only what it keeps whole. A Slot whose windows could not begin -
// its activations unread - cannot be read in slices and is retried.
func (stream *streamedExecution) landSlice(ctx context.Context) error {
	reading := stream.slices
	if reading.landed == nil {
		reading.landed = make(map[execution.PlanIdentity]uint64)
		reading.landedSlices = make(map[execution.PlanIdentity]uint64)
	}
	for plan, series := range reading.sliceSeries {
		reading.landed[plan] += series
		reading.landedSlices[plan]++
	}
	reading.sliceSeries = nil
	stream.windows.reached = len(stream.windows.prepared)
	if err := stream.cutWindow(ctx); err != nil {
		return err
	}
	if stream.windows.single {
		return errors.New("alarmd worker: a Slot read in slices could not begin its windows")
	}
	stream.windows.prepared, stream.windows.reached, stream.windows.dropped = nil, 0, 0
	return nil
}

// applySliceHead is a slice's own head, after the first (§7.5): in a
// Sequence of its own before the slice's first events, the open statements
// its series propose are folded into each Plan's marker - grown, never
// shrunk, so every event written is covered by a marker already down - and
// the first-seen judgement is carried on: a Plan judged first-seen that this
// slice finds had run is judged seen from this slice on, warming up, and the
// slices it wrote before as first-seen - the ones that held series of it; a
// slice that held none wrote nothing of it - and their series are counted on
// the completion row.
func (coordinator *SlotExecutionCoordinator) applySliceHead(ctx context.Context, stream *streamedExecution) error {
	windows := &stream.windows
	if !windows.started || windows.apply == nil {
		return nil
	}
	opens, err := stream.headOpenStatements()
	if err != nil {
		return err
	}
	apply := windows.apply
	return coordinator.ports.Sequencer.Sequence(ctx, sequencingScope(stream.header, nil, stream.gapItems), func(sequenceCtx context.Context) error {
		defer stream.enter(slotPhaseCut)()
		if candidates := firstSeenCandidates(apply.forced, stream.gaps); len(candidates) != 0 {
			before := stream.unseenPlans()
			if err := stream.firstSeenPrepass(sequenceCtx, candidates); err != nil {
				return err
			}
			after := stream.unseenPlans()
			if len(after) != len(before) {
				for key := range before {
					if _, still := after[key]; still {
						continue
					}
					for _, due := range stream.header.DuePlans {
						if due.Key() == key && stream.slices.landed[due.Identity] > 0 {
							stream.slices.unseenSlices += stream.slices.landedSlices[due.Identity]
							stream.slices.unseenSeries += stream.slices.landed[due.Identity]
						}
					}
				}
				apply.judgeForced(after)
			}
		}
		for _, due := range sortedDuePlans(stream.header.DuePlans) {
			if apply.changed(due) {
				apply.withheldNoData[due.Identity] = struct{}{}
				continue
			}
			open, found := opens[due.Identity]
			if !found {
				continue
			}
			if previous, written := windows.headStatements[due.Identity]; written {
				grown, grows, err := growOpenStatement(previous, open)
				if err != nil {
					return err
				}
				if !grows {
					continue
				}
				open = grown
			} else if err := coordinator.admit(sequenceCtx, stream.request, due); err != nil {
				return err
			}
			statement := execution.PlanEvaluationResult{Plan: due.Identity, GuardBeforeEvents: []execution.PlanGapMutation{open}}
			if _, err := apply.planGapsBefore(sequenceCtx, due, &statement); err != nil {
				return err
			}
			if windows.headOpened == nil {
				windows.headOpened = make(map[execution.PlanIdentity]struct{})
				windows.headStatements = make(map[execution.PlanIdentity]execution.PlanGapMutation)
			}
			windows.headOpened[due.Identity] = struct{}{}
			windows.headStatements[due.Identity] = open
		}
		return nil
	})
}

// growOpenStatement is the statement that grows the marker an earlier
// slice's head left (previous) by what this slice's open adds, and whether
// it adds anything. It is a second statement at the Slot's version, so it
// says only what the store extends a marker with at its own version
// (execution.ExtendSameSlotGap): a scope the marker does not hold, more
// FULL Slots on one it does, or a worse reason on one it does (UNAVAILABLE
// over PARTIAL, execution.GapReasonRank). Fewer FULL Slots or another reason
// of the same rank is no growth - the store refuses either alone as a
// CONFLICT - so the marker never shrinks and every event written stays
// under it.
func growOpenStatement(previous, open execution.PlanGapMutation) (execution.PlanGapMutation, bool, error) {
	if previous.Identity != open.Identity || previous.ScheduleRevision != open.ScheduleRevision ||
		execution.CompareApplyVersion(previous.ApplyVersion, open.ApplyVersion) != execution.ApplyVersionEqual {
		return execution.PlanGapMutation{}, false, errors.New("alarmd worker: two slices' open statements address different markers")
	}
	scopes := append([]execution.GapScopeMutation(nil), previous.Scopes...)
	grows := false
	for _, proposed := range open.Scopes {
		held := -1
		for index := range scopes {
			if scopes[index].Scope == proposed.Scope {
				held = index
			}
		}
		switch {
		case held < 0:
			scopes = append(scopes, proposed)
			grows = true
		default:
			if proposed.RequiredFullSlots > scopes[held].RequiredFullSlots {
				scopes[held].RequiredFullSlots = proposed.RequiredFullSlots
				grows = true
			}
			// A later slice answering a held scope worse: the worse reason
			// is what the marker says (execution.GapReasonRank).
			if execution.GapReasonRank(proposed.ReasonCode) > execution.GapReasonRank(scopes[held].ReasonCode) {
				scopes[held].ReasonCode = proposed.ReasonCode
				grows = true
			}
		}
	}
	if !grows {
		return previous, false, nil
	}
	sort.Slice(scopes, func(left, right int) bool {
		if scopes[left].Scope.HasLevel != scopes[right].Scope.HasLevel {
			return !scopes[left].Scope.HasLevel
		}
		return scopes[left].Scope.LevelID < scopes[right].Scope.LevelID
	})
	// The marker as the earlier head left it: one revision on.
	grown, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity: previous.Identity, ExpectedMarkerRevision: previous.ExpectedMarkerRevision + 1,
		ApplyVersion: previous.ApplyVersion, ScheduleRevision: previous.ScheduleRevision, Scopes: scopes,
	})
	if err != nil {
		return execution.PlanGapMutation{}, false, fmt.Errorf("alarmd worker: grow a slice's open statement: %w", err)
	}
	return grown, true, nil
}

// sliceReadingFor is how this Slot's input is read: slice by slice from its
// first read on, the first slice the whole Query Group, unless this
// replica's last completed Slot of it read more input than one slice may
// hold, in which case the first read is already the slices that reading
// asks for (§7.4). A supplement is read whole: it reads late series of a
// Slot already decided.
func (coordinator *SlotExecutionCoordinator) sliceReadingFor(ctx context.Context, request execution.SlotExecutionRequest) *sliceReading {
	if request.Supplement != nil {
		return nil
	}
	reading := &sliceReading{}
	coordinator.slicedInput.start(request.Contract.Slot.QueryGroup, coordinator.now(), slotPeriod(ctx, request))
	// A query cooldown's probe asks only whether the Query Group can be read
	// again: read whole, one request a query, not in the slices a Query
	// Group read before holds - each of those is one more request the
	// cooldown is there to stop, and a target that is missing is missing in
	// every slice of it. Read past its limit, it is split as any read whole.
	if request.QueryCooldownProbe {
		return reading
	}
	share := coordinator.qgShareBytes()
	last := coordinator.slicedInput.lastInput(request.Contract.Slot.QueryGroup)
	reading.unknownSize = last.series == 0 && last.bytes == 0
	reading.deferred = last.deferred
	dimensions := last.dimensions
	if len(dimensions) == 0 {
		return reading
	}
	// As many slices as the last read's input over half the share, its
	// series over a slice's (sliceSeries), or - a read that ran out of
	// time - its time over the target, whichever asks for most: a Query
	// Group within its share may still be too large to read in one request.
	if tree := coordinator.shapeTree(request, last, reading); tree != nil {
		reading.noteStarting(tree)
		// The slices the Query Group was last read in, a slice that ran out
		// of time split among them; the count the shape started in is what
		// it falls back to once its kept splits are merged back.
		reading.tree, reading.dimensions = tree, dimensions
		reading.asked = max(last.slices, 1)
		return reading
	}
	leaves := max(slicesBySeries(last.series), slicesByTime(last.timedOutMillis), last.slices)
	if share > 0 && last.bytes > 0 {
		leaves = max(leaves, int((last.bytes+share/2-1)/(share/2)))
	}
	// The starting slices are cut on the dimension a read chose. A Query
	// Group no read delivered a series of - its read whole the provider
	// timed out - has no sample to choose by, and its first dimension is all
	// there is; any other with none chosen is read whole, and its sample
	// chooses.
	on, from := last.cut, last.cutFrom
	if !slices.Contains(dimensions, on.Dimension) {
		on, from = inputslice.Cut{}, ""
		if last.series == 0 && last.bytes == 0 {
			on, from = inputslice.FixedCut(dimensions[0]), observability.InputSliceCutOnFirstDimension
		}
	}
	if on.IsZero() {
		leaves = 1
	}
	reading.plannedSliceMillis = last.timedOutMillis / uint64(max(leaves, 1))
	reading.asked = max(leaves, 1)
	if leaves <= 1 {
		return reading
	}
	tree, err := inputslice.NewCut(dimensions, min(leaves, inputslice.MaxLeaves-3), on)
	if err != nil {
		return reading
	}
	reading.tree, reading.dimensions, reading.cutFrom = tree, dimensions, from
	reading.noteStarting(tree)
	return reading
}

// noteStarting keeps the leaves a read starts in from its memory.
func (reading *sliceReading) noteStarting(tree *inputslice.Tree) {
	reading.starting = make(map[int]bool)
	for _, leaf := range tree.Pending() {
		reading.starting[leaf.ID] = true
	}
}

// slicesDeferred is the slices of this Slot read last for the provider
// having timed them out the Slot before (sliceReading.deferred).
func (stream *streamedExecution) slicesDeferred() uint64 {
	if !stream.slicing() {
		return 0
	}
	return stream.slices.deferredRead
}

// sliceStartDiscarded is 1 when a slice this Slot started in from its
// memory was let go for its size.
func (stream *streamedExecution) sliceStartDiscarded() uint64 {
	if stream.slicing() && stream.slices.startDiscarded {
		return 1
	}
	return 0
}

// sliceCutFrom is where the dimension this Slot's starting slices were cut
// on came from, empty for a Slot not started in slices counted.
func (stream *streamedExecution) sliceCutFrom() string {
	if !stream.slicing() {
		return ""
	}
	return stream.slices.cutFrom
}

// sliceDimension is the dimension this Slot's slices were cut on at their
// root, however they started, empty for a Slot read whole.
func (stream *streamedExecution) sliceDimension() string {
	if !stream.readInSlices() {
		return ""
	}
	return stream.slices.tree.RootCut().Dimension
}

// cutOnNext is the dimension the next read's starting slices are cut on,
// and where it came from: the one this read's tree had its root split on;
// for a read whole, the one its sample splits most evenly; failing both, the
// one before carried, while it is one of the dimensions; and only failing
// that, for a read whole that ran out of time (timedOut, or the provider's,
// wholeTimedOut) before any series reached its sample, the first dimension
// - there is no sample to choose by, and read whole again it would run out
// again, every round.
func (reading *sliceReading) cutOnNext(dimensions []string, before slicedReading, timedOut bool) (inputslice.Cut, string) {
	if reading.tree != nil && slices.Equal(reading.dimensions, dimensions) {
		if cut := reading.tree.RootCut(); !cut.IsZero() {
			return cut, observability.InputSliceCutOnRootSplit
		}
	}
	if reading.tree == nil && slices.Equal(reading.dimensions, dimensions) {
		if cut, ok := inputslice.BestCut(dimensions, reading.sample); ok {
			return cut, observability.InputSliceCutOnWholeSample
		}
	}
	if slices.Equal(before.dimensions, dimensions) && slices.Contains(dimensions, before.cut.Dimension) {
		return before.cut, before.cutFrom
	}
	if reading.tree == nil && len(reading.sample) == 0 && (timedOut || reading.wholeTimedOut) && len(dimensions) > 0 {
		return inputslice.FixedCut(dimensions[0]), observability.InputSliceCutOnFirstDimension
	}
	return inputslice.Cut{}, ""
}

// pending is the slices left to read, in the order to read them: the
// whole Query Group while no slice has been split off it; the tree's, the
// slices the provider timed out the Slot before (deferred) after the rest.
// A slice the provider times out with no slice read before it cannot be put
// on its size - the backend may be slow as a whole - and, read first again,
// never would be: read after the others, it is judged against them, as any
// slice is. Every reader of the order - the next slice, the reads ahead,
// the last - takes it from here, by leaf.
func (reading *sliceReading) pending() []inputslice.Leaf {
	if reading.tree == nil {
		return []inputslice.Leaf{{}}
	}
	pending := reading.tree.Pending()
	if len(reading.deferred) == 0 {
		return pending
	}
	ordered := make([]inputslice.Leaf, 0, len(pending))
	var last []inputslice.Leaf
	for _, leaf := range pending {
		if reading.deferred[leaf.Slice.Digest()] {
			last = append(last, leaf)
		} else {
			ordered = append(ordered, leaf)
		}
	}
	return append(ordered, last...)
}

// noteDegradedTimeout keeps the slice being read, which the provider timed
// out and which completes degraded, for the next Slot to read after the
// others (deferred).
func (reading *sliceReading) noteDegradedTimeout() {
	if reading == nil || reading.tree == nil {
		return
	}
	if reading.degraded == nil {
		reading.degraded = make(map[string]bool)
	}
	reading.degraded[reading.leaf.Slice.Digest()] = true
}

// splitSlice lets the slice being read go and splits it, the children read
// in its place. The first split of a Slot read whole makes the tree, on the
// dimensions every physical query of the Slot groups by. It returns why the
// Slot cannot be read in slices when it cannot: no dimension to split on
// (execution.InputSliceUnsupportedError), or a slice that splitting cannot
// make fit (inputslice.ErrValueConcentrated).
func (stream *streamedExecution) splitSlice(leaf inputslice.Leaf) error {
	return stream.splitSliceAs(leaf, false)
}

// splitSliceKept is splitSlice for a slice split for its time: the split is
// kept in the Query Group's shape, for its next Slot to be read split too
// (inputslice.Tree.SplitKept).
func (stream *streamedExecution) splitSliceKept(leaf inputslice.Leaf) error {
	return stream.splitSliceAs(leaf, true)
}

func (stream *streamedExecution) splitSliceAs(leaf inputslice.Leaf, kept bool) error {
	reading := stream.slices
	if !kept && reading.starting[leaf.ID] {
		reading.startDiscarded = true
	}
	sample := stream.abandonSlice()
	if reading.tree == nil {
		dimensions, err := stream.sliceDimensions()
		if err != nil {
			return err
		}
		tree, err := inputslice.New(dimensions, 1, "")
		if err != nil {
			return err
		}
		reading.tree, reading.dimensions = tree, dimensions
		// The root is cut by the cut its sample makes (inputslice.BestCut):
		// the dimension and the classes that split its series most evenly.
		// That cut is what this Slot's retry and the Slot after it start in
		// (RootCut, NewCut), the same root slices - first-seen by slice, the
		// markers and the replays hold to them. Only with no sample to cut by
		// is the first dimension cut by the fixed alphabet, and the row says
		// so (first_dimension_fallback).
		if cut, ok := inputslice.BestCut(dimensions, sample); ok {
			if _, err := tree.SplitCut(tree.Pending()[0].ID, cut); err != nil {
				return fmt.Errorf("alarmd worker: split input slice: %w", err)
			}
			return nil
		}
		reading.cutFrom = observability.InputSliceCutOnFirstDimension
		leaf, sample = tree.Pending()[0], nil
	}
	split := reading.tree.Split
	if kept {
		split = reading.tree.SplitKept
	}
	if _, err := split(leaf.ID, sample); err != nil {
		return fmt.Errorf("alarmd worker: split input slice: %w", err)
	}
	return nil
}

// notePrepared counts the series of each Plan the slice being read holds,
// as its series are prepared (sliceSeries).
func (reading *sliceReading) notePrepared(prepared []preparedSeries) {
	for _, entry := range prepared {
		if entry.due == nil {
			continue
		}
		if reading.sliceSeries == nil {
			reading.sliceSeries = make(map[execution.PlanIdentity]uint64)
		}
		reading.sliceSeries[entry.due.Identity]++
	}
}

// landSliceRead counts the slice just completed as landed and reports
// whether it was the last.
func (reading *sliceReading) landSliceRead(leaf inputslice.Leaf) bool {
	reading.read += reading.input
	reading.seriesRead += reading.series
	if reading.tree == nil {
		return true
	}
	_ = reading.tree.Land(leaf.ID)
	if reading.deferred[leaf.Slice.Digest()] {
		reading.deferredRead++
	}
	reading.index++
	return len(reading.tree.Pending()) == 0
}

// noteSliceCompletion counts a slice that completed, and whether the
// provider answered its target missing for every physical query of it.
func (reading *sliceReading) noteSliceCompletion(completion execution.QueryExecutionCompletion) {
	reading.readSlices++
	if targetMissingCompletion(completion) {
		reading.targetMissingSlices++
	}
}

// everySliceTargetMissing is whether every slice the Slot read completed
// with its target missing.
func (reading *sliceReading) everySliceTargetMissing() bool {
	return reading.readSlices > 0 && reading.targetMissingSlices == reading.readSlices
}

// targetMissingCompletion is whether the provider answered every physical
// query of completion with its target missing: UNAVAILABLE, attributed
// QUERY_TARGET_MISSING (access/uq's SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS with
// no series).
func targetMissingCompletion(completion execution.QueryExecutionCompletion) bool {
	if len(completion.PhysicalQueries) == 0 {
		return false
	}
	for _, item := range completion.PhysicalQueries {
		if item.Completeness != execution.CompletenessUnavailable {
			return false
		}
		if code, _ := physicalFailureReason(item.RouteFacts); code != execution.ReasonCode(contract.ReasonQueryTargetMissing) {
			return false
		}
	}
	return true
}

// rememberSlotInput keeps what this Slot read, for the next Slot of its
// Query Group on this replica to count its first slices from: the bytes and
// series its slices held in all, the dimensions it split on, and, for a read
// that ran out of time (timedOut), the time its queries took. A read that
// ran out keeps the series the Query Group's read before held, where it read
// fewer: it did not read them all.
func (coordinator *SlotExecutionCoordinator) rememberSlotInput(request execution.SlotExecutionRequest, stream *streamedExecution, timedOut bool) {
	if !stream.slicing() {
		return
	}
	reading := stream.slices
	dimensions := reading.dimensions
	// What the Query Group is sliced on now, by its header: the dimensions
	// it would be split on when read whole, and the ones a shape is held to.
	current, sliceable := stream.sliceDimensions()
	if sliceable == nil && (len(dimensions) == 0 || !slices.Equal(current, dimensions)) {
		dimensions = current
	}
	before := coordinator.slicedInput.lastInput(request.Contract.Slot.QueryGroup)
	remembered := slicedReading{bytes: reading.read, series: reading.seriesRead, dimensions: dimensions,
		revision: request.Contract.QueryRevision}
	remembered.cut, remembered.cutFrom = reading.cutOnNext(dimensions, before, timedOut)
	remembered.deferred = reading.degraded
	if timedOut {
		remembered.bytes = max(remembered.bytes, before.bytes)
		remembered.series = max(remembered.series, before.series)
		remembered.slices = before.slices
		// A read that ran out of time keeps the shape it started in, unless
		// the slice that ran out is put on its size: then the tree it was
		// read in, that slice split too (keepTimedOutSplit), is the Query
		// Group's next, a Slot that failed so read a level deeper each time
		// it is read again - its retry too, as a retry after a read out of
		// time in slices counted is read in more of them (slicesByTime) -
		// until it completes or grows past a starting tree's leaves.
		remembered.shape, remembered.shapeQuickRounds = before.shape, before.shapeQuickRounds
		remembered.leafSeries, remembered.seriesMicros = before.leafSeries, before.seriesMicros
		// A read that failed read none of its slices to the end: what the
		// one before deferred is deferred still, with what this one did.
		for digest := range before.deferred {
			if remembered.deferred == nil {
				remembered.deferred = make(map[string]bool)
			}
			remembered.deferred[digest] = true
		}
		if remembered.revision != before.revision {
			remembered.shape = inputslice.Shape{}
		}
		if reading.keptOnFailure {
			remembered.shape, remembered.shapeQuickRounds = coordinator.shapeOnFailure(reading), 0
		}
		// The time slices the next read only when the provider said the
		// read ran out (timedOutByProvider) and it is put on the slice's size
		// (timedOutForSize, as a slice is split for it), or the Query Group's
		// size is not known yet - its first read. A backend slow as a whole
		// times out a small Query Group as it does a large one, and slicing
		// for it would read the same slow answer once a slice; a read
		// alarmd's deadline ended says only that the window was short.
		if reading.timedOutByProvider && (reading.timedOutForSize(reading.timedOutTook, stream.cooldownHeld()) || before.series == 0 && before.bytes == 0) {
			remembered.timedOutMillis = reading.queryMillis
		}
	} else {
		// The slices this read was read in are what the next starts in at
		// least, one fewer after sliceDecayRounds quick reads in a row.
		slices := max(reading.asked, 1)
		quick := true
		for _, took := range reading.sliceMillis {
			quick = quick && took < sliceQueryTargetMillis/2
		}
		remembered.slices = slices
		// A Query Group of unknown size the provider timed out read whole
		// completed degraded, and is read in slices by that time next
		// (providerTimeout).
		remembered.timedOutMillis = reading.learnedMillis
		// A read whole the provider timed out read nothing: it keeps the
		// size the read before saw, as a read that failed does, so a Query
		// Group known large is still read in slices next, on the dimension
		// chosen above, and one known small whole.
		if reading.wholeTimedOut {
			remembered.bytes = max(remembered.bytes, before.bytes)
			remembered.series = max(remembered.series, before.series)
		}
		remembered.shape, remembered.shapeQuickRounds = coordinator.shapeAfter(reading, before, current, sliceable == nil)
		if !remembered.shape.IsZero() && reading.seriesRead > 0 {
			remembered.leafSeries = reading.leafSeries
			remembered.seriesMicros = reading.queryMillis * 1000 / reading.seriesRead
		}
		if remembered.shape.IsZero() {
			if quick && slices > 1 {
				remembered.quickRounds = before.quickRounds + 1
			}
			if remembered.quickRounds >= sliceDecayRounds {
				remembered.slices, remembered.quickRounds = slices-1, 0
			}
		}
	}
	coordinator.slicedInput.remember(request.Contract.Slot.QueryGroup, remembered)
}

// InputSliceRefusal names why a Slot that has to be read in slices cannot be,
// when err says so: the store, the query or a dimension it cannot be sliced
// on (INPUT_SLICE_UNSUPPORTED), or values no slice can hold
// (INPUT_SLICE_VALUE_CONCENTRATED).
func InputSliceRefusal(err error) (string, bool) {
	if _, unsupported := execution.InputSliceUnsupportedOf(err); unsupported {
		return contract.ReasonInputSliceUnsupported, true
	}
	if errors.Is(err, inputslice.ErrValueConcentrated) {
		return contract.ReasonInputSliceValueConcentrated, true
	}
	var tooLong *inputSliceQueryTooLongError
	if errors.As(err, &tooLong) {
		return contract.ReasonInputSliceQueryTooLong, true
	}
	return "", false
}

// slicesRead, slicesDiscarded, unseenSlices and unseenSeries are the
// completion row's counts of how the Slot's input was read.
func (stream *streamedExecution) slicesRead() uint64 {
	if !stream.slicing() {
		return 0
	}
	if stream.slices.tree == nil {
		return 1
	}
	return uint64(stream.slices.index)
}

func (stream *streamedExecution) slicesDiscarded() uint64 {
	if !stream.slicing() || stream.slices.tree == nil {
		return 0
	}
	return uint64(stream.slices.tree.Discarded())
}

func (stream *streamedExecution) unseenSlices() uint64 {
	if !stream.slicing() {
		return 0
	}
	return stream.slices.unseenSlices
}

func (stream *streamedExecution) unseenSeries() uint64 {
	if !stream.slicing() {
		return 0
	}
	return stream.slices.unseenSeries
}

// sliceCeilingFactor is how many times its first slice's windows a Slot's
// deadline may move: once for each slice read and each let go, at most
// 2*K_max-1 - every query's round trips when the tree is as split as it may
// be. One for a Slot read whole.
func (stream *streamedExecution) sliceCeilingFactor() uint64 {
	if !stream.slicing() || stream.slices.tree == nil {
		return 1
	}
	return min(uint64(stream.slices.tree.Leaves()+stream.slices.tree.Discarded()), 2*inputslice.MaxLeaves-1)
}

// sliceQueryTooLong is a slice whose read the store refused as longer than
// it takes: the slice's conditions made the query so, and the Slot cannot
// be read in that slice (INPUT_SLICE_QUERY_TOO_LONG). A read of the whole
// Query Group refused so is the query's own length, and stays the query's
// rejection.
func sliceQueryTooLong(slice execution.InputSlice, completion execution.QueryExecutionCompletion) error {
	if slice.IsZero() {
		return nil
	}
	for _, query := range completion.PhysicalQueries {
		for _, attempt := range query.RouteFacts.Attempts {
			if attempt.Rejection != nil && attempt.Rejection.Class == execution.RejectionClassQueryTooLong {
				return &inputSliceQueryTooLongError{bytes: slice.ConditionBytes()}
			}
		}
	}
	return nil
}

// inputSliceQueryTooLongError is a slice whose conditions, of bytes bytes,
// made its query longer than the store takes.
type inputSliceQueryTooLongError struct{ bytes int }

func (err *inputSliceQueryTooLongError) Error() string {
	return fmt.Sprintf("alarmd worker: the store refused an input slice's query as too long (the slice's conditions are %d bytes)", err.bytes)
}

// unsliceableShareRefusal is a Slot that could not be sliced refused by its
// share: the share's refusal, its facts and its finalization as they are
// (DeterministicShareRefusal), named for why the Slot was read whole
// (INPUT_SLICE_UNSUPPORTED with the reason on the query failure line).
type unsliceableShareRefusal struct {
	unsupported execution.InputSliceUnsupportedError
	share       error
}

func (err *unsliceableShareRefusal) Error() string {
	return err.unsupported.Error() + ", read whole: " + err.share.Error()
}

func (err *unsliceableShareRefusal) Unwrap() []error {
	return []error{&err.unsupported, err.share}
}

// nameUnsliceableRefusal is err, a refusal of the input the Slot is
// reading, named for why the Slot was read whole when it could not be
// sliced and its share refused it.
func (stream *streamedExecution) nameUnsliceableRefusal(err error) error {
	var exceeded *provisionalBudgetExceededError
	if !stream.slicing() || !stream.slices.unsliceable || !errors.As(err, &exceeded) || !exceeded.share {
		return err
	}
	return &unsliceableShareRefusal{unsupported: execution.InputSliceUnsupportedError{Reason: stream.slices.unsupported}, share: err}
}

// finalSliceRefusal is err, a Slot whose slices no split separates
// (INPUT_SLICE_VALUE_CONCENTRATED) or whose slice the store will not take
// (INPUT_SLICE_QUERY_TOO_LONG), as the deterministic refusal the scheduler
// finalizes a Slot on, the way it does one past its share: the same window
// read again splits the same way and is refused the same way, and every
// retry reads the slices landed before it again. Its code and why stay on
// the refusal (execution.ShareRefusal Reason and Detail). Any other error
// is err.
func (stream *streamedExecution) finalSliceRefusal(err error) error {
	word, sliced := InputSliceRefusal(err)
	if !sliced || word == contract.ReasonInputSliceUnsupported || !stream.slicing() {
		return err
	}
	refusal := execution.ShareRefusal{Phase: "input_slice", OwnUsedBytes: stream.slices.read,
		LimitBytes: stream.coordinator.qgShareBytes(), Reason: word}
	var concentrated *inputslice.ValueConcentratedError
	var tooLong *inputSliceQueryTooLongError
	switch {
	case errors.As(err, &concentrated):
		refusal.Detail = concentrated.Reason + " on " + concentrated.Dimension
	case errors.As(err, &tooLong):
		refusal.Detail = fmt.Sprintf("conditions of %d bytes", tooLong.bytes)
	}
	return &finalSliceRefusalError{err: err, refusal: refusal}
}

// finalSliceRefusalError is a slice refusal the scheduler finalizes on.
type finalSliceRefusalError struct {
	err     error
	refusal execution.ShareRefusal
}

func (err *finalSliceRefusalError) Error() string { return err.err.Error() }

func (err *finalSliceRefusalError) Unwrap() error { return err.err }

func (err *finalSliceRefusalError) DeterministicShareRefusal() (execution.ShareRefusal, bool) {
	return err.refusal, true
}
