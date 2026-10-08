// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// A relation endpoint observes the root even when traversal cannot follow
// that edge in the requested direction.
func TestSharedTopologyInboundEndpointKeepsRoot(t *testing.T) {
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.schemas = []RelationSchema{
		{RelationType: "forward", Category: RelationCategoryStatic, FromType: "source", ToType: "middle", IsDirectional: true, MetricName: "forward_flow"},
		{RelationType: "back", Category: RelationCategoryStatic, FromType: "middle", ToType: "source", IsDirectional: true, MetricName: "back_flow"},
	}
	model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
	model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		var matrix pl.Matrix
		switch q.QueryList[0].FieldName {
		case "forward_flow":
			matrix = contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000)
		case "back_flow":
			matrix = contractMatrix(map[string]string{"middle_id": "c", "source_id": "a"}, 1700000100000)
		default:
			t.Fatalf("unexpected query: %s", q.QueryList[0].FieldName)
		}
		return filterTopologyRegressionMatrix(q, matrix), nil
	}
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		StartTime: 1700000000, EndTime: 1700000100, Step: "100s", MaxHops: 2,
	})
	require.NoError(t, err)
	require.Len(t, result.Snapshots, 2)
	require.Len(t, result.Snapshots[0].Nodes, 2)
	require.Len(t, result.Snapshots[0].Edges, 1)
	require.Len(t, result.Snapshots[1].Nodes, 1)
	require.Equal(t, "a", result.Snapshots[1].Nodes[0].Dimensions["source_id"])
	require.Empty(t, result.Snapshots[1].Edges)
	require.False(t, result.Snapshots[1].Partial)
	request := cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		StartTime: 1700000000, EndTime: 1700000100, Step: "100s", MaxHops: 2,
		ResponseFormat: cmdb.CompactTopologyFormat,
	}
	compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.Equal(t, result.Snapshots, decodeCompactForTest(t, compact.Compact))

	// With only the incoming relation in scope there is no outgoing first hop.
	request.AllowedRelationTypes = []string{"back"}
	request.ResponseFormat = ""
	incoming, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.Empty(t, incoming.Snapshots[0].Nodes)
	require.Len(t, incoming.Snapshots[1].Nodes, 1)
	require.Equal(t, "a", incoming.Snapshots[1].Nodes[0].Dimensions["source_id"])
	require.Empty(t, incoming.Snapshots[1].Edges)
	request.ResponseFormat = cmdb.CompactTopologyFormat
	incomingCompact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.Equal(t, incoming.Snapshots, decodeCompactForTest(t, incomingCompact.Compact))
}

type topologyOracleEdge struct {
	from, to, relation string
	frame              int
}

