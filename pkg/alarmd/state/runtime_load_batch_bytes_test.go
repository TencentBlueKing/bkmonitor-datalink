// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func mustExpect(t *testing.T, store *ExecutionStore, group execution.QueryGroupIdentity) uint64 {
	t.Helper()
	size, learned := store.expectedValueBytes(group)
	if !learned {
		t.Fatalf("no record size learned for %q", group)
	}
	return size
}

func sizedStore() *ExecutionStore {
	return &ExecutionStore{options: ExecutionStoreOptions{MaxValueBytes: 512 << 10}}
}

// A preflight batch is bounded by what it is expected to move, not by key count
// alone.
//
// The apply side has had a byte bound since it was written, and its own comment
// gives the reason: with MaxValueBytes up to 512 KiB an item bound alone lets
// one call carry 128 MiB. The read side had only the item bound, so a Query
// Group whose records had grown to 345 KiB each asked for 86 MB in one MGET -
// which does not fit a 3 s read timeout, failed identically on all four
// attempts, and took the whole Slot with it, 99 times in a day.
func TestAPreflightBatchIsBoundedByWhatItWillMove(t *testing.T) {
	store := sizedStore()
	const group = execution.QueryGroupIdentity("qg-heavy")

	// Nothing read for this Query Group yet. The bound is the only one that
	// holds whatever its records turn out to be: the batch budget over the
	// largest value the store accepts.
	first := store.runtimeLoadBatchLimit(group, 0, false)
	if want := int(runtimeLoadBatchBytes / (512 << 10)); first != want {
		t.Fatalf("first batch limit = %d, want %d - the only shape-independent bound there is", first, want)
	}
	if uint64(first)*(512<<10) > runtimeLoadBatchBytes {
		t.Fatal("the first batch for an unknown Query Group can exceed the batch budget")
	}

	// The shape that produced this decision: records of about 345 KiB.
	store.commitValueBytes(group, 345*1024, true)
	limit := store.runtimeLoadBatchLimit(group, 0, false)
	if expected := uint64(limit) * mustExpect(t, store, group); expected > runtimeLoadBatchBytes {
		t.Fatalf("a batch of %d records of %d bytes is %d, over the %d bound",
			limit, mustExpect(t, store, group), expected, runtimeLoadBatchBytes)
	}

	// Ordinary records get the full item bound once they are known: the bound
	// is what the batch will move, so learning is what buys back the batch size
	// the safe first call gave up.
	small := sizedStore()
	small.commitValueBytes("qg-small", 2048, true)
	if limit := small.runtimeLoadBatchLimit("qg-small", 0, false); limit != runtimeLoadBatchItems {
		t.Fatalf("batch limit = %d for 2 KiB records, want the full item bound %d", limit, runtimeLoadBatchItems)
	}

	// A single record over the whole batch budget is still read, alone.
	huge := sizedStore()
	huge.commitValueBytes("qg-huge", runtimeLoadBatchBytes*2, true)
	if limit := huge.runtimeLoadBatchLimit("qg-huge", 0, false); limit != 1 {
		t.Fatalf("batch limit = %d for a record larger than the batch budget, want 1", limit)
	}
}

// What one Query Group's records weigh says nothing about another's, so the
// size is learned per Query Group.
//
// Record size is a property of one strategy's retention and Level count. A
// replica holding two thousand ordinary objects of a few KiB and one object of
// 345 KiB averages to a few KiB, so a process-wide figure hands the largest
// batch to the one object that needs the smallest - the bound is defeated by
// exactly the population it exists for, and it looks like it is working.
func TestTheRecordSizeIsLearnedPerQueryGroup(t *testing.T) {
	store := sizedStore()
	const ordinary, heavy = execution.QueryGroupIdentity("qg-ordinary"), execution.QueryGroupIdentity("qg-heavy")

	// Twenty ordinary reads, as a busy replica produces between two Slots of
	// the heavy object.
	for range 20 {
		store.commitValueBytes(ordinary, 3*1024, true)
	}
	store.commitValueBytes(heavy, 345*1024, true)

	// Interleaved again, which is what a real replica does.
	for range 20 {
		store.commitValueBytes(ordinary, 3*1024, true)
	}

	if limit := store.runtimeLoadBatchLimit(ordinary, 0, false); limit != runtimeLoadBatchItems {
		t.Fatalf("ordinary batch limit = %d, want the full item bound: the heavy object must not shrink "+
			"everyone else's batches either", limit)
	}
	heavyLimit := store.runtimeLoadBatchLimit(heavy, 0, false)
	if moved := uint64(heavyLimit) * mustExpect(t, store, heavy); moved > runtimeLoadBatchBytes {
		t.Fatalf("heavy batch of %d keys moves %d, over the %d bound; twenty ordinary reads in between "+
			"must not raise what this Query Group is allowed to ask for", heavyLimit, moved, runtimeLoadBatchBytes)
	}
	if heavyLimit >= runtimeLoadBatchItems {
		t.Fatalf("heavy batch limit = %d, the full item bound - which is the 86 MB call, unchanged", heavyLimit)
	}
}

