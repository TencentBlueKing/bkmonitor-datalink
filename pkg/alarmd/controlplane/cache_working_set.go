// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import "sync"

// cacheWorkingSet is what a cache is about to hold, as a detection budget of
// observation memory (package memoryline) reads it: its size is not the
// cache's ceiling but what it holds and what it is reading to store.
//
// A cache's ceiling is its eviction bound, derived large on purpose so a
// Worker that takes on more Query Groups has room; read as the budget, it
// held hundreds of megabytes off observation for a few megabytes of working
// set. What a cache is about to hold is known only to the reader that is
// about to store it, and only before the bytes arrive: every path that
// reads to store announces the entries first (announceLocked), each is
// struck as it is stored, and whatever the read never stores - it failed,
// was cancelled, panicked, or the entry was already there - is struck when
// the reader settles, which it does with defer.
//
// Guarded by the mutex of the cache it belongs to.
type cacheWorkingSet struct {
	// reading is the entries announced and not yet stored or settled.
	reading int
	// largest is the largest charge of one entry the cache has stored, never
	// lowered: what an entry not yet read is taken to cost. Its size is not
	// known before it is read, and one Query Group's object can be a hundred
	// times another's.
	largest int
}

// cacheReading is one reader's announcement: the entries it has not yet
// stored. Its fields are the cache's, under the cache's mutex.
type cacheReading struct {
	mu   *sync.Mutex
	set  *cacheWorkingSet
	left int
}

// announceLocked counts entries a reader is about to read and store.
func (set *cacheWorkingSet) announceLocked(mu *sync.Mutex, entries int) *cacheReading {
	entries = max(entries, 0)
	set.reading += entries
	return &cacheReading{mu: mu, set: set, left: entries}
}

// storedLocked counts one entry stored at charge, and strikes it from the
// reading that announced it. A reading announced to another cache - one the
// object cache was replaced by since - is struck from its own when it
// settles.
func (set *cacheWorkingSet) storedLocked(reading *cacheReading, charge int) {
	set.largest = max(set.largest, charge)
	if reading != nil && reading.set == set && reading.left > 0 {
		reading.left--
		set.reading--
	}
}

// sizeLocked is the working set within ceiling: held, and an entry at the
// largest charge for each one announced. Before any entry was stored an
// announced one is taken at the ceiling: its shape has not been seen.
func (set *cacheWorkingSet) sizeLocked(held, ceiling int) int {
	held, ceiling = max(held, 0), max(ceiling, 0)
	if held >= ceiling {
		return ceiling
	}
	if set.reading == 0 {
		return held
	}
	if set.largest == 0 || set.reading > (ceiling-held)/set.largest {
		return ceiling
	}
	return held + set.reading*set.largest
}

// settle strikes what the reader announced and never stored. Deferred by
// every reader right after it announces; a nil reading - nothing was
// announced, the cache is not configured - settles nothing.
func (reading *cacheReading) settle() {
	if reading == nil {
		return
	}
	reading.mu.Lock()
	defer reading.mu.Unlock()
	reading.set.reading -= reading.left
	reading.left = 0
}
