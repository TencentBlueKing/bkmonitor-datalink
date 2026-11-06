// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestTopologySerialFetchStopsAfterFailureAndReleasesReservations(t *testing.T) {
	for _, outcome := range []string{"backend", "apply", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			topologyAdmissionTestEnvironment(t, 1)
			MaxSharedTopologyReservedBytes = 1 << 30
			topologyMemoryHeadroom = func() (int64, bool) { return 4 << 30, true }

			parent, cancel := context.WithCancel(metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment()))
			defer cancel()
			ctx, release, err := AcquireSharedTopology(parent)
			require.NoError(t, err)
			defer release()
			provider := selectorProvider()
			provider.schemas = provider.schemas[:1]
			model := &Model{schemaProvider: provider}
			grid, err := NewTopologyGrid([]int64{1700000000000})
			require.NoError(t, err)
			graph := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
			defer graph.Clean(ctx)
			require.NoError(t, graph.enableResidency(ctx))
			loader := &timeGraphMatrixLoader{model: model, graph: graph, topology: true, start: time.Unix(1700000000, 0), end: time.Unix(1700000000, 0), step: time.Minute}
			failure := errors.New("later group backend unavailable")
			var fetched, applied []string
			loader.override = func(ctx context.Context, query *structured.QueryTs) (pl.Matrix, error) {
				fetched = append(fetched, query.SpaceUid)
				if query.SpaceUid == "0" {
					return contractMatrix(map[string]string{"a_id": "root", "b_id": "first"}, grid.timestamps...), nil
				}
				require.Equal(t, "1", query.SpaceUid, "no later group may run after a failure")
				require.Equal(t, []string{"first", "first"}, applied, "the previous Matrix is applied before fetching the next")
				switch outcome {
				case "cancel":
					cancel()
					return nil, ctx.Err()
				case "apply":
					return contractMatrix(map[string]string{"a_id": "root"}, grid.timestamps...), nil
				default:
					return nil, failure
				}
			}
			relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)
			groups := make([]topologyFetchGroup, 3)
			for i := range groups {
				groups[i] = topologyFetchGroup{ctx: ctx, query: &structured.QueryTs{SpaceUid: strconv.Itoa(i), Start: "1700000000", End: "1700000000", Step: "1m0s"}, relations: relations}
			}
			queries := 0
			err = loader.fetchTopologyGroups(groups, "space", &queries, func(_ cmdb.Relation, matrix pl.Matrix) {
				applied = append(applied, matrix[0].Metric.Get("b_id"))
			})
			if outcome == "backend" {
				require.ErrorIs(t, err, failure)
			} else if outcome == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorContains(t, err, "b_id")
			}
			require.Equal(t, []string{"0", "1"}, fetched)
			require.Equal(t, 2, queries)
			require.Equal(t, 2, loader.physicalQueryCount)
			require.Zero(t, matrixResidencyState(graph).ReceivingBytes)
			graph.Clean(ctx)
			require.Zero(t, matrixResidencyState(graph).ResidentBytes)
			release()
			require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyAdmissionTestState())
		})
	}
}