// A batch that did not come back teaches nothing.
//
// A failed read has loaded zero bytes. Folded in, it lowers the estimate and
// the next batch is allowed to be at least as large, so the read that was
// already too big to finish is reissued at the same size - and every failure
// makes the estimate smaller. An instrument that learns "smaller" from the
// event it exists to catch reports the reverse of the truth exactly when
// consulted.
//
// Stated through the load path rather than against the accounting function,
// because the distinction lives at the call: "came back empty" is a reading
// about records that are not there, and "did not come back" is no reading at
// all, and the two are told apart by the error. Written against the function,
// the case cannot see which of them is in force.
//
// The records are written first, and that is what makes this a test. Read
// against keys the backend does not hold, the estimate learns zero - and a
// failed batch that taught zero would leave it at zero too, so both answers
// are the same number and removing the guard changes nothing. A guard case has
// to make the two branches disagree before a mutant can fail it.
func TestAFailedBatchTeachesNothing(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	group := frozenRef().Slot.QueryGroup

	mutations := seriesMutations(t, 4, applyVersion(), 0)
	applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: mutations,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireAllStatus(t, applied, execution.StateApplied)

	request := execution.StatePreflightRequest{Contract: frozenRef(), Items: preflightItems(mutations)}
	if _, err := store.LoadRuntime(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	learned, ok := store.expectedValueBytes(group)
	if !ok {
		t.Fatal("a read that came back taught nothing")
	}
	if learned == 0 {
		t.Fatal("the records read back as empty, so a failed batch teaching zero would be the same " +
			"answer and this case could not tell the two apart")
	}

	backend.failMGet = errors.New("i/o timeout")
	if _, err := store.LoadRuntime(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	after, _ := store.expectedValueBytes(group)
	if after != learned {
		t.Fatalf("a failed batch moved the estimate from %d to %d; every timeout would let the next "+
			"batch be at least as large, which is the wrong direction from the only evidence there is",
			learned, after)
	}
}

// The learned size follows a strategy whose records just grew, because that is
// when the bound matters and a lifetime mean is slowest then.
func TestTheLearnedRecordSizeFollowsAStrategyThatGrew(t *testing.T) {
	store := sizedStore()
	const group = execution.QueryGroupIdentity("qg")
	for range 20 {
		store.commitValueBytes(group, 4096, true)
	}
	small := mustExpect(t, store, group)
	if small == 0 {
		t.Fatal("nothing learned from twenty batches")
	}
	for range 20 {
		store.commitValueBytes(group, 345*1024, true)
	}
	if grown := mustExpect(t, store, group); grown <= small*10 {
		t.Fatalf("expected bytes went from %d to %d after the records grew 86x; a bound that lags this "+
			"far behind is the item bound with extra steps", small, grown)
	}
}

// The table is bounded, and running past the bound costs one safe batch per
// Query Group rather than unbounded memory.
func TestTheLearnedSizeTableIsBounded(t *testing.T) {
	store := sizedStore()
	for index := range runtimeValueSizeGroups + 64 {
		store.commitValueBytes(execution.QueryGroupIdentity(string(rune('a'+index%26))+string(rune(index))), 4096, true)
	}
	store.valueSizes.mu.RLock()
	size := len(store.valueSizes.bytes)
	store.valueSizes.mu.RUnlock()
	if size > runtimeValueSizeGroups {
		t.Fatalf("learned sizes for %d Query Groups, over the %d bound", size, runtimeValueSizeGroups)
	}
}

// The bound is built from the round's largest record, not its mean.
//
// One round's keys are not a uniform population - a Query Group holds every
// shape its Plans produce, and during a representation migration it holds two
// populations tens of times apart. A mean is pulled down by the many small
// records, the batch grows to match, and the few large ones in it move more
// than the budget allows. The two answers have to be different numbers here or
// this case cannot tell which statistic is in force.
func TestTheBatchBoundComesFromTheLargestRecordNotTheMean(t *testing.T) {
	store := newBatchStore(t, newPipelineMemoryBackend(), nil)
	group := execution.QueryGroupIdentity("qg-mixed")

	const small, large = 4 * 1024, 512 * 1024
	// Ninety-nine small records and one large one: the mean is about 9 KiB and
	// the largest is 512 KiB, so the two size a batch fifty-six times apart.
	byMean := store.runtimeLoadBatchLimit(group, (99*small+large)/100, true)
	byLargest := store.runtimeLoadBatchLimit(group, large, true)
	if byMean == byLargest {
		t.Fatalf("mean and largest both allow %d keys, so this case cannot separate them", byMean)
	}
	if byLargest > byMean {
		t.Fatalf("the largest allows %d keys and the mean %d; a bound sized by the largest cannot be the "+
			"looser of the two", byLargest, byMean)
	}
	if moved := uint64(byLargest) * large; moved > runtimeLoadBatchBytes {
		t.Fatalf("a batch of %d keys of the largest record moves %d bytes, over the %d bound",
			byLargest, moved, runtimeLoadBatchBytes)
	}
}

// The bound comes back down when the records do.
//
// Scoped to the round for this reason. A high-water mark, or one reset only
// when the state generation changes, never falls for a strategy whose
// generation is stable - so a Query Group whose records shrank, which is
// exactly what changing their representation does, would stay on the batch
// its largest record ever needed for as long as the process ran, and the
// learning this bound exists for would be dead.
func TestTheBatchBoundFallsWhenTheRecordsShrink(t *testing.T) {
	store := newBatchStore(t, newPipelineMemoryBackend(), nil)
	group := execution.QueryGroupIdentity("qg-shrinking")

	store.commitValueBytes(group, 345*1024, true)
	wide := store.runtimeLoadBatchLimit(group, 0, false)

	// The next round reads the same keys and they are fifty times smaller.
	store.commitValueBytes(group, 7*1024, true)
	after := store.runtimeLoadBatchLimit(group, 0, false)

	if after <= wide {
		t.Fatalf("the batch limit was %d when the records were 345 KiB and %d after they shrank to 7 KiB; "+
			"a bound that only ever rises stops being a measurement of the population", wide, after)
	}
}

// A round that did not come back leaves the bound where it was.
func TestARoundThatDidNotComeBackLeavesTheBoundAlone(t *testing.T) {
	store := newBatchStore(t, newPipelineMemoryBackend(), nil)
	group := execution.QueryGroupIdentity("qg-failing")

	store.commitValueBytes(group, 345*1024, true)
	before, ok := store.expectedValueBytes(group)
	if !ok || before == 0 {
		t.Fatal("the first commit taught nothing, so a failed round teaching nothing would be the same " +
			"number and this case could not tell the two apart")
	}
	store.commitValueBytes(group, 4*1024, false)
	after, _ := store.expectedValueBytes(group)
	if after != before {
		t.Fatalf("a round that did not come back moved the bound from %d to %d; every failure would let the "+
			"next batch be at least as large, which is the wrong direction from the only evidence there is",
			before, after)
	}
}

// The bound is the round's largest record, not the last batch's.
//
// A round reads a Query Group in several batches, and after the first batch
// that came back the running largest sizes the ones after it, so a large
// record read early puts the small ones that follow into one wide batch.
// The largest of that last batch is then small. Committing it would hand the
// next round a bound sized by the small records, and its first batch would
// take the large one beside as many small ones as the small size allows -
// the over-budget read this bound exists to prevent, arriving one round late
// and on exactly the mixed population the bound was rewritten for. The other
// cases drive the accounting function directly and cannot see the fold
// across batches; this one goes through the load.
func TestTheRoundCommitsItsLargestRecordNotTheLastBatchs(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	group := frozenRef().Slot.QueryGroup

	// Sixty series: the first carries a record far larger than the rest, so
	// it lands in the first batch (eight keys while nothing is learned, at
	// MaxValueBytes 1 MiB); the running largest then allows forty keys, so
	// the small ones fill a second batch inside the loop and a third at the
	// end - two batches after the large one, both small, so a fold that
	// keeps only the latest batch's largest is small at the commit whichever
	// of the two sites it happens at.
	const largePadding = 200 * 1024
	mutations := make([]execution.StateMutation, 60)
	for index := range mutations {
		padding := ""
		if index == 0 {
			padding = strings.Repeat("x", largePadding)
		}
		mutations[index] = seriesMutation(t, seriesIdentity(index), applyVersion(), 0, padding)
	}
	applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: mutations,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireAllStatus(t, applied, execution.StateApplied)

	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{
		Contract: frozenRef(), Items: preflightItems(mutations),
	}); err != nil {
		t.Fatal(err)
	}
	learned := mustExpect(t, store, group)
	if learned < largePadding {
		t.Fatalf("the round read a %d-byte record and committed %d; a bound taken from the last batch "+
			"lets the next round put that record beside a batch sized for the small ones", largePadding, learned)
	}
}

// The running largest tightens a committed bound and never loosens it.
//
// The batches of one round are sized by the bound, so the first batch is
// whatever keys came first. A first batch of small records says nothing
// about the keys not yet read; the committed bound is the last complete
// measurement of them. A round that let its first sixteen replace it would
// size its second batch for 4 KiB records and read 512 KiB ones with it -
// the mixed population this bound exists for, a Query Group whose records
// are changing representation, or whose series differ in age.
func TestTheRunningLargestOnlyTightensACommittedBound(t *testing.T) {
	store := newBatchStore(t, newPipelineMemoryBackend(), nil)
	group := execution.QueryGroupIdentity("qg-mixed-round")

	const small, large = 4 * 1024, 512 * 1024
	store.commitValueBytes(group, large, true)
	committed := store.runtimeLoadBatchLimit(group, 0, false)
	afterSmall := store.runtimeLoadBatchLimit(group, small, true)
	if committed >= runtimeLoadBatchItems {
		t.Fatalf("the committed bound allows %d keys, the item cap; the fixture cannot show a widening past it", committed)
	}
	if afterSmall > committed {
		t.Fatalf("a first batch of %d-byte records widened the batch from %d to %d keys while the round's "+
			"unread keys may still weigh %d bytes each; the running largest may only tighten a committed bound",
			small, committed, afterSmall, large)
	}
	// Within the same round a larger record than committed still tightens.
	if tighter := store.runtimeLoadBatchLimit(group, 2*large, true); tighter >= committed {
		t.Fatalf("a %d-byte record read this round left the batch at %d keys, committed %d; the running "+
			"largest is direct evidence and must tighten", 2*large, tighter, committed)
	}
	// A Query Group nothing is committed for is sized by what its first batch
	// weighed, which is what keeps a cold one cheap.
	cold := execution.QueryGroupIdentity("qg-cold")
	if first := store.runtimeLoadBatchLimit(cold, 0, false); first != runtimeLoadBatchBytes/store.options.MaxValueBytes {
		t.Fatalf("an unmeasured Query Group's first batch is %d keys, want the safe %d", first, runtimeLoadBatchBytes/store.options.MaxValueBytes)
	}
	if widened := store.runtimeLoadBatchLimit(cold, small, true); widened <= runtimeLoadBatchBytes/store.options.MaxValueBytes {
		t.Fatalf("an unmeasured Query Group whose first batch weighed %d bytes stayed at %d keys; nothing is "+
			"committed to tighten against, so the running largest is the bound", small, widened)
	}
}
