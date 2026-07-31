// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package featureFlag

import (
	"context"
	"testing"

	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
)

type featureFlagProviderStub struct {
	data []byte
}

func (p *featureFlagProviderStub) GetFeatureFlags(context.Context) ([]byte, error) {
	return p.data, nil
}

func (p *featureFlagProviderStub) WatchFeatureFlags(context.Context) (<-chan any, error) {
	return make(chan any), nil
}

func (p *featureFlagProviderStub) GetFeatureFlagsPath() string {
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
