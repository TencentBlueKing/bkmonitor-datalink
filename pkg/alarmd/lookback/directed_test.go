// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// delivered is a provider answer whose deliveries are whole, as a query's
// are, over the Query Group's query.
func delivered(series ...*execution.Dataset) answer {
	return deliveredFor("qg-query", series...)
}

// deliveredFor is delivered over the physical query given.
func deliveredFor(digest execution.PhysicalQueryDigest, series ...*execution.Dataset) answer {
	return func(sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		var total execution.SeriesDelivery
		for index, one := range series {
			delivery := execution.SeriesDelivery{PhysicalQuery: digest, QueryRevision: "revision", Series: 1,
				Records: uint64(one.Len()), Bytes: 100, Digest: fmt.Sprintf("%064x", index+1)}
			_ = sink.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{PhysicalQuery: digest,
				CompletionRef: "ref", Dataset: one, Delivery: delivery})
			total, _ = execution.AccumulateSeriesDelivery(total, delivery)
		}
		return execution.ProviderCompletion{Ref: "ref", PhysicalQuery: digest, Completeness: execution.CompletenessFull,
			DataState: execution.DataStateData, Delivery: total}, nil
	}
}

type supplementRecorder struct {
	mu      sync.Mutex
	jobs    []SupplementJob
	outcome SupplementOutcome
}

func (recorder *supplementRecorder) run(_ context.Context, job SupplementJob) SupplementOutcome {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.jobs = append(recorder.jobs, job)
	return recorder.outcome
}

func (recorder *supplementRecorder) taken() []SupplementJob {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]SupplementJob(nil), recorder.jobs...)
}

// directedFixture is a fixture whose Query Group qg is series_late - h2 came
// at the first rung of a sample - and whose samples rest, so every first
// read from here on is a directed one alone.
func directedFixture(t *testing.T, outcome SupplementOutcome) (*fixture, *supplementRecorder) {
	t.Helper()
	f := newFixture(t)
	recorder := &supplementRecorder{outcome: outcome}
	f.engine.options.Supplement = recorder.run
	f.classSample(0, []*execution.Dataset{point(f.clock.now().Unix(), "1")}, func(slot int64) []*execution.Dataset {
		return []*execution.Dataset{point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"})}
	})
	f.engine.mu.Lock()
	state := f.engine.groups["qg"]
	if state.seriesLate == nil {
		f.engine.mu.Unlock()
		t.Fatal("the fixture's group is not series_late")
	}
	state.nextAt = f.clock.now().Add(24 * time.Hour)
	f.engine.mu.Unlock()
	return f, recorder
}

// firstRead is the Slot's formal first read of qg, with h1 in it, ending
// complete or not.
func (f *fixture) firstRead(slot int64, completeness execution.Completeness) *Read {
	f.t.Helper()
	read := f.engine.Begin(query("qg", slot, minute, sourceLog))
	if read == nil || read.directed == nil {
		f.t.Fatal("a directed Query Group's first read was not kept for its directed read")
	}
	read.Series(point(slot, "1"), 100)
	read.Complete(execution.ProviderCompletion{Completeness: completeness}, nil)
	return read
}

type replaySink struct {
	batches []execution.ProviderSeriesBatch
}

func (sink *replaySink) ConsumeProviderSeries(_ context.Context, batch execution.ProviderSeriesBatch) error {
	sink.batches = append(sink.batches, batch)
	return nil
}

// Every Slot of a series_late Query Group is read again at the rung its
// late series were seen at: the Slot's own frozen query, whole. The series
// the read has that the Slot's first read did not are handed to the
// supplement, as the read they came in - replayed, it delivers those series
// alone with the query's completion restated for them - and the Slot is
// counted by what the supplement came to.
func TestASeriesLateGroupsSlotIsReadAgainAndItsLateSeriesSupplemented(t *testing.T) {
	f, recorder := directedFixture(t, SupplementOutcome{Ran: true,
		Facts: execution.SupplementFacts{Candidates: 1, Admitted: 1, Points: 1}})
	slot := f.clock.now().Unix()
	f.firstRead(slot, execution.CompletenessFull)
	readAt := f.clock.now()
	f.clock.set(readAt.Add(rungDelay(0, minute)))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })

	// The Slot's frozen query itself, as the Slot read it, not a tail.
	f.mu.Lock()
	asked := f.specs[len(f.specs)-1]
	f.mu.Unlock()
	if frozen := query("qg", slot, minute, sourceLog).Spec; !reflect.DeepEqual(asked, frozen) {
		t.Fatalf("the directed read asked for %+v, want the Slot's frozen query %+v", asked, frozen)
	}
	jobs := recorder.taken()
	if len(jobs) != 1 || len(jobs[0].Series) != 1 || jobs[0].Series[0] != "digest-h2" || jobs[0].QueryGroup != "qg" ||
		jobs[0].EvaluationTime != execution.EvaluationTime(slot) ||
		!jobs[0].Deadline.Equal(readAt.Add(rungDelay(0, minute)+rungWindow(0, minute))) {
		t.Fatalf("jobs %+v", jobs)
	}
	sink := &replaySink{}
	completion, err := jobs[0].Read.Replay(context.Background(), query("qg", slot, minute, sourceLog).Spec, sink)
	if err != nil || len(sink.batches) != 1 || completion.Delivery.Series != 1 || completion.Delivery != sink.batches[0].Delivery ||
		completion.DataState != execution.DataStateData || completion.Ref != "ref" {
		t.Fatalf("replay %+v completion %+v error %v", sink.batches, completion, err)
	}
	record, _ := sink.batches[0].Dataset.Record(0)
	if record.DimensionIdentityDigest() != "digest-h2" {
		t.Fatalf("replayed %s, want the late series only", record.DimensionIdentityDigest())
	}
	if _, err := jobs[0].Read.Replay(context.Background(), query("qg-b", slot, minute, sourceLog).Spec, sink); !errors.Is(err, ErrNotKept) {
		t.Fatalf("another query replayed: %v", err)
	}

	source := stats.Sources[sourceLog]
	if source.SupplementSeries[SeriesAdmitted] != 1 || source.SupplementPoints != 1 || source.DirectedReadBytes != 200 {
		t.Fatalf("series %v points %d bytes %d", source.SupplementSeries, source.SupplementPoints, source.DirectedReadBytes)
	}
	if len(stats.Supplements) != 1 || stats.Supplements[0].Windows[DirectedSupplemented] != 1 ||
		stats.Supplements[0].Series.Admitted != 1 || stats.Supplements[0].Coverage != 1 || stats.Supplements[0].Rung != RungNames[0] {
		t.Fatalf("supplements %+v", stats.Supplements)
	}
}

