// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// Only these three read commands are implemented: any accidental ownership
// read, full GET, or registry cleanup fails through the nil embedded client.
type observationRegistryRedis struct {
	redis.Cmdable
	t           *testing.T
	store       *RedisStore
	ids         []string
	payloads    map[string]string
	total       int64
	calls       []string
	page        redis.ZRangeBy
	ends        []int64
	errAt       string
	blockCount  bool
	shortPage   bool
	countBounds string
}

func (reader *observationRegistryRedis) record(ctx context.Context, command string) error {
	reader.t.Helper()
	if _, ok := ctx.Deadline(); !ok {
		reader.t.Fatal("diagnostic command has no deadline")
	}
	reader.calls = append(reader.calls, command)
	if reader.errAt == command {
		return errors.New("synthetic read error")
	}
	return nil
}

func (reader *observationRegistryRedis) ZCount(ctx context.Context, key, min, max string) *redis.IntCmd {
	err := reader.record(ctx, "zcount")
	if key != reader.store.workerRegistryKey() || max != "+inf" {
		reader.t.Fatalf("ZCOUNT used unexpected key or bound: %q %q", key, max)
	}
	reader.countBounds = min
	if reader.blockCount {
		<-ctx.Done()
		err = ctx.Err()
	}
	return redis.NewIntResult(reader.total, err)
}

func (reader *observationRegistryRedis) ZRangeByScore(ctx context.Context, key string, page *redis.ZRangeBy) *redis.StringSliceCmd {
	err := reader.record(ctx, "zrangebyscore")
	if key != reader.store.workerRegistryKey() || page.Min != reader.countBounds || page.Max != "+inf" || page.Count <= 0 {
		reader.t.Fatalf("unbounded or inconsistent registry page: key=%q page=%+v", key, page)
	}
	reader.page = *page
	start := min(page.Offset, int64(len(reader.ids)))
	end := min(start+page.Count, int64(len(reader.ids)))
	if reader.shortPage && end > start {
		end--
	}
	return redis.NewStringSliceResult(reader.ids[start:end], err)
}

func (reader *observationRegistryRedis) GetRange(ctx context.Context, key string, start, end int64) *redis.StringCmd {
	err := reader.record(ctx, "getrange")
	if start != 0 || end < 0 {
		reader.t.Fatalf("unbounded registration read: %d..%d", start, end)
	}
	reader.ends = append(reader.ends, end)
	payload := reader.payloads[key]
	return redis.NewStringResult(payload[:min(int64(len(payload)), end+1)], err)
}

func newObservationRegistryRedis(t *testing.T, ids ...string) (*RedisStore, *observationRegistryRedis, time.Time, ObservationRegistryLimits) {
	t.Helper()
	store := &RedisStore{prefix: "observation-test"}
	now := time.UnixMilli(1_700_000_000_000)
	reader := &observationRegistryRedis{t: t, store: store, ids: ids, total: int64(len(ids)), payloads: map[string]string{}}
	for _, id := range ids {
		reader.payloads[store.workerKey(id)] = observationWorkerJSON(t, WorkerRegistration{
			WorkerID: id, AssignmentReadiness: WorkerReady, DependencyStatus: DependencyHealthy,
			DeploymentProfile: "synthetic", CapabilitiesDigest: "v1", ExpiresAt: now.Add(time.Minute),
		})
	}
	return store, reader, now, ObservationRegistryLimits{Bytes: 32 << 10, Commands: 16, Rows: 8, Timeout: time.Second}
}

