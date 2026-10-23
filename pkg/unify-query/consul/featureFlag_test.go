// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package consul

import (
	"context"
	"testing"
)

func TestFeatureFlagProviderPassesContextToConsulRead(t *testing.T) {
	original := GetKVDataContext
	defer func() {
		GetKVDataContext = original
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	GetKVDataContext = func(gotCtx context.Context, path string) ([]byte, error) {
		if gotCtx != ctx {
			t.Fatal("provider did not pass the caller context")
		}
		if gotCtx.Err() != context.Canceled {
			t.Fatalf("expected canceled context, got %v", gotCtx.Err())
		}
		if path != GetFeatureFlagsPath() {
			t.Fatalf("unexpected feature flag path: %s", path)
		}
		return []byte("{}"), nil
	}

	data, err := NewFeatureFlagProvider().GetFeatureFlags(ctx)
	if err != nil {
		t.Fatalf("get feature flags failed: %v", err)
	}
	if string(data) != "{}" {
		t.Fatalf("unexpected feature flags: %s", data)
	}
}

func TestFeatureFlagProviderUsesEmptySnapshotWhenKeyIsMissing(t *testing.T) {
	original := GetKVDataContext
	defer func() {
		GetKVDataContext = original
	}()
	GetKVDataContext = func(context.Context, string) ([]byte, error) {
		return nil, nil
	}

	data, err := NewFeatureFlagProvider().GetFeatureFlags(context.Background())
	if err != nil {
		t.Fatalf("get feature flags failed: %v", err)
	}
	if string(data) != "{}" {
		t.Fatalf("expected missing feature flags to become an empty snapshot, got %q", data)
	}
}