// A Slot whose read again has no series its first read did not is nothing
// to supplement: counted, and no supplement is asked for.
func TestADirectedSlotWithNothingLateAsksForNoSupplement(t *testing.T) {
	f, recorder := directedFixture(t, SupplementOutcome{Ran: true})
	slot := f.clock.now().Unix()
	f.firstRead(slot, execution.CompletenessFull)
	f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
	f.answers <- delivered(point(slot, "1"))
	f.engine.Step(context.Background())
	stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedNothingLate] == 1 })
	if len(recorder.taken()) != 0 || stats.Supplements[0].Coverage != 0 {
		t.Fatalf("jobs %d coverage %v", len(recorder.taken()), stats.Supplements[0].Coverage)
	}
}

// A Slot not read is unobserved, by why: its rung passed without a permit,
// its read failed, its own first read was not whole, it has more than one
// physical query, or the memory line refused what it had to keep. None of
// them asks for a supplement, and each counts against the coverage.
func TestADirectedSlotNotReadIsUnobservedByWhy(t *testing.T) {
	for _, tc := range []struct {
		reason string
		setup  func(f *fixture, slot int64)
		answer answer
	}{
		{UnobservedYielded, func(f *fixture, slot int64) {
			f.firstRead(slot, execution.CompletenessFull)
			f.clock.set(f.clock.now().Add(rungDelay(0, minute) + rungWindow(0, minute) + time.Second))
		}, nil},
		{UnobservedReadFailed, func(f *fixture, slot int64) {
			f.firstRead(slot, execution.CompletenessFull)
			f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
		}, func(execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			return execution.ProviderCompletion{Completeness: execution.CompletenessPartial}, nil
		}},
		{UnobservedFirstReadIncomplete, func(f *fixture, slot int64) {
			f.firstRead(slot, execution.CompletenessPartial)
			f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
		}, nil},
		{UnobservedMultiQuery, func(f *fixture, slot int64) {
			f.firstRead(slot, execution.CompletenessFull)
			other := query("qg", slot, minute, sourceLog)
			other.Spec.Digest = "qg-dependency"
			read := f.engine.Begin(other)
			read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
			f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
		}, nil},
		{UnobservedMemoryRefused, func(f *fixture, slot int64) {
			f.engine.options.Memory = func(uint64) bool { return false }
			f.firstRead(slot, execution.CompletenessFull)
			f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
		}, nil},
	} {
		f, recorder := directedFixture(t, SupplementOutcome{Ran: true})
		tc.setup(f, f.clock.now().Unix())
		if tc.answer != nil {
			f.answers <- tc.answer
		}
		f.engine.Step(context.Background())
		stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedUnobserved] == 1 })
		if got := stats.Sources[sourceLog].SupplementUnobserved; got[tc.reason] != 1 || len(recorder.taken()) != 0 {
			t.Fatalf("%s: unobserved %v jobs %d", tc.reason, got, len(recorder.taken()))
		}
	}
}

