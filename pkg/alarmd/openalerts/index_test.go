// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type setReaderFunc func(context.Context, StrategyKey) ([]string, error)

func (f setReaderFunc) ReadSet(ctx context.Context, key StrategyKey) ([]string, error) {
	return f(ctx, key)
}

type reconcilerFunc func(context.Context, StrategyKey) (Reconciliation, error)

func (f reconcilerFunc) Reconcile(ctx context.Context, key StrategyKey) (Reconciliation, error) {
	return f(ctx, key)
}

type subscriberFunc func(context.Context, func(bool), func(StrategyKey)) error

func (f subscriberFunc) Watch(ctx context.Context, ready func(bool), changed func(StrategyKey)) error {
	return f(ctx, ready, changed)
}

func indexOptions(c *clock) IndexOptions {
	return IndexOptions{
		Source: setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return nil, nil }),
		Subscriber: subscriberFunc(func(ctx context.Context, ready func(bool), _ func(StrategyKey)) error {
			ready(true)
			<-ctx.Done()
			return ctx.Err()
		}),
		Now: c.now, MaxStrategies: 20, MaxMembers: 200, MaxBytes: 1 << 20, MaxLocalEntries: 20, ReadBatch: 4, ReconcileBatch: 2,
		RefreshInterval: time.Minute, IndexInterval: time.Hour, ReconcileInterval: 2 * time.Hour, CalibrationMaxAge: 3 * time.Hour, LocalRetention: 2 * time.Minute, CycleTimeout: time.Second,
	}
}

func mustIndex(t *testing.T, options IndexOptions) *Cache {
	t.Helper()
	cache, err := NewIndex(options)
	if err != nil {
		t.Fatal(err)
	}
	cache.indexReady(true)
	return cache
}

func TestIndexCalibrationSurvivesOrdinaryReadsAndExpiresSeparately(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	values := []string{"stale", "matched"}
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return values, nil })
	result := Reconciliation{Members: []string{"missing", "matched"}, Missing: []string{"missing"}, Suppressed: []string{"stale"}, Alerts: []Alert{{AlertID: "a", EventSourceID: "s", Fingerprint: "missing", Severity: "critical"}}}
	fail := false
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		if fail {
			return Reconciliation{}, ErrIncomplete
		}
		return result, nil
	})
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	baseline := cache.Snapshot(keyA)
	if !baseline.Calibrated || !cache.Contains(tenant, keyA.StrategyID, "missing") || cache.Contains(tenant, keyA.StrategyID, "stale") {
		t.Fatalf("baseline %+v", baseline)
	}
	if len(cache.ActiveAlerts(keyA)) != 1 {
		t.Fatal("calibrated metadata missing")
	}
	c.advance(time.Minute)
	cache.indexChanged(keyA)
	fail = true
	cache.Refresh(context.Background())
	if !cache.Contains(tenant, keyA.StrategyID, "missing") || cache.Contains(tenant, keyA.StrategyID, "stale") {
		t.Fatal("ordinary read or partial calibration erased correction")
	}
	if !cache.Snapshot(keyA).CalibratedAt.Equal(baseline.CalibratedAt) {
		t.Fatal("SET read renewed calibration age")
	}
	c.advance(4 * time.Hour)
	cache.Refresh(context.Background())
	if cache.Snapshot(keyA).Calibrated || len(cache.ActiveAlerts(keyA)) != 0 {
		t.Fatal("expired calibration usable for close")
	}
	if !cache.Contains(tenant, keyA.StrategyID, "stale") {
		t.Fatal("expired negative correction permanently suppressed the index")
	}
	if !cache.StaleBeyondBound() {
		t.Fatal("stale calibration invisible")
	}
}

func TestIndexPartialAndLocalAcknowledgements(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	readFail := false
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) {
		if readFail {
			return nil, ErrIncomplete
		}
		return []string{"fp"}, nil
	})
	cache := mustIndex(t, options)
	_ = cache.SetTracked([]StrategyKey{keyA})
	cache.Refresh(context.Background())
	if !cache.Contains(tenant, keyA.StrategyID, "fp") || cache.Stats().Calibrated != 0 || cache.Stats().Mode != ModeSelfMaintained {
		t.Fatal("uncalibrated index claimed authoritative")
	}
	readFail = true
	c.advance(time.Hour)
	cache.Refresh(context.Background())
	if !cache.Contains(tenant, keyA.StrategyID, "fp") {
		t.Fatal("partial erased prior members")
	}
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "new")})
	if !cache.Contains(tenant, keyA.StrategyID, "new") {
		t.Fatal("local abnormal not retained")
	}
	cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "new")})
	if cache.Contains(tenant, keyA.StrategyID, "new") {
		t.Fatal("local recovery not applied")
	}
	cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "fp")})
	readFail = false
	c.advance(time.Hour)
	cache.Refresh(context.Background())
	if !cache.Contains(tenant, keyA.StrategyID, "fp") {
		t.Fatal("index-only mode permanently masked an active member after recovery ACK")
	}
	cache.Untrack(keyA)
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "late")})
	if len(cache.Members(keyA)) != 0 || cache.Stats().Added != 0 {
		t.Fatal("released owner retained late ACK")
	}
}

func TestIndexInFlightNoticeAndOwnerRelease(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	var cache *Cache
	mode := "notice"
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) {
		if mode == "notice" {
			cache.indexChanged(keyA)
		} else {
			cache.Untrack(keyA)
			_ = cache.SetTracked([]StrategyKey{keyA})
		}
		return []string{"old"}, nil
	})
	cache = mustIndex(t, options)
	_ = cache.SetTracked([]StrategyKey{keyA})
	cache.Refresh(context.Background())
	if cache.Stats().PendingReads != 1 {
		t.Fatal("notice received during read was cleared")
	}
	mode = "release"
	c.advance(time.Minute)
	cache.Refresh(context.Background())
	if cache.Contains(tenant, keyA.StrategyID, "old") {
		t.Fatal("old owner's response populated reacquired entry")
	}
}

