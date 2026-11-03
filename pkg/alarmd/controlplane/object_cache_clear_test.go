// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"strconv"
	"sync"
	"testing"
)

// Dropping the cached objects keeps the budget the cache was configured
// with, and a read announced before the drop still stores what it read and
// settles its announcement.
func TestForgettingCachedObjectsKeepsTheBudgetAndTheReadsInFlight(t *testing.T) {
	repository := &RedisCatalogRepository{}
	if err := repository.ConfigureObjectCache(64, 4096); err != nil {
		t.Fatal(err)
	}
	cache := repository.objects()
	early := cache.announce(1)
	cache.store(cache.announce(1), "a", "object a", 100)
	inFlight := cache.announce(1)

	repository.ForgetCachedObjects()
	stats := repository.ObjectCacheStats()
	if stats.Occupancy.Entries != 0 || stats.Occupancy.Bytes != 0 || stats.Clears != 1 {
		t.Fatalf("after the drop: %+v, want empty with one clear", stats)
	}
	if stats.Occupancy.MaxEntries != 64 || stats.Occupancy.MaxBytes != 4096 {
		t.Fatalf("budget after the drop = %d entries, %d bytes; want 64 and 4096", stats.Occupancy.MaxEntries, stats.Occupancy.MaxBytes)
	}
	cache.store(inFlight, "b", "object b", 200)
	early.settle()
	if value, _, ok := cache.lookup("b"); !ok || value != "object b" {
		t.Fatalf("a read in flight across the drop stored (%v, %v)", value, ok)
	}
	if _, _, ok := cache.lookup("a"); ok {
		t.Fatal("an object from before the drop is still cached")
	}
	cache.mu.Lock()
	reading := cache.working.reading
	cache.mu.Unlock()
	if reading != 0 {
		t.Fatalf("announced reads left = %d after every reading stored or settled, want 0", reading)
	}
}

// Reads, stores and drops running at once neither race nor hand a reader an
// object stored under another key. Run with -race.
func TestObjectReadsRunningAcrossDropsStayConsistent(t *testing.T) {
	repository := &RedisCatalogRepository{}
	if err := repository.ConfigureObjectCache(32, 1<<20); err != nil {
		t.Fatal(err)
	}
	cache := repository.objects()
	var group sync.WaitGroup
	stop := make(chan struct{})
	failures := make(chan string, 8)
	for worker := range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			for round := 0; ; round++ {
				select {
				case <-stop:
					return
				default:
				}
				key := strconv.Itoa(worker) + "/" + strconv.Itoa(round%16)
				if value, _, ok := cache.lookup(key); ok && value != key {
					failures <- "lookup of " + key + " returned another object"
					return
				}
				reading := cache.announce(1)
				cache.store(reading, key, key, 64)
				reading.settle()
			}
		}()
	}
	for range 200 {
		repository.ForgetCachedObjects()
	}
	close(stop)
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	if stats := repository.ObjectCacheStats(); stats.Clears != 200 || stats.Occupancy.Entries > 32 {
		t.Fatalf("after the drops: %+v", stats)
	}
}
