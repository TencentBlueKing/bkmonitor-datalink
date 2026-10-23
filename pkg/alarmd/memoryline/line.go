// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package memoryline is the one line the process's observation memory grows
// under. Detection has its memory budgets - the Slots' retained bytes, the
// control caches - and the Go runtime its soft limit. Observation - the cost
// summary, the cost projection, the criterion samples, the late-data
// lookback - takes no share of its own: it may grow while the live heap after
// the last collection, plus what the detection budgets may still take, plus
// what observation was granted since that collection, stays within the soft
// limit. At the line every consumer stops taking more and keeps what it has,
// and each refusal is counted under the consumer it refused.
//
// A consumer asks in one of two ways, by what becomes of what it takes.
// State that stays - a summary, a sample buffer, rounds kept per object, a
// view kept in a cache - is admitted (Admit) and never given back: it is in
// the live heap from the next collection on, and the grant counts until
// then. A reader that takes memory for one piece of work and drops it when
// the work is done - a page that decodes the fleet's snapshots and builds a
// view of them, garbage once the response is written - holds it (Hold) and
// releases it: what is released is not in the live heap at the next
// collection, so counting it until then would hold observation off with
// memory nothing keeps. Work whose result is then kept - a view put in a
// cache - releases its hold and admits what it keeps as it keeps it: a hold
// on something that stays would count it twice, in the live heap and held,
// for as long as it stays.
//
// There is no ratio, no order among the consumers and no count they are held
// to: a deployment far from its limit never refuses, and one near it refuses
// whichever consumer asks next.
package memoryline

import (
	"math"
	"runtime"
	runtimemetrics "runtime/metrics"
	"sync"
	"sync/atomic"
)

// Consumer is one observation reader of memory. Closed.
type Consumer string

const (
	// ConsumerCostSummary is the per-group cost summary, sized by the Query
	// Groups this replica owns.
	ConsumerCostSummary Consumer = "cost_summary"
	// ConsumerCostProjection is the cross-replica cost projection: what one
	// refresh reads of the other replicas' published rankings.
	ConsumerCostProjection Consumer = "cost_projection"
	// ConsumerSeriesSampler is the criterion samples' encode buffers, one per
	// open sample window.
	ConsumerSeriesSampler Consumer = "series_sampler"
	// ConsumerLookback is the late-data lookback's per-series tables.
	ConsumerLookback Consumer = "lookback"
	// ConsumerFleetRounds is the fleet tracker's rounds kept per object past
	// the fixed last few, sized by how far each object's windows reach back.
	ConsumerFleetRounds Consumer = "fleet_rounds"
	// ConsumerFleetRestore is the Progress records a publish reads back to
	// restore the objects it owns, one batch at a time.
	ConsumerFleetRestore Consumer = "fleet_restore"
	// ConsumerDiagnosisProgress is the Progress records a diagnosis page
	// reads for its objects.
	ConsumerDiagnosisProgress Consumer = "diagnosis_progress"
	// ConsumerFleetView is the replicas' snapshots a fleet view a reader
	// asks for reads whole: the objects route, the diagnosis, the strategy
	// standing. The verdict scrape's read does not ask.
	ConsumerFleetView Consumer = "fleet_view"
)

// Consumers is every consumer, in the order they are reported.
var Consumers = []Consumer{ConsumerCostSummary, ConsumerCostProjection, ConsumerSeriesSampler, ConsumerLookback, ConsumerFleetRounds,
	ConsumerFleetRestore, ConsumerDiagnosisProgress, ConsumerFleetView}

// Budget is one detection budget as the line reads it: its size - the most
// it can come to hold, a cache's working set rather than its ceiling - and
// what it holds now. Its room, the size less what it holds, is left to
// detection.
type Budget func() (size, held uint64)

// room is what a budget may still take.
func room(size, held uint64) uint64 {
	if held >= size {
		return 0
	}
	return size - held
}

// heap is the runtime's reading the line is drawn from.
type heap struct {
	limit, live, cycles uint64
}