func observationWorkerJSON(t *testing.T, worker WorkerRegistration) string {
	t.Helper()
	payload, err := json.Marshal(worker)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestObservationRegistryReadyIdentityAndLifetime(t *testing.T) {
	store, reader, at, limits := newObservationRegistryRedis(t, "ready", "degraded", "starting", "expired")
	for _, id := range reader.ids[1:] {
		var worker WorkerRegistration
		if err := json.Unmarshal([]byte(reader.payloads[store.workerKey(id)]), &worker); err != nil {
			t.Fatal(err)
		}
		switch id {
		case "degraded":
			worker.DependencyStatus = DependencyDegraded
		case "starting":
			worker.AssignmentReadiness = WorkerStarting
		case "expired":
			worker.ExpiresAt = at // equality is already expired
		}
		reader.payloads[store.workerKey(id)] = observationWorkerJSON(t, worker)
	}
	got := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
	if !got.Complete || got.Reason != "" || !reflect.DeepEqual(got.ReadyIDs, []string{"ready", "degraded"}) ||
		got.Total != 4 || got.ScannedRows != 4 || got.ReadCommands != 6 || got.NextOffset != 0 {
		t.Fatalf("unexpected registry observation: %+v", got)
	}
	var bytes int64
	for _, id := range reader.ids {
		bytes += int64(len(id) + len(reader.payloads[store.workerKey(id)]))
	}
	if got.ReadBytes != bytes || reader.countBounds != "(1700000000000" || reader.page.Count != 4 {
		t.Fatalf("incorrect accounting or candidate range: %+v reader=%+v", got, reader)
	}
}

func TestObservationRegistryUnknownWorkerNeverCompletesDenominator(t *testing.T) {
	for _, fixture := range []struct {
		name, payload, reason string
	}{
		{"missing", "", "missing_worker"},
		{"invalid_json", "{", "invalid_worker"},
		{"identity_mismatch", `{"worker_id":"other","assignment_readiness":"READY","dependency_status":"HEALTHY","deployment_profile":"synthetic","capabilities_digest":"v1","expires_at":"2030-01-01T00:00:00Z"}`, "invalid_worker"},
		{"unknown_readiness", `{"worker_id":"bad","assignment_readiness":"FUTURE","dependency_status":"HEALTHY","deployment_profile":"synthetic","capabilities_digest":"v1","expires_at":"2030-01-01T00:00:00Z"}`, "invalid_worker"},
		{"missing_expiry", `{"worker_id":"bad","assignment_readiness":"READY","dependency_status":"HEALTHY","deployment_profile":"synthetic","capabilities_digest":"v1"}`, "invalid_worker"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			store, reader, at, limits := newObservationRegistryRedis(t, "good", "bad")
			reader.payloads[store.workerKey("bad")] = fixture.payload
			got := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
			if got.Complete || got.Reason != fixture.reason || got.Total != 2 || !reflect.DeepEqual(got.ReadyIDs, []string{"good"}) {
				t.Fatalf("incomplete registry was treated as complete: %+v", got)
			}
		})
	}
}

func TestObservationRegistryBoundedPagesRotate(t *testing.T) {
	for _, useCommands := range []bool{false, true} {
		store, reader, at, limits := newObservationRegistryRedis(t, "a", "b", "c")
		limits.Rows = 2
		reason := "rows_budget"
		if useCommands {
			limits.Rows, limits.Commands, reason = 8, 4, "commands_budget"
		}
		first := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
		if first.Complete || first.Reason != reason || first.NextOffset != 2 || first.ScannedRows != 2 ||
			reader.page.Count != 2 || first.ReadCommands != 4 || !reflect.DeepEqual(first.ReadyIDs, []string{"a", "b"}) {
			t.Fatalf("unbounded first page: %+v", first)
		}
		last := store.ReadObservationRegistry(context.Background(), reader, at, first.NextOffset, limits)
		if last.Complete || last.Reason != "offset_window" || last.NextOffset != 0 || last.Offset != 2 ||
			last.Total != 3 || !reflect.DeepEqual(last.ReadyIDs, []string{"c"}) {
			t.Fatalf("partial rotation became a full denominator: %+v", last)
		}
		limits.Rows, limits.Commands = 8, 16
		wrapped := store.ReadObservationRegistry(context.Background(), reader, at, 20, limits)
		if !wrapped.Complete || wrapped.Offset != 0 || len(wrapped.ReadyIDs) != 3 {
			t.Fatalf("offset beyond a shrinking registry did not wrap: %+v", wrapped)
		}
	}
}

func TestObservationRegistryByteBudgetAndOversizedMember(t *testing.T) {
	store, reader, at, limits := newObservationRegistryRedis(t, "a", "b")
	firstSize := int64(len(reader.payloads[store.workerKey("a")]))
	limits.Bytes = 2 + firstSize + 20
	got := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
	if got.Complete || got.Reason != "bytes_budget" || got.ReadBytes != limits.Bytes ||
		!reflect.DeepEqual(got.ReadyIDs, []string{"a"}) || !reflect.DeepEqual(reader.ends, []int64{firstSize + 19, 19}) {
		t.Fatalf("GETRANGE did not obey remaining byte budget: %+v ends=%v", got, reader.ends)
	}
	store, reader, at, limits = newObservationRegistryRedis(t, "a")
	limits.Bytes = 1 + int64(len(reader.payloads[store.workerKey("a")]))
	exact := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
	if exact.Complete || exact.Reason != "bytes_budget" || len(exact.ReadyIDs) != 0 {
		t.Fatalf("full-sized reply cannot prove no truncation: %+v", exact)
	}
	limits.Bytes++
	if full := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits); !full.Complete {
		t.Fatalf("complete small registration not accepted: %+v", full)
	}
	store, reader, at, limits = newObservationRegistryRedis(t, strings.Repeat("x", 100))
	limits.Bytes = 8
	member := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
	if member.Complete || member.Reason != "bytes_budget" || member.ReadBytes != 100 || member.ReadCommands != 2 || len(reader.ends) != 0 {
		t.Fatalf("oversized member was hidden or followed by more reads: %+v", member)
	}
}

