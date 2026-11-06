// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
)

func TestTopologySerialMatrixResidencyTracksReceiveReplaceAndFailure(t *testing.T) {
	topologyAdmissionTestEnvironment(t, 1)
	ctx, release, err := AcquireSharedTopology(metadata.WithBackendResponseLimit(context.Background(), 64))
	require.NoError(t, err)
	defer release()
	graph := &TimeGraph{matrixResidency: &matrixResidency{ctx: ctx}}
	defer graph.matrixResidency.clear()
	loader := &timeGraphMatrixLoader{graph: graph, topology: true}
	matrix := contractMatrix(map[string]string{"id": "first"}, 1700000000000)
	assertReceiving := func() {
		t.Helper()
		state := matrixResidencyState(graph)
		require.Equal(t, int64(512), state.ReceivingBytes)
		require.Zero(t, state.ResidentBytes)
		require.Equal(t, int64(512), topologyAdmissionTestState()["reserved"])
		require.Zero(t, topologyAdmissionTestState()["materialized"], "borrowed Matrix storage gets no materialized credit")
	}
	got, partial, err := loader.fetchResident(ctx, func() (pl.Matrix, bool, error) {
		assertReceiving()
		return matrix, true, nil
	})
	require.NoError(t, err)
	require.True(t, partial)
	require.Equal(t, matrix, got)
	state := matrixResidencyState(graph)
	require.Zero(t, state.ReceivingBytes)
	require.Equal(t, matrixResidentEstimate(matrix), state.ResidentBytes)
	require.Equal(t, state.ResidentBytes, topologyAdmissionTestState()["reserved"])
	failure := errors.New("next physical fetch failed")
	got, _, err = loader.fetchResident(ctx, func() (pl.Matrix, bool, error) {
		assertReceiving()
		return nil, false, failure
	})
	require.ErrorIs(t, err, failure)
	require.Nil(t, got)
	require.Zero(t, matrixResidencyState(graph).ReceivingBytes)
	require.Zero(t, matrixResidencyState(graph).ResidentBytes)
	require.Zero(t, topologyAdmissionTestState()["reserved"])

	_, _, err = loader.fetchResident(metadata.WithBackendResponseLimit(ctx, 1024), func() (pl.Matrix, bool, error) {
		t.Fatal("receive reservation must be accepted before calling the backend")
		return nil, false, nil
	})
	require.ErrorContains(t, err, "max_topology_reserved_bytes")
	require.Zero(t, topologyAdmissionTestState()["reserved"])
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = loader.fetchResident(canceled, func() (pl.Matrix, bool, error) {
		t.Fatal("a canceled request must stop before the backend call")
		return nil, false, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, topologyAdmissionTestState()["reserved"])
}

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