// Line is the process's observation memory line. The zero of a nil *Line
// admits everything.
type Line struct {
	read func() heap

	mu      sync.Mutex
	budgets []Budget
	// names are the budgets' names, as a reading reports them.
	names []string
	// sizes is each budget's size as the collection ended (or as it was
	// reserved, for one reserved since), and peaks the largest size each has
	// had since, in the order of budgets.
	sizes []uint64
	peaks []uint64
	// cycle is the collection the grants below were made after: from the
	// next one on, what they took is in the live heap. reserved is what the
	// detection budgets could still take as that collection ended (New) or,
	// on a line not told of collections, at its first reading after one:
	// detection that grows before the next one takes room the live heap
	// does not show yet, so the room stays detection's until then; and what
	// detection gives back before the next one is still in the live heap
	// that collection measured, so it is not detection's room a second
	// time. A budget reserved since adds its own room to it, and a budget
	// that grew since adds what it grew by: a Worker that took on more Query
	// Groups, a Leader reading a larger publication, is room detection is
	// about to take, before it has taken it.
	cycle   uint64
	granted uint64
	// held is what the holds not yet released take (Hold). A collection
	// does not end a hold: a hold that spans one is counted in the live
	// heap it measured as well as here, until it is released, because the
	// part of it not yet allocated when the collection ran is in neither.
	held uint64
	// reserved is the budgets' room as the collection ended, and grown the
	// room they have grown by since (reservedLocked).
	reserved uint64
	grown    uint64

	// refused and admitted are by consumer, in the order of Consumers.
	refused  []atomic.Uint64
	admitted []atomic.Uint64
	// closed ends the watch of collections: see Close.
	closed atomic.Bool
}