// A Slot read whose supplement did not run is counted by why, beside the
// coverage: its Query Group's Slot was executing, its contract is no longer
// kept, or it failed.
func TestADirectedSlotWhoseSupplementDidNotRunIsCountedByWhy(t *testing.T) {
	for refused, want := range map[string]string{DirectedFlightBusy: DirectedFlightBusy,
		DirectedContractExpired: DirectedContractExpired, "redis": DirectedFailed} {
		f, _ := directedFixture(t, SupplementOutcome{Refused: refused})
		slot := f.clock.now().Unix()
		f.firstRead(slot, execution.CompletenessFull)
		f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
		f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
		f.engine.Step(context.Background())
		stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[want] == 1 })
		if stats.Supplements[0].Coverage != 0 || stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] != 0 {
			t.Fatalf("%s: supplements %+v", refused, stats.Supplements)
		}
	}
}

// Its series late no more for two complete samples in a row, a Query Group
// is not read directed from then on; and without a supplement to hand late
// series to, none is.
func TestADirectedGroupStopsWhenItsSeriesAreLateNoMore(t *testing.T) {
	f, recorder := directedFixture(t, SupplementOutcome{Ran: true})
	f.engine.mu.Lock()
	f.engine.groups["qg"].nextAt = f.clock.now()
	f.engine.mu.Unlock()
	// The samples alone, so their rungs are the only reads answered.
	f.engine.options.Supplement = nil
	whole := func(slot int64) []*execution.Dataset { return []*execution.Dataset{point(slot, "1")} }
	for round := 0; round < seriesLateCleanToEnd; round++ {
		f.classSample(0, whole(f.clock.now().Unix()), whole)
	}
	f.engine.mu.Lock()
	late := f.engine.groups["qg"].seriesLate
	f.engine.mu.Unlock()
	if late != nil {
		t.Fatalf("still series_late after %d complete samples: %+v", seriesLateCleanToEnd, late)
	}
	f.engine.options.Supplement = recorder.run
	if read := f.engine.Begin(query("qg", f.clock.now().Unix(), minute, sourceLog)); read.directed != nil {
		t.Fatal("a Query Group late no more was read directed")
	}

	plain, _ := directedFixture(t, SupplementOutcome{Ran: true})
	plain.engine.options.Supplement = nil
	if read := plain.engine.Begin(query("qg", plain.clock.now().Unix(), minute, sourceLog)); read.directed != nil {
		t.Fatal("a directed read was kept with no supplement to hand it to")
	}
}