// Compare the public query with a per-frame BFS over raw fixture edges. The
// oracle does not use the production graph, bitmap, or candidate planner.
func TestSharedTopologyMatchesIndependentFrameBFS(t *testing.T) {
	const start int64 = 1700000000000
	provider := contractSchemaProvider{
		resources: []ResourceType{"service"},
		primary:   map[ResourceType][]string{"service": {"id"}},
		fields:    map[ResourceType][]string{"service": {"id"}},
		schemas: []RelationSchema{
			{RelationType: "calls", Category: RelationCategoryDynamic, FromType: "service", ToType: "service", IsDirectional: true, MetricName: "calls_flow"},
			{RelationType: "depends", Category: RelationCategoryDynamic, FromType: "service", ToType: "service", IsDirectional: true, MetricName: "depends_flow"},
		},
	}
	for seed := int64(0); seed < 8; seed++ {
		rng := rand.New(rand.NewSource(seed))
		fixtures := map[topologyOracleEdge]bool{
			{from: "0", to: "1", relation: "calls", frame: 0}:   true,
			{from: "1", to: "2", relation: "calls", frame: 0}:   true,
			{from: "0", to: "2", relation: "depends", frame: 0}: true,
			{from: "2", to: "1", relation: "depends", frame: 0}: true,
			{from: "0", to: "3", relation: "calls", frame: 1}:   true,
			{from: "3", to: "4", relation: "depends", frame: 1}: true,
			{from: "4", to: "0", relation: "calls", frame: 1}:   true,
			{from: "4", to: "0", relation: "calls", frame: 2}:   true,
		}
		for frame := 0; frame < 2; frame++ {
			for _, relation := range []string{"calls", "depends"} {
				for from := 0; from < 5; from++ {
					for to := 0; to < 5; to++ {
						if rng.Intn(5) == 0 {
							fixtures[topologyOracleEdge{from: fmt.Sprint(from), to: fmt.Sprint(to), relation: relation, frame: frame}] = true
						}
					}
				}
			}
		}
		var edges []topologyOracleEdge
		matrices := map[string]pl.Matrix{"calls_flow": {}, "depends_flow": {}}
		for edge := range fixtures {
			edges = append(edges, edge)
			metric := edge.relation + "_flow"
			matrices[metric] = append(matrices[metric], contractMatrix(map[string]string{
				"from_id": edge.from, "to_id": edge.to,
			}, start+int64(edge.frame)*60000)...)
		}
		for _, relationFilter := range []string{"", "calls"} {
			for hops := 1; hops <= 3; hops++ {
				t.Run(fmt.Sprintf("seed-%d/filter-%s/hops-%d", seed, relationFilter, hops), func(t *testing.T) {
					model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
					model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
						metric := q.QueryList[0].FieldName
						matrix, ok := matrices[metric]
						require.True(t, ok, "unexpected metric %s", metric)
						return filteredTimeGraphMatrix(t, matrix, q.QueryList[0].Conditions), nil
					}
					request := cmdb.SharedTopologyQuery{
						SpaceUID: "space", SourceType: "service", SourceInfo: cmdb.Matcher{"id": "0"},
						StartTime: 1700000000, EndTime: 1700000120, Step: "60s", MaxHops: hops,
						DynamicRelationDirection: "outbound",
					}
					if relationFilter != "" {
						request.AllowedRelationTypes = []string{relationFilter}
					}
					got, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
					require.NoError(t, err)
					require.Len(t, got.Snapshots, 3)
					for frame, snapshot := range got.Snapshots {
						wantNodes, wantEdges := independentTopologyFrame(edges, frame, hops, relationFilter)
						actualNodes, actualEdges := topologySnapshotIdentities(t, snapshot)
						require.Equal(t, wantNodes, actualNodes, "frame %d nodes", frame)
						require.Equal(t, wantEdges, actualEdges, "frame %d edges", frame)
						require.False(t, snapshot.Partial)
					}
					request.ResponseFormat = cmdb.CompactTopologyFormat
					compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
					require.NoError(t, err)
					for frame, snapshot := range decodeCompactForTest(t, compact.Compact) {
						wantNodes, wantEdges := independentTopologyFrame(edges, frame, hops, relationFilter)
						actualNodes, actualEdges := topologySnapshotIdentities(t, snapshot)
						require.Equal(t, wantNodes, actualNodes, "compact frame %d nodes", frame)
						require.Equal(t, wantEdges, actualEdges, "compact frame %d edges", frame)
					}
				})
			}
		}
	}
}

func independentTopologyFrame(edges []topologyOracleEdge, frame, hops int, relationFilter string) (map[string]bool, map[string]bool) {
	active := make([]topologyOracleEdge, 0)
	for _, edge := range edges {
		if edge.frame == frame && (relationFilter == "" || edge.relation == relationFilter) {
			active = append(active, edge)
		}
	}
	nodes := make(map[string]bool)
	resultEdges := make(map[string]bool)
	for _, edge := range active {
		if edge.from == "0" || edge.to == "0" {
			nodes["0"] = true
			break
		}
	}
	frontier := map[string]bool{}
	if nodes["0"] {
		frontier["0"] = true
	}
	for hop := 0; hop < hops; hop++ {
		next := make(map[string]bool)
		for _, edge := range active {
			if frontier[edge.from] && !nodes[edge.to] {
				next[edge.to] = true
			}
		}
		for node := range next {
			nodes[node] = true
		}
		frontier = next
	}
	for _, edge := range active {
		if nodes[edge.from] && nodes[edge.to] {
			resultEdges[edge.from+">"+edge.to+"/"+edge.relation] = true
		}
	}
	return nodes, resultEdges
}

func topologySnapshotIdentities(t *testing.T, snapshot cmdb.SharedTopologySnapshot) (map[string]bool, map[string]bool) {
	t.Helper()
	nodes := make(map[string]bool)
	ids := make(map[uint64]string)
	for _, node := range snapshot.Nodes {
		require.Equal(t, cmdb.Resource("service"), node.ResourceType)
		identity := node.Dimensions["id"]
		require.NotEmpty(t, identity)
		require.False(t, nodes[identity], "duplicate node %s", identity)
		nodes[identity] = true
		ids[node.ID] = identity
	}
	edges := make(map[string]bool)
	for _, edge := range snapshot.Edges {
		from, to := ids[edge.Source], ids[edge.Target]
		require.NotEmpty(t, from)
		require.NotEmpty(t, to)
		require.Equal(t, "outbound", edge.Direction)
		key := from + ">" + to + "/" + edge.RelationType
		require.False(t, edges[key], "duplicate edge %s", key)
		edges[key] = true
	}
	return nodes, edges
}
