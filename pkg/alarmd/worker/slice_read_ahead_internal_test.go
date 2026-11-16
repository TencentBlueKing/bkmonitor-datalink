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
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/inputslice"
)

// heldReadAhead is a read ahead of the stream that has ended and keeps
// bytes, in the pool and in the stream's count, as one that read them would.
func heldReadAhead(t *testing.T, stream *streamedExecution, bytes uint64) *sliceReadAhead {
	t.Helper()
	done := make(chan struct{})
	close(done)
	ahead := &sliceReadAhead{coordinator: stream.coordinator, counter: &stream.aheadBytes, limit: bytes,
		cancel: func() {}, done: done}
	ahead.changed = sync.NewCond(&ahead.mu)
	ahead.mu.Lock()
	pass, err := ahead.keep(context.Background(), bytes)
	ahead.mu.Unlock()
	if pass != nil || err != nil {
		t.Fatalf("the read ahead could not keep %d bytes: %v", bytes, err)
	}
	stream.aheads = append(stream.aheads, ahead)
	return ahead
}

// A reservation of the Slot's own that fits its share without the read
// ahead and not with it is not refused: the read ahead yields - it is let
// go, the pool and the stream's count back as they were without it - and
// the reservation is judged again and taken.
func TestAReadAheadYieldsToTheSlotsOwnReservationAtTheShare(t *testing.T) {
	stream := newWindowedFixture(t).stream
	share := stream.coordinator.qgShareBytes()
	room := share - stream.retainedTotal()
	poolBefore := RetainedReserved(stream.coordinator)
	heldReadAhead(t, stream, room/2)
	if got := stream.retainedTotal(); got != share-room+room/2 {
		t.Fatalf("the stream counts %d with the read ahead, want its own %d and the read ahead's %d", got, share-room, room/2)
	}
	request := room - room/4
	if err := stream.reserveRetainedInput(context.Background(), request); err != nil {
		t.Fatalf("a reservation that fits the share without the read ahead was refused: %v", err)
	}
	if len(stream.aheads) != 0 || stream.aheadBytes.Load() != 0 {
		t.Fatalf("the read ahead was kept (%d bytes) beside a reservation it stood in the way of", stream.aheadBytes.Load())
	}
	if got := stream.readAheadEnds[readAheadYielded]; got != 1 {
		t.Fatalf("reads ahead counted yielded = %d, want the one let go", got)
	}
	if got := RetainedReserved(stream.coordinator); got != poolBefore+request {
		t.Fatalf("the pool holds %d, want %d before and the reservation's %d: the read ahead's bytes given back", got, poolBefore, request)
	}
}

// The same with the pool short and the share wide: what others hold leaves
// room for the reservation only without the read ahead, and the read ahead
// yields to it.
func TestAReadAheadYieldsToTheSlotsOwnReservationAtThePool(t *testing.T) {
	stream := newWindowedFixture(t).stream
	coordinator := stream.coordinator
	free := coordinator.budget.MaxRetainedBytes - RetainedReserved(coordinator)
	// Others hold all but 64 KiB of the pool.
	others := free - 64<<10
	if !coordinator.tryAcquireRetained(others) {
		t.Fatal("fixture: the pool would not take what others hold")
	}
	heldReadAhead(t, stream, 40<<10)
	if err := stream.reserveRetainedInput(context.Background(), 48<<10); err != nil {
		t.Fatalf("a reservation the pool has room for without the read ahead was refused: %v", err)
	}
	if len(stream.aheads) != 0 || stream.aheadBytes.Load() != 0 {
		t.Fatal("the read ahead was kept beside a reservation the pool had no room for with it")
	}
}

// While the Slot waits for the rest of a replayed read, the watchdog reads
// the stage of an input read: the read ahead carries no marker of its own,
// and the slice before it has landed.
func TestTheSlotWaitingForAReadAheadIsReadingInput(t *testing.T) {
	stream := newWindowedFixture(t).stream
	marker := &execution.StageMarker{}
	marker.Begin(execution.SlotStageState)
	ctx := execution.WithStageMarker(context.Background(), marker)
	leaf := inputslice.Leaf{ID: 7}
	ahead := &sliceReadAhead{coordinator: stream.coordinator, counter: &stream.aheadBytes, leaf: leaf, limit: 1 << 10,
		cancel: func() {}, done: make(chan struct{})}
	ahead.changed = sync.NewCond(&ahead.mu)
	stream.aheads = append(stream.aheads, ahead)
	replayed := make(chan bool)
	go func() {
		_, _, ok := stream.replayReadAhead(ctx, leaf)
		replayed <- ok
	}()
	for deadline := time.Now().Add(2 * time.Second); marker.Stage() != execution.SlotStageQuery && time.Now().Before(deadline); { // fixed-wait: polling a stage the replay sets; the case fails on the stage after it
		time.Sleep(time.Millisecond) // fixed-wait: polling a stage the replay sets, bounded above
	}
	if got := marker.Stage(); got != execution.SlotStageQuery {
		t.Fatalf("stage while waiting for the read ahead = %q, want %q", got, execution.SlotStageQuery)
	}
	ahead.mu.Lock()
	passed := ahead.pass == stream
	ahead.mu.Unlock()
	if !passed {
		t.Fatal("the read ahead's later batches are not passed to the stream while the Slot waits")
	}
	close(ahead.done)
	if !<-replayed {
		t.Fatal("the replayed read ahead was not taken as the slice's read")
	}
}