func TestTopologySerialFetchPreservesApplicationAndAttributeOrder(t *testing.T) {
	ctx := metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment())
	provider := selectorProvider()
	provider.schemas = provider.schemas[:1]
	provider.fields["a"] = []string{"a_id", "b_id"}
	model := &Model{schemaProvider: provider}
	grid, err := NewTopologyGrid([]int64{1700000000000})
	require.NoError(t, err)
	graph := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
	defer graph.Clean(ctx)
	wantGraph := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
	defer wantGraph.Clean(ctx)
	loader := &timeGraphMatrixLoader{model: model, graph: graph, topology: true, start: time.Unix(1700000000, 0), end: time.Unix(1700000000, 0), step: time.Minute}
	var fetched, applied []string
	loader.override = func(_ context.Context, query *structured.QueryTs) (pl.Matrix, error) {
		require.Len(t, applied, 2*len(fetched), "finish both logical applications before the next physical fetch")
		fetched = append(fetched, query.SpaceUid)
		return contractMatrix(map[string]string{"a_id": "root", "b_id": query.SpaceUid}, grid.timestamps...), nil
	}
	relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)
	groups := make([]topologyFetchGroup, 4)
	for i := range groups {
		id := strconv.Itoa(len(groups) - 1 - i)
		groups[i] = topologyFetchGroup{ctx: ctx, query: &structured.QueryTs{SpaceUid: id, Start: "1700000000", End: "1700000000", Step: "1m0s"}, relations: relations}
		for _, relation := range relations {
			require.NoError(t, wantGraph.applyRelationMatrix(ctx, contractMatrix(map[string]string{"a_id": "root", "b_id": id}, grid.timestamps...), relation, nil, nil))
		}
	}
	queries := 0
	require.NoError(t, loader.fetchTopologyGroups(groups, "space", &queries, func(_ cmdb.Relation, matrix pl.Matrix) {
		applied = append(applied, matrix[0].Metric.Get("b_id"))
	}))
	require.Equal(t, []string{"3", "2", "1", "0"}, fetched)
	require.Equal(t, []string{"3", "3", "2", "2", "1", "1", "0", "0"}, applied)
	require.Equal(t, 4, queries)
	require.Equal(t, 4, loader.physicalQueryCount)
	require.Equal(t, 4, loader.reusedQueryCount)
	require.Equal(t, 8, loader.pointCount)
	query := SharedTopologyQuery{SourceType: "a", SourceMatcher: cmdb.Matcher{"a_id": "root"}, MaxHops: 2}
	want, err := wantGraph.findSharedTopologyDTO(ctx, grid, query)
	require.NoError(t, err)
	got, err := graph.findSharedTopologyDTO(ctx, grid, query)
	require.NoError(t, err)
	require.Equal(t, canonicalTopologyForTest(t, want), canonicalTopologyForTest(t, got))
}

func TestFullRootSerialFetchPreservesK60H6SnapshotsAndCompact(t *testing.T) {
	times := sharedGraphTestTimes(60)
	data := map[string]pl.Matrix{
		"cp": contractMatrix(map[string]string{"cluster": "alpha", "namespace": "one", "container_id": "c0", "pod_id": "p0"}, times...),
		"np": contractMatrix(map[string]string{"cluster": "alpha", "namespace": "one", "node_id": "n0", "pod_id": "p0"}, times...),
		"ns": contractMatrix(map[string]string{"cluster": "alpha", "node_id": "n0", "system_id": "s0"}, times...),
		"hs": contractMatrix(map[string]string{"host_id": "h0", "system_id": "s0"}, times...),
	}
	model := &Model{schemaProvider: selectorScopeProvider(), timeGraphQueryReference: selectorQueryReference}
	var fetched []string
	model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		name := query.QueryList[0].FieldName
		fetched = append(fetched, name)
		return data[name], nil
	}
	oldPlan, oldReuse := SharedTopologyPlanCandidates, SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = oldPlan, oldReuse })
	request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "container", StartTime: times[0] / 1000, EndTime: times[len(times)-1] / 1000, Step: "1s", MaxHops: 6}
	SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = false, false
	baseline, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	want := canonicalTopologyForTest(t, baseline.Snapshots)
	SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = true, true
	for _, format := range []string{"", cmdb.CompactTopologyFormat} {
		request.ResponseFormat = format
		fetched = nil
		result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
		require.NoError(t, err)
		require.Len(t, fetched, 4, "reverse logical relations share four physical responses")
		require.ElementsMatch(t, []string{"cp", "np", "ns", "hs"}, fetched)
		snapshots := result.Snapshots
		if format == cmdb.CompactTopologyFormat {
			snapshots = decodeCompactForTest(t, result.Compact)
		}
		require.Equal(t, want, canonicalTopologyForTest(t, snapshots))
		require.Len(t, snapshots, 60)
		for _, snapshot := range snapshots {
			require.False(t, snapshot.Partial)
			require.Len(t, snapshot.Nodes, 5)
			require.Len(t, snapshot.Edges, 8)
		}
	}
}
