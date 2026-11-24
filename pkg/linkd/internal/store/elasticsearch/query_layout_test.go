// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package elasticsearchstore

import (
	"testing"
	"time"
)

func TestQueryLayoutMatchesManagedAliasesAndBucketMetadata(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	router, err := newBucketRouter("discovery", BucketConfig{EventBucketDays: 3, AlertHistoryBucketDays: 5, AlertLogBucketDays: 9,
		ActiveAlertRefreshInterval: 5 * time.Second}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	transport := newManagerTransport()
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newManager(repository, router, ManagerConfig{PrecreatePastBuckets: 1, PrecreateFutureBuckets: 1, MaxBucketsPerEntity: 512}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileSchemaAndActive(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileBuckets(t.Context()); err != nil {
		t.Fatal(err)
	}
	layout := router.QueryLayout()
	for _, targets := range [][]string{layout.EventTargets, layout.AlertTargets, layout.AlertLogTargets, layout.ActiveAlertTargets, layout.AlertHistoryTargets} {
		if len(targets) != 1 || len(transport.aliases[targets[0]]) == 0 {
			t.Fatal("advertised query alias was not created by the actual manager")
		}
	}
	// 告警总 alias 必须同时覆盖活动和历史；发现信息不能丢掉归档后的告警。
	for _, subset := range [][]string{layout.ActiveAlertTargets, layout.AlertHistoryTargets} {
		for index := range transport.aliases[subset[0]] {
			if _, exists := transport.aliases[layout.AlertTargets[0]][index]; !exists {
				t.Fatal("advertised alert alias misses active or history member")
			}
		}
	}
	for _, family := range []struct {
		targets []string
		days    int
	}{
		{layout.EventTargets, layout.TimePartition.EventBucketDays},
		{layout.AlertHistoryTargets, layout.TimePartition.AlertHistoryBucketDays},
		{layout.AlertLogTargets, layout.TimePartition.AlertLogBucketDays},
	} {
		for index := range transport.aliases[family.targets[0]] {
			if transport.indices[index].BucketDays != family.days {
				t.Fatal("advertised bucket days disagree with actual managed index metadata")
			}
		}
	}
}