// New is the line over the running process's heap. It is told of every
// collection until Close, and nothing else lets it go before then.
func New() *Line {
	samples := []runtimemetrics.Sample{{Name: "/gc/gomemlimit:bytes"}, {Name: "/gc/heap/live:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}}
	var mu sync.Mutex
	line := newLine(func() heap {
		mu.Lock()
		defer mu.Unlock()
		runtimemetrics.Read(samples)
		return heap{limit: sampleValue(samples[0]), live: sampleValue(samples[1]), cycles: sampleValue(samples[2])}
	})
	line.snapshotAtCollections()
	return line
}

// collectionSentinel is an object nothing refers to, whose finalizer tells
// the line a collection has ended. It holds a pointer so the runtime does
// not batch it into a tiny allocation, whose finalizer may never run.
type collectionSentinel struct{ _ *byte }

// snapshotAtCollections takes the detection reserve as each collection
// ends, rather than at the line's first reading after it. Detection that
// filled between the two was in neither: not in the live heap the
// collection measured, and no longer in the reserve read afterwards, so the
// line counted that fill nowhere and observation could take its room. The
// finalizer of an unreferenced object runs once the collection that found
// it has swept; it takes the snapshot and arms the next.
func (line *Line) snapshotAtCollections() {
	runtime.SetFinalizer(new(collectionSentinel), func(*collectionSentinel) {
		if line.closed.Load() {
			return
		}
		line.collected(line.read().cycles)
		line.snapshotAtCollections()
	})
}

// Close ends the line's watch of collections: the next collection arms no
// other, and the line is let go once nothing else holds it. A closed line
// still answers, reading the reserve at its first reading after a
// collection as a line not told of them does.
func (line *Line) Close() {
	if line != nil {
		line.closed.Store(true)
	}
}

// collected starts cycle's accounting unless a reading after the collection
// already did.
func (line *Line) collected(cycle uint64) {
	line.mu.Lock()
	defer line.mu.Unlock()
	line.reservedLocked(cycle)
}

func newLine(read func() heap) *Line {
	return &Line{read: read, refused: make([]atomic.Uint64, len(Consumers)), admitted: make([]atomic.Uint64, len(Consumers))}
}

func sampleValue(sample runtimemetrics.Sample) uint64 {
	if sample.Value.Kind() != runtimemetrics.KindUint64 {
		return 0
	}
	return sample.Value.Uint64()
}

// Reserve leaves room for one more detection budget, reported under name.
func (line *Line) Reserve(name string, budget Budget) {
	if line == nil || budget == nil {
		return
	}
	line.mu.Lock()
	size, held := budget()
	line.budgets = append(line.budgets, budget)
	line.names = append(line.names, name)
	line.sizes = append(line.sizes, size)
	line.peaks = append(line.peaks, size)
	// Its own room only: taking every budget's room again would drop what
	// the others took since the collection, which is in neither the live
	// heap it measured nor their room now.
	line.reserved = saturatingAdd(line.reserved, room(size, held))
	line.mu.Unlock()
}

// Admit answers whether consumer may take bytes more. Admitted, the bytes
// count against the line until the next collection, whose live heap holds
// them from then on; nothing is given back. Refused, the consumer keeps
// what it has and takes no more, and the refusal is counted.
func (line *Line) Admit(consumer Consumer, bytes uint64) bool {
	if line == nil {
		return true
	}
	index := consumerIndex(consumer)
	reading := line.read()
	line.mu.Lock()
	admitted := line.fitsLocked(reading, bytes)
	if admitted {
		line.granted = saturatingAdd(line.granted, bytes)
	}
	line.mu.Unlock()
	line.count(index, admitted, bytes)
	return admitted
}

// Hold answers whether consumer may take bytes for one piece of work it
// drops when done, by the same line as Admit. Held, the bytes count against
// the line until release is called, and no longer: release once the memory
// is no longer referred to - a page's response written. What is kept past
// the work is not held but admitted as it is kept (see the package comment).
// release is safe to call more than once, and to defer; a refused hold's
// release does nothing. A hold never released is never given back: the
// line counts it until the process ends, and observation_memory_held_bytes
// stays above zero between pieces of work. Defer the release as soon as the
// hold is taken. Refused, the consumer does not take the memory, and the
// refusal is counted.
func (line *Line) Hold(consumer Consumer, bytes uint64) (release func(), held bool) {
	if line == nil {
		return func() {}, true
	}
	index := consumerIndex(consumer)
	reading := line.read()
	line.mu.Lock()
	held = line.fitsLocked(reading, bytes)
	if held {
		line.held = saturatingAdd(line.held, bytes)
	}
	line.mu.Unlock()
	line.count(index, held, bytes)
	if !held {
		return func() {}, false
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			line.mu.Lock()
			line.held -= min(line.held, bytes)
			line.mu.Unlock()
		})
	}, true
}

// fitsLocked answers whether bytes more fit under the line: the live heap
// after the last collection, the detection budgets' room, what observation
// was granted since and what it holds now, and bytes, within the limit.
func (line *Line) fitsLocked(reading heap, bytes uint64) bool {
	reserved := line.reservedLocked(reading.cycles)
	taken := saturatingAdd(saturatingAdd(saturatingAdd(reading.live, reserved), line.granted), line.held)
	return reading.limit > 0 && taken <= reading.limit && bytes <= reading.limit-taken
}

// count records an answer under consumer, a known one.
func (line *Line) count(index int, admitted bool, bytes uint64) {
	if index < 0 {
		return
	}
	if admitted {
		line.admitted[index].Add(bytes)
	} else {
		line.refused[index].Add(1)
	}
}

