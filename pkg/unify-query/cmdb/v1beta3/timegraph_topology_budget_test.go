// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestSharedTopologyResourceRejections(t *testing.T) {
	for _, test := range []struct {
		name, reason            string
		points, elements, bytes int
		backendLimit            bool
	}{
		{name: "累计 Matrix 点数", points: 1, reason: "max_topology_matrix_points"},
		{name: "快照节点与边总数", elements: 1, reason: "max_topology_output_elements"},
		{name: "物化前字节预算", bytes: 16, reason: "max_topology_output_bytes"},
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
			model := sharedTopologyQueryModel(map[string]pl.Matrix{"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, stamp), "source_middle_flow": contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, stamp), "middle_target_flow": {}})
			if test.backendLimit {
				model.timeGraphVMQueryWithPartial = func(ctx context.Context, _ *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, bool, error) {
					require.Equal(t, int64(16*1024*1024), metadata.BackendResponseLimit(ctx))
					metadata.MarkBackendResponseLimitExceeded(ctx)
					return nil, true, nil
				}
			}
			result, err := model.QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: stamp / 1000, MaxHops: 1})
			var limit *ResultLimitError
			require.ErrorAs(t, err, &limit)
			require.Equal(t, test.reason, limit.Reason)
			require.Empty(t, result.Snapshots)
		})
	}
}

func TestSharedTopologyConcurrentAdmissionAndRecovery(t *testing.T) {
	initTimeGraphQueryTestEnvironment()
	old := MaxSharedTopologyConcurrency
	MaxSharedTopologyConcurrency = 2
	t.Cleanup(func() { MaxSharedTopologyConcurrency = old })
	entered := make(chan struct{}, 2)
	model := sharedTopologyQueryModel(nil)
	model.timeGraphVMQuery = func(ctx context.Context, _ *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: 1700000000, MaxHops: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := model.QuerySharedTopology(metadata.InitHashID(ctx), request); done <- err }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("backend was not reached")
		}
	}
	_, err := model.QuerySharedTopology(metadata.InitHashID(context.Background()), request)
	var limit *ResultLimitError
	require.ErrorAs(t, err, &limit)
	require.Equal(t, "max_topology_concurrency", limit.Reason)
	cancel()
	for i := 0; i < 2; i++ {
		require.ErrorIs(t, <-done, context.Canceled)
	}
	admitted, release, err := AcquireSharedTopology(context.Background())
	require.NoError(t, err)
	_, nestedRelease, err := AcquireSharedTopology(admitted)
	require.NoError(t, err)
	nestedRelease()
	release()
	release()
	topologyAdmission.Lock()
	active := topologyAdmission.active
	topologyAdmission.Unlock()
	require.Zero(t, active)
}

func TestTopologyJSONByteBound(t *testing.T) {
	for _, text := range []string{"", "普通属性", "\x00\n\"\\<>&\u2028", string([]byte{0xff, 0xfe}), strings.Repeat("x", 1000)} {
		info := cmdb.Matcher{text: text, "id": "a"}
		node := cmdb.SharedTopologyNode{ID: ^uint64(0), ResourceType: cmdb.Resource(text), Dimensions: info}
		encoded, err := json.Marshal(node)
		require.NoError(t, err)
		require.GreaterOrEqual(t, topologyNodeByteBound(node.ResourceType, info), int64(len(encoded)+1))
		key := timeGraphTopologyEdgeKey{relation: timeGraphEdgeRelation{relationType: text, metricName: text, category: text, direction: text}}
		edge := cmdb.SharedTopologyEdge{Source: ^uint64(0), Target: ^uint64(0), RelationType: text, MetricName: text, Category: text, Direction: text}
		encoded, err = json.Marshal(edge)
		require.NoError(t, err)
		require.GreaterOrEqual(t, topologyEdgeByteBound(key), int64(len(encoded)+1))
	}
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