// blockedQuery is a query source whose every read waits until it is let go.
type blockedQuery struct{}

func (blockedQuery) Execute(ctx context.Context, _ execution.QueryExecutionRequest, _ execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
	<-ctx.Done()
	return execution.QueryExecutionCompletion{}, ctx.Err()
}

// The slices after the one the Slot is on are read ahead, up to the width
// in flight with it, in pending's order, each kept to an equal part of the
// half the share leaves beside the slice the Slot is on. Reads already in
// flight for the next slices are kept; ones a split put past them are let
// go, counted, and read again when they come.
func TestTheSlicesAfterTheSlotsAreReadAheadUpToTheWidth(t *testing.T) {
	stream := newWindowedFixture(t).stream
	stream.coordinator.ports.Query = blockedQuery{}
	stream.slices = &sliceReading{}
	defer stream.letGoReadAhead()
	leaves := func(ids ...int) []inputslice.Leaf {
		pending := make([]inputslice.Leaf, 0, len(ids))
		for _, id := range ids {
			pending = append(pending, inputslice.Leaf{ID: id})
		}
		return pending
	}
	reading := func() []int {
		ids := make([]int, 0, len(stream.aheads))
		for _, ahead := range stream.aheads {
			ids = append(ids, ahead.leaf.ID)
		}
		return ids
	}
	stream.startSliceReadAheads(context.Background(), execution.QueryExecutionRequest{}, leaves(1, 2, 3, 4, 5), 4)
	if got := reading(); !reflect.DeepEqual(got, []int{2, 3, 4}) {
		t.Fatalf("reading ahead %v, want the three slices after the one the Slot is on", got)
	}
	if want := stream.sliceLimit() / 3; stream.aheads[0].limit != want {
		t.Fatalf("each read ahead is kept to %d, want a third of the half the share leaves, %d", stream.aheads[0].limit, want)
	}
	// Slice 1 landed and slice 2 was replayed: 3 and 4 stay in flight and 5
	// is started.
	stream.aheads[0].stop()
	stream.aheads = stream.aheads[1:]
	stream.startSliceReadAheads(context.Background(), execution.QueryExecutionRequest{}, leaves(2, 3, 4, 5, 6), 4)
	if got := reading(); !reflect.DeepEqual(got, []int{3, 4, 5}) {
		t.Fatalf("reading ahead %v, want 3 and 4 kept and 5 started", got)
	}
	// A split put slices before 3: 3 is still among the next three and is
	// kept, 4 and 5 are past them and go, and 7 and 8 are read.
	stream.startSliceReadAheads(context.Background(), execution.QueryExecutionRequest{}, leaves(2, 7, 8, 3, 4), 4)
	if got := reading(); !reflect.DeepEqual(got, []int{7, 8, 3}) {
		t.Fatalf("reading ahead %v after the split, want the new next slices", got)
	}
	if got := stream.readAheadEnds[readAheadOtherLeaf]; got != 2 {
		t.Fatalf("reads ahead let go for another slice = %d, want 4's and 5's", got)
	}
}

// answeringQuery is a query source whose every read waits for answer, then
// holds named bytes for its answer's body, as a provider decoding it would,
// and completes; reads counts the reads it was asked, and holding is closed
// as the first asks for its bytes.
type answeringQuery struct {
	answer  chan struct{}
	holding chan struct{}
	named   uint64
	reads   *atomic.Int32
}

func (query answeringQuery) Execute(ctx context.Context, _ execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
	query.reads.Add(1)
	select {
	case <-query.answer:
	case <-ctx.Done():
		return execution.QueryExecutionCompletion{}, ctx.Err()
	}
	if query.reads.Load() == 1 {
		close(query.holding)
	}
	if err := consumer.(execution.RetainedBytesReserver).ReserveRetainedBytes(ctx, query.named); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	return execution.QueryExecutionCompletion{AllRequiredCompleted: true}, nil
}