// reservedLocked is the room left to detection at collection cycle: what
// its budgets could take as the collection ended, and the most each has
// grown by since - but not what they gave back since.
//
// A budget that gave back what it held since the collection gave back bytes
// the collection's live heap still holds: reading its room again as well
// counted those bytes twice, once live and once as room, until the next
// collection - on a replica whose Slots released their retained bytes after
// a collection that ran while they held them, the line went hundreds of
// megabytes past itself every round and refused observation that fit.
//
// A budget that grew is the other way round: the room it grew by is in
// neither the live heap nor the snapshot, and is detection's the moment its
// size says so - a cache that names the reads it is about to store has
// grown before it stores them. Its highest size since the collection is
// what counts, not each rise: a cache whose size rises by a batch as the
// batch is named and falls back as it is stored would otherwise add every
// batch again, and hold observation off with room no batch took. Whatever
// it stored since stays in the heap however it evicts, so a size that fell
// back does not take the growth away. A new collection forgets the grants,
// whose bytes its live heap holds, and the growth, which its live heap
// holds too.
func (line *Line) reservedLocked(cycle uint64) uint64 {
	if cycle != line.cycle {
		line.cycle, line.granted, line.reserved, line.grown = cycle, 0, 0, 0
		for index, budget := range line.budgets {
			size, held := budget()
			line.sizes[index], line.peaks[index] = size, size
			line.reserved = saturatingAdd(line.reserved, room(size, held))
		}
		return line.reserved
	}
	line.grown = 0
	for index, budget := range line.budgets {
		if size, _ := budget(); size > line.peaks[index] {
			line.peaks[index] = size
		}
		line.grown = saturatingAdd(line.grown, line.peaks[index]-line.sizes[index])
	}
	return saturatingAdd(line.reserved, line.grown)
}

// Reading is the line as a reader sees it: the soft limit, the live heap
// after the last collection, what the detection budgets may still take,
// Headroom - the limit less the other two, negative past the line - and what
// observation was granted since that collection and what it holds now
// (Hold), which admission also takes out of the headroom.
type Reading struct {
	LimitBytes     uint64
	LiveBytes      uint64
	ReservedBytes  uint64
	HeadroomBytes  int64
	GrantedBytes   uint64
	HeldBytes      uint64
	RefusedTotal   map[Consumer]uint64
	AdmittedBytes  map[Consumer]uint64
	LimitUnlimited bool
	// Budgets is each detection budget now, in the order reserved.
	Budgets []BudgetReading
}

// BudgetReading is one detection budget as read now: its size and what it
// holds. Its part of ReservedBytes is not the difference of the two - that
// was taken as the last collection ended, and grows by the most the size
// has grown since - but the two read together say what the budget is
// holding observation off with.
type BudgetReading struct {
	Name      string
	SizeBytes uint64
	HeldBytes uint64
}

// Read is the line now.
func (line *Line) Read() Reading {
	reading := Reading{RefusedTotal: map[Consumer]uint64{}, AdmittedBytes: map[Consumer]uint64{}}
	for _, consumer := range Consumers {
		reading.RefusedTotal[consumer], reading.AdmittedBytes[consumer] = 0, 0
	}
	if line == nil {
		return reading
	}
	heap := line.read()
	line.mu.Lock()
	reserved := line.reservedLocked(heap.cycles)
	granted, held := line.granted, line.held
	for index, budget := range line.budgets {
		size, held := budget()
		reading.Budgets = append(reading.Budgets, BudgetReading{Name: line.names[index], SizeBytes: size, HeldBytes: held})
	}
	line.mu.Unlock()
	reading.LimitBytes, reading.LiveBytes, reading.ReservedBytes, reading.GrantedBytes = heap.limit, heap.live, reserved, granted
	reading.HeldBytes = held
	reading.LimitUnlimited = heap.limit == math.MaxInt64
	reading.HeadroomBytes = signedDifference(heap.limit, saturatingAdd(heap.live, reserved))
	for index, consumer := range Consumers {
		reading.RefusedTotal[consumer] = line.refused[index].Load()
		reading.AdmittedBytes[consumer] = line.admitted[index].Load()
	}
	return reading
}

func consumerIndex(consumer Consumer) int {
	for index, known := range Consumers {
		if known == consumer {
			return index
		}
	}
	return -1
}

func saturatingAdd(left, right uint64) uint64 {
	if left > math.MaxUint64-right {
		return math.MaxUint64
	}
	return left + right
}

func signedDifference(left, right uint64) int64 {
	if left >= right {
		return int64(min(left-right, math.MaxInt64))
	}
	return -int64(min(right-left, math.MaxInt64))
}