func TestIndexPeriodicReadAndRoundBudgets(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.ReadBatch = 1
	options.ReconcileBatch = 1
	reads, reconciles := 0, 0
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { reads++; return []string{fmt.Sprint(reads)}, nil })
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		reconciles++
		return Reconciliation{}, ErrIncomplete
	})
	cache := mustIndex(t, options)
	keys := []StrategyKey{keyA, keyB}
	_ = cache.SetTracked(keys)
	cache.Refresh(context.Background())
	if reads != 1 || reconciles != 1 {
		t.Fatal("round budget exceeded")
	}
	// Reapplying unchanged owner scope must not restart the rotation.
	_ = cache.SetTracked(keys)
	c.advance(time.Minute)
	cache.Refresh(context.Background())
	if cache.Snapshot(keyB).IndexReadAt.IsZero() {
		t.Fatal("rotation starved the second strategy")
	}
	c.advance(time.Hour)
	cache.Refresh(context.Background())
	if reads != 3 {
		t.Fatal("missed notice not repaired by periodic read")
	}
}

func TestIndexCapacityAndAtomicTracking(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.MaxStrategies = 1
	options.MaxMembers = 1
	values := []string{"a"}
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return values, nil })
	cache := mustIndex(t, options)
	_ = cache.SetTracked([]StrategyKey{keyA})
	cache.Refresh(context.Background())
	if !errors.Is(cache.SetTracked([]StrategyKey{keyA, keyB}), ErrCapacity) || cache.Stats().Tracked != 1 {
		t.Fatal("tracking overflow partially replaced ownership")
	}
	if !errors.Is(cache.TrackOwned(keyB), ErrCapacity) || cache.Stats().Tracked != 1 {
		t.Fatal("incremental ownership exceeded capacity")
	}
	values = []string{"b", "c"}
	c.advance(time.Hour)
	cache.Refresh(context.Background())
	if !reflect.DeepEqual(cache.Members(keyA), []string{"a"}) || cache.Snapshot(keyA).Reason != "capacity" {
		t.Fatal("oversized set replaced the old cache")
	}
	if err := cache.SetTracked(nil); err != nil {
		t.Fatal(err)
	}
	if stats := cache.Stats(); stats.Tracked != 0 || stats.Members != 0 || stats.MemberBytes != 0 {
		t.Fatalf("release leaked %+v", stats)
	}
}

func TestIndexCalibrationDoesNotEraseConcurrentACKAndCanCorrectOldRecovery(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	var cache *Cache
	concurrent := true
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		if concurrent {
			cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "new")})
		}
		return Reconciliation{Members: []string{"fp"}}, nil
	})
	cache = mustIndex(t, options)
	_ = cache.SetTracked([]StrategyKey{keyA})
	cache.Refresh(context.Background())
	if !cache.Contains(tenant, keyA.StrategyID, "new") {
		t.Fatal("concurrent abnormal erased")
	}
	cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "fp")})
	if cache.Contains(tenant, keyA.StrategyID, "fp") {
		t.Fatal("recent recovery did not subtract")
	}
	concurrent = false
	c.advance(time.Hour)
	cache.RequestReconcile(keyA)
	cache.Refresh(context.Background())
	if !cache.Contains(tenant, keyA.StrategyID, "fp") {
		t.Fatal("old ACK permanently masks still active alert")
	}
}

func TestIndexFailedCalibrationDoesNotPermanentlySuppressAnAcknowledgedRecovery(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return []string{"fp"}, nil })
	fail := false
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		if fail {
			return Reconciliation{}, ErrIncomplete
		}
		return Reconciliation{Members: []string{"fp"}}, nil
	})
	cache := mustIndex(t, options)
	_ = cache.TrackOwned(keyA)
	cache.Refresh(context.Background())
	c.advance(time.Minute)
	cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "fp")})
	if cache.Contains(tenant, keyA.StrategyID, "fp") {
		t.Fatal("recent recovery was not suppressed")
	}
	fail = true
	c.advance(4 * time.Hour)
	cache.Refresh(context.Background())
	if cache.Snapshot(keyA).Calibrated || !cache.Contains(tenant, keyA.StrategyID, "fp") {
		t.Fatal("failed calibration permanently suppressed an index member after both bounds expired")
	}
}

func TestIndexRunWaitsForSubscribeACKAndReconnectRereads(t *testing.T) {
	options := indexOptions(&clock{})
	options.Now = time.Now
	options.RefreshInterval = 10 * time.Millisecond
	options.IndexInterval = time.Hour
	ack := make(chan bool, 2)
	options.Subscriber = subscriberFunc(func(ctx context.Context, ready func(bool), _ func(StrategyKey)) error {
		for {
			select {
			case value := <-ack:
				ready(value)
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
	var mu sync.Mutex
	reads := 0
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { mu.Lock(); reads++; mu.Unlock(); return nil, nil })
	cache := mustIndex(t, options)
	_ = cache.SetTracked([]StrategyKey{keyA})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cache.Run(ctx) }()
	defer func() { cancel(); <-done }()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	before := reads
	mu.Unlock()
	if before != 0 {
		t.Fatal("read before subscription ACK")
	}
	ack <- true
	waitIndexCondition(t, func() bool { mu.Lock(); defer mu.Unlock(); return reads == 1 })
	ack <- false
	ack <- true
	waitIndexCondition(t, func() bool { mu.Lock(); defer mu.Unlock(); return reads >= 2 })
}

func waitIndexCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