// A read ahead whose query is still waiting for its answer keeps nothing and
// stands in no reservation's way: the Slot's own reservation past its share
// is refused as it would be without it, and the read is kept, not let go.
// Its answer, coming while the share is still full, waits in the read for
// room - no byte taken past the share - and is the slice's read once the
// Slot takes it on: one query for the slice, not a second from the start.
func TestAReadAheadWaitingForItsAnswerIsNotLetGoAtAFullShare(t *testing.T) {
	stream := newWindowedFixture(t).stream
	query := answeringQuery{answer: make(chan struct{}), holding: make(chan struct{}), named: 4 << 10, reads: &atomic.Int32{}}
	stream.coordinator.ports.Query = query
	stream.slices = &sliceReading{}
	defer stream.letGoReadAhead()
	next := inputslice.Leaf{ID: 2}
	stream.startSliceReadAheads(context.Background(), execution.QueryExecutionRequest{}, []inputslice.Leaf{{ID: 1}, next}, 2)
	share := stream.coordinator.qgShareBytes()
	room := share - stream.retainedTotal()
	if err := stream.reserveRetainedInput(context.Background(), room); err != nil {
		t.Fatalf("fixture: the share's room was refused: %v", err)
	}
	if err := stream.reserveRetainedInput(context.Background(), 1); err == nil {
		t.Fatal("a reservation past the share was taken")
	}
	if len(stream.aheads) != 1 || stream.readAheadEnds[readAheadYielded] != 0 {
		t.Fatalf("reads ahead in flight %d, yielded %d: want the one waiting for its answer kept", len(stream.aheads), stream.readAheadEnds[readAheadYielded])
	}
	close(query.answer)
	<-query.holding
	// The answer waits in the read: a read that took its bytes past the share
	// would end within this wait, one that waits for room does not. The
	// Slot's windows then give the share back before it takes the read on.
	ahead := stream.aheads[0]
	select {
	case <-ahead.done:
	case <-time.After(200 * time.Millisecond): // fixed-wait: a read waiting for room never ends here; one past the share ends at once
	}
	if got := stream.retainedTotal(); got != share {
		t.Fatalf("the stream holds %d with the answer come, want the share %d: nothing taken past it", got, share)
	}
	stream.unretainInput(room)
	completion, err, replayed := stream.replayReadAhead(context.Background(), next)
	if !replayed || err != nil || !completion.AllRequiredCompleted {
		t.Fatalf("replayed %t err %v completion %+v: want the read ahead taken as the slice's read", replayed, err, completion)
	}
	if got := stream.readAheadEnds[readAheadReplayed]; got != 1 {
		t.Fatalf("reads ahead replayed %d, want 1", got)
	}
	if got := query.reads.Load(); got != 1 {
		t.Fatalf("the slice was asked %d times, want once", got)
	}
	if got := stream.namedReserved; got != query.named {
		t.Fatalf("the stream holds %d of the answer's body, want its %d", got, query.named)
	}
}

// A refusal judged while the reads ahead kept bytes they have since given
// back is judged again, though none is let go now: what they keep fell below
// what it was at the refusal. Kept as much as at the refusal, nothing was
// let go and nothing fell, and the refusal stands. A read that keeps nothing
// is kept either way.
func TestAReadAheadThatGaveBackSinceTheRefusalHasItJudgedAgain(t *testing.T) {
	for _, test := range []struct {
		name    string
		refused uint64
		again   bool
	}{{"fell since", 4 << 10, true}, {"as at the refusal", 0, false}} {
		t.Run(test.name, func(t *testing.T) {
			stream := newWindowedFixture(t).stream
			done := make(chan struct{})
			close(done)
			waiting := &sliceReadAhead{coordinator: stream.coordinator, stream: stream, counter: &stream.aheadBytes,
				leaf: inputslice.Leaf{ID: 2}, limit: 1 << 20, cancel: func() {}, done: done}
			waiting.changed = sync.NewCond(&waiting.mu)
			stream.aheads = []*sliceReadAhead{waiting}
			stream.aheadAtRefusal.Store(test.refused)
			if got := stream.yieldReadAhead(); got != test.again {
				t.Fatalf("judge again = %t, want %t", got, test.again)
			}
			if len(stream.aheads) != 1 || stream.aheads[0] != waiting || stream.readAheadEnds[readAheadYielded] != 0 {
				t.Fatalf("reads ahead %d, yielded %d: want the one keeping nothing kept", len(stream.aheads), stream.readAheadEnds[readAheadYielded])
			}
		})
	}
}
