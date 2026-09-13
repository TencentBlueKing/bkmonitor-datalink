// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"errors"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// countingRedisClient counts the round trips the backend issues so tests can
// assert the batching contract against a real server.
type countingRedisClient struct {
	redis.UniversalClient
	mgets     int64
	pipelines int64
	evals     int64
}

func (client *countingRedisClient) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	atomic.AddInt64(&client.mgets, 1)
	return client.UniversalClient.MGet(ctx, keys...)
}

func (client *countingRedisClient) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	atomic.AddInt64(&client.pipelines, 1)
	return client.UniversalClient.Pipelined(ctx, fn)
}

func (client *countingRedisClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	atomic.AddInt64(&client.evals, 1)
	return client.UniversalClient.Eval(ctx, script, keys, args...)
}

func (client *countingRedisClient) reset() {
	atomic.StoreInt64(&client.mgets, 0)
	atomic.StoreInt64(&client.pipelines, 0)
	atomic.StoreInt64(&client.evals, 0)
}

type redisBatchFixture struct {
	address string
	client  *countingRedisClient
	backend *RedisBackend
	owners  *ownership.RedisStore
	fence   execution.OwnerFence
	leased  time.Time
}

func newRedisBatchFixture(t *testing.T) *redisBatchFixture {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	raw := redis.NewClient(&redis.Options{Addr: address, DialTimeout: time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, PoolSize: 4})
	t.Cleanup(func() { _ = raw.Close() })
	client := &countingRedisClient{UniversalClient: raw}
	backend := &RedisBackend{address: address, client: client}
	waitRedisReady(t, backend)

	owners, err := ownership.NewRedisStore(ownership.RedisStoreOptions{Address: address, Prefix: "alarmd-ownership",
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owners.Close() })
	now := time.Now().Truncate(time.Millisecond)
	ctx := context.Background()
	authority, err := owners.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := frozenRef().Slot.QueryGroup
	if _, err := owners.PublishAssignment(ctx, authority, ownership.AssignmentDecision{QueryGroup: queryGroup, DesiredWorkerID: "worker-1",
		PlacementReason: ownership.PlacementRendezvous, DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	lease, err := owners.Acquire(ctx, queryGroup, "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	client.reset()
	return &redisBatchFixture{address: address, client: client, backend: backend, owners: owners, fence: lease.Fence, leased: now}
}

func (fixture *redisBatchFixture) store(t *testing.T, prefix string, fenced bool) *ExecutionStore {
	t.Helper()
	router, err := NewFixedRouter("redis", fixture.backend)
	if err != nil {
		t.Fatal(err)
	}
	options := ExecutionStoreOptions{Prefix: prefix, Router: router, MaxValueBytes: 1 << 20, MaxItemsPerCall: 8192, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute}
	if fenced {
		options.FenceKeys = fixture.owners
	}
	store, err := NewExecutionStore(options)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func (fixture *redisBatchFixture) applyFence(at time.Time) execution.StateApplyFence {
	return execution.StateApplyFence{Fence: fixture.fence, At: at}
}

// loadInStreamBatches reads the way the worker does: one LoadRuntime per
// StatePreflightBatchItems series.
func loadInStreamBatches(t *testing.T, store *ExecutionStore, items []execution.StatePreflightItem) execution.StatePreflightResult {
	t.Helper()
	result := execution.StatePreflightResult{}
	for start := 0; start < len(items); start += execution.StatePreflightBatchItems {
		end := start + execution.StatePreflightBatchItems
		if end > len(items) {
			end = len(items)
		}
		loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: items[start:end]})
		if err != nil {
			t.Fatalf("LoadRuntime() error = %v", err)
		}
		result.Items = append(result.Items, loaded.Items...)
	}
	return result
}

func TestRedisFencedBatchApplyStoresSequentialBytesWithinBoundedRoundTrips(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	mutations := seriesMutations(t, 1000, applyVersion(), 0)
	items := preflightItems(mutations)

	// Sequential path: a store that never witnessed the keys re-reads and
	// compares exact bytes per key, exactly as before this change.
	sequential := fixture.store(t, "sequential", false)
	fixture.client.reset()
	sequentialResult, err := sequential.ApplyRuntime(ctx, execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations})
	if err != nil {
		t.Fatalf("sequential ApplyRuntime() error = %v", err)
	}
	requireAllStatus(t, sequentialResult, execution.StateApplied)
	if fixture.client.mgets != 1000 || fixture.client.evals != 1000 || fixture.client.pipelines != 0 {
		t.Fatalf("sequential round trips: mget=%d eval=%d pipelines=%d", fixture.client.mgets, fixture.client.evals, fixture.client.pipelines)
	}

	batched := fixture.store(t, "batched", true)
	fixture.client.reset()
	loaded := loadInStreamBatches(t, batched, items)
	if fixture.client.mgets != 4 {
		t.Fatalf("preflight MGET round trips = %d, want ceil(1000/256) = 4", fixture.client.mgets)
	}
	for index, view := range loaded.Items {
		if view.Status != execution.StateMissingWarming {
			t.Fatalf("item %d = %+v", index, view)
		}
	}
	fixture.client.reset()
	batchedResult, err := batched.ApplyRuntimeFenced(ctx, execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}, fixture.applyFence(fixture.leased.Add(time.Second)))
	if err != nil {
		t.Fatalf("ApplyRuntimeFenced() error = %v", err)
	}
	requireAllStatus(t, batchedResult, execution.StateApplied)
	if fixture.client.pipelines != 4 || fixture.client.mgets != 0 || fixture.client.evals != 0 {
		t.Fatalf("fenced apply round trips: pipelines=%d mget=%d eval=%d, want 4 pipelines only", fixture.client.pipelines, fixture.client.mgets, fixture.client.evals)
	}

	for _, mutation := range mutations {
		sequentialKey, _ := RuntimeStateKeyV2("sequential", mutation.Identity)
		batchedKey, _ := RuntimeStateKeyV2("batched", mutation.Identity)
		want, err := fixture.client.Get(ctx, sequentialKey).Bytes()
		if err != nil {
			t.Fatal(err)
		}
		got, err := fixture.client.Get(ctx, batchedKey).Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("stored bytes differ for %s", mutation.Identity.SeriesIdentityDigest)
		}
		ttl := fixture.client.PTTL(ctx, batchedKey).Val()
		if ttl <= 0 || ttl > time.Hour {
			t.Fatalf("batched TTL = %v", ttl)
		}
	}

	// Crash between State apply and Progress commit: the replay preflights
	// again and short-circuits with ALREADY_APPLIED without touching storage.
	for index := range mutations {
		mutations[index].ExpectedBlobRevision = 1
	}
	loaded = loadInStreamBatches(t, batched, preflightItems(mutations))
	for index, view := range loaded.Items {
		if execution.ClassifyStateMutation(view, mutations[index]) != execution.StateAlreadyApplied {
			t.Fatalf("replay preflight %d = %+v", index, view)
		}
	}
	fixture.client.reset()
	replay, err := batched.ApplyRuntimeFenced(ctx, execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}, fixture.applyFence(fixture.leased.Add(time.Second)))
	if err != nil {
		t.Fatalf("replay ApplyRuntimeFenced() error = %v", err)
	}
	requireAllStatus(t, replay, execution.StateApplyAlreadyApplied)
	if fixture.client.pipelines != 0 || fixture.client.evals != 0 || fixture.client.mgets != 0 {
		t.Fatalf("already-applied replay reached storage: pipelines=%d eval=%d mget=%d", fixture.client.pipelines, fixture.client.evals, fixture.client.mgets)
	}
}

