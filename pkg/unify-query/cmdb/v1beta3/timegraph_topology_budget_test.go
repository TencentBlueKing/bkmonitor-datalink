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

func TestSharedTopologyResourcesAndDeprecatedCountLimits(t *testing.T) {
	for _, test := range []struct {
		name, reason            string
		points, elements, bytes int
		backendLimit            bool
	}{
		{name: "累计 Matrix 点数", points: 1},
		{name: "快照节点与边总数", elements: 1},
		{name: "物化前字节预算", bytes: 16},
		{name: "聚合后端不能吞掉容量拒绝", backendLimit: true, reason: "max_response_bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			oldPoints, oldElements, oldBytes := MaxSharedTopologyMatrixPoints, MaxSharedTopologyOutputElements, MaxSharedTopologyOutputBytes
			t.Cleanup(func() {
				MaxSharedTopologyMatrixPoints = oldPoints
				MaxSharedTopologyOutputElements = oldElements
				MaxSharedTopologyOutputBytes = oldBytes
			})
			if test.points > 0 {
				MaxSharedTopologyMatrixPoints = test.points
			}
			if test.elements > 0 {
				MaxSharedTopologyOutputElements = test.elements
			}
			if test.bytes > 0 {
				MaxSharedTopologyOutputBytes = test.bytes
			}
			stamp := int64(1700000000000)
			relationStamps := []int64{stamp}
			request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: stamp / 1000, MaxHops: 1}
			if test.points > 0 {
				relationStamps = append(relationStamps, stamp+1000)
				request.Timestamp = 0
				request.StartTime, request.EndTime, request.Step = stamp/1000, stamp/1000+1, "1s"
			}
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
	oldHeadroom := topologyMemoryHeadroom
	topologyMemoryHeadroom = func() (int64, bool) { return 16 * 1024 * 1024 * 1024, true }
	t.Cleanup(func() { topologyMemoryHeadroom = oldHeadroom })
	for _, mode := range []struct {
		name string
		yolo bool
	}{
		{name: "normal"},
		{name: "yolo", yolo: true},
	} {
		for _, outcome := range []string{"success", "canceled", "backend failure"} {
			t.Run(mode.name+"/"+outcome, func(t *testing.T) {
				initTimeGraphQueryTestEnvironment()
				oldYolo := yoloMode
				yoloMode = mode.yolo
				t.Cleanup(func() { yoloMode = oldYolo })

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
				// Admission still accepts eight requests, while the process lets
				// only four physical backend calls run at the same time.
				for i := 0; i < cap(topologyBackendSlots); i++ {
					select {
					case <-entered:
					case err := <-done:
						t.Fatalf("query completed before four requests reached the backend: %v", err)
					case <-ctx.Done():
						t.Fatal("four concurrent requests did not reach the backend")
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
				if outcome != "canceled" {
					require.Len(t, entered, concurrency-cap(topologyBackendSlots))
				}
				require.Zero(t, readTimeGraphMetric(t, "cmdb_topology_admission_active", nil, "gauge"))
			})
		}
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
		_, err := NewTopologyGrid(sharedGraphTestTimes(60))
		require.NoError(t, err)
		_, err = NewTopologyGrid(sharedGraphTestTimes(61))
		require.Error(t, err)
	}
}
