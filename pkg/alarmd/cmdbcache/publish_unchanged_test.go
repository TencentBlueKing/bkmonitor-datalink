// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func (client *markerClient) MGet(_ context.Context, keys ...string) *redis.SliceCmd {
	values := make([]interface{}, len(keys))
	for index, key := range keys {
		client.scans = append(client.scans, "GET "+key)
		if err, failing := client.failing[key]; failing {
			return redis.NewSliceResult(nil, err)
		}
		if value, found := client.strings[key]; found {
			values[index] = value
		}
	}
	return redis.NewSliceResult(values, nil)
}

const (
	unchangedPrefix    = "bk_monitorv3.ce"
	unchangedHostKey   = unchangedPrefix + ".cache.cmdb.host"
	unchangedPublished = unchangedPrefix + ".cache.cmdb_published_at.host_topo"
	unchangedInstances = unchangedPrefix + ".cache.cmdb_published_at.service_instance"
	unchangedRefreshed = unchangedPrefix + ".cache.cmdb_last_refresh_all_time.host_topo"
)

// unchangedHarness is a store over a reader whose client answers the marker
// keys and the host hash, on a clock the test moves a minute at a time.
type unchangedHarness struct {
	t        *testing.T
	client   *markerClient
	store    *Store
	now      time.Time
	observed []string
}

func newUnchangedHarness(t *testing.T, markers map[string]string) *unchangedHarness {
	t.Helper()
	harness := &unchangedHarness{t: t, now: time.Unix(1700000000, 0).UTC(),
		client: &markerClient{hashClient: &hashClient{hashes: map[string][]string{unchangedHostKey: {"501", hostUnderSet}}},
			strings: markers}}
	reader, err := NewReader(harness.client, unchangedPrefix)
	if err != nil {
		t.Fatal(err)
	}
	harness.store, err = NewStore(reader, StoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute,
		PublishedMaxAge: 15 * time.Minute, Now: func() time.Time { return harness.now },
		ObserveRead: func(result string) { harness.observed = append(harness.observed, result) }})
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

// refresh moves the clock by step and refreshes once, and reports whether
// the host hash was read.
func (harness *unchangedHarness) refresh(step time.Duration) (scanned bool, err error) {
	harness.now = harness.now.Add(step)
	harness.client.scans = nil
	err = harness.store.Refresh(context.Background())
	for _, read := range harness.client.scans {
		if read == unchangedHostKey {
			scanned = true
		}
	}
	return scanned, err
}

func (harness *unchangedHarness) wantScans(want ...bool) {
	harness.t.Helper()
	for round, wanted := range want {
		scanned, err := harness.refresh(time.Minute)
		if err != nil {
			harness.t.Fatalf("round %d: %v", round, err)
		}
		if scanned != wanted {
			harness.t.Fatalf("round %d read the host hash: %v, want %v", round, scanned, wanted)
		}
	}
}

func epoch(at time.Time) string { return strconv.FormatInt(at.Unix(), 10) }

// A writer that publishes whole snapshots with a publish time, and no second
// writer: a refresh that finds the time where the held index has it keeps
// the index without reading the hosts, for as long as that lasts -- past
// alarmd's own read staleness bound too, since the facts age on the publish
// time -- and a new time is read in full.
func TestAnUnchangedPublishTimeKeepsTheHeldIndexWithoutReadingTheHosts(t *testing.T) {
	published := time.Unix(1699999990, 0).UTC()
	harness := newUnchangedHarness(t, map[string]string{unchangedPublished: epoch(published), unchangedInstances: epoch(published)})
	harness.wantScans(true, false, false)
	if health := harness.store.Health(); health.Unchanged != 2 || health.Refreshes != 1 || health.Degraded {
		t.Fatalf("health = %+v, want one full read and two unchanged", health)
	}
	harness.client.strings[unchangedPublished] = epoch(harness.now)
	harness.wantScans(true, false)
	if got := harness.store.Health().PublishedAge; got != 2*time.Minute {
		t.Fatalf("published age = %s, want two minutes: the kept index keeps the new publish time", got)
	}
	// Twelve minutes more with no publish: kept throughout, and judged on
	// the publish time, not degraded until that is past its own bound.
	unchanged := make([]bool, 12)
	harness.wantScans(unchanged...)
	if health := harness.store.Health(); health.Degraded || health.PublishedAge != 14*time.Minute {
		t.Fatalf("health = %+v, want kept, published 14 minutes ago and not yet stale", health)
	}
	harness.wantScans(false, false)
	if health := harness.store.Health(); !health.Degraded || health.DegradedReason != IndexStale {
		t.Fatalf("health = %+v, want stale once the publish time is past its bound", health)
	}
	want := []string{ReadFull, ReadUnchanged, ReadUnchanged, ReadFull}
	for range 15 {
		want = append(want, ReadUnchanged)
	}
	if !reflect.DeepEqual(harness.observed, want) {
		t.Fatalf("observed %v, want %v", harness.observed, want)
	}
}

// Every refresh reads in full when the writer publishes no time, when it
// publishes none for the service instance hash (the group it publishes
// apart, on its own schedule), when a second writer's refresh time is there
// (it may change the hashes in place), and when that second writer appears
// after the held index was read. A service instance publish alone, with the
// host and topology time unmoved, is read in full.
func TestEveryRefreshReadsInFullUnlessTheOnlyWriterSaysItHasNotPublished(t *testing.T) {
	published := epoch(time.Unix(1699999990, 0))
	t.Run("no publish time", func(t *testing.T) {
		newUnchangedHarness(t, map[string]string{}).wantScans(true, true, true)
	})
	t.Run("no service instance publish time", func(t *testing.T) {
		newUnchangedHarness(t, map[string]string{unchangedPublished: published}).wantScans(true, true, true)
	})
	t.Run("a second writer", func(t *testing.T) {
		newUnchangedHarness(t, map[string]string{unchangedPublished: published, unchangedInstances: published,
			unchangedRefreshed: published}).wantScans(true, true, true)
	})
	t.Run("a second writer that appears later", func(t *testing.T) {
		harness := newUnchangedHarness(t, map[string]string{unchangedPublished: published, unchangedInstances: published})
		harness.wantScans(true, false)
		harness.client.strings[unchangedRefreshed] = published
		harness.wantScans(true, true)
	})
	t.Run("the service instance hash published alone", func(t *testing.T) {
		harness := newUnchangedHarness(t, map[string]string{unchangedPublished: published, unchangedInstances: published})
		harness.wantScans(true, false)
		harness.client.strings[unchangedInstances] = epoch(harness.now)
		harness.wantScans(true, false)
	})
}

// The publish marks unreadable: the refresh reads in full, which fails on
// the same store and keeps the held index, counted as failed.
func TestUnreadablePublishMarksFallBackToTheFullRead(t *testing.T) {
	published := epoch(time.Unix(1699999990, 0))
	harness := newUnchangedHarness(t, map[string]string{unchangedPublished: published, unchangedInstances: published})
	harness.wantScans(true)
	harness.client.failing = map[string]error{unchangedPublished: errors.New("i/o timeout")}
	if _, err := harness.refresh(time.Minute); err == nil {
		t.Fatal("a refresh whose publish marks could not be read succeeded")
	}
	if last := harness.observed[len(harness.observed)-1]; last != ReadFailed {
		t.Fatalf("observed %v, want the last read failed", harness.observed)
	}
	if index, reason := harness.store.Usable(); index == nil || reason != "" {
		t.Fatalf("after a failed read the store holds %v (%q), want the held index", index, reason)
	}
}
