// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package featureFlag

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"

	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	redisStorage "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

type featureFlagProviderStub struct {
	data       []byte
	err        error
	watch      <-chan any
	watchErr   error
	path       string
	calls      *[]string
	name       string
	watchCalls int
}

func (p *featureFlagProviderStub) GetFeatureFlags(context.Context) ([]byte, error) {
	if p.calls != nil {
		*p.calls = append(*p.calls, p.name)
	}
	return p.data, p.err
}

func (p *featureFlagProviderStub) WatchFeatureFlags(context.Context) (<-chan any, error) {
	p.watchCalls++
	if p.watch != nil || p.watchErr != nil {
		return p.watch, p.watchErr
	}
	return make(chan any), nil
}

func (p *featureFlagProviderStub) GetFeatureFlagsPath() string {
	if p.path != "" {
		return p.path
	}
	return "test-feature-flags"
}

func featureFlagConfig(value bool) []byte {
	return []byte(`{
		"test-flag": {
			"variations": {"enabled": true, "disabled": false},
			"targeting": [],
			"defaultRule": {"variation": "` + map[bool]string{true: "enabled", false: "disabled"}[value] + `"}
		}
	}`)
}

func TestEnsureFeatureFlagClientInitializesFromValidConfig(t *testing.T) {
	service := &Service{}
	defer service.Close()

	if err := inner.ReloadFeatureFlags([]byte("{}")); err != nil {
		t.Fatalf("cache valid feature flags failed: %v", err)
	}
	if err := service.ensureFeatureFlagClient(context.Background()); err != nil {
		t.Fatalf("initialization failed: %v", err)
	}
	if !service.clientInitialized {
		t.Fatal("client should be initialized from valid JSON")
	}
}

func TestReconcileFeatureFlagsRefreshesInitializedClientOnChange(t *testing.T) {
	ctx := context.Background()
	provider := &featureFlagProviderStub{data: featureFlagConfig(false)}
	service := &Service{provider: provider}
	service.Close()
	defer service.Close()
	service.registerAsActive()

	if err := RefreshFeatureFlags(ctx); err != nil {
		t.Fatalf("initialize feature flag client: %v", err)
	}
	user := inner.FFUser("test-user", nil)
	if value := inner.BoolVariation(ctx, user, "test-flag", true); value {
		t.Fatal("expected initial Feature Flag value to be false")
	}

	provider.data = featureFlagConfig(true)
	if err := RefreshFeatureFlags(ctx); err != nil {
		t.Fatalf("refresh feature flag client: %v", err)
	}
	if value := inner.BoolVariation(ctx, user, "test-flag", false); !value {
		t.Fatal("expected updated Feature Flag value to be applied immediately")
	}

	provider.data = []byte("{")
	if err := RefreshFeatureFlags(ctx); err == nil {
		t.Fatal("expected malformed Feature Flag JSON to be rejected")
	}
	if value := inner.BoolVariation(ctx, user, "test-flag", false); !value {
		t.Fatal("expected malformed refresh to preserve the last valid Feature Flag value")
	}

	provider.data = []byte(`{"test-flag": {}}`)
	if err := RefreshFeatureFlags(ctx); err == nil {
		t.Fatal("expected invalid Feature Flag schema to be rejected")
	}
	if value := inner.BoolVariation(ctx, user, "test-flag", false); !value {
		t.Fatal("expected invalid schema refresh to preserve the last valid Feature Flag value")
	}
}

