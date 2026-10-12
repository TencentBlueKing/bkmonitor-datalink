// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package memoryline

import (
	"math"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// fakeHeap is the runtime's reading, set by the test.
type fakeHeap struct{ heap }

func (h *fakeHeap) read() heap { return h.heap }

// Observation grows while the live heap, what detection may still take and
// what observation was granted since the last collection fit the soft limit:
// up to the last byte, not one past it. Each refusal is counted under the
// consumer refused and nothing else is.
func TestObservationGrowsUpToTheLineAndNoFurther(t *testing.T) {
	h := &fakeHeap{heap{limit: 1000, live: 600, cycles: 1}}
	line := newLine(h.read)
	held := uint64(0)
	line.Reserve("detection", func() (uint64, uint64) { return 100, held })

	if !line.Admit(ConsumerCostSummary, 200) {
		t.Fatal("200 refused with 300 of room")
	}
	if !line.Admit(ConsumerLookback, 100) {
		t.Fatal("the last 100 refused: 600 live + 100 detection + 200 granted + 100 is the limit exactly")
	}
	if line.Admit(ConsumerLookback, 1) {
		t.Fatal("one byte past the line admitted")
	}
	reading := line.Read()
	if reading.RefusedTotal[ConsumerLookback] != 1 || reading.RefusedTotal[ConsumerCostSummary] != 0 ||
		reading.AdmittedBytes[ConsumerCostSummary] != 200 || reading.AdmittedBytes[ConsumerLookback] != 100 {
		t.Fatalf("reading = %+v, want one lookback refusal and the two grants by consumer", reading)
	}
	if reading.HeadroomBytes != 300 || reading.GrantedBytes != 300 || reading.ReservedBytes != 100 || reading.LiveBytes != 600 {
		t.Fatalf("reading = %+v, want headroom 1000-600-100 = 300 with 300 granted", reading)
	}

	// Detection taking what it had room for gives observation nothing: the
	// room was never observation's, and until the next collection the live
	// heap does not show that detection took it.
	held = 100
	if line.Admit(ConsumerSeriesSampler, 1) {
		t.Fatal("admitted into room detection had and filled")
	}

	// The next collection holds what the grants took: the grants are
	// forgotten, and the live heap is the reading.
	h.heap = heap{limit: 1000, live: 950, cycles: 2}
	if !line.Admit(ConsumerSeriesSampler, 50) || line.Admit(ConsumerSeriesSampler, 1) {
		t.Fatal("after a collection: want exactly the 50 the live heap leaves")
	}
	// Past the line: every consumer is refused, whichever asks.
	h.heap = heap{limit: 1000, live: 1200, cycles: 3}
	for _, consumer := range Consumers {
		if line.Admit(consumer, 1) {
			t.Fatalf("%s admitted past the line", consumer)
		}
	}
	if reading := line.Read(); reading.HeadroomBytes != -200 {
		t.Fatalf("headroom past the line = %d, want -200", reading.HeadroomBytes)
	}
}

// What detection gives back after a collection is still in the live heap
// that collection measured: the line reads detection's room as the
// collection left it, not again as room. A budget reserved after the
// collection is room from the next reading on.
func TestWhatDetectionGivesBackIsNotItsRoomTwice(t *testing.T) {
	// Detection held 300 of its 400 when the collection measured 700 live.
	h := &fakeHeap{heap{limit: 1000, live: 700, cycles: 1}}
	line := newLine(h.read)
	held := uint64(300)
	line.Reserve("detection", func() (uint64, uint64) { return 400, held })
	if reading := line.Read(); reading.HeadroomBytes != 200 {
		t.Fatalf("headroom = %d, want 1000-700-100", reading.HeadroomBytes)
	}
	// Its Slots finish: all 400 are room again, and the 300 they held are
	// still in the 700 until the next collection.
	held = 0
	if reading := line.Read(); reading.HeadroomBytes != 200 || reading.ReservedBytes != 100 {
		t.Fatalf("after detection gave back = %+v, want headroom 200: the 300 are live, not room as well", reading)
	}
	if !line.Admit(ConsumerCostProjection, 200) {
		t.Fatal("observation refused the 200 the line has")
	}
	// The next collection finds the 300 gone and all 400 room.
	h.heap = heap{limit: 1000, live: 600, cycles: 2}
	if reading := line.Read(); reading.HeadroomBytes != 0 || reading.ReservedBytes != 400 {
		t.Fatalf("after the next collection = %+v, want 1000-600-400", reading)
	}
	// A budget reserved since is room at once, its own: what the others took
	// since the collection stays counted in their room as the collection
	// left it.
	held = 300
	line.Reserve("detection", func() (uint64, uint64) { return 50, 0 })
	if reading := line.Read(); reading.ReservedBytes != 450 {
		t.Fatalf("reserved = %d, want the new budget's 50 beside the 400 the collection left", reading.ReservedBytes)
	}
}

// A budget that grows between two collections - a Worker taking on Query
// Groups, a Leader reading a larger publication - is room at once, before
// anything is read into it; one that shrinks keeps its room until the next
// collection, and what it holds moving in between changes nothing.
func TestABudgetThatGrowsIsRoomAtOnce(t *testing.T) {
	h := &fakeHeap{heap{limit: 1000, live: 500, cycles: 1}}
	line := newLine(h.read)
	size, held := uint64(100), uint64(40)
	line.Reserve("detection", func() (uint64, uint64) { return size, held })
	if reading := line.Read(); reading.ReservedBytes != 60 {
		t.Fatalf("reserved = %d, want 100-40", reading.ReservedBytes)
	}
	size = 300
	if reading := line.Read(); reading.ReservedBytes != 260 {
		t.Fatalf("after growing by 200 = %d, want 60+200 before anything is read into it", reading.ReservedBytes)
	}
	held = 250
	if line.Admit(ConsumerLookback, 250) {
		t.Fatal("observation took the room the grown budget was filling")
	}
	size, held = 150, 0
	if reading := line.Read(); reading.ReservedBytes != 260 {
		t.Fatalf("after shrinking and giving back = %d, want 260 until the next collection", reading.ReservedBytes)
	}
	h.heap = heap{limit: 1000, live: 500, cycles: 2}
	if reading := line.Read(); reading.ReservedBytes != 150 {
		t.Fatalf("at the next collection = %d, want 150-0", reading.ReservedBytes)
	}
}

// A reading names each budget with its size and what it holds now, in the
// order reserved.
func TestAReadingNamesEachBudget(t *testing.T) {
	line := newLine((&fakeHeap{heap{limit: 1000, live: 100, cycles: 1}}).read)
	size := uint64(300)
	line.Reserve("retained", func() (uint64, uint64) { return 500, 20 })
	line.Reserve("object_cache", func() (uint64, uint64) { return size, 200 })
	size = 250
	budgets := line.Read().Budgets
	if len(budgets) != 2 || budgets[0] != (BudgetReading{Name: "retained", SizeBytes: 500, HeldBytes: 20}) ||
		budgets[1] != (BudgetReading{Name: "object_cache", SizeBytes: 250, HeldBytes: 200}) {
		t.Fatalf("budgets = %+v, want each by name as it reads now", budgets)
	}
}

// A cache that names each batch it is about to store grows by the batch and
// falls back as it stores it; two batches in turn are room for the larger
// rise, not for the two added, or the line would hold observation off with
// room no batch took until the next collection.
func TestABudgetsGrowthIsItsHighestSizeNotItsRisesAdded(t *testing.T) {
	h := &fakeHeap{heap{limit: 1000, live: 400, cycles: 1}}
	line := newLine(h.read)
	size, held := uint64(100), uint64(100)
	line.Reserve("detection", func() (uint64, uint64) { return size, held })
	for _, batch := range []struct{ named, stored uint64 }{{200, 20}, {150, 20}, {200, 20}} {
		size = held + batch.named
		line.Read()
		held += batch.stored
		size = held
		line.Read()
	}
	// The highest size was the third batch named over the 40 stored before
	// it: 100 + 40 + 200. Its rise over the collection's 100 is what detection
	// can have taken since - the 60 stored lie within it - where the rises
	// added would be 200 + 150 + 200.
	if reading := line.Read(); reading.ReservedBytes != 240 {
		t.Fatalf("after three batches = %d, want the highest size's rise, 240, not the rises added (550)", reading.ReservedBytes)
	}
}

// Without a soft limit the runtime reports the largest one; the line then
// never refuses. A nil line admits everything and reads as empty.
func TestAProcessWithoutASoftLimitNeverRefuses(t *testing.T) {
	line := newLine((&fakeHeap{heap{limit: math.MaxInt64, live: 1 << 40, cycles: 1}}).read)
	if !line.Admit(ConsumerLookback, 1<<40) || !line.Read().LimitUnlimited {
		t.Fatal("no soft limit: refused, or not read as unlimited")
	}
	var none *Line
	if !none.Admit(ConsumerLookback, math.MaxUint64) || none.Read().HeadroomBytes != 0 || len(none.Read().RefusedTotal) != len(Consumers) {
		t.Fatal("a nil line refused, or read as other than empty")
	}
}

// The line over the running process reads the runtime: a limit and a live
// heap, and a grant within them is admitted.
func TestTheLineReadsTheRunningProcess(t *testing.T) {
	runtime.GC()
	line := New()
	reading := line.Read()
	if reading.LimitBytes == 0 || reading.LiveBytes == 0 {
		t.Fatalf("reading = %+v, want the runtime's limit and live heap", reading)
	}
	if !line.Admit(ConsumerCostProjection, 1) {
		t.Fatalf("one byte refused by a test process: %+v", reading)
	}
}

// The line over the running process takes detection's reserve as a
// collection ends. Detection that fills after the collection and before
// observation next asks is in neither the live heap nor what the budgets can
// still take by then; read at that next ask, the line would have counted the
// fill nowhere and given its room to observation.
func TestTheReserveIsTakenAsTheCollectionEnds(t *testing.T) {
	line := New()
	var held atomic.Uint64
	held.Store(0)
	line.Reserve("detection", func() (uint64, uint64) { return 1 << 30, held.Load() })
	before := line.read().cycles
	runtime.GC()
	deadline := time.Now().Add(10 * time.Second)
	for {
		line.mu.Lock()
		cycle := line.cycle
		line.mu.Unlock()
		if cycle > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no snapshot after the collection: the line is at cycle %d, the collection made it past %d", cycle, before)
		}
		time.Sleep(time.Millisecond)
	}
	// Detection fills 900 MiB of its budget before observation asks.
	held.Store(900 << 20)
	if reading := line.Read(); reading.ReservedBytes != 1<<30 {
		t.Fatalf("reserved = %d, want the %d the budgets could take as the collection ended", reading.ReservedBytes, uint64(1<<30))
	}
}