// A directed read asks for the Slot's frozen query whole - a day's window
// here, far longer than the tail a recheck reads - and the read it keeps
// replays for that query alone: a tail of it, which a supplement would
// evaluate on a shortened history, is refused rather than replayed.
func TestADirectedReadAsksForTheSlotsFrozenQueryWhole(t *testing.T) {
	f := newFixture(t)
	recorder := &supplementRecorder{outcome: SupplementOutcome{Ran: true}}
	f.engine.options.Supplement = recorder.run
	end := f.clock.now().Unix()
	day := func(slot int64) Query {
		spec := execution.PhysicalQuerySpec{PlanFacts: facts(t, minute, "", execution.QueryClause{}),
			LogicalWindow: execution.QueryWindow{Start: slot - 24*60*60, End: slot}}
		spec.ProviderRange, spec.AcceptedRange = spec.LogicalWindow, spec.LogicalWindow
		digest, err := execution.DerivePhysicalQueryDigest(spec)
		if err != nil {
			t.Fatal(err)
		}
		spec.Digest = digest
		return Query{Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "qg",
			EvaluationTime: execution.EvaluationTime(slot)}}, Spec: spec, Operation: execution.OperationNormal, AttemptNo: 1}
	}
	// The group, series_late, with its samples resting.
	f.engine.Begin(day(end-60)).Complete(execution.ProviderCompletion{Completeness: execution.CompletenessPartial}, nil)
	f.engine.mu.Lock()
	state := f.engine.groups["qg"]
	state.seriesLate, state.nextAt = &seriesLateState{since: f.clock.now()}, f.clock.now().Add(24*time.Hour)
	f.engine.mu.Unlock()

	frozen := day(end)
	read := f.engine.Begin(frozen)
	read.Series(dataset("h1", map[int64]string{end - 60: "1"}), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
	f.answers <- deliveredFor(frozen.Spec.Digest, dataset("h1", map[int64]string{end - 60: "1"}),
		dataset("h2", map[int64]string{end - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool {
		return stats.Sources[sourceTimeSeries].SupplementWindows[DirectedSupplemented] == 1
	})

	f.mu.Lock()
	asked := f.specs[len(f.specs)-1]
	f.mu.Unlock()
	if !reflect.DeepEqual(asked, frozen.Spec) {
		t.Fatalf("the directed read asked for %+v, want the Slot's frozen query %+v", asked.LogicalWindow, frozen.Spec.LogicalWindow)
	}
	tail := tailSpec(frozen.Spec, end-65*60)
	if tail.Digest == frozen.Spec.Digest {
		t.Fatal("the fixture's tail is the whole query")
	}
	jobs := recorder.taken()
	if len(jobs) != 1 {
		t.Fatalf("jobs %d", len(jobs))
	}
	if _, err := jobs[0].Read.Replay(context.Background(), tail, &replaySink{}); !errors.Is(err, ErrNotKept) {
		t.Fatalf("a tail of the query replayed: %v", err)
	}
	if _, err := jobs[0].Read.Replay(context.Background(), frozen.Spec, &replaySink{}); err != nil {
		t.Fatalf("the frozen query did not replay: %v", err)
	}
}

// A directed Query Group's Slots are read one at a time, the oldest first:
// the supplement of a Slot runs before the supplement of the Slot after
// it, so a series late in both is supplemented at the first before the
// second moves its State past it.
func TestADirectedGroupsSlotsAreReadOneAtATimeOldestFirst(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{Ran: true})
	release := make(chan struct{})
	var mu sync.Mutex
	var order []execution.EvaluationTime
	f.engine.options.Supplement = func(_ context.Context, job SupplementJob) SupplementOutcome {
		mu.Lock()
		order = append(order, job.EvaluationTime)
		first := len(order) == 1
		mu.Unlock()
		if first {
			<-release
		}
		return SupplementOutcome{Ran: true}
	}
	first := f.clock.now().Unix()
	f.firstRead(first, execution.CompletenessFull)
	readAt := f.clock.now()
	f.clock.set(readAt.Add(minute))
	second := f.clock.now().Unix()
	f.firstRead(second, execution.CompletenessFull)
	// Both due: the first at the end of its rung's window, the second at its
	// moment.
	f.clock.set(readAt.Add(rungDelay(0, minute) + minute))
	late := func(slot int64) answer {
		return delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	}
	f.answers <- late(first)
	f.engine.Step(context.Background())
	for {
		mu.Lock()
		started := len(order)
		mu.Unlock()
		if started == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// Its supplement still running, the second is not read.
	f.engine.Step(context.Background())
	if len(f.answers) != 0 {
		t.Fatal("an answer was taken off the queue with no read due")
	}
	f.answers <- late(second)
	f.engine.Step(context.Background())
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	if len(order) != 1 {
		mu.Unlock()
		t.Fatalf("supplements %v, want the second Slot to wait for the first", order)
	}
	mu.Unlock()
	close(release)
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 2 })
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != execution.EvaluationTime(first) || order[1] != execution.EvaluationTime(second) {
		t.Fatalf("supplements %v, want %d then %d", order, first, second)
	}
}

