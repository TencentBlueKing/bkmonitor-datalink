// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// The confirmation still has to do its job: a catalog seen only once is not
// published, so a transient source state cannot reach the execution path.
func TestACatalogSeenOnlyOnceIsNotPublished(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(documents[0]), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:confirmation-transient", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	if first, err := reconciler.Refresh(ctx, source, planner); err != nil ||
		first.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("first refresh = (%+v, %v)", first, err)
	}
	// A different strategy appears for exactly one refresh.
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001, 1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", string(documents[1]), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if transient, err := reconciler.Refresh(ctx, source, planner); err != nil ||
		transient.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("a catalog seen once = (%+v, %v), want it withheld", transient, err)
	}
}