// A closed line is told of no collection after the one that ends its watch:
// a bundle that closed let its line go instead of keeping it, and every
// budget it reserves, for the life of the process.
func TestAClosedLineIsToldOfNoMoreCollections(t *testing.T) {
	line := New()
	line.Close()
	// The collection whose finalizer sees the line closed.
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	line.mu.Lock()
	cycle := line.cycle
	line.mu.Unlock()
	for range 3 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	line.mu.Lock()
	defer line.mu.Unlock()
	if line.cycle != cycle {
		t.Fatalf("a closed line moved from cycle %d to %d with no reading of its own", cycle, line.cycle)
	}
}

// A hold counts against the line while it is held and not after: released,
// its bytes are the next asker's at once, without waiting for a collection.
// Holds and grants share the one line.
func TestAHoldCountsOnlyWhileItIsHeld(t *testing.T) {
	line := newLine((&fakeHeap{heap{limit: 1000, live: 600, cycles: 1}}).read)
	release, held := line.Hold(ConsumerDiagnosisProgress, 300)
	if !held {
		t.Fatal("300 refused with 400 of room")
	}
	if line.Admit(ConsumerLookback, 101) {
		t.Fatal("admitted past the line while 300 were held")
	}
	if !line.Admit(ConsumerLookback, 100) {
		t.Fatal("the last 100 refused beside the 300 held")
	}
	if reading := line.Read(); reading.HeldBytes != 300 || reading.GrantedBytes != 100 {
		t.Fatalf("reading = %+v, want 300 held and 100 granted", reading)
	}
	release()
	if reading := line.Read(); reading.HeldBytes != 0 {
		t.Fatalf("held after release = %d, want 0", reading.HeldBytes)
	}
	if _, held := line.Hold(ConsumerDiagnosisProgress, 300); !held {
		t.Fatal("the released 300 were not the next asker's before a collection")
	}
	reading := line.Read()
	if reading.AdmittedBytes[ConsumerDiagnosisProgress] != 600 || reading.RefusedTotal[ConsumerLookback] != 1 {
		t.Fatalf("counters = %+v, want both holds admitted and the one lookback refusal", reading)
	}
}

