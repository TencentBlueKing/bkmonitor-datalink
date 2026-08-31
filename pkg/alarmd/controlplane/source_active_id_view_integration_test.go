// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package controlplane_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

func TestActiveIDViewAuditsAllCanonicalObjectsAndCompilesOnlySelected(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(documents[0]), 0).Err(); err != nil {
		t.Fatal(err)
	}
	canonical := newRedisStrategySource(t, client)
	view, err := controlplane.NewActiveIDStrategySourceView(canonical, []string{"1001"})
	if err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:g1-view", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controlplane.NewSourceReconciler(repository)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, view, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("first refresh=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, view, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("second refresh=(%#v, %v)", published, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, published.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QueryGroups) != 1 || len(snapshot.QueryGroups[0].Plans) != 1 ||
		snapshot.QueryGroups[0].Plans[0].Identity.StrategyID != "1001" {
		t.Fatalf("selected Snapshot = %#v", snapshot.QueryGroups)
	}
	audit, err := repository.LoadLatestAudit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundIncomplete := false
	for _, disposition := range audit.Dispositions {
		if disposition.SourceID == "1002" {
			if disposition.Disposition != controlplane.DispositionSourceIncomplete ||
				disposition.Reason != "SOURCE_OBJECT_INCOMPLETE" {
				t.Fatalf("unselected Source audit disposition = %#v", disposition)
			}
			foundIncomplete = true
		}
	}
	if !foundIncomplete {
		t.Fatalf("unselected canonical object absent from Source audit: %#v", audit.Dispositions)
	}
	incompleteObservation := audit.ObservationID

	// A readable but unsupported unselected object remains canonical Source
	// evidence but never enters normal compilation or changes the Snapshot.
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", `{
        "id":1002,"bk_biz_id":2,"bk_tenant_id":"tenant-a","space_uid":"space-a","items":[]
    }`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, view, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("unselected invalid pending=(%#v, %v)", result, err)
	}
	unchanged, err := reconciler.Refresh(ctx, view, planner)
	if err != nil || unchanged.Status != controlplane.SourceRefreshPublished ||
		unchanged.Publication != published.Publication {
		t.Fatalf("unselected invalid publish=(%#v, %v), want existing publication %#v", unchanged, err, published.Publication)
	}
	audit, err = repository.LoadLatestAudit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, disposition := range audit.Dispositions {
		if disposition.SourceID == "1002" {
			t.Fatalf("unselected compiler-invalid object gained selector disposition: %#v", disposition)
		}
	}
	if audit.ObservationID == incompleteObservation {
		t.Fatal("unselected canonical object change was absent from Source observation digest")
	}
}

func TestActiveIDViewSelectorChangeDoesNotCreateRemovalOrRetainUnselectedLastGood(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	canonical := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:g1-selector-change", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controlplane.NewSourceReconciler(repository)
	if err != nil {
		t.Fatal(err)
	}
	initialView, err := controlplane.NewActiveIDStrategySourceView(canonical, []string{"1001", "1002"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, initialView, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, initialView, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", initial, err)
	}

	narrowedView, err := controlplane.NewActiveIDStrategySourceView(canonical, []string{"1001"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, narrowedView, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("narrowed pending=(%#v, %v)", result, err)
	}
	narrowed, err := reconciler.Refresh(ctx, narrowedView, planner)
	if err != nil || narrowed.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("narrowed publish=(%#v, %v)", narrowed, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, narrowed.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QueryGroups) != 1 || len(snapshot.QueryGroups[0].Plans) != 1 ||
		snapshot.QueryGroups[0].Plans[0].Identity.StrategyID != "1001" {
		t.Fatalf("narrowed Snapshot retained unselected last-good: %#v", snapshot.QueryGroups)
	}
	audit, err := repository.LoadLatestAudit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, disposition := range audit.Dispositions {
		if disposition.SourceID == "1002" {
			t.Fatalf("selector change created unselected removal/audit mutation: %#v", disposition)
		}
	}
}

func TestActiveIDViewRejectsSelectedObjectLossInsteadOfRetainingLastGood(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := realThresholdDocuments(t)[0]
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	canonical := newRedisStrategySource(t, client)
	view, err := controlplane.NewActiveIDStrategySourceView(canonical, []string{"1001"})
	if err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:g1-selected-loss", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controlplane.NewSourceReconciler(repository)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, view, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, view, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", published, err)
	}
	if err := client.Del(ctx, "bkmonitor.cache.strategy_1001").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Refresh(ctx, view, planner); !errors.Is(err, controlplane.ErrSelectedStrategyUnavailable) {
		t.Fatalf("selected object loss error = %v, want readiness failure", err)
	}
	latest, err := repository.LoadLatestPublication(ctx)
	if err != nil || latest != published.Publication {
		t.Fatalf("latest publication=(%#v, %v), want unchanged %#v", latest, err, published.Publication)
	}
}

func TestActiveIDViewRejectsSelectedCompilerFailureInsteadOfRetainingLastGood(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := realThresholdDocuments(t)[0]
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	canonical := newRedisStrategySource(t, client)
	view, err := controlplane.NewActiveIDStrategySourceView(canonical, []string{"1001"})
	if err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:g1-selected-invalid", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controlplane.NewSourceReconciler(repository)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, view, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, view, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", published, err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", `{
        "id":1001,"bk_biz_id":2,"bk_tenant_id":"tenant-a","space_uid":"space-a","items":[]
    }`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Refresh(ctx, view, planner); !errors.Is(err, controlplane.ErrSelectedStrategyUnavailable) {
		t.Fatalf("selected compiler failure error = %v, want strict G1 rejection", err)
	}
	latest, err := repository.LoadLatestPublication(ctx)
	if err != nil || latest != published.Publication {
		t.Fatalf("latest publication=(%#v, %v), want unchanged %#v", latest, err, published.Publication)
	}
}

func TestActiveIDViewRejectsOneInvalidSelectedStrategyDespiteHealthySibling(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := realThresholdDocuments(t)[0]
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", `{
        "id":1002,"bk_biz_id":2,"bk_tenant_id":"tenant-a","space_uid":"space-a","items":[]
    }`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	canonical := newRedisStrategySource(t, client)
	view, err := controlplane.NewActiveIDStrategySourceView(canonical, []string{"1001", "1002"})
	if err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:g1-selected-sibling", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controlplane.NewSourceReconciler(repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Refresh(ctx, view, planner); !errors.Is(err, controlplane.ErrSelectedStrategyUnavailable) {
		t.Fatalf("selected sibling compiler failure error = %v, want strict G1 rejection", err)
	}
}

func TestActiveIDViewRejectsMultipleQueryGroupsBeforePublication(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	documents[1] = []byte(strings.Replace(string(documents[1]), "shared-query-md5", "independent-query-md5", 1))
	documents[1] = []byte(strings.Replace(string(documents[1]), "system.cpu", "system.mem", 1))
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	canonical := newRedisStrategySource(t, client)
	view, err := controlplane.NewActiveIDStrategySourceView(canonical, []string{"1001", "1002"})
	if err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:g1-multiple-groups", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controlplane.NewSourceReconciler(repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Refresh(ctx, view, planner); !errors.Is(err, controlplane.ErrSelectedStrategyUnavailable) {
		t.Fatalf("multiple Query Group error = %v, want pre-publication G1 rejection", err)
	}
	if _, err := repository.LoadLatestPublication(ctx); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("invalid multi-group candidate was published: %v", err)
	}
}