func TestFallbackFeatureFlagProviderMigratesConsulAndUsesRedisUpdates(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	redisProvider := redisStorage.NewFeatureFlagClient(client, "test")
	var calls []string
	consulProvider := &featureFlagProviderStub{
		name:  "consul",
		calls: &calls,
		data:  featureFlagConfig(false),
	}
	provider := newFallbackFeatureFlagProvider(redisProvider, consulProvider, redisProvider.InitializeFeatureFlags)
	service := &Service{provider: provider}
	defer service.Close()

	if err := service.reconcileFeatureFlags(ctx); err != nil {
		t.Fatalf("migrate feature flags failed: %v", err)
	}
	persisted, err := mr.Get(redisProvider.GetFeatureFlagsPath())
	if err != nil || persisted != string(featureFlagConfig(false)) {
		t.Fatalf("expected Consul snapshot to be persisted, got %q, error: %v", persisted, err)
	}
	if ttl := mr.TTL(redisProvider.GetFeatureFlagsPath()); ttl != 0 {
		t.Fatalf("migrated snapshot must not expire, got TTL %v", ttl)
	}
	user := inner.FFUser("test-user", nil)
	if inner.BoolVariation(ctx, user, "test-flag", true) {
		t.Fatal("expected migrated flag value to be false")
	}
	consulProvider.data = featureFlagConfig(true)
	if err := service.reconcileFeatureFlags(ctx); err != nil {
		t.Fatalf("reconcile migrated flags: %v", err)
	}
	if len(calls) != 1 || inner.BoolVariation(ctx, user, "test-flag", true) {
		t.Fatal("Consul updates must not affect the migrated Redis snapshot")
	}
	if err := redisProvider.SetFeatureFlags(ctx, featureFlagConfig(true)); err != nil {
		t.Fatalf("update Redis flags: %v", err)
	}
	if err := service.reconcileFeatureFlags(ctx); err != nil {
		t.Fatalf("reconcile updated flags: %v", err)
	}
	if !inner.BoolVariation(ctx, user, "test-flag", false) {
		t.Fatal("expected Redis update to change the runtime flag value")
	}
}

func TestFallbackFeatureFlagProviderUsesRedisWhenSnapshotExists(t *testing.T) {
	for _, snapshot := range [][]byte{featureFlagConfig(true), []byte("{}"), []byte(""), []byte(" "), []byte("{")} {
		t.Run(string(snapshot), func(t *testing.T) {
			var calls []string
			redisProvider := &featureFlagProviderStub{
				name:  "redis",
				calls: &calls,
				data:  snapshot,
			}
			consulProvider := &featureFlagProviderStub{
				name:  "consul",
				calls: &calls,
				data:  featureFlagConfig(false),
			}
			provider := newFallbackFeatureFlagProvider(redisProvider, consulProvider, nil)

			data, err := provider.GetFeatureFlags(context.Background())
			if err != nil {
				t.Fatalf("get feature flags failed: %v", err)
			}
			if string(data) != string(snapshot) {
				t.Fatalf("expected Redis snapshot, got %q", data)
			}
			if len(calls) != 1 || calls[0] != "redis" {
				t.Fatalf("expected only Redis to be read, got %v", calls)
			}
		})
	}
}

func TestFallbackFeatureFlagProviderPreservesRuntimeWhenRedisReadFails(t *testing.T) {
	ctx := context.Background()
	redisProvider := &featureFlagProviderStub{data: featureFlagConfig(true)}
	var calls []string
	consulProvider := &featureFlagProviderStub{data: featureFlagConfig(false), calls: &calls, name: "consul"}
	provider := newFallbackFeatureFlagProvider(redisProvider, consulProvider, func(context.Context, []byte) (bool, error) {
		t.Fatal("must not backfill after a Redis read error")
		return false, nil
	})
	service := &Service{provider: provider}
	defer service.Close()
	if err := service.reconcileFeatureFlags(ctx); err != nil {
		t.Fatalf("initialize Redis snapshot: %v", err)
	}
	redisProvider.err = errors.New("redis unavailable")
	if err := service.reconcileFeatureFlags(ctx); !errors.Is(err, redisProvider.err) {
		t.Fatalf("expected Redis read error, got %v", err)
	}
	if len(calls) != 0 || !inner.BoolVariation(ctx, inner.FFUser("test-user", nil), "test-flag", false) {
		t.Fatal("Redis read failure must preserve the last valid snapshot without reading Consul")
	}
}

func TestFallbackFeatureFlagProviderOnlyWatchesRedis(t *testing.T) {
	redisProvider := &featureFlagProviderStub{watch: make(chan any)}
	consulProvider := &featureFlagProviderStub{}
	provider := newFallbackFeatureFlagProvider(redisProvider, consulProvider, nil)
	watch, err := provider.WatchFeatureFlags(context.Background())
	if err != nil {
		t.Fatalf("watch feature flags failed: %v", err)
	}
	if watch != redisProvider.watch || redisProvider.watchCalls != 1 || consulProvider.watchCalls != 0 {
		t.Fatal("expected only the Redis watcher after enabling Redis migration")
	}
}

