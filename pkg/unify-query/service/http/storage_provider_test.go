// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License.

package http

import (
	"context"
	"testing"

	"github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

func TestConsulStorageInfoProviderPassesContext(t *testing.T) {
	original := consul.GetDataWithPrefixContext
	t.Cleanup(func() {
		consul.GetDataWithPrefixContext = original
	})

	ctx := context.WithValue(context.Background(), "request", "storage-print")
	calls := 0
	consul.GetDataWithPrefixContext = func(got context.Context, _ string) (api.KVPairs, error) {
		if got != ctx {
			t.Fatalf("expected provider to pass request context")
		}
		calls++
		return api.KVPairs{}, nil
	}

	provider := &consulStorageInfoProvider{}
	if _, err := provider.GetStorageInfo(ctx); err != nil {
		t.Fatalf("get storage info failed: %v", err)
	}
	if _, err := provider.GetTsDBStorageInfo(ctx); err != nil {
		t.Fatalf("get tsdb storage info failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected two context-aware reads, got %d", calls)
	}
}
