// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The pool record on a real Redis: what one owner writes the next reads
// back; an owner that has been replaced cannot write over its successor;
// nothing there, and a record that does not decode, are both no record; and
// the record expires on its own.
func TestTheQueryCooldownRecordIsFencedByOwnerOnRedis(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	store := newRedisQueryCooldownStore(client, "test.cooldown", nil, nil, nil)
	if _, found, err := store.LoadQueryCooldown(ctx, "qg"); found || err != nil {
		t.Fatalf("empty store = (found %t, %v), want no record and no error", found, err)
	}
	fence := func(epoch uint64) execution.OwnerFence {
		return execution.OwnerFence{QueryGroup: "qg", OwnerID: "worker", OwnerEpoch: epoch, LeaseToken: "token"}
	}
	entered := time.Unix(1_000, 0).UTC()
	successor := scheduler.QueryCooldownRecord{QueryGroup: "qg", OwnerEpoch: 2, EnteredAt: entered, Until: entered.Add(time.Minute), Failures: 4}
	if err := store.SaveQueryCooldown(ctx, fence(2), successor); err != nil {
		t.Fatal(err)
	}
	stale := successor
	stale.OwnerEpoch, stale.Until = 1, time.Time{}
	if err := store.SaveQueryCooldown(ctx, fence(1), stale); !errors.Is(err, errQueryCooldownSuperseded) {
		t.Fatalf("a replaced owner's save = %v, want refused", err)
	}
	got, found, err := store.LoadQueryCooldown(ctx, "qg")
	if err != nil || !found || got.OwnerEpoch != 2 || !got.Until.Equal(successor.Until) || !got.EnteredAt.Equal(entered) {
		t.Fatalf("record = (%+v, %t, %v), want the successor's", got, found, err)
	}
	// The same owner writes its own record again: the exit after the entry
	// is what is read back, not the entry.
	exited := successor
	exited.Until, exited.ExitedAt, exited.ExitReason = time.Time{}, entered.Add(2*time.Minute), "recovered"
	if err := store.SaveQueryCooldown(ctx, fence(2), exited); err != nil {
		t.Fatalf("the same owner's second save = %v, want written", err)
	}
	if got, _, _ := store.LoadQueryCooldown(ctx, "qg"); !got.Until.IsZero() || got.ExitReason != "recovered" {
		t.Fatalf("record after the same owner's exit = %+v, want the exit", got)
	}
	// A later owner writes.
	later := successor
	later.OwnerEpoch, later.Failures = 3, 5
	if err := store.SaveQueryCooldown(ctx, fence(3), later); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(ctx, "test.cooldown:qg").Val(); ttl <= 0 || ttl > queryCooldownRecordTTL {
		t.Fatalf("record TTL = %s, want it to expire within %s", ttl, queryCooldownRecordTTL)
	}
	if err := client.Set(ctx, "test.cooldown:qg", "not json", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.LoadQueryCooldown(ctx, "qg"); found || !errors.Is(err, scheduler.ErrQueryCooldownUndecodable) {
		t.Fatalf("undecodable record = (found %t, %v), want no record, named as undecodable", found, err)
	}
	// A record that does not decode does not block the next owner's write.
	if err := store.SaveQueryCooldown(ctx, fence(1), stale); err != nil {
		t.Fatalf("a save over an undecodable record = %v, want it written", err)
	}
	if newRedisQueryCooldownStore(nil, "p", nil, nil, nil) != nil {
		t.Fatal("a store without a client is not nil")
	}
}

// Every write is counted by what became of it -- written, superseded by a
// later owner, failed in the store -- and a failed one is also a line naming
// its Query Group, so a pool state lost before the next restart is seen when
// it is lost, not guessed at afterwards.
func TestEveryPoolRecordWriteIsCountedByItsResult(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	results := map[string]int{}
	var lines []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, o observability.Observation) { lines = append(lines, o) })
	store := newRedisQueryCooldownStore(client, "test.cooldown", func(result string) { results[result]++ }, nil, observer)
	fence := func(epoch uint64) execution.OwnerFence {
		return execution.OwnerFence{QueryGroup: "qg", OwnerID: "worker", OwnerEpoch: epoch, LeaseToken: "token"}
	}
	record := scheduler.QueryCooldownRecord{QueryGroup: "qg", OwnerEpoch: 2, Until: time.Unix(2_000, 0)}
	if err := store.SaveQueryCooldown(ctx, fence(2), record); err != nil {
		t.Fatal(err)
	}
	record.OwnerEpoch = 1
	if err := store.SaveQueryCooldown(ctx, fence(1), record); !errors.Is(err, errQueryCooldownSuperseded) {
		t.Fatalf("stale save = %v", err)
	}
	down := redis.NewClient(&redis.Options{Addr: "192.0.2.1:6379", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = down.Close() })
	failing := newRedisQueryCooldownStore(down, "test.cooldown", func(result string) { results[result]++ }, nil, observer)
	if err := failing.SaveQueryCooldown(ctx, fence(3), record); err == nil || errors.Is(err, errQueryCooldownSuperseded) {
		t.Fatalf("save to an unreachable store = %v, want the store's error", err)
	}
	if results["written"] != 1 || results["superseded"] != 1 || results["failed"] != 1 {
		t.Fatalf("results = %v, want one of each", results)
	}
	if len(lines) != 1 || lines[0].Result != observability.ResultFailed || lines[0].Trace.QueryGroupKey != "qg" || lines[0].Err == nil {
		t.Fatalf("lines = %+v, want one failed line naming the Query Group", lines)
	}
}