// Releasing twice gives back once: a deferred release beside an early one
// must not give back another hold's bytes.
func TestAHoldIsReleasedOnce(t *testing.T) {
	line := newLine((&fakeHeap{heap{limit: 1000, live: 400, cycles: 1}}).read)
	first, _ := line.Hold(ConsumerDiagnosisProgress, 300)
	if _, held := line.Hold(ConsumerDiagnosisProgress, 300); !held {
		t.Fatal("second hold refused with 300 of room")
	}
	first()
	first()
	if reading := line.Read(); reading.HeldBytes != 300 {
		t.Fatalf("held after releasing the first twice = %d, want the second's 300", reading.HeldBytes)
	}
	if line.Admit(ConsumerLookback, 301) {
		t.Fatal("admitted into the second hold's bytes")
	}
}

// A refused hold takes nothing, is counted, and its release does nothing.
func TestARefusedHoldTakesNothing(t *testing.T) {
	line := newLine((&fakeHeap{heap{limit: 1000, live: 900, cycles: 1}}).read)
	keep, _ := line.Hold(ConsumerDiagnosisProgress, 50)
	release, held := line.Hold(ConsumerDiagnosisProgress, 51)
	if held {
		t.Fatal("51 held with 50 of room")
	}
	release()
	if reading := line.Read(); reading.HeldBytes != 50 || reading.RefusedTotal[ConsumerDiagnosisProgress] != 1 {
		t.Fatalf("reading = %+v, want the first hold's 50 and one refusal", reading)
	}
	keep()
}

