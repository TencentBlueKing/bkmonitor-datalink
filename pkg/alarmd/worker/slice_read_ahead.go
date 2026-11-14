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
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
)

// readAheadEnd is how a read ahead ended: handed to the Slot, or let go
// because the next slice was another, because it yielded to a reservation of
// the Slot's own, or because it failed before it was handed on.
type readAheadEnd int

const (
	readAheadReplayed readAheadEnd = iota
	readAheadOtherLeaf
	readAheadYielded
	readAheadFailed
	readAheadEnds
)

// sliceReadAheadOff, set only by the alarmd_measure build, reads every slice
// on the Slot's goroutine: the measurement's reading without the read ahead.
var sliceReadAheadOff atomic.Bool

// sliceReadWidthForMeasurement, set only by the alarmd_measure build, is the
// slices read at once in place of sliceReadWidth's, zero for its own: the
// measurement's reading of each width.
var sliceReadWidthForMeasurement atomic.Int32

// readAheadWaitsForMeasurement, set only by the alarmd_measure build, is
// told how long each wait of a read ahead for room lasted (keep), as it ends:
// the measurement's reading of how long reads ahead held their queries
// unread.
var readAheadWaitsForMeasurement atomic.Pointer[func(waited time.Duration)]

// windowSizesForMeasurement, set only by the alarmd_measure build, is told
// each window's bytes (windows.bytes) and series as it is let go: the
// measurement's reading of the windows' sizes.
var windowSizesForMeasurement atomic.Pointer[func(bytes uint64, series int)]

// sliceReadAhead is the next slice of a Slot read in slices, read on a
// goroutine of its own while the slice before it completes (decision-020
// throughput design 3.1, way 5). Its sink keeps the batches and touches
// nothing of the stream; what it keeps is in the pool and in the stream's
// aheadBytes, so the stream's total, its slice limit and its windows count
// it. A batch past its limit, past what the pool has left or past the
// stream's share waits: the read's body is not read on until the batch fits
// or the Slot takes the read on.
//
// Once the slice before has landed, the Slot hands the kept batches to the
// stream in the order they came (replay) and turns the sink into a pass:
// the read's later batches go to the stream from the read's goroutine while
// the Slot waits for the read to end, as a read the Slot makes itself
// delivers them. A read ahead that is not replayed - the next slice is
// another, it yielded, it failed before the replay - is let go and the slice
// read as it always was.
type sliceReadAhead struct {
	coordinator *SlotExecutionCoordinator
	stream      *streamedExecution
	counter     *atomic.Uint64
	leaf        inputslice.Leaf
	contract    execution.FrozenExecutionContractRef
	limit       uint64
	cancel      context.CancelFunc
	done        chan struct{}

	targets execution.TargetMemberships
	groups  execution.ScopeGroupMemberships

	// Guarded by mu; changed signals a pass or a let-go to a batch waiting.
	mu         sync.Mutex
	changed    *sync.Cond
	pass       *streamedExecution
	stopped    bool
	began      bool
	header     execution.InternalExecutionHeader
	batches    []execution.SeriesExecutionBatch
	sizes      []uint64
	named      uint64
	held       uint64
	completion execution.QueryExecutionCompletion
	err        error
}

// startSliceReadAheads reads ahead the slices after the one the Slot is on,
// pending's first, up to width slices in flight with it, each on a goroutine
// of its own: the ones already in flight for those slices are kept, the rest
// started, all in pending's order. The reads ahead share half of what the
// share leaves beside the slice the Slot is on (sliceLimit), each an equal
// part; the slice the Slot is on keeps its own half, so a series that fits a
// slice read alone fits it read beside others. Reads in flight for slices
// past those - a split put others before them, or pending no longer lists
// them - are let go first. A read let go for a reservation (yieldReadAhead)
// leaves a gap this fills.
//
// Each read keeps at most a (width-1)th of half the share, and at most
// width-1 are in flight: together they keep at most half the share. A read
// kept from an earlier call keeps the limit it was started with; the bound
// holds because the share does not change within a Slot.
func (stream *streamedExecution) startSliceReadAheads(ctx context.Context, request execution.QueryExecutionRequest, pending []inputslice.Leaf, width int) {
	next := pending[1:min(len(pending), max(width, 1))]
	inFlight := make(map[int]*sliceReadAhead, len(stream.aheads))
	var past []*sliceReadAhead
	for _, ahead := range stream.aheads {
		if slices.ContainsFunc(next, func(leaf inputslice.Leaf) bool { return leaf.ID == ahead.leaf.ID }) {
			inFlight[ahead.leaf.ID] = ahead
		} else {
			past = append(past, ahead)
		}
	}
	stream.aheads = past
	stream.letGoReadAheads(readAheadOtherLeaf)
	limit := stream.sliceLimit() / uint64(max(width-1, 1))
	for _, leaf := range next {
		if ahead := inFlight[leaf.ID]; ahead != nil {
			stream.aheads = append(stream.aheads, ahead)
			continue
		}
		stream.startSliceReadAhead(ctx, request, leaf, limit)
	}
}

