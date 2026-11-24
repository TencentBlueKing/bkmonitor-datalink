// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	elasticsearchstore "linkd/internal/store/elasticsearch"
	"linkd/internal/store/storetest"
)

type burstObserver struct {
	started time.Time
	done    atomic.Int64
	failed  atomic.Int64
	mu      sync.Mutex
	latency []float64
}

func (o *burstObserver) KACResult(_ context.Context, stage string, err error, _ time.Duration, n int) {
	if stage != "projection" {
		return
	}
	if err != nil {
		o.failed.Add(int64(n))
	}
	o.mu.Lock()
	o.latency = append(o.latency, time.Since(o.started).Seconds())
	o.mu.Unlock()
	o.done.Add(int64(n))
}

func (*burstObserver) KACQueue(context.Context, int64, time.Duration) {}

// TestRedisElasticsearchBurst 使用真实 Redis、Linkd ES 实时读取及 KAC 批量写入。
// 默认跳过；LINKD_TEST_KAC_BURST=50000 开启隔离突发压测，并验证慢动作下重启恢复。
func TestRedisElasticsearchBurst(t *testing.T) {
	count, err := strconv.Atoi(os.Getenv("LINKD_TEST_KAC_BURST"))
	if err != nil || count < 1 {
		t.Skip("set LINKD_TEST_KAC_BURST for real burst integration")
	}
	redisAddr := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if redisAddr == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	client := integrationClient(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr, PoolSize: 80, ContextTimeoutEnabled: true})
	defer func() { _ = redisClient.Close() }()
	scope := strings.ReplaceAll(uuid.NewString(), "-", "")
	stream := "linkd:kac:v3-test:" + scope
	defer func() { _ = redisClient.Del(context.Background(), stream).Err() }()
	prefix := "linkd-source-v3-" + scope
	router, err := elasticsearchstore.NewStaticRouter(prefix)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := elasticsearchstore.New(client.transport, router, elasticsearchstore.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.request(ctx, http.MethodDelete, "/"+prefix+"*", nil, nil, nil)
		for _, name := range router.Targets() {
			_ = client.request(ctx, http.MethodDelete, "/_index_template/"+name, nil, nil, nil)
		}
	})
	if err = repo.EnsureSchema(t.Context(), router.SchemaConfig()); err != nil {
		t.Fatal(err)
	}
	if err = repo.EnsureIndex(t.Context(), prefix+"-alerts", "alert"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.request(context.Background(), http.MethodDelete, "/"+prefix+"*", nil, nil, nil) }()
	batched, batch, err := repo.EnableWriteBatch(elasticsearchstore.WriteBatchConfig{MaxOperations: 500, MaxBytes: 4 << 20, Wait: time.Millisecond, MaxConcurrentBatches: 16, MaxCalls: 256, Timeout: 10 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	seeded := make(chan int, count)
	for i := range count {
		seeded <- i
	}
	close(seeded)
	seedErrors := make(chan error, 64)
	var seed sync.WaitGroup
	for range 64 {
		seed.Go(func() {
			for i := range seeded {
				a := storetest.Alert("tenant-a", fmt.Sprintf("alert-%d", i), "event-a", fmt.Sprint(i), "warning")
				if _, err := batched.CreateAlertAfterActiveLookup(t.Context(), a); err != nil {
					seedErrors <- err
					return
				}
			}
		})
	}
	seed.Wait()
	batch.Close()
	close(seedErrors)
	for err := range seedErrors {
		t.Fatal("seed", err)
	}
	enqueue := func(n int, actions bool) {
		t.Helper()
		for offset := 0; offset < n; offset += 500 {
			_, err := redisClient.Pipelined(t.Context(), func(pipe redis.Pipeliner) error {
				for i := offset; i < min(n, offset+500); i++ {
					change := domain.AlertChange{BKTenantID: "tenant-a", AlertID: fmt.Sprintf("alert-%d", i)}
					if actions {
						change.Action = &domain.AlertAction{ID: fmt.Sprintf("action-%d", i), Type: "firing", Cause: domain.AlertActionCause{Type: "source_event", ID: "event-a"}}
					}
					raw, _ := json.Marshal(change)
					pipe.XAdd(t.Context(), &redis.XAddArgs{Stream: stream, Values: map[string]any{"body": string(raw)}})
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := func(observer *burstObserver, sender ActionSender) (context.CancelFunc, <-chan error) {
		r := NewRuntime(config.KACDeliveryConfig{}, redisClient, stream, repo, client, sender, observer, logger)
		stop, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- r.Run(stop) }()
		return cancel, done
	}
	waitCount := func(observer *burstObserver, n int) {
		t.Helper()
		deadline := time.After(2 * time.Minute)
		for observer.done.Load() < int64(n) {
			select {
			case <-deadline:
				t.Fatal("projection did not drain", observer.done.Load())
			case <-time.After(10 * time.Millisecond):
			}
		}
		if observer.failed.Load() != 0 {
			t.Fatal("projection failed", observer.failed.Load())
		}
	}
	waitEmpty := func() {
		t.Helper()
		deadline := time.After(time.Minute)
		for {
			length, err := redisClient.XLen(t.Context(), stream).Result()
			if err != nil {
				t.Fatal(err)
			}
			if length == 0 {
				return
			}
			select {
			case <-deadline:
				t.Fatal("stream did not settle", length)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	stopRun := func(cancel context.CancelFunc, done <-chan error) {
		t.Helper()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("graceful stop timed out")
		}
	}
	enqueue(count, false)
	stats := &burstObserver{started: time.Now()}
	cancel, done := run(stats, senderFunc(func(context.Context, ActionRequest) error { return nil }))
	defer cancel()
	waitCount(stats, count)
	elapsed := time.Since(stats.started)
	waitEmpty()
	stopRun(cancel, done)
	stats.mu.Lock()
	slices.Sort(stats.latency)
	p95, p99 := stats.latency[len(stats.latency)*95/100], stats.latency[len(stats.latency)*99/100]
	stats.mu.Unlock()
	var memory goruntime.MemStats
	goruntime.ReadMemStats(&memory)
	t.Logf("burst count=%d lanes=64 batch=500 elapsed=%.3fs throughput=%.0f/s p95=%.3fs p99=%.3fs alloc=%.1fMiB", count, elapsed.Seconds(), float64(count)/elapsed.Seconds(), p95, p99, float64(memory.Alloc)/(1<<20))
	// 故意阻塞已开始的动作：投影仍须清空；停止后释放这些调用，未开始动作保留在 PEL。
	slowCount := min(count, 5000)
	enqueue(slowCount, true)
	started := make(chan struct{}, 64)
	release := make(chan struct{})
	var sent atomic.Int64
	slow := senderFunc(func(ctx context.Context, _ ActionRequest) error {
		started <- struct{}{}
		select {
		case <-release:
			sent.Add(1)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	slowStats := &burstObserver{started: time.Now()}
	cancel, done = run(slowStats, slow)
	defer cancel()
	waitCount(slowStats, slowCount)
	waitSignal(t, started)
	cancel()
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("inflight actions failed to finish")
	}
	remaining, err := redisClient.XLen(t.Context(), stream).Result()
	if err != nil || remaining == 0 || sent.Load() == 0 {
		t.Fatal("unstarted work not retained", remaining, sent.Load(), err)
	}
	t.Logf("slow-action projection_count=%d elapsed=%.3fs finished_http=%d retained_for_restart=%d", slowCount, time.Since(slowStats.started).Seconds(), sent.Load(), remaining)
	restarted := &burstObserver{started: time.Now()}
	cancel, done = run(restarted, senderFunc(func(context.Context, ActionRequest) error { sent.Add(1); return nil }))
	defer cancel()
	waitEmpty()
	stopRun(cancel, done)
	if sent.Load() != int64(slowCount) {
		t.Fatal("restart lost or duplicated actions", sent.Load(), slowCount)
	}
}