// A collection does not end a hold: what it has allocated is in the live
// heap the collection measured, what it has not is in neither, so it counts
// until it is released - twice for the part both hold, for as long as the
// work lasts - where grants start over.
func TestAHoldOutlastsACollectionUntilReleased(t *testing.T) {
	h := &fakeHeap{heap{limit: 1000, live: 400, cycles: 1}}
	line := newLine(h.read)
	release, _ := line.Hold(ConsumerDiagnosisProgress, 300)
	line.Admit(ConsumerLookback, 100)
	h.heap = heap{limit: 1000, live: 500, cycles: 2}
	reading := line.Read()
	if reading.HeldBytes != 300 || reading.GrantedBytes != 0 {
		t.Fatalf("after the collection = %+v, want the hold still counted and the grant forgotten", reading)
	}
	if line.Admit(ConsumerLookback, 201) {
		t.Fatal("admitted into the room of a hold that outlasted the collection")
	}
	release()
	if !line.Admit(ConsumerLookback, 500) {
		t.Fatal("the released hold's room was not given back")
	}
}

// A nil line holds everything and its release does nothing.
func TestANilLineHoldsEverything(t *testing.T) {
	var line *Line
	release, held := line.Hold(ConsumerDiagnosisProgress, math.MaxUint64)
	if !held {
		t.Fatal("a nil line refused a hold")
	}
	release()
}
