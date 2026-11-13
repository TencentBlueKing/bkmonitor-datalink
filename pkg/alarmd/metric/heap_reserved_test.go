// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"sync/atomic"
	"testing"
	"time"
)

// A Slot gives its reservation back while the collection runs: the reading
// says what the pool held on either side of it, and keeps saying so until
// the next collection, whatever the pool holds by then.
func TestTheHeapReadingCarriesThePoolsReservationOnEitherSideOfItsCollection(t *testing.T) {
	clock := time.Date(2026, 11, 10, 21, 14, 13, 0, time.UTC)
	collector := newHeapSiteCollector(func() time.Time { return clock })
	var reserved atomic.Uint64
	reserved.Store(800 << 20)
	collector.bindReserved(func() uint64 { return reserved.Load() })
	collect := collector.collect
	collector.collect = func() {
		reserved.Store(0)
		collect()
	}
	gathered := gatherHeapSites(t, collector)
	at := gathered["alarmd_heap_inuse_reserved_bytes"]
	if at["gc_start"] != 800<<20 || at["gc_end"] != 0 {
		t.Fatalf("reserved at the collection = %v, want 800 MiB at gc_start and 0 at gc_end", at)
	}
	reserved.Store(300 << 20)
	reading, known := collector.cachedLive()
	if !known || !reading.ReservedKnown || reading.ReservedAtStart != 800<<20 || reading.ReservedAtEnd != 0 || reading.Live == 0 {
		t.Fatalf("cached reading = %+v (known %v), want the live heap with the collection's own 800 MiB and 0", reading, known)
	}
	clock = clock.Add(heapSiteRefresh - time.Second)
	if again := gatherHeapSites(t, collector)["alarmd_heap_inuse_reserved_bytes"]; again["gc_start"] != 800<<20 || again["gc_end"] != 0 {
		t.Fatalf("a scrape inside the minute read %v, want the collection's pair and not the pool's 300 MiB now", again)
	}
}

// Before a pool is bound there is no reservation to read the live heap
// against, and none is published as zero.
func TestTheHeapReadingSaysNothingOfAPoolNotBound(t *testing.T) {
	collector := newHeapSiteCollector(time.Now)
	if _, present := gatherHeapSites(t, collector)["alarmd_heap_inuse_reserved_bytes"]; present {
		t.Fatal("an unbound pool's reservation was published")
	}
	if reading, known := collector.cachedLive(); !known || reading.ReservedKnown {
		t.Fatalf("cached reading = %+v (known %v), want the live heap and no reservation", reading, known)
	}
}

// The recorder hands the pool it is bound to to its heap reading, and the
// flow carries the pair.
func TestTheRecordersPoolIsReadBesideItsHeap(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	if err := r.BindRetainedReservation(func() uint64 { return 42 << 20 }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Gatherer().Gather(); err != nil {
		t.Fatal(err)
	}
	flow := r.FlowCounters()
	if !flow.LiveHeapKnown || !flow.LiveHeapReserved.ReservedKnown || flow.LiveHeapReserved.ReservedAtStart != 42<<20 ||
		flow.LiveHeapReserved.ReservedAtEnd != 42<<20 || flow.LiveHeapReserved.Live != flow.LiveHeapBytes {
		t.Fatalf("flow heap = %d (known %v) reserved %+v, want the pool's 42 MiB on both sides", flow.LiveHeapBytes,
			flow.LiveHeapKnown, flow.LiveHeapReserved)
	}
}
