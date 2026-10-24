// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricmeta

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestEntryLimitsAndRestoreCapacity(t *testing.T) {
	e := testExporter(t, func(context.Context, []byte) error { return nil })
	e.store.config.MaxPendingEntries = 1
	entry := testEntry()
	observe(t, e, entry)
	old := e.store.take(false)
	newer := entry
	newer.Key.Metric = "new"
	observe(t, e, newer)
	e.store.restore(old.items)
	if e.store.Stats().Pending != 1 || e.store.Stats().Dropped["restore_capacity"] != 1 {
		t.Fatal(e.store.Stats())
	}
	if e.store.Observe(entry.Key, entry.Value) {
		t.Fatal("pending capacity")
	}
	newer.Value.Description = strings.Repeat("x", e.config.MaxEntryBytes)
	if e.store.Observe(newer.Key, newer.Value) {
		t.Fatal("entry bytes")
	}
	newer = entry
	newer.Key.DataID = 0
	if e.store.Observe(newer.Key, newer.Value) {
		t.Fatal("invalid DataID")
	}
	newer = entry
	newer.Value.Present, newer.Value.Interval = PresentInterval, -1
	if e.store.Observe(newer.Key, newer.Value) {
		t.Fatal("invalid interval")
	}
	e.config.ExportBatchBytes = 2
	flush(t, e)
	if e.store.Stats().Dropped["batch_bytes"] != 1 || e.store.Stats().Exported != 0 {
		t.Fatal("oversized JSON must be counted and discarded")
	}
}

func TestNewerValueInFlightAndTargetIsolation(t *testing.T) {
	entry := testEntry()
	newer := entry
	newer.Value.Unit = "ms"
	var first *Exporter
	calls := 0
	first = testExporter(t, func(context.Context, []byte) error {
		calls++
		if calls == 1 {
			observe(t, first, newer)
		}
		return nil
	})
	observe(t, first, entry)
	flush(t, first)
	if !first.store.pending[entry.Key].Force {
		t.Fatal("newer value lost forced send")
	}
	flush(t, first)
	if first.store.Stats().Exported != 2 {
		t.Fatal(first.store.Stats())
	}
	secondCalls := 0
	second := testExporter(t, func(context.Context, []byte) error { secondCalls++; return nil })
	observe(t, second, entry)
	flush(t, second)
	if secondCalls != 1 || calls != 2 {
		t.Fatal("target isolation")
	}
}

func TestStopCancelsBackgroundAndUsesIndependentFinalDeadline(t *testing.T) {
	c := DefaultConfig()
	c.FlushInterval = 20 * time.Millisecond
	c.ExportTimeout = 10 * time.Millisecond
	c.ExportMinInterval = time.Microsecond
	c.DebounceInterval = 0
	entered := make(chan struct{}, 1)
	calls := 0
	e, err := NewExporter(c, func(ctx context.Context, _ []byte) error {
		calls++
		if calls == 1 {
			entered <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	observe(t, e, testEntry())
	if err := e.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	if err := e.Stop(); err != nil {
		t.Fatal("final deadline inherited canceled worker", err)
	}
	if calls != 2 || e.store.Stats().Exported != 1 {
		t.Fatal("incomplete final flush", calls, e.store.Stats())
	}
}

func TestConfigurationValidation(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.BloomErrorRate = math.NaN() }, func(c *Config) { c.BloomErrorRate = math.Inf(1) },
		func(c *Config) { c.BloomCapacity = math.MaxInt }, func(c *Config) { c.FullReportInterval = c.FlushInterval },
		func(c *Config) { c.ExportTimeout = c.FlushInterval }, func(c *Config) { c.ExportBatchSize = c.MaxPendingEntries + 1 },
		func(c *Config) { c.DebounceMaxWait = 0 }, func(c *Config) { c.MaxUpdatesPerDataID = 0 },
		func(c *Config) { c.DataIDLimits = []DataIDLimit{{1, -1}} }, func(c *Config) { c.DataIDLimits = []DataIDLimit{{1, 1}, {1, 2}} },
	} {
		c := DefaultConfig()
		change(&c)
		if _, err := NewExporter(c, func(context.Context, []byte) error { return nil }); err == nil {
			t.Fatalf("invalid config: %+v", c)
		}
	}
	if _, err := NewExporter(DefaultConfig(), nil); !errors.Is(err, ErrUnconfigured) {
		t.Fatal(err)
	}
}

func TestKeyIsolationAndProtocolValidation(t *testing.T) {
	e := testExporter(t, func(context.Context, []byte) error { return nil })
	entry := testEntry()
	keys := []Key{
		NewKey(1, Scope{ScopeDefault, ""}, "metric"), NewKey(2, Scope{ScopeDefault, ""}, "metric"),
		NewKey(1, Scope{ScopePlugin, ""}, "metric"), NewKey(1, Scope{ScopeType("CUSTOM"), "x"}, "metric"),
	}
	for _, key := range keys {
		observe(t, e, Entry{key, entry.Value})
	}
	if e.store.Stats().Pending != len(keys) {
		t.Fatal("key identities merged")
	}
	for _, change := range []func(*Entry){
		func(e *Entry) { e.Key.ScopeType = "" }, func(e *Entry) { e.Value.Type = "invalid" },
		func(e *Entry) { e.Value.OriginalType = "invalid" }, func(e *Entry) { e.Value.Description = "\xff" },
		func(e *Entry) { e.Value.Present = PresentUsage; e.Value.Usage = "invalid" },
	} {
		invalid := entry
		change(&invalid)
		if e.store.Observe(invalid.Key, invalid.Value) {
			t.Fatalf("invalid entry: %+v", invalid)
		}
	}
}

func BenchmarkObserveDuplicate(b *testing.B) {
	e, _ := NewExporter(DefaultConfig(), func(context.Context, []byte) error { return nil })
	entry := testEntry()
	e.store.Observe(entry.Key, entry.Value)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.store.Observe(entry.Key, entry.Value)
	}
}

