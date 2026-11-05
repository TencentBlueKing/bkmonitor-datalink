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
	"strings"
	"testing"
	"time"
)

func TestEntryLimitsAndRestoreCapacity(t *testing.T) {
	config := DefaultConfig()
	config.Exporter.QueueSize = 1
	e, err := NewExporter(config, func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	entry := testEntry()
	observe(t, e, entry)
	old := e.store.take()
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
	newer = entry
	newer.Key.Metric = "new"
	newer.Value.Description = strings.Repeat("x", 8192)
	newer.Value.Present |= PresentDescription
	observe(t, e, newer)
	flush(t, e)
	if e.store.Stats().Exported != 1 {
		t.Fatal(e.store.Stats())
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
	entered := make(chan struct{}, 1)
	calls := 0
	config := DefaultConfig()
	config.Exporter.Interval = 20 * time.Millisecond
	config.Exporter.Timeout = 10 * time.Millisecond
	e, err := NewExporter(config, func(ctx context.Context, _ []byte) error {
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
		func(c *Config) { c.Dedup.MaxAge = 0 },
		func(c *Config) { c.Limits.DefaultSource = 0 },
		func(c *Config) { c.Exporter.Interval = 0 },
		func(c *Config) { c.Exporter.Timeout = -1 },
		func(c *Config) { c.Exporter.BatchSize = 0 },
		func(c *Config) { c.Exporter.QueueSize = -1 },
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

func TestBloomContinuesPastDesignLoadAndRotatesHourly(t *testing.T) {
	e, err := NewExporter(DefaultConfig(), func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer e.Discard()
	e.store.filters = newBloomPair(100)
	now := time.Unix(1, 0)
	e.store.now = func() time.Time { return now }
	entry := testEntry()
	for i := 0; i < 101; i++ {
		entry.Key.Metric = fmt.Sprint(i)
		observe(t, e, entry)
	}
	flush(t, e)
	entry.Key.Metric = "after-design-load"
	observe(t, e, entry)
	flush(t, e)
	if s := e.store.Stats(); s.Exported != 102 || s.BloomVersions != 102 || s.Pending != 0 || s.Rotations != 0 {
		t.Fatal(s)
	}
	now = now.Add(time.Hour)
	observe(t, e, entry)
	if s := e.store.Stats(); s.Suppressed != 1 || s.Rotations != 1 {
		t.Fatal(s)
	}
	now = now.Add(time.Hour)
	observe(t, e, entry)
	flush(t, e)
	if s := e.store.Stats(); s.Exported != 103 || s.BloomVersions != 1 || s.Rotations != 2 {
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

func TestSourceBusinessIsPartOfSuccessfulVersion(t *testing.T) {
	e := testExporter(t, func(context.Context, []byte) error { return nil })
	defer e.Discard()
	entry := testEntry()
	for _, biz := range []int32{73, -73, 73} {
		entry.Key.BizID = biz
		observe(t, e, entry)
		flush(t, e)
	}
	if got := e.Store().Stats(); got.Exported != 2 || got.BloomVersions != 2 || got.Suppressed != 1 {
		t.Fatal(got)
	}
	config := DefaultConfig()
	config.Limits = Limits{Instance: 3, DefaultSource: 1}
	if err := e.Reload(config); err != nil {
		t.Fatal(err)
	}
	observe(t, e, entry)
	if s := e.Store().Stats(); s.BloomVersions != 2 || s.Suppressed != 2 {
		t.Fatal("quota reload reset dedup", s)
	}
}
