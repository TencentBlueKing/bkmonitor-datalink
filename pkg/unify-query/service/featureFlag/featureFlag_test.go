// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package featureFlag

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/viper"

	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	redisStorage "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	redisService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/redis"
)

type featureFlagProviderStub struct {
	data       []byte
	err        error
	watch      <-chan any
	watchErr   error
	path       string
	calls      *[]string
	name       string
	get        func(context.Context) ([]byte, error)
	watchCalls int
}

func (p *featureFlagProviderStub) GetFeatureFlags(ctx context.Context) ([]byte, error) {
	if p.get != nil {
		return p.get(ctx)
	}
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

	for _, data := range []string{`{"test-flag": {}}`, `{"test-flag":{"variations":{"enabled":true}}}`} {
		provider.data = []byte(data)
		if err := RefreshFeatureFlags(ctx); err == nil {
			t.Fatal("expected invalid Feature Flag schema to be rejected")
		}
		if value := inner.BoolVariation(ctx, user, "test-flag", false); !value {
			t.Fatal("expected invalid schema refresh to preserve the last valid Feature Flag value")
		}
	}
}

func TestFallbackFeatureFlagProviderMigratesLegacyConsulAndUsesRedisUpdates(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	redisProvider := redisStorage.NewFeatureFlagClient(client, "test")
	legacySnapshot := []byte(`{"test-flag":{"rule":"key eq \"test-user\"","percentage":100,"true":false,"false":false,"default":false}}`)
	var calls []string
	consulProvider := &featureFlagProviderStub{
		name:  "consul",
		calls: &calls,
		data:  legacySnapshot,
	}
	provider := newFallbackFeatureFlagProvider(redisProvider, consulProvider, redisProvider.InitializeFeatureFlags)
	service := &Service{provider: provider}
	defer service.Close()

	if err := service.reconcileFeatureFlags(ctx); err != nil {
		t.Fatalf("migrate feature flags failed: %v", err)
	}
	persisted, err := mr.Get(redisProvider.GetFeatureFlagsPath())
	if err != nil || persisted != string(legacySnapshot) {
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

	consulProvider.data = featureFlagConfig(false)
	if err := redisProvider.ResetFeatureFlags(ctx); err != nil {
		t.Fatalf("reset migrated flags: %v", err)
	}
	if err := service.reconcileFeatureFlags(ctx); err != nil {
		t.Fatalf("migrate flags again: %v", err)
	}
	if len(calls) != 2 || inner.BoolVariation(ctx, user, "test-flag", true) {
		t.Fatal("reset must migrate the current Consul snapshot and apply it")
	}
	if persisted, err := mr.Get(redisProvider.GetFeatureFlagsPath()); err != nil || persisted != string(consulProvider.data) {
		t.Fatalf("expected current Consul snapshot to be persisted again, got %q, error: %v", persisted, err)
	}
	if ttl := mr.TTL(redisProvider.GetFeatureFlagsPath()); ttl != 0 {
		t.Fatalf("re-migrated snapshot must not expire, got TTL %v", ttl)
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
	for _, snapshot := range []string{"{", "null", `{"test-flag":{}}`, `{"test-flag":{"variations":{"enabled":true}}}`} {
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

func newRedisOnlyFeatureFlagProvider(t *testing.T) (*miniredis.Miniredis, FeatureFlagProvider) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	primary := redisStorage.NewFeatureFlagClient(client, "test")
	return mr, newFallbackFeatureFlagProvider(primary, nil, primary.InitializeFeatureFlags)
}

func TestRedisOnlyFeatureFlagProviderPreservesExistingRuntime(t *testing.T) {
	mr, provider := newRedisOnlyFeatureFlagProvider(t)
	ctx := context.Background()
	key := provider.GetFeatureFlagsPath()
	snapshot := string(featureFlagConfig(false))
	if err := mr.Set(key, snapshot); err != nil {
		t.Fatal(err)
	}
	service := &Service{provider: provider}
	defer service.Close()
	if err := service.reconcileFeatureFlags(ctx); err != nil {
		t.Fatal(err)
	}
	if actual, _ := mr.Get(key); actual != snapshot || inner.BoolVariation(ctx, inner.FFUser("test-user", nil), "test-flag", true) {
		t.Fatal("existing custom flags must remain unchanged and control runtime evaluation")
	}
	for _, invalid := range []string{"", "{", `{"test-flag":{}}`} {
		if err := mr.Set(key, invalid); err != nil {
			t.Fatal(err)
		}
		if err := service.reconcileFeatureFlags(ctx); err == nil {
			t.Fatal("invalid existing snapshot must fail instead of initializing defaults")
		}
		if actual, _ := mr.Get(key); actual != invalid || inner.BoolVariation(ctx, inner.FFUser("test-user", nil), "test-flag", true) {
			t.Fatal("invalid snapshot must not be overwritten or replace the last valid runtime flags")
		}
	}
	mr.SetError("ERR redis unavailable")
	if err := service.reconcileFeatureFlags(ctx); err == nil || inner.BoolVariation(ctx, inner.FFUser("test-user", nil), "test-flag", true) {
		t.Fatal("Redis read errors must be reported while retaining the last valid runtime flags")
	}
}

func TestFallbackFeatureFlagProviderDoesNotReplaceUnavailableConsulWithDefaults(t *testing.T) {
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	primary := redisStorage.NewFeatureFlagClient(client, "test")
	consulErr := errors.New("consul unavailable")
	fallback := &featureFlagProviderStub{err: consulErr, data: featureFlagConfig(false)}
	provider := newFallbackFeatureFlagProvider(primary, fallback, primary.InitializeFeatureFlags)
	service := &Service{provider: provider}
	defer service.Close()
	if err := service.reconcileFeatureFlags(context.Background()); !errors.Is(err, consulErr) || mr.Exists(primary.GetFeatureFlagsPath()) {
		t.Fatal("Consul failure must leave the Redis key missing for the original migration to recover")
	}
	fallback.err = nil
	if err := service.reconcileFeatureFlags(context.Background()); err != nil {
		t.Fatal(err)
	}
	if actual, _ := mr.Get(primary.GetFeatureFlagsPath()); actual != string(fallback.data) ||
		inner.BoolVariation(context.Background(), inner.FFUser("test-user", nil), "test-flag", true) {
		t.Fatal("recovery must migrate the original Consul rules instead of defaulting them")
	}
}

func TestNewFeatureFlagProviderRetainsConsulFallbackWithLegacyDisabledSetting(t *testing.T) {
	viper.Reset()
	previousBasePath := redisService.KVBasePath
	t.Cleanup(func() { viper.Reset(); redisService.KVBasePath = previousBasePath })
	mr := miniredis.RunT(t)
	if err := redisStorage.SetInstance(context.Background(), "test", "test", &goRedis.UniversalOptions{Addrs: []string{mr.Addr()}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(redisStorage.Close)
	redisService.KVBasePath = "test"
	viper.Set("consul.enabled", false)
	viper.Set("storage.source", "redis")
	provider, err := newFeatureFlagProvider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := provider.(*fallbackFeatureFlagProvider); !ok || p.fallback == nil {
		t.Fatal("legacy consul.enabled=false must retain the migration provider")
	}
	redisService.KVBasePath = ""
	if _, err := newFeatureFlagProvider(context.Background()); err != nil {
		t.Fatal("empty Redis prefix must retain the legacy direct Consul provider")
	}
	viper.Reset()
	if _, err := newFeatureFlagProvider(context.Background()); err != nil {
		t.Fatalf("legacy deployments must retain their Consul fallback: %v", err)
	}
	redisService.KVBasePath = "test"
	provider, err = newFeatureFlagProvider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := provider.(*fallbackFeatureFlagProvider); !ok || p.fallback == nil {
		t.Fatal("unset consul.enabled must retain Consul migration")
	}
}

// Consul 不存在时仅使用进程内默认值，不能把迁移失败固化为 Redis 空规则。
func TestFallbackTimeoutKeepsDefaultsAndRecoversOriginalRules(t *testing.T) {
	if err := inner.ReloadFeatureFlags([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	primary := redisStorage.NewFeatureFlagClient(client, "test")
	fallback := &featureFlagProviderStub{get: func(ctx context.Context) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > consulFallbackTimeout {
			t.Fatal("Consul fallback must have a finite deadline")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	service := &Service{provider: newFallbackFeatureFlagProvider(primary, fallback, primary.InitializeFeatureFlags)}
	defer service.Close()
	ctx := metadata.InitHashID(context.Background())
	started := time.Now()
	if err := service.reconcileFeatureFlags(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded Consul failure, got %v", err)
	}
	if time.Since(started) > consulFallbackTimeout+2*time.Second {
		t.Fatal("Consul fallback blocked beyond its timeout")
	}
	if mr.Exists(primary.GetFeatureFlagsPath()) || !service.clientInitialized {
		t.Fatal("timeout must leave Redis missing while initializing the in-memory client")
	}
	if !inner.GetMustVmQueryFeatureFlag(ctx, "test-table") || inner.GetIsK8sFeatureFlag(ctx) || inner.GetBkDataTableIDCheck(ctx, "test-table") {
		t.Fatal("fresh instance must retain existing query defaults")
	}
	fallback.get = nil
	fallback.data = featureFlagConfig(false)
	if err := service.reconcileFeatureFlags(ctx); err != nil {
		t.Fatal(err)
	}
	if actual, _ := mr.Get(primary.GetFeatureFlagsPath()); actual != string(fallback.data) || mr.TTL(primary.GetFeatureFlagsPath()) != 0 || inner.BoolVariation(ctx, inner.FFUser("user", nil), "test-flag", true) {
		t.Fatal("recovery must migrate and apply original rules with no TTL")
	}
	// Reload closes the client but keeps the valid in-memory snapshot.
	service.Close()
	mr.Del(primary.GetFeatureFlagsPath())
	fallback.err = errors.New("consul unavailable again")
	if err := service.reconcileFeatureFlags(ctx); err == nil || !service.clientInitialized || inner.BoolVariation(ctx, inner.FFUser("user", nil), "test-flag", true) || mr.Exists(primary.GetFeatureFlagsPath()) {
		t.Fatal("failed reinitialization must preserve the previous effective rules")
	}
}