// A supplement that took its Query Group's flight is counted by how long it
// held it - the time the group's own Slot waited behind it - with the
// longest kept; one refused for the flight never held it and is not counted.
func TestASupplementIsCountedByHowLongItHeldItsQueryGroupsFlight(t *testing.T) {
	for _, test := range []struct {
		outcome SupplementOutcome
		bucket  string
	}{
		{SupplementOutcome{Ran: true, Held: 40 * time.Millisecond}, "le_100ms"},
		{SupplementOutcome{Ran: true, Held: 300 * time.Millisecond}, "le_500ms"},
		{SupplementOutcome{Refused: DirectedContractExpired, Held: 900 * time.Millisecond}, "le_1s"},
		{SupplementOutcome{Refused: "redis", Held: 4 * time.Second}, "le_5s"},
		{SupplementOutcome{Ran: true, Held: 7 * time.Second}, "gt_5s"},
		{SupplementOutcome{Refused: DirectedFlightBusy}, ""},
	} {
		f, _ := directedFixture(t, test.outcome)
		slot := f.clock.now().Unix()
		f.firstRead(slot, execution.CompletenessFull)
		f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
		f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
		f.engine.Step(context.Background())
		stats := f.waitFor(func(stats Stats) bool {
			windows := uint64(0)
			for _, n := range stats.Sources[sourceLog].SupplementWindows {
				windows += n
			}
			return windows == 1
		})
		source := stats.Sources[sourceLog]
		counted := uint64(0)
		for _, bucket := range SupplementHoldBuckets {
			counted += source.SupplementHold[bucket]
		}
		switch {
		case test.bucket == "":
			if counted != 0 || source.SupplementHoldMaxSeconds != 0 {
				t.Errorf("refused for the flight: hold %v max %v, want nothing counted", source.SupplementHold, source.SupplementHoldMaxSeconds)
			}
		case counted != 1 || source.SupplementHold[test.bucket] != 1 || source.SupplementHoldMaxSeconds != test.outcome.Held.Seconds():
			t.Errorf("held %v: hold %v max %v, want one under %s and the max %v", test.outcome.Held, source.SupplementHold,
				source.SupplementHoldMaxSeconds, test.bucket, test.outcome.Held.Seconds())
		}
	}
}

// Each bound is the top of its bucket: exactly 100 ms is le_100ms and a
// nanosecond past it is le_500ms, and so on up to gt_5s.
func TestASupplementsHoldFallsInTheBucketItsBoundCloses(t *testing.T) {
	for held, want := range map[time.Duration]string{
		0: "le_100ms", 100 * time.Millisecond: "le_100ms", 100*time.Millisecond + 1: "le_500ms",
		500 * time.Millisecond: "le_500ms", 500*time.Millisecond + 1: "le_1s",
		time.Second: "le_1s", time.Second + 1: "le_5s",
		5 * time.Second: "le_5s", 5*time.Second + 1: "gt_5s",
	} {
		if got := holdBucket(held); got != want {
			t.Errorf("holdBucket(%v) = %s, want %s", held, got, want)
		}
	}
}

// The longest hold is kept, not the last one: a long supplement followed by
// a short one leaves the long one as the maximum, both counted.
func TestTheLongestSupplementHoldIsKeptNotTheLast(t *testing.T) {
	f, recorder := directedFixture(t, SupplementOutcome{Ran: true, Held: 3 * time.Second})
	for index, held := range []time.Duration{3 * time.Second, time.Second} {
		recorder.mu.Lock()
		recorder.outcome = SupplementOutcome{Ran: true, Held: held}
		recorder.mu.Unlock()
		slot := f.clock.now().Unix()
		f.firstRead(slot, execution.CompletenessFull)
		f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
		f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
		f.engine.Step(context.Background())
		want := uint64(index + 1)
		f.waitFor(func(stats Stats) bool {
			return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == want
		})
	}
	source := f.engine.Stats().Sources[sourceLog]
	if source.SupplementHoldMaxSeconds != 3 || source.SupplementHold["le_5s"] != 1 || source.SupplementHold["le_1s"] != 1 {
		t.Fatalf("hold %v max %v, want both counted and the longest, 3 s, kept", source.SupplementHold, source.SupplementHoldMaxSeconds)
	}
}