func TestObservationRegistryReadFailuresAndEmptyRegistry(t *testing.T) {
	for _, command := range []string{"zcount", "zrangebyscore", "getrange"} {
		t.Run(command, func(t *testing.T) {
			store, reader, at, limits := newObservationRegistryRedis(t, "a")
			reader.errAt = command
			got := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
			if got.Complete || got.Reason != "read_error" || got.ReadCommands != len(reader.calls) {
				t.Fatalf("read failure was hidden: %+v", got)
			}
			if command == "zcount" && got.Total != -1 {
				t.Fatalf("unavailable count reported as known: %+v", got)
			}
		})
	}
	store, reader, at, limits := newObservationRegistryRedis(t)
	limits.Commands = 1
	empty := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
	if !empty.Complete || empty.Total != 0 || empty.ReadCommands != 1 || empty.ReadBytes != 0 {
		t.Fatalf("empty registry not distinguished from unavailable: %+v", empty)
	}
	store, reader, at, limits = newObservationRegistryRedis(t, "a", "b")
	reader.shortPage = true
	changed := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
	if changed.Complete || changed.Reason != "registry_changed" || changed.Total != 2 || changed.ScannedRows != 1 {
		t.Fatalf("count/page mismatch was not unknown: %+v", changed)
	}
}

func TestObservationRegistryDisabledCommandsAndTimeout(t *testing.T) {
	store, reader, at, limits := newObservationRegistryRedis(t, "a")
	limits.Bytes = 0
	if disabled := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits); disabled.Complete || disabled.Reason != "disabled" || disabled.ReadCommands != 0 {
		t.Fatalf("disabled registry performed reads: %+v", disabled)
	}
	limits.Bytes, limits.Commands = 1024, 2
	commands := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
	if commands.Complete || commands.Reason != "commands_budget" || commands.ReadCommands != 1 || len(reader.ends) != 0 {
		t.Fatalf("worker commands were not reserved: %+v", commands)
	}
	limits.Commands, limits.Timeout, reader.blockCount = 8, time.Millisecond, true
	timedOut := store.ReadObservationRegistry(context.Background(), reader, at, 0, limits)
	if timedOut.Complete || timedOut.Reason != "timeout" || timedOut.Total != -1 || timedOut.ReadCommands != 1 {
		t.Fatalf("timeout did not stop the read: %+v", timedOut)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := store.ReadObservationRegistry(ctx, reader, at, 0, limits)
	if canceled.Complete || canceled.Reason != "timeout" || canceled.ReadCommands != 0 {
		t.Fatalf("canceled request still read registry: %+v", canceled)
	}
}

func TestObservationRegistryRealRedisDoesNotCleanRegistry(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	at := time.Now().Truncate(time.Millisecond)
	worker := WorkerRegistration{
		WorkerID: "synthetic-ready", AssignmentReadiness: WorkerReady, DependencyStatus: DependencyHealthy,
		DeploymentProfile: "synthetic", CapabilitiesDigest: "v1", ExpiresAt: at.Add(time.Minute),
	}
	if err := store.RegisterWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if err := store.client.ZAdd(ctx, store.workerRegistryKey(),
		&redis.Z{Score: float64(at.Add(time.Minute).UnixMilli()), Member: "synthetic-missing"},
		&redis.Z{Score: float64(at.UnixMilli()), Member: "synthetic-expired"},
	).Err(); err != nil {
		t.Fatal(err)
	}
	got := store.ReadObservationRegistry(ctx, store.client, at, 0, ObservationRegistryLimits{
		Bytes: 4096, Commands: 8, Rows: 8, Timeout: time.Second,
	})
	if got.Complete || got.Reason != "missing_worker" || got.Total != 2 || got.ReadCommands != 4 ||
		!reflect.DeepEqual(got.ReadyIDs, []string{worker.WorkerID}) {
		t.Fatalf("unexpected real Redis observation: %+v", got)
	}
	if size, err := store.client.ZCard(ctx, store.workerRegistryKey()).Result(); err != nil || size != 3 {
		t.Fatalf("diagnostic reader changed registry: size=%d err=%v", size, err)
	}
}