func BenchmarkBloomContains(b *testing.B) {
	for _, capacity := range []int{10000, 100000, 1000000} {
		b.Run(fmt.Sprint(capacity), func(b *testing.B) {
			filter := newBloom(capacity, 5e-7)
			digests := make([]fingerprint, capacity)
			entry := testEntry()
			for i := range digests {
				entry.Key.Metric = fmt.Sprint(i)
				digests[i] = digest(entry)
				filter.add(digests[i])
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				filter.contains(digests[i%capacity])
			}
			b.ReportMetric(float64(len(filter.bits)*8*2), "bitmap-bytes")
		})
	}
}

func BenchmarkObserveSent(b *testing.B) {
	c := DefaultConfig()
	c.DebounceInterval = 0
	e, _ := NewExporter(c, func(context.Context, []byte) error { return nil })
	entry := testEntry()
	e.store.Observe(entry.Key, entry.Value)
	_ = e.Flush(context.Background())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.store.Observe(entry.Key, entry.Value)
	}
}

func TestFalsePositiveExpiresWithoutRefresh(t *testing.T) {
	sent := 0
	e := testExporter(t, func(context.Context, []byte) error { sent++; return nil })
	now := time.Now()
	e.store.now = func() time.Time { return now }
	e.store.rotate(now)
	for i := range e.store.filters {
		for j := range e.store.filters[i].bits {
			e.store.filters[i].bits[j] = ^uint64(0)
		}
	}
	observe(t, e, testEntry())
	flush(t, e)
	if sent != 0 {
		t.Fatal("false positive was not simulated")
	}
	now = now.Add(2 * time.Hour)
	observe(t, e, testEntry())
	flush(t, e)
	if sent != 1 {
		t.Fatal("suppressed observations refreshed the old filter")
	}
}

func TestBloomSaturationDefersAcceptedBacklogUntilRotation(t *testing.T) {
	e := testExporter(t, func(context.Context, []byte) error { return nil })
	e.store.config.BloomCapacity = 1
	now := time.Now()
	e.store.now = func() time.Time { return now }
	entry := testEntry()
	for _, name := range []string{"first", "second", "third"} {
		entry.Key.Metric = name
		observe(t, e, entry)
	}
	flush(t, e)
	if s := e.store.Stats(); s.Exported != 1 || s.Pending != 2 || s.BloomVersions != 1 {
		t.Fatal(s)
	}
	now = now.Add(time.Hour)
	flush(t, e)
	if s := e.store.Stats(); s.Exported != 2 || s.Pending != 1 || s.BloomVersions != 2 {
		t.Fatal(s)
	}
}

func BenchmarkClean(b *testing.B) {
	e, _ := NewExporter(DefaultConfig(), func(context.Context, []byte) error { return nil })
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.store.Clean()
	}
}
