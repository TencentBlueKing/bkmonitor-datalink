// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package featureFlag

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	redisStorage "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	redisService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/redis"
)

func TestNewFeatureFlagProviderFollowsConsulAddress(t *testing.T) {
	ctx := context.Background()
	previousBasePath := redisService.KVBasePath
	t.Cleanup(func() { redisService.KVBasePath = previousBasePath })
	mr := miniredis.RunT(t)
	if err := redisStorage.SetInstance(ctx, "test", "test", &goRedis.UniversalOptions{Addrs: []string{mr.Addr()}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(redisStorage.Close)
	t.Cleanup(consul.Close)
	redisService.KVBasePath = "test"

	for _, tc := range []struct {
		address string
		reason  string
	}{
		{"", consul.ReasonAddressNotConfigured},
		{":8500", consul.ReasonAddressMissingHost},
		{"http://192.0.2.1:8500", ""},
	} {
		_ = consul.SetInstance(ctx, "", tc.address, "", "", "")
		provider, err := newFeatureFlagProvider(ctx)
		if err != nil {
			t.Fatalf("address %q: %v", tc.address, err)
		}
		p, ok := provider.(*fallbackFeatureFlagProvider)
		if !ok || p.primary == nil {
			t.Fatalf("address %q: Redis must stay the primary source, got %T", tc.address, provider)
		}
		if tc.reason == "" && (p.fallback == nil || p.noFallbackReason != "") {
			t.Errorf("address %q: a usable Consul address keeps the migration source", tc.address)
		}
		if tc.reason != "" && (p.fallback != nil || p.noFallbackReason != tc.reason) {
			t.Errorf("address %q: want no Consul source and reason %s, got fallback=%v reason=%q", tc.address, tc.reason, p.fallback != nil, p.noFallbackReason)
		}
	}

	// Without Redis and without Consul there is no source at all.
	redisService.KVBasePath = ""
	_ = consul.SetInstance(ctx, "", "", "", "", "")
	var addressErr *consul.AddressError
	if _, err := newFeatureFlagProvider(ctx); !errors.As(err, &addressErr) || addressErr.Reason != consul.ReasonAddressNotConfigured {
		t.Fatalf("missing Redis prefix and Consul address must be reported by name, got %v", err)
	}
	_ = consul.SetInstance(ctx, "", "http://192.0.2.1:8500", "", "", "")
	if provider, err := newFeatureFlagProvider(ctx); err != nil {
		t.Fatalf("missing Redis prefix keeps the direct Consul provider: %v", err)
	} else if _, ok := provider.(*consul.FeatureFlagProvider); !ok {
		t.Fatalf("want the direct Consul provider, got %T", provider)
	}
}

func TestRedisOnlyFeatureFlagsUseDefaultsAndReportOnce(t *testing.T) {
	builtInDefaultsReported.Store(false)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.InitTestLogger()
	t.Cleanup(func() {
		builtInDefaultsReported.Store(false)
		log.SetOutput(nil)
		log.InitTestLogger()
	})
	if err := inner.ReloadFeatureFlags([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	client := goRedis.NewClient(&goRedis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	primary := redisStorage.NewFeatureFlagClient(client, "test")
	key := primary.GetFeatureFlagsPath()
	service := &Service{provider: newRedisFeatureFlagProvider(primary, consul.ReasonAddressNotConfigured)}
	defer service.Close()
	ctx := metadata.InitHashID(context.Background())
	user := inner.FFUser("test-user", nil)

	for i := 0; i < 3; i++ {
		if err := service.reconcileFeatureFlags(ctx); err != nil {
			t.Fatalf("reconcile %d: a missing key without Consul is not an error: %v", i, err)
		}
	}
	if mr.Exists(key) || !service.clientInitialized || inner.Print() != "{}" {
		t.Fatal("defaults must be applied in memory without writing Redis")
	}

	if err := mr.Set(key, string(featureFlagConfig(false))); err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileFeatureFlags(ctx); err != nil || inner.BoolVariation(ctx, user, "test-flag", true) {
		t.Fatalf("written rules must apply on the next reconcile: %v", err)
	}
	mr.Del(key)
	if err := service.reconcileFeatureFlags(ctx); err != nil || !inner.BoolVariation(ctx, user, "test-flag", true) || inner.Print() != "{}" {
		t.Fatalf("a deleted key must restore the defaults: %v", err)
	}
	if mr.Exists(key) {
		t.Fatal("defaults must not be written to Redis")
	}

	output := logs.String()
	if count := strings.Count(output, "using built-in defaults"); count != 1 {
		t.Errorf("defaults must be reported once per process, got %d:\n%s", count, output)
	}
	if !strings.Contains(output, "reason="+consul.ReasonAddressNotConfigured) {
		t.Errorf("the report must name the reason:\n%s", output)
	}
	if strings.Contains(output, "\terror\t") {
		t.Errorf("no error may be logged:\n%s", output)
	}
}
