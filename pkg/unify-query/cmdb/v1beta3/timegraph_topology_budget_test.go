// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestSharedTopologyResourcesAndBackendLimit(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		backendLimit bool
	}{
		{name: "模型返回完整拓扑"},
		{name: "聚合后端不能吞掉容量拒绝", backendLimit: true, reason: "max_response_bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			stamp := int64(1700000000000)
			relationStamps := []int64{stamp}
			request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: stamp / 1000, MaxHops: 1}
			model := sharedTopologyQueryModel(map[string]pl.Matrix{"source_middle_flow": contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, relationStamps...), "middle_target_flow": {}})
			if test.backendLimit {
				model.timeGraphVMQueryWithPartial = func(ctx context.Context, _ *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
					require.Equal(t, int64(16*1024*1024), metadata.BackendResponseLimit(ctx))
					metadata.MarkBackendResponseLimitExceeded(ctx)
					return nil, true, nil
				}
			}
			result, err := model.QuerySharedTopology(ctx, request)
			if test.backendLimit {
				var limit *ResultLimitError
				require.ErrorAs(t, err, &limit)
				require.Equal(t, test.reason, limit.Reason)
				require.Empty(t, result.Snapshots)
			} else {
				require.NoError(t, err)
				require.Len(t, result.Snapshots, len(relationStamps))
				for _, frame := range result.Snapshots {
					require.Len(t, frame.Nodes, 2)
					require.Len(t, frame.Edges, 1)
				}
			}
		})
	}
}

func TestSharedTopologyConcurrentLoadingAndRecovery(t *testing.T) {
	for _, outcome := range []string{"success", "canceled", "backend failure"} {
		t.Run(outcome, func(t *testing.T) {
			initTimeGraphQueryTestEnvironment()

			const concurrency = 8
			entered := make(chan struct{}, concurrency)
			resume := make(chan struct{})
			done := make(chan error, concurrency)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			var workers sync.WaitGroup
			t.Cleanup(func() { cancel(); workers.Wait() })
			backendErr := errors.New("backend unavailable")
			model := sharedTopologyQueryModel(nil)
			model.timeGraphVMQuery = func(ctx context.Context, query *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
				if query.QueryList[0].FieldName == "source_middle_flow" {
					entered <- struct{}{}
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-resume:
					}
					if outcome == "backend failure" {
						return nil, backendErr
					}
				}
				return nil, nil
			}
			request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: 1700000000, MaxHops: 1}
			for i := 0; i < concurrency; i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					_, err := model.QuerySharedTopology(metadata.InitHashID(ctx), request)
					done <- err
				}()
			}
			// Each admitted request may perform one physical fetch at a time.
			for i := 0; i < concurrency; i++ {
				select {
				case <-entered:
				case err := <-done:
					t.Fatalf("query completed before all admitted requests reached the backend: %v", err)
				case <-ctx.Done():
					t.Fatal("all admitted requests did not reach the backend")
				}
			}
			require.Eventually(t, func() bool {
				return float64(concurrency) == readTimeGraphMetric(t, "cmdb_topology_admission_active", nil, "gauge")
			}, time.Second, 10*time.Millisecond)
			if outcome == "canceled" {
				cancel()
			} else {
				close(resume)
			}
			for i := 0; i < concurrency; i++ {
				err := <-done
				switch outcome {
				case "success":
					require.NoError(t, err)
				case "canceled":
					require.ErrorIs(t, err, context.Canceled)
				case "backend failure":
					require.ErrorIs(t, err, backendErr)
				}
			}
			require.Empty(t, entered)
			require.Zero(t, readTimeGraphMetric(t, "cmdb_topology_admission_active", nil, "gauge"))
		})
	}
}

func TestSharedTopologyActivityNestedAndCanceled(t *testing.T) {
	admitted, release, err := AcquireSharedTopology(context.Background())
	require.NoError(t, err)
	t.Cleanup(release)
	_, nestedRelease, err := AcquireSharedTopology(admitted)
	require.NoError(t, err)
	require.Equal(t, 1.0, readTimeGraphMetric(t, "cmdb_topology_admission_active", nil, "gauge"))
	nestedRelease()
	require.Equal(t, 1.0, readTimeGraphMetric(t, "cmdb_topology_admission_active", nil, "gauge"))
	release()
	release()
	topologyAdmission.Lock()
	active := topologyAdmission.active
	topologyAdmission.Unlock()
	require.Zero(t, active)
	require.Zero(t, readTimeGraphMetric(t, "cmdb_topology_admission_active", nil, "gauge"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = AcquireSharedTopology(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, readTimeGraphMetric(t, "cmdb_topology_admission_active", nil, "gauge"))
}

func TestTopologyHardSixtyPoints(t *testing.T) {
	old := MaxSharedTopologyPoints
	t.Cleanup(func() { MaxSharedTopologyPoints = old })
	for _, configured := range []int{0, -1, 60, 61, 64, 1000} {
		MaxSharedTopologyPoints = configured
		_, err := NewTopologyGrid(sharedTopologyTestTimes(60))
		require.NoError(t, err)
		_, err = NewTopologyGrid(sharedTopologyTestTimes(61))
		require.Error(t, err)
	}
}
