// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

func TestTopologyGraphResidencyCountsUniqueStorage(t *testing.T) {
	topologyAdmissionTestEnvironment(t, 2)
	MaxSharedTopologyReservedBytes = 64 << 20
	topologyMemoryHeadroom = func() (int64, bool) { return 1 << 30, true }
	var one graphResidencyStats
	for _, points := range []int{1, 60} {
		ctx, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		t.Cleanup(release)
		times := sharedGraphTestTimes(points)
		grid, err := NewTopologyGrid(times)
		require.NoError(t, err)
		g := newSharedTimeGraph(sharedGraphTestConfig(), grid)
		require.NoError(t, g.enableResidency(ctx))
		g.maxNodes, g.maxEdges = 1, 1
		rel := sharedGraphTestRelation("calls")
		require.NoError(t, g.AddTimeRelationWithRelation(ctx, rel, cmdb.Matcher{"from_id": "a", "to_id": "b"}, times...))
		first := graphResidencyState(g)
		require.Positive(t, first.ReservedBytes)
		require.LessOrEqual(t, first.MaterializedLowerBoundBytes, first.EstimatedBytes)
		require.NoError(t, g.AddTimeRelationWithRelation(ctx, rel, cmdb.Matcher{"from_id": "a", "to_id": "b"}, times...))
		require.Equal(t, first, graphResidencyState(g), "duplicate observations do not allocate another edge")
		if points == 1 {
			one = first
		} else {
			require.Equal(t, one.EstimatedBytes, first.EstimatedBytes)
		}
		other := rel
		other.MetricName = "other_flow"
		require.NoError(t, g.AddTimeRelationWithRelation(ctx, other, cmdb.Matcher{"from_id": "a", "to_id": "b"}, times...))
		require.Greater(t, graphResidencyState(g).EstimatedBytes, first.EstimatedBytes)
		frames, err := g.FindSharedTopology(ctx, grid, SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 1})
		require.NoError(t, err)
		require.Len(t, frames, points)
		for _, frame := range frames {
			require.Len(t, frame.Nodes, 2)
			require.Len(t, frame.Edges, 2)
		}
		g.Clean(ctx)
		require.Zero(t, graphResidencyState(g).ReservedBytes)
		release()
		require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyAdmissionTestState())
	}
}

func TestTopologyYOLOSkipsResidencyAndOutputScan(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	ctx, release, err := AcquireSharedTopology(context.Background())
	require.NoError(t, err)
	defer release()
	grid, err := NewTopologyGrid(sharedGraphTestTimes(60))
	require.NoError(t, err)
	g := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	defer g.Clean(ctx)
	require.NoError(t, g.enableResidency(ctx))
	require.Nil(t, g.residency)
	require.Nil(t, g.matrixResidency)
	require.NoError(t, commitTopologyOutput(ctx, func() int64 { t.Fatal("YOLO must not walk DTO ownership"); return 0 }))
}

func TestTopologyInitialReservationFailureClosesStorageSpan(t *testing.T) {
	parent := initTimeGraphQueryTestEnvironment()
	topologyAdmissionTestEnvironment(t, 1)
	observer := newBuildCleanupObserver(t)
	ctx, release, err := AcquireSharedTopology(parent)
	require.NoError(t, err)
	defer release()
	start := time.Unix(1700000000, 0)
	grid, err := NewTopologyGrid([]int64{start.UnixMilli()})
	require.NoError(t, err)
	graph, err := sharedTopologyQueryModel(nil).buildTimeGraph(ctx, "space", start, start, time.Second, "source", nil, nil, nil, nil, "1m", nil, &grid)
	require.ErrorContains(t, err, "max_topology_reserved_bytes")
	require.Nil(t, graph)
	release()
	require.Equal(t, 1, observer.ended["timegraph-create-storage"])
	require.Equal(t, 1, observer.ended["timegraph-clean"])
	require.Equal(t, observer.started, observer.ended)
	require.Zero(t, topologyAdmissionTestState()["reserved"])
}
