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

	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
)

type featureFlagProviderStub struct {
	data     []byte
	err      error
	watch    <-chan any
	watchErr error
	path     string
	calls    *[]string
	name     string
}

func (p *featureFlagProviderStub) GetFeatureFlags(context.Context) ([]byte, error) {
	if p.calls != nil {
		*p.calls = append(*p.calls, p.name)
	}
	return p.data, p.err
}

func (p *featureFlagProviderStub) WatchFeatureFlags(context.Context) (<-chan any, error) {
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

func TestFallbackFeatureFlagProviderUsesConsulWhenRedisSnapshotIsMissing(t *testing.T) {
	var calls []string
	redisProvider := &featureFlagProviderStub{
		name:  "redis",
		calls: &calls,
	}
	consulProvider := &featureFlagProviderStub{
		name:  "consul",
		calls: &calls,
		data:  featureFlagConfig(false),
	}
	provider := newFallbackFeatureFlagProvider(redisProvider, consulProvider)

	data, err := provider.GetFeatureFlags(context.Background())
	if err != nil {
		t.Fatalf("get feature flags failed: %v", err)
	}
	if string(data) != string(featureFlagConfig(false)) {
		t.Fatalf("expected Consul snapshot, got %q", data)
	}
	if len(calls) != 2 || calls[0] != "redis" || calls[1] != "consul" {
		t.Fatalf("expected Redis then Consul reads, got %v", calls)
	}
}

func TestFallbackFeatureFlagProviderUsesRedisWhenSnapshotExists(t *testing.T) {
	var calls []string
	redisProvider := &featureFlagProviderStub{
		name:  "redis",
		calls: &calls,
		data:  featureFlagConfig(true),
	}
	consulProvider := &featureFlagProviderStub{
		name:  "consul",
		calls: &calls,
		data:  featureFlagConfig(false),
	}
	provider := newFallbackFeatureFlagProvider(redisProvider, consulProvider)

	data, err := provider.GetFeatureFlags(context.Background())
	if err != nil {
		t.Fatalf("get feature flags failed: %v", err)
	}
	if string(data) != string(featureFlagConfig(true)) {
		t.Fatalf("expected Redis snapshot, got %q", data)
	}
	if len(calls) != 1 || calls[0] != "redis" {
		t.Fatalf("expected only Redis to be read, got %v", calls)
	}
}

func TestFallbackFeatureFlagProviderUsesConsulWhenRedisReadFails(t *testing.T) {
	redisProvider := &featureFlagProviderStub{err: errors.New("redis unavailable")}
	consulProvider := &featureFlagProviderStub{data: featureFlagConfig(false)}
	provider := newFallbackFeatureFlagProvider(redisProvider, consulProvider)

	data, err := provider.GetFeatureFlags(context.Background())
	if err != nil {
		t.Fatalf("get feature flags failed: %v", err)
	}
	if string(data) != string(featureFlagConfig(false)) {
		t.Fatalf("expected Consul snapshot, got %q", data)
	}
}

func TestFallbackFeatureFlagProviderWatchesRedisAndConsul(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redisWatch := make(chan any, 1)
	consulWatch := make(chan any, 1)
	provider := newFallbackFeatureFlagProvider(
		&featureFlagProviderStub{watch: redisWatch},
		&featureFlagProviderStub{watch: consulWatch},
	)

	merged, err := provider.WatchFeatureFlags(ctx)
	if err != nil {
		t.Fatalf("watch feature flags failed: %v", err)
	}

	redisWatch <- "redis change"
	select {
	case <-merged:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Redis feature flag notification")
	}

	consulWatch <- "consul change"
	select {
	case <-merged:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Consul feature flag notification")
	}

	close(redisWatch)
	close(consulWatch)
	select {
	case _, ok := <-merged:
		if ok {
			select {
			case _, ok = <-merged:
				if ok {
					t.Fatal("expected merged feature flag watcher to close")
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for merged feature flag watcher to close")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for merged feature flag watcher to close")
	}
}
