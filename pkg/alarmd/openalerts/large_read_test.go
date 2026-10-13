// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A set handed back as too slow for the round is read on its own, outside
// the round's deadline, and its members are applied; while it is being read,
// a second such set waits for a later round rather than starting a second
// long scan beside it.
func TestALargeSetIsReadOnItsOwnOneAtATime(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	// The large read runs on its own goroutine and reads the clock there.
	var clockMu sync.Mutex
	at := c.at
	options.Now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return at }
	advance := func(by time.Duration) { clockMu.Lock(); at = at.Add(by); clockMu.Unlock() }
	release := make(chan struct{})
	var mu sync.Mutex
	own := map[StrategyKey]int{}
	var running, most atomic.Int32
	options.Source = setReaderFunc(func(ctx context.Context, key StrategyKey) ([]string, error) {
		if ctx.Value(largeReadKey{}) == nil {
			return nil, &LargeSetError{Members: 5000, Budget: time.Minute}
		}
		now := running.Add(1)
		for {
			seen := most.Load()
			if now <= seen || most.CompareAndSwap(seen, now) {
				break
			}
		}
		defer running.Add(-1)
		mu.Lock()
		own[key]++
		mu.Unlock()
		<-release
		return []string{"member-of-" + key.StrategyID}, nil
	})
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA, keyB}); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	started := len(own)
	mu.Unlock()
	if started != 1 {
		t.Fatalf("%d sets read on their own at once, want one", started)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cache.Snapshot(keyA).IndexReadAt.IsZero() && cache.Snapshot(keyB).IndexReadAt.IsZero() {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		break
	}
	advance(2 * time.Minute)
	for index := 0; index < 20 && (cache.Snapshot(keyA).IndexReadAt.IsZero() || cache.Snapshot(keyB).IndexReadAt.IsZero()); index++ {
		cache.Refresh(context.Background())
		time.Sleep(20 * time.Millisecond)
		advance(2 * time.Minute)
	}
	if cache.Snapshot(keyA).IndexReadAt.IsZero() || cache.Snapshot(keyB).IndexReadAt.IsZero() {
		t.Fatalf("large sets not read: %+v %+v", cache.Snapshot(keyA), cache.Snapshot(keyB))
	}
	if most.Load() != 1 {
		t.Fatalf("%d large reads ran at once, want one", most.Load())
	}
}

// A set refused for its size is named in the stats, with its members and
// the budget, until a read of it succeeds.
func TestStatsNameTheSetsRefusedForTheirSize(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	refuse := true
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) {
		if refuse {
			return nil, &SetTooLargeError{Members: 90000, MaxMembers: 50000, MaxBytes: 1 << 20}
		}
		return []string{"member"}, nil
	})
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	stats := cache.Stats()
	if len(stats.TooLarge) != 1 || stats.TooLarge[0].Strategy != keyA || stats.TooLarge[0].Refusal.Members != 90000 || stats.TooLarge[0].Refusal.MaxMembers != 50000 {
		t.Fatalf("too large %+v, want strategy A at 90000 of 50000", stats.TooLarge)
	}
	refuse = false
	c.at = c.at.Add(2 * time.Hour)
	cache.indexChanged(keyA)
	cache.Refresh(context.Background())
	if stats := cache.Stats(); len(stats.TooLarge) != 0 {
		t.Fatalf("still named after a complete read: %+v", stats.TooLarge)
	}
}