func TestRedisFencedBatchApplyRejectsStaleOwnerLikeCheckFence(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	store := fixture.store(t, "fenced", true)
	mutations := seriesMutations(t, 3, applyVersion(), 0)
	request := execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}
	valid := fixture.leased.Add(time.Second)

	wrongToken := fixture.fence
	wrongToken.LeaseToken = "someone-else"
	wrongEpoch := fixture.fence
	wrongEpoch.OwnerEpoch++
	cases := []struct {
		name  string
		fence execution.OwnerFence
		at    time.Time
		stale bool
	}{
		{name: "live lease", fence: fixture.fence, at: valid, stale: false},
		{name: "different token", fence: wrongToken, at: valid, stale: true},
		{name: "different epoch", fence: wrongEpoch, at: valid, stale: true},
		{name: "expired deadline", fence: fixture.fence, at: fixture.leased.Add(2 * time.Minute), stale: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			checked := fixture.owners.CheckFence(ctx, test.fence, test.at)
			if errors.Is(checked, ownership.ErrStaleFence) != test.stale {
				t.Fatalf("CheckFence() = %v, want stale=%t", checked, test.stale)
			}
			loadInStreamBatches(t, store, preflightItems(mutations))
			result, err := store.ApplyRuntimeFenced(ctx, request, execution.StateApplyFence{Fence: test.fence, At: test.at})
			keys := make([]string, len(mutations))
			for index, mutation := range mutations {
				keys[index], _ = RuntimeStateKeyV2("fenced", mutation.Identity)
			}
			exists := fixture.client.Exists(ctx, keys...).Val()
			if test.stale {
				if !errors.Is(err, ownership.ErrStaleFence) || len(result.Items) != 0 || exists != 0 {
					t.Fatalf("stale apply = (%+v, %v) exists=%d, want ErrStaleFence and no keys", result, err, exists)
				}
				return
			}
			if err != nil || exists != int64(len(keys)) {
				t.Fatalf("live apply = (%+v, %v) exists=%d", result, err, exists)
			}
			requireAllStatus(t, result, execution.StateApplied)
			if err := fixture.client.Del(ctx, keys...).Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRedisFencedBatchApplyDetectsValueChangedAfterPreflight(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	store := fixture.store(t, "fenced", true)
	mutations := seriesMutations(t, 2, applyVersion(), 0)
	loadInStreamBatches(t, store, preflightItems(mutations))
	key, _ := RuntimeStateKeyV2("fenced", mutations[1].Identity)
	other, _ := encodeRuntime(seriesMutation(t, mutations[1].Identity, applyVersion(), 0, "other"), 1)
	if err := fixture.client.Set(ctx, key, other, 0).Err(); err != nil {
		t.Fatal(err)
	}
	result, err := store.ApplyRuntimeFenced(ctx, execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}, fixture.applyFence(fixture.leased.Add(time.Second)))
	if err != nil {
		t.Fatalf("ApplyRuntimeFenced() error = %v", err)
	}
	if result.Items[0].Status != execution.StateApplied || result.Items[1].Status != execution.StateApplyVersionConflict {
		t.Fatalf("result = %+v", result)
	}
	if current, _ := fixture.client.Get(ctx, key).Bytes(); string(current) != string(other) {
		t.Fatal("conflicting apply overwrote the moved value")
	}
}

func TestRedisFencedBatchApplyReturnsRepliesPerCommandError(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	// A key holding a HASH makes GET fail inside the script for that command
	// only; the sibling write must still be applied and reported.
	if err := fixture.client.HSet(ctx, "broken", "field", "value").Err(); err != nil {
		t.Fatal(err)
	}
	outcomes, err := fixture.backend.CompareAndSetManyByDigest(ctx, nil, []FencedWrite{
		{Key: "broken", ExpectedMissing: true, Value: []byte("v"), TTL: time.Minute},
		{Key: "healthy", ExpectedMissing: true, Value: []byte("v"), TTL: time.Minute},
	})
	if err != nil || len(outcomes) != 2 {
		t.Fatalf("CompareAndSetManyByDigest() = (%+v, %v)", outcomes, err)
	}
	if outcomes[0].Err == nil || outcomes[1].Status != FencedWriteApplied {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if fixture.client.Get(ctx, "healthy").Val() != "v" {
		t.Fatal("sibling write was lost")
	}
	digest := ExpectedValueDigest([]byte("v"))
	outcomes, err = fixture.backend.CompareAndSetManyByDigest(ctx, nil, []FencedWrite{
		{Key: "healthy", ExpectedDigest: digest, Value: []byte("v2"), TTL: 0},
		{Key: "healthy", ExpectedDigest: digest, Value: []byte("v3"), TTL: 0},
		{Key: "absent", ExpectedDigest: digest, Value: []byte("v3"), TTL: 0},
	})
	if err != nil || outcomes[0].Status != FencedWriteApplied || outcomes[1].Status != FencedWriteConflict ||
		string(outcomes[1].Current) != "v2" || outcomes[2].Status != FencedWriteConflictMissing {
		t.Fatalf("digest outcomes = (%+v, %v)", outcomes, err)
	}
	if ttl := fixture.client.TTL(ctx, "healthy").Val(); ttl != -1 {
		t.Fatalf("zero TTL must keep the key persistent, got %v", ttl)
	}
}

// TestRedisRuntimeStateHotModelRoundTrips measures the 4065-series model on a
// local server: the sequential path against the batched fenced path.
func TestRedisRuntimeStateHotModelRoundTrips(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	const series = 4065
	mutations := seriesMutations(t, series, applyVersion(), 0)
	items := preflightItems(mutations)

	sequential := fixture.store(t, "sequential", false)
	fixture.client.reset()
	started := time.Now()
	for _, item := range items {
		if _, err := sequential.LoadRuntime(ctx, execution.StatePreflightRequest{Contract: frozenRef(), Items: []execution.StatePreflightItem{item}}); err != nil {
			t.Fatal(err)
		}
	}
	// A second store instance has no witnesses, which reproduces the old
	// re-read-then-EVAL apply exactly.
	oldApply := fixture.store(t, "sequential", false)
	result, err := oldApply.ApplyRuntime(ctx, execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations})
	if err != nil {
		t.Fatal(err)
	}
	requireAllStatus(t, result, execution.StateApplied)
	oldElapsed := time.Since(started)
	oldTrips := fixture.client.mgets + fixture.client.evals + fixture.client.pipelines

	batched := fixture.store(t, "batched", true)
	fixture.client.reset()
	started = time.Now()
	loadInStreamBatches(t, batched, items)
	result, err = batched.ApplyRuntimeFenced(ctx, execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(), Items: mutations}, fixture.applyFence(fixture.leased.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	requireAllStatus(t, result, execution.StateApplied)
	newElapsed := time.Since(started)
	newTrips := fixture.client.mgets + fixture.client.evals + fixture.client.pipelines
	t.Logf("hot model %d series: old path %d round trips in %v; batched fenced path %d round trips (mget=%d pipelines=%d) in %v",
		series, oldTrips, oldElapsed, newTrips, fixture.client.mgets, fixture.client.pipelines, newElapsed)
	if oldTrips != 3*series {
		t.Fatalf("old path round trips = %d, want %d", oldTrips, 3*series)
	}
	if fixture.client.mgets != 16 || fixture.client.pipelines != 16 || fixture.client.evals != 0 {
		t.Fatalf("batched round trips: mget=%d pipelines=%d eval=%d, want 16 + 16", fixture.client.mgets, fixture.client.pipelines, fixture.client.evals)
	}
	for _, mutation := range mutations {
		sequentialKey, _ := RuntimeStateKeyV2("sequential", mutation.Identity)
		batchedKey, _ := RuntimeStateKeyV2("batched", mutation.Identity)
		if fixture.client.Get(ctx, sequentialKey).Val() != fixture.client.Get(ctx, batchedKey).Val() {
			t.Fatalf("stored bytes differ for %s", mutation.Identity.SeriesIdentityDigest)
		}
	}
}