// The production store counts on the process's Recorder: a write made
// through it is read back from query_cooldown_saves_total, so a wiring that
// counted nowhere would read as zero failures and fail here instead.
func TestTheProductionPoolStoreCountsOnTheRecorder(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	cfg := config.Default()
	cfg.Redis.Address = address
	recorder := metric.NewRecorder(metric.BuildInfo{})
	store := newProductionQueryCooldownStore(cfg, client, recorder, observability.NopObserver{})
	fence := execution.OwnerFence{QueryGroup: "qg", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "token"}
	if err := store.SaveQueryCooldown(context.Background(), fence, scheduler.QueryCooldownRecord{QueryGroup: "qg", OwnerEpoch: 1}); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(context.Background(), queryCooldownPrefix(cfg)+":qg").Err(); err != nil {
		t.Fatalf("record not under the prefix store.inspect reads: %v", err)
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	written := -1.0
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_query_cooldown_saves_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			if m.GetLabel()[0].GetValue() == "written" {
				written = m.GetCounter().GetValue()
			}
		}
	}
	if written != 1 {
		t.Fatalf("written = %v on the process Recorder, want 1", written)
	}
	if _, found, err := store.LoadQueryCooldown(context.Background(), "qg"); !found || err != nil {
		t.Fatalf("load = (found %t, %v), want the record", found, err)
	}
	loaded := map[string]float64{}
	families, err = recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "bkmonitor_alarmd_query_cooldown_loads_total" {
			for _, m := range family.GetMetric() {
				loaded[m.GetLabel()[0].GetValue()] = m.GetCounter().GetValue()
			}
		}
	}
	if loaded["found"] != 1 || len(loaded) != len(metric.QueryCooldownLoadResults) {
		t.Fatalf("loads on the process Recorder = %v, want found 1 and every result present", loaded)
	}
}

// Every read is counted by what it found -- a record, none, one that does
// not decode, or a store that did not answer -- and a failed one is also a
// line naming its Query Group. The Runner reads again on the next round
// after a failed read and writes nothing until one succeeds, so a store that
// keeps failing counts failed on every round, which is the reading wanted.
// A record that does not decode is told apart from a failed read: reading
// it again gives the same bytes, so the Runner takes it as no record.
func TestEveryPoolRecordReadIsCountedByWhatItFound(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	results := map[string]int{}
	var lines []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, o observability.Observation) { lines = append(lines, o) })
	store := newRedisQueryCooldownStore(client, "test.cooldown", nil, func(result string) { results[result]++ }, observer)
	if _, found, err := store.LoadQueryCooldown(ctx, "qg"); found || err != nil {
		t.Fatalf("absent = (found %t, %v)", found, err)
	}
	fence := execution.OwnerFence{QueryGroup: "qg", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "token"}
	if err := store.SaveQueryCooldown(ctx, fence, scheduler.QueryCooldownRecord{QueryGroup: "qg", OwnerEpoch: 1}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.LoadQueryCooldown(ctx, "qg"); !found || err != nil {
		t.Fatalf("found = (found %t, %v)", found, err)
	}
	if err := client.Set(ctx, "test.cooldown:qg", "not json", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadQueryCooldown(ctx, "qg"); !errors.Is(err, scheduler.ErrQueryCooldownUndecodable) {
		t.Fatalf("undecodable = %v, want ErrQueryCooldownUndecodable", err)
	}
	down := redis.NewClient(&redis.Options{Addr: "192.0.2.1:6379", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = down.Close() })
	failing := newRedisQueryCooldownStore(down, "test.cooldown", nil, func(result string) { results[result]++ }, observer)
	if _, _, err := failing.LoadQueryCooldown(ctx, "qg"); err == nil || errors.Is(err, scheduler.ErrQueryCooldownUndecodable) {
		t.Fatalf("unreachable = %v, want the store's error", err)
	}
	if want := map[string]int{"absent": 1, "found": 1, "undecodable": 1, "failed": 1}; !reflect.DeepEqual(results, want) {
		t.Fatalf("results = %v, want %v", results, want)
	}
	if len(lines) != 1 || lines[0].Result != observability.ResultFailed || lines[0].Trace.QueryGroupKey != "qg" || lines[0].Err == nil {
		t.Fatalf("lines = %+v, want one failed line naming the Query Group", lines)
	}
}