// startSliceReadAhead reads leaf on a goroutine of its own with request, up
// to limit bytes kept. ctx carries no stage marker - the watchdog judges the
// slice the Slot is on - and no owner of retained bytes: nothing the read
// runs reaches the stream until it passes batches on.
func (stream *streamedExecution) startSliceReadAhead(ctx context.Context, request execution.QueryExecutionRequest, leaf inputslice.Leaf, limit uint64) {
	readCtx, cancel := context.WithCancel(withRetainedOwner(execution.WithoutStageMarker(ctx), nil))
	ahead := &sliceReadAhead{coordinator: stream.coordinator, stream: stream, counter: &stream.aheadBytes, leaf: leaf, limit: limit,
		cancel: cancel, done: make(chan struct{}), targets: stream.ResolvedTargets(), groups: stream.ResolvedScopeGroups()}
	ahead.changed = sync.NewCond(&ahead.mu)
	request.Slice = leaf.Slice
	ahead.contract = request.Contract
	stream.aheads = append(stream.aheads, ahead)
	query := stream.coordinator.ports.Query
	go func() {
		defer close(ahead.done)
		completion, err := query.Execute(readCtx, request, ahead)
		ahead.mu.Lock()
		ahead.completion, ahead.err = completion, err
		ahead.mu.Unlock()
	}()
}

func (ahead *sliceReadAhead) Begin(ctx context.Context, header execution.InternalExecutionHeader) error {
	ahead.mu.Lock()
	pass := ahead.pass
	if pass == nil {
		ahead.began, ahead.header = true, header
	}
	ahead.mu.Unlock()
	if pass != nil {
		return pass.Begin(ctx, header)
	}
	return nil
}

func (ahead *sliceReadAhead) ResolvedTargets() execution.TargetMemberships { return ahead.targets }
func (ahead *sliceReadAhead) ResolvedScopeGroups() execution.ScopeGroupMemberships {
	return ahead.groups
}

