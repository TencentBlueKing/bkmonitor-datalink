// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package controlplaneprocess

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/activeindex"
	"linkd/internal/config"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type activeRows []activeindex.Row

func (rows activeRows) ScanActiveIndex(_ context.Context, _ activeindex.Query, consume func([]activeindex.Row) error) error {
	return consume(rows)
}

func TestActiveIndexRepairReusesLifecycleLock(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("LINKD_TEST_REDIS_ADDRESS is not set")
	}
	client := redis.NewClient(&redis.Options{Addr: address, ContextTimeoutEnabled: true, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	id := uuid.NewString()
	prefix := "linkd-limit-repair-test:" + id
	source := config.EventSource{EventSourceID: "source", ActiveAlertLimits: &config.ActiveAlertLimits{Rules: []config.ActiveAlertLimitRule{{Name: "total", MaxActive: 1, KeyPrefix: prefix}}}}
	cfg := config.Config{Storage: &config.StorageConfig{Repository: config.RepositoryTypeMySQL}, Lifecycle: &config.LifecycleConfig{Lock: config.LifecycleLockConfig{KeyPrefix: prefix + ":lock"}}}
	repository := memory.New()
	a := storetest.Alert("tenant", "active", "event", "fp", "warning")
	a.EventSourceID = source.EventSourceID
	if _, err := repository.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	cache := activeindex.New(client, id, source, nil)
	settings := config.ActiveIndexConfig{}.WithDefaults()
	// 与 Worker 使用相同的 ForSource 作用域；锁忙时不能改集合，下一轮再补齐。
	locker, err := scheduler.NewRedisLocker(client, cfg.Lifecycle.ForSource(cfg.Dispatch.WithDefaults().Deployment, source.EventSourceID).SchedulerConfig())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := locker.Acquire(t.Context(), scheduler.CorrelationKey(a.BKTenantID, a.EventSourceID, a.Fingerprint))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = locker.Release(context.Background(), lease)
		_ = client.Del(context.Background(), prefix+":tenant").Err()
	})
	rows := activeRows{activeindex.AlertRow(a)}
	if err := reconcileActiveSource(t.Context(), cfg, source, cache, rows, repository, client, settings); err != nil {
		t.Fatal(err)
	}
	if client.Exists(t.Context(), prefix+":tenant").Val() != 0 {
		t.Fatal("repair bypassed existing fingerprint lock")
	}
	if err := locker.Release(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if err := reconcileActiveSource(t.Context(), cfg, source, cache, rows, repository, client, settings); err != nil {
		t.Fatal(err)
	}
	if !client.SIsMember(t.Context(), prefix+":tenant", a.Fingerprint).Val() {
		t.Fatal("missing member not repaired")
	}
	keys, err := client.Keys(t.Context(), prefix+"*").Result()
	if err != nil || len(keys) != 1 || keys[0] != prefix+":tenant" {
		t.Fatal("unexpected repair keys", keys, err)
	}
}
