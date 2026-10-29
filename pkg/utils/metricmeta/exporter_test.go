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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func testEntry() Entry {
	return Entry{
		NewKey(1, Scope{ScopeAPM, "service-scope"}, "metric"),
		Value{Name: "original", Type: "counter", OriginalType: "sum", Temporality: "delta"},
	}
}

func testExporter(t *testing.T, export ExportFunc) *Exporter {
	t.Helper()
	c := DefaultConfig()
	c.DebounceInterval = 0
	c.ExportMinInterval = time.Microsecond
	e, err := NewExporter(c, export)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func observe(t *testing.T, e *Exporter, entry Entry) {
	t.Helper()
	if !e.Store().Observe(entry.Key, entry.Value) {
		t.Fatalf("observe rejected: %+v", e.Store().Stats())
	}
}

func flush(t *testing.T, e *Exporter) {
	t.Helper()
	if err := e.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWireAndFingerprint(t *testing.T) {
	e := testEntry()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"__unit", "__description", "__interval", "__usage"} {
		if v, ok := wire[field]; !ok || v != nil {
			t.Fatalf("%s must be explicit null: %s", field, b)
		}
	}
	base := digest(e)
	v := reflect.ValueOf(&e.Value).Elem()
	for i := 0; i < v.NumField(); i++ {
		old := reflect.New(v.Field(i).Type()).Elem()
		old.Set(v.Field(i))
		switch v.Field(i).Kind() {
		case reflect.String:
			v.Field(i).SetString("changed")
		case reflect.Int64:
			v.Field(i).SetInt(1)
		case reflect.Uint8:
			v.Field(i).SetUint(uint64(PresentUnit))
		}
		if digest(e) == base {
			t.Fatalf("field %s absent from fingerprint", v.Type().Field(i).Name)
		}
		v.Field(i).Set(old)
	}
	a, bEntry := testEntry(), testEntry()
	a.Key.Scope, a.Key.Metric = "ab", "c"
	bEntry.Key.Scope, bEntry.Key.Metric = "a", "bc"
	if digest(a) == digest(bEntry) {
		t.Fatal("ambiguous string encoding")
	}
}

func TestHourlyRotationAndFallback(t *testing.T) {
	var sent int
	e := testExporter(t, func(context.Context, []byte) error { sent++; return nil })
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.store.now = func() time.Time { return now }
	entry := testEntry()
	observe(t, e, entry)
	flush(t, e)
	for _, elapsed := range []time.Duration{59 * time.Minute, time.Hour, 119 * time.Minute} {
		now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(elapsed)
		observe(t, e, entry)
		flush(t, e)
		if sent != 1 {
			t.Fatalf("premature expiry at %v", elapsed)
		}
	}
	now = now.Add(time.Minute)
	observe(t, e, entry)
	flush(t, e)
	if sent != 2 || e.store.Stats().Rotations != 2 {
		t.Fatalf("two-hour expiry: sent=%d stats=%+v", sent, e.store.Stats())
	}
	// 已发送 A、B 后回退 A，等时间轮换或 Clean 才重发。
	entry.Value.Description = "B"
	observe(t, e, entry)
	flush(t, e)
	entry.Value.Description = ""
	observe(t, e, entry)
	flush(t, e)
	if sent != 3 {
		t.Fatal("historical version should be suppressed")
	}
	if err := e.store.Clean(); err != nil {
		t.Fatal(err)
	}
	observe(t, e, entry)
	flush(t, e)
	if sent != 4 {
		t.Fatal("Clean did not restore current value")
	}
}

func TestDebounceLatestAndMaxWait(t *testing.T) {
	var descriptions []string
	e := testExporter(t, func(_ context.Context, body []byte) error {
		var rows []map[string]any
		if err := json.Unmarshal(body, &rows); err != nil {
			return err
		}
		for _, row := range rows {
			descriptions = append(descriptions, row["__description"].(string))
		}
		return nil
	})
	e.store.config.DebounceInterval = time.Minute
	e.store.config.DebounceMaxWait = 3 * time.Minute
	now := time.Now()
	e.store.now = func() time.Time { return now }
	entry := testEntry()
	entry.Value.Present = PresentDescription
	for i := 0; i < 7; i++ {
		entry.Value.Description = fmt.Sprint(i)
		observe(t, e, entry)
		flush(t, e)
		now = now.Add(30 * time.Second)
	}
	if !reflect.DeepEqual(descriptions, []string{"6"}) {
		t.Fatalf("max wait: %v", descriptions)
	}
	entry.Value.Description = "stable"
	observe(t, e, entry)
	flush(t, e)
	now = now.Add(time.Minute)
	observe(t, e, entry) // 相同值不推迟稳定时间。
	flush(t, e)
	if !reflect.DeepEqual(descriptions, []string{"6", "stable"}) {
		t.Fatalf("quiet window: %v", descriptions)
	}
}

func TestBlacklistCircuitCapacityAndRecovery(t *testing.T) {
	c := DefaultConfig()
	c.BlockedDataIDs = []int32{9}
	c.DataIDLimits = []DataIDLimit{{2, 1}, {3, 0}}
	c.MaxUpdates = 3
	c.BloomCapacity = 2
	c.DebounceInterval = 0
	e, err := NewExporter(c, func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	e.store.now = func() time.Time { return now }
	entry := testEntry()
	entry.Key.DataID = 9
	if e.store.Observe(entry.Key, entry.Value) {
		t.Fatal("blacklist")
	}
	entry.Key.DataID = 3
	if e.store.Observe(entry.Key, entry.Value) {
		t.Fatal("zero dataid limit")
	}
	entry.Key.DataID = 2
	observe(t, e, entry)
	entry.Value.Description = "change"
	if e.store.Observe(entry.Key, entry.Value) {
		t.Fatal("dataid circuit")
	}
	entry.Key.DataID = 1
	observe(t, e, entry)
	entry.Value.Description = "change2"
	observe(t, e, entry)
	entry.Value.Description = "change3"
	if e.store.Observe(entry.Key, entry.Value) {
		t.Fatal("global circuit")
	}
	now = now.Add(c.CircuitWindow)
	observe(t, e, entry)
	flush(t, e)
	entry.Key.Metric = "second"
	observe(t, e, entry)
	flush(t, e)
	entry.Key.Metric = "third"
	if e.store.Observe(entry.Key, entry.Value) {
		t.Fatal("saturated Bloom must reject, not reset")
	}
	if e.store.Stats().Rotations != 0 {
		t.Fatal("capacity must not trigger rotation")
	}
	now = now.Add(time.Hour)
	observe(t, e, entry)
}

func TestRetryPreservesNewerAndClean(t *testing.T) {
	entry := testEntry()
	newer := entry
	newer.Value.Description = "new"
	var e *Exporter
	calls := 0
	e = testExporter(t, func(context.Context, []byte) error {
		calls++
		if calls == 1 {
			observe(t, e, newer)
			if err := e.store.Clean(); err != nil {
				t.Fatal(err)
			}
			return errors.New("retry")
		}
		return nil
	})
	observe(t, e, entry)
	if e.Flush(context.Background()) == nil {
		t.Fatal("missing failure")
	}
	if e.store.pending[entry.Key].Value != newer.Value {
		t.Fatal("retry overwrote newer metadata")
	}
	flush(t, e)
	if e.store.Stats().Exported != 1 || e.store.Stats().Pending != 0 {
		t.Fatalf("%+v", e.store.Stats())
	}
}

func TestSuccessfulInFlightCleanCannotRepopulateBloom(t *testing.T) {
	var e *Exporter
	e = testExporter(t, func(context.Context, []byte) error { return e.store.Clean() })
	observe(t, e, testEntry())
	flush(t, e)
	if e.store.Stats().BloomVersions != 0 {
		t.Fatal("pre-Clean acknowledgment refilled Bloom")
	}
}

func TestBatchLimitsTimeoutAndRetry(t *testing.T) {
	var starts []time.Time
	total := 0
	e := testExporter(t, func(_ context.Context, body []byte) error {
		if len(body) > 650 {
			t.Fatalf("oversized batch: %d", len(body))
		}
		var rows []map[string]any
		if err := json.Unmarshal(body, &rows); err != nil {
			return err
		}
		if len(rows) > 2 {
			t.Fatal("item limit")
		}
		starts = append(starts, time.Now())
		total += len(rows)
		return nil
	})
	e.config.ExportBatchSize = 2
	e.config.ExportBatchBytes = 650
	e.config.ExportMinInterval = 10 * time.Millisecond
	e.config.ExportTimeout = 25 * time.Millisecond
	entry := testEntry()
	for i := 0; i < 10; i++ {
		entry.Key.Metric = fmt.Sprint(i)
		observe(t, e, entry)
	}
	if !errors.Is(e.Flush(context.Background()), context.DeadlineExceeded) {
		t.Fatal("missing whole-flush timeout")
	}
	e.config.ExportTimeout = time.Second
	flush(t, e)
	if total != 10 || e.store.Stats().Pending != 0 {
		t.Fatalf("total=%d stats=%+v", total, e.store.Stats())
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 10*time.Millisecond {
			t.Fatal("min interval violated")
		}
	}
}

func TestConcurrentLifecycle(t *testing.T) {
	e := testExporter(t, func(context.Context, []byte) error { return nil })
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.Start()
			entry := testEntry()
			for j := 0; j < 20; j++ {
				e.store.Observe(entry.Key, entry.Value)
				_ = e.store.Clean()
				_ = e.Flush(context.Background())
			}
			_ = e.Stop()
		}()
	}
	wg.Wait()
	if !errors.Is(e.Start(), ErrClosed) || !errors.Is(e.store.Clean(), ErrClosed) || e.store.Observe(testEntry().Key, testEntry().Value) {
		t.Fatal("closed exporter accepted work")
	}
	// 未 Start 的实例也发送最后一轮，并跳过防抖。
	other := testExporter(t, func(context.Context, []byte) error { return nil })
	other.store.config.DebounceInterval = time.Hour
	observe(t, other, testEntry())
	if err := other.Stop(); err != nil || other.store.Stats().Exported != 1 {
		t.Fatal("stop-before-start lost final flush", err)
	}
}