// keep waits until bytes fit the read's limit, the pool and the stream's
// share, and takes them; or returns the stream to pass them to, or the
// read's end once it is let go. A read that waits keeps what it took and
// reads no more of its answer; it is woken when the Slot takes it on or lets
// it go. Caller holds ahead.mu.
func (ahead *sliceReadAhead) keep(ctx context.Context, bytes uint64) (*streamedExecution, error) {
	var waitedFrom time.Time
	if note := readAheadWaitsForMeasurement.Load(); note != nil {
		defer func() {
			if !waitedFrom.IsZero() {
				(*note)(time.Since(waitedFrom))
			}
		}()
	}
	for {
		if ahead.pass != nil {
			return ahead.pass, nil
		}
		if ahead.stopped {
			return nil, context.Canceled
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if ahead.held+bytes <= ahead.limit && ahead.fitsShare(bytes) && ahead.coordinator.tryAcquireRetained(bytes) {
			ahead.held += bytes
			ahead.counter.Add(bytes)
			return nil, nil
		}
		if waitedFrom.IsZero() {
			waitedFrom = time.Now()
		}
		ahead.changed.Wait()
	}
}

// fitsShare reports whether bytes more fit the stream's share beside all it
// holds, its reads ahead included, read from the read's goroutine
// (retainedNow). Reads ahead judging at once may each pass with one batch:
// the Slot's own reservation past the share then has them yield.
func (ahead *sliceReadAhead) fitsShare(bytes uint64) bool {
	share := ahead.coordinator.qgShareBytes()
	if share == 0 || ahead.stream == nil {
		return true
	}
	held := ahead.counter.Load()
	for phase := range ahead.stream.retainedNow {
		held += ahead.stream.retainedNow[phase].Load()
	}
	return held+bytes <= share
}

// ConsumeSeries keeps the batch, its bytes taken as the stream would charge
// them (ConsumeSeries), a series' hold included; or, once passed on, hands it
// to the stream.
func (ahead *sliceReadAhead) ConsumeSeries(ctx context.Context, batch execution.SeriesExecutionBatch) error {
	bytes := streamedRetainedSize(batch) + hostOverlaysRetainedBytes(batch.HostOverlays) + seriesBookkeepingBytes
	ahead.mu.Lock()
	pass, err := ahead.keep(ctx, bytes)
	if err == nil && pass == nil {
		ahead.batches = append(ahead.batches, batch)
		ahead.sizes = append(ahead.sizes, bytes)
	}
	ahead.mu.Unlock()
	if pass != nil {
		return pass.ConsumeSeries(ctx, batch)
	}
	return err
}

// ReserveRetainedBytes and ReleaseRetainedBytes hold what the provider keeps
// beside the series, within the read's limit, or the stream's once passed on
// (execution.RetainedBytesReserver).
func (ahead *sliceReadAhead) ReserveRetainedBytes(ctx context.Context, bytes uint64) error {
	ahead.mu.Lock()
	pass, err := ahead.keep(ctx, bytes)
	if err == nil && pass == nil {
		ahead.named += bytes
	}
	ahead.mu.Unlock()
	if pass != nil {
		return pass.ReserveRetainedBytes(ctx, bytes)
	}
	return err
}

func (ahead *sliceReadAhead) ReleaseRetainedBytes(bytes uint64) {
	ahead.mu.Lock()
	pass := ahead.pass
	own := min(bytes, ahead.named)
	ahead.named -= own
	ahead.give(own)
	ahead.mu.Unlock()
	if pass != nil && bytes > own {
		pass.ReleaseRetainedBytes(bytes - own)
	}
}

// RetainedInputBytesNow is what the read holds, or the stream once passed on
// (execution.RetainedInputReporter).
func (ahead *sliceReadAhead) RetainedInputBytesNow() uint64 {
	ahead.mu.Lock()
	defer ahead.mu.Unlock()
	if ahead.pass != nil {
		return ahead.pass.RetainedInputBytesNow()
	}
	return ahead.held
}

// give returns bytes of what the read keeps to the pool and wakes a batch
// waiting for room. Caller holds ahead.mu.
func (ahead *sliceReadAhead) give(bytes uint64) {
	bytes = min(bytes, ahead.held)
	if bytes == 0 {
		return
	}
	ahead.held -= bytes
	ahead.counter.Add(^(bytes - 1))
	ahead.coordinator.releaseProvisional(0, bytes)
	ahead.changed.Broadcast()
}

// sliceReadDiscarder is a query source whose lookback captures a slice's read
// as it completes, and takes it back for a slice read ahead and let go
// (access.Source.DiscardSliceRead).
type sliceReadDiscarder interface {
	DiscardSliceRead(contract execution.FrozenExecutionContractRef, slice execution.InputSlice)
}

// stop ends the read, waits for it, gives back all it keeps and takes back
// what the lookback captured of it: a slice read ahead and let go is read
// again, and its first read is that one.
func (ahead *sliceReadAhead) stop() {
	defer func() {
		if discarder, ok := ahead.coordinator.ports.Query.(sliceReadDiscarder); ok {
			discarder.DiscardSliceRead(ahead.contract, ahead.leaf.Slice)
		}
	}()
	ahead.mu.Lock()
	ahead.stopped = true
	ahead.changed.Broadcast()
	ahead.mu.Unlock()
	ahead.cancel()
	<-ahead.done
	ahead.mu.Lock()
	defer ahead.mu.Unlock()
	ahead.batches, ahead.sizes = nil, nil
	ahead.give(ahead.held)
}

// letGoReadAheads stops every read ahead of the stream, waits for them and
// gives back all they keep, each counted under end; their slices are read
// on the Slot's goroutine when they come. Safe with none.
func (stream *streamedExecution) letGoReadAheads(end readAheadEnd) {
	aheads := stream.aheads
	stream.aheads = nil
	for _, ahead := range aheads {
		ahead.stop()
		stream.readAheadEnds[end]++
	}
}

// letGoReadAhead lets every read ahead go as the Slot ends, uncounted: a
// Slot that ends reports no usage.
func (stream *streamedExecution) letGoReadAhead() {
	aheads := stream.aheads
	stream.aheads = nil
	for _, ahead := range aheads {
		ahead.stop()
	}
}

// yieldReadAhead lets go every read ahead that keeps bytes, for a
// reservation they may stand in the way of, and says whether the caller
// should judge the reservation again: one was let go, or what the reads
// ahead keep fell since the refusal was judged (aheadAtRefusal). A read that
// keeps nothing - its query still waiting for an answer - stands in no
// reservation's way and is kept: it waits for room when its answer comes
// (keep). A reservation the Slot's own slice would get without the reads
// ahead is never refused for them.
func (stream *streamedExecution) yieldReadAhead() bool {
	if stream == nil || len(stream.aheads) == 0 {
		return false
	}
	var keeping, waiting []*sliceReadAhead
	for _, ahead := range stream.aheads {
		ahead.mu.Lock()
		held := ahead.held
		ahead.mu.Unlock()
		if held > 0 {
			keeping = append(keeping, ahead)
		} else {
			waiting = append(waiting, ahead)
		}
	}
	stream.aheads = keeping
	stream.letGoReadAheads(readAheadYielded)
	stream.aheads = waiting
	return len(keeping) > 0 || stream.aheadBytes.Load() < stream.aheadAtRefusal.Load()
}

// replayReadAhead hands leaf's read ahead to the stream when the stream is
// reading leaf ahead and the read has not failed: its Begin, its kept batches
// in the order they came - each given back by the read just before the
// stream takes it on its own account - and what it held beside them; then
// it passes the read's later batches on and waits for the read to end. ok is
// false when there is nothing to replay, and the slice is read on the Slot's
// goroutine; otherwise the read's completion and error are the slice's, a
// slice overflow among them, handled as a read the Slot made would be.
func (stream *streamedExecution) replayReadAhead(ctx context.Context, leaf inputslice.Leaf) (execution.QueryExecutionCompletion, error, bool) {
	if len(stream.aheads) == 0 {
		return execution.QueryExecutionCompletion{}, nil, false
	}
	// None for leaf: the slices pending changed - a split - or its read
	// ahead yielded. The reads ahead of the slices after it are kept for
	// startSliceReadAheads to judge once leaf is read.
	index := slices.IndexFunc(stream.aheads, func(ahead *sliceReadAhead) bool { return ahead.leaf.ID == leaf.ID })
	if index < 0 {
		return execution.QueryExecutionCompletion{}, nil, false
	}
	ahead := stream.aheads[index]
	ahead.mu.Lock()
	failed := ahead.err != nil
	ahead.mu.Unlock()
	stream.aheads = slices.Delete(stream.aheads, index, index+1)
	if failed {
		ahead.stop()
		stream.readAheadEnds[readAheadFailed]++
		return execution.QueryExecutionCompletion{}, nil, false
	}
	stream.readAheadEnds[readAheadReplayed]++
	replayErr := func() error {
		ahead.mu.Lock()
		defer ahead.mu.Unlock()
		if ahead.began {
			if err := stream.Begin(ctx, ahead.header); err != nil {
				return err
			}
		}
		for index, batch := range ahead.batches {
			ahead.give(ahead.sizes[index])
			ahead.batches[index] = execution.SeriesExecutionBatch{}
			if err := stream.ConsumeSeries(ctx, batch); err != nil {
				return err
			}
		}
		ahead.batches, ahead.sizes = nil, nil
		if named := ahead.named; named > 0 {
			ahead.named = 0
			ahead.give(named)
			if err := stream.ReserveRetainedBytes(ctx, named); err != nil {
				return err
			}
		}
		ahead.pass = stream
		ahead.changed.Broadcast()
		return nil
	}()
	if replayErr != nil {
		ahead.stop()
		return execution.QueryExecutionCompletion{}, replayErr, true
	}
	// The Slot waits for the rest of the read here: the watchdog reads it as
	// the input the Slot is reading.
	execution.MarkStage(ctx, execution.SlotStageQuery)
	<-ahead.done
	ahead.mu.Lock()
	defer ahead.mu.Unlock()
	ahead.give(ahead.held)
	return ahead.completion, ahead.err, true
}