func TestFallbackFeatureFlagProviderRejectsInvalidConsulBeforeBackfill(t *testing.T) {
	for _, snapshot := range []string{"{", "null", `{"test-flag":{}}`} {
		t.Run(snapshot, func(t *testing.T) {
			provider := newFallbackFeatureFlagProvider(&featureFlagProviderStub{}, &featureFlagProviderStub{data: []byte(snapshot)}, func(context.Context, []byte) (bool, error) {
				t.Fatal("invalid Consul snapshot must not be persisted")
				return false, nil
			})
			if _, err := provider.GetFeatureFlags(context.Background()); err == nil {
				t.Fatal("expected invalid Consul snapshot to be rejected")
			}
		})
	}
}

func TestFallbackFeatureFlagProviderRetriesFailedBackfill(t *testing.T) {
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	redisProvider := redisStorage.NewFeatureFlagClient(client, "test")
	var calls []string
	consulProvider := &featureFlagProviderStub{data: featureFlagConfig(false), calls: &calls, name: "consul"}
	attempts := 0
	provider := newFallbackFeatureFlagProvider(redisProvider, consulProvider, func(ctx context.Context, data []byte) (bool, error) {
		attempts++
		if attempts == 1 {
			return false, errors.New("write unavailable")
		}
		return redisProvider.InitializeFeatureFlags(ctx, data)
	})
	for i := 0; i < 3; i++ {
		data, err := provider.GetFeatureFlags(context.Background())
		if err != nil || string(data) != string(featureFlagConfig(false)) {
			t.Fatalf("expected usable fallback or migrated snapshot on read %d, got %q, error: %v", i, data, err)
		}
		if i == 0 && mr.Exists(redisProvider.GetFeatureFlagsPath()) {
			t.Fatal("failed backfill must leave the Redis key missing")
		}
	}
	if attempts != 2 || len(calls) != 2 || !mr.Exists(redisProvider.GetFeatureFlagsPath()) {
		t.Fatal("expected failed backfill to retry, then stop reading Consul after success")
	}
}

func TestFallbackFeatureFlagProviderUsesConcurrentRedisWrite(t *testing.T) {
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	redisProvider := redisStorage.NewFeatureFlagClient(client, "test")
	provider := newFallbackFeatureFlagProvider(redisProvider, &featureFlagProviderStub{data: featureFlagConfig(false)}, func(ctx context.Context, data []byte) (bool, error) {
		// 模拟首次 GET 后、回填前有用户设置了新配置。
		if err := redisProvider.SetFeatureFlags(ctx, featureFlagConfig(true)); err != nil {
			return false, err
		}
		return redisProvider.InitializeFeatureFlags(ctx, data)
	})
	data, err := provider.GetFeatureFlags(context.Background())
	if err != nil || string(data) != string(featureFlagConfig(true)) {
		t.Fatalf("expected concurrent Redis configuration to take precedence, got %q, error: %v", data, err)
	}
	persisted, err := mr.Get(redisProvider.GetFeatureFlagsPath())
	if err != nil || persisted != string(data) {
		t.Fatalf("concurrent Redis configuration was overwritten: %q, error: %v", persisted, err)
	}
}

func TestFallbackFeatureFlagProviderReportsConcurrentRedisReadFailure(t *testing.T) {
	redisProvider := &featureFlagProviderStub{}
	readErr := errors.New("concurrent Redis snapshot cannot be read")
	provider := newFallbackFeatureFlagProvider(redisProvider, &featureFlagProviderStub{data: featureFlagConfig(false)}, func(context.Context, []byte) (bool, error) {
		redisProvider.err = readErr
		return false, nil
	})
	if _, err := provider.GetFeatureFlags(context.Background()); !errors.Is(err, readErr) {
		t.Fatalf("must not use stale Consul snapshot after losing initialization to a Redis writer, got %v", err)
	}
}
