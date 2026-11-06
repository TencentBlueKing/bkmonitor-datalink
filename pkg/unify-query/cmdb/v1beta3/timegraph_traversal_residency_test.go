// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"testing"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/stretchr/testify/require"
)

func traversalTestGraph(t *testing.T) (*TimeGraph, TopologyGrid, SharedTopologyQuery) {
	t.Helper()
	grid, err := NewTopologyGrid(sharedGraphTestTimes(60))
	require.NoError(t, err)
	graph := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	for _, edge := range []cmdb.Matcher{{"from_id": "a", "to_id": "b"}, {"from_id": "b", "to_id": "c"}, {"from_id": "a", "to_id": "c"}} {
		require.NoError(t, graph.AddTimeRelationWithRelation(context.Background(), sharedGraphTestRelation("calls"), edge, grid.timestamps...))
	}
	return graph, grid, SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 2}
}

func TestTopologyTraversalReservation(t *testing.T) {
	t.Run("same_answer_nested_lifecycle", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		MaxSharedTopologyReservedBytes = 64 << 20
		topologyMemoryHeadroom = func() (int64, bool) { return 0, false }
		graph, grid, query := traversalTestGraph(t)
		defer graph.Clean(context.Background())
		// Baseline has no lease; this is a deterministic mechanism graph, not
		// production capacity or the separate retained real-fixture replay.
		want, err := graph.FindCompactTopology(context.Background(), grid, query, nil)
		require.NoError(t, err)
		ctx, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		require.NoError(t, ReserveTopologyStage(ctx, "graph", 1024))
		child, done, err := graph.withTraversalReservation(ctx)
		require.NoError(t, err)
		before := topologyAdmissionTestState()["reserved"]
		nested, nestedDone, err := graph.withTraversalReservation(child)
		require.NoError(t, err)
		nestedDone()
		require.Equal(t, before, topologyAdmissionTestState()["reserved"])
		got, err := graph.FindCompactTopology(nested, grid, query, nil)
		require.NoError(t, err)
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)
		require.JSONEq(t, string(wantJSON), string(gotJSON))
		lease := ctx.Value(topologyAdmissionKey{}).(*topologyLease)
		require.Positive(t, lease.stages["traversal"])
		done()
		require.Zero(t, lease.stages["traversal"])
		require.EqualValues(t, 1024, lease.stages["graph"])
		require.Positive(t, lease.stages["output"])
		release()
		require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyAdmissionTestState())
	})
	t.Run("reject_before_state_or_output", func(t *testing.T) {
		for _, format := range []string{"compact", "snapshots"} {
			t.Run(format, func(t *testing.T) {
				topologyAdmissionTestEnvironment(t, 1)
				topologyMemoryHeadroom = func() (int64, bool) { return 0, false }
				graph, grid, query := traversalTestGraph(t)
				defer graph.Clean(context.Background())
				want, err := traversalReservationBytes(len(graph.shared.nodeBits), len(graph.shared.relationNodeBits), graph.shared.edgeCount(), 60)
				require.NoError(t, err)
				MaxSharedTopologyReservedBytes = int(want - 1)
				ctx, release, err := AcquireSharedTopology(context.Background())
				require.NoError(t, err)
				defer release()
				if format == "compact" {
					value, e := graph.FindCompactTopology(ctx, grid, query, nil)
					require.Nil(t, value)
					err = e
				} else {
					value, e := graph.findSharedTopologyDTO(ctx, grid, query)
					require.Nil(t, value)
					err = e
				}
				var limit *ResultLimitError
				require.ErrorAs(t, err, &limit)
				require.Equal(t, "max_topology_reserved_bytes", limit.Reason)
				require.EqualValues(t, want, limit.Count)
				require.Zero(t, topologyAdmissionTestState()["reserved"])
				require.Empty(t, ctx.Value(topologyAdmissionKey{}).(*topologyLease).stages)
				require.Equal(t, 3, graph.shared.edgeCount())
			})
		}
	})
	t.Run("error_and_cancel_clear_scope", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		MaxSharedTopologyReservedBytes = 64 << 20
		topologyMemoryHeadroom = func() (int64, bool) { return 0, false }
		graph, grid, query := traversalTestGraph(t)
		defer graph.Clean(context.Background())
		parent, cancel := context.WithCancel(context.Background())
		ctx, release, err := AcquireSharedTopology(parent)
		require.NoError(t, err)
		defer release()
		badGrid, err := NewTopologyGrid(grid.timestamps[:59])
		require.NoError(t, err)
		_, err = graph.FindCompactTopology(ctx, badGrid, query, nil)
		require.ErrorContains(t, err, "grid does not match")
		require.Zero(t, topologyAdmissionTestState()["reserved"])
		// Cancellation after acquiring the stage must still release it.
		_, done, err := graph.withTraversalReservation(ctx)
		require.NoError(t, err)
		require.Positive(t, topologyAdmissionTestState()["reserved"])
		cancel()
		done()
		require.Zero(t, topologyAdmissionTestState()["reserved"])
	})
	t.Run("public_empty_frames_keep_nil_contract", func(t *testing.T) {
		topologyAdmissionTestEnvironment(t, 1)
		MaxSharedTopologyReservedBytes = 64 << 20
		topologyMemoryHeadroom = func() (int64, bool) { return 0, false }
		graph, grid, query := traversalTestGraph(t)
		defer graph.Clean(context.Background())
		query.SourceMatcher = cmdb.Matcher{"id": "missing"}
		ctx, release, err := AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		frames, err := graph.FindSharedTopology(ctx, grid, query)
		require.NoError(t, err)
		require.Len(t, frames, 60)
		for _, frame := range frames {
			require.Nil(t, frame.Nodes)
			require.Nil(t, frame.Edges)
		}
	})
	t.Run("estimate_dimensions", func(t *testing.T) {
		one, err := traversalReservationBytes(30000, 30000, 50000, 1)
		require.NoError(t, err)
		sixty, err := traversalReservationBytes(30000, 30000, 50000, 60)
		require.NoError(t, err)
		require.EqualValues(t, 59*24, sixty-one)
		t.Logf("N=30000,E=50000,K60 reservation=%d bytes; edge-state=%d bitmap=%d; simulated counts only", sixty, unsafe.Sizeof(sharedTopologyEdgeState{}), unsafe.Sizeof(timeBitmap{}))
		_, err = traversalReservationBytes(-1, 0, 0, 60)
		require.Error(t, err)
	})
}
