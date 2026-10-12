// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	querypromql "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
)

func TestTopologyAttributesSelectRootsPerFrame(t *testing.T) {
	times := []int64{1700000000000, 1700000060000, 1700000120000}
	for _, format := range []string{"snapshots", cmdb.CompactTopologyFormat} {
		t.Run(format, func(t *testing.T) {
			responses := map[string]pl.Matrix{
				"node_info_relation":  append(contractMatrix(map[string]string{"node_id": "n1", "zone": "east"}, times[0]), contractMatrix(map[string]string{"node_id": "n2", "zone": "east"}, times[1])...),
				"node_to_middle_flow": append(contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, times[0], times[1]), contractMatrix(map[string]string{"node_id": "n2", "middle_id": "m2"}, times[0], times[1])...),
			}
			vm := &publicTimeGraphVM{responses: responses}
			model := &Model{schemaProvider: publicSourceExpandProvider(), timeGraphQueryReference: timeGraphTestQueryReference, timeGraphVMQuery: vm.query}
			request := cmdb.SharedTopologyQuery{SpaceUID: "space", StartTime: 1700000000, EndTime: 1700000120, Step: "60s", SourceType: "node", SourceExpandInfo: cmdb.Matcher{"zone": "east"}, MaxHops: 1, ResponseFormat: format}
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.NoError(t, err)
			snapshots := result.Snapshots
			if result.Compact != nil {
				snapshots = decodeCompactForTest(t, result.Compact)
			}
			require.Len(t, snapshots, 3)
			for i, want := range []string{"n1", "n2", ""} {
				var roots []string
				for _, node := range snapshots[i].Nodes {
					if node.ResourceType == "node" {
						roots = append(roots, node.Dimensions["node_id"])
					}
				}
				if want == "" {
					require.Empty(t, snapshots[i].Nodes)
				} else {
					require.Equal(t, []string{want}, roots)
					require.Len(t, snapshots[i].Nodes, 2)
				}
			}
			require.Equal(t, "node_info_relation", vm.calls[0].metric)
			foundZone := false
			for _, condition := range vm.calls[0].conditions.FieldList {
				if condition.DimensionName == "zone" {
					require.Equal(t, []string{"east"}, condition.Value)
					foundZone = true
				}
			}
			require.True(t, foundZone)
			request.SourceInfo = cmdb.Matcher{"node_id": "n1"}
			request.SourceExpandInfo = cmdb.Matcher{"node_id": "n2"}
			_, err = model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.ErrorContains(t, err, "conflicts")
			request.SourceExpandInfo = cmdb.Matcher{"unknown": "value"}
			_, err = model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.Error(t, err)
			request.SourceExpandInfo = cmdb.Matcher{"unknown": ""}
			_, err = model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.Error(t, err)
		})
	}
}

func TestTopologyInfoOnlyDoesNotCreateRootsOrRestrictSameTypeNeighbors(t *testing.T) {
	provider := contractSchemaProvider{resources: []ResourceType{"entity"}, primary: map[ResourceType][]string{"entity": {"id"}}, fields: map[ResourceType][]string{"entity": {"id", "version"}}, schemas: []RelationSchema{{RelationType: "calls", Category: RelationCategoryDynamic, FromType: "entity", ToType: "entity", MetricName: "calls_flow", IsDirectional: true}}}
	responses := map[string]pl.Matrix{
		"entity_info_relation": contractMatrix(map[string]string{"id": "a", "version": "v2"}, 1700000000000, 1700000060000),
		"calls_flow":           append(contractMatrix(map[string]string{"from_id": "a", "to_id": "b"}, 1700000060000), contractMatrix(map[string]string{"from_id": "b", "to_id": "c"}, 1700000060000)...),
	}
	for _, format := range []string{"snapshots", cmdb.CompactTopologyFormat} {
		vm := &publicTimeGraphVM{responses: responses}
		model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference, timeGraphVMQuery: vm.query}
		result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", StartTime: 1700000000, EndTime: 1700000060, Step: "60s", SourceType: "entity", SourceExpandInfo: cmdb.Matcher{"version": "v2"}, MaxHops: 2, ResponseFormat: format})
		require.NoError(t, err)
		snapshots := result.Snapshots
		if result.Compact != nil {
			snapshots = decodeCompactForTest(t, result.Compact)
		}
		require.Empty(t, snapshots[0].Nodes)
		require.Len(t, snapshots[1].Nodes, 3)
		require.Len(t, snapshots[1].Edges, 4)
	}
}

func TestSourceInfoQualityRetainedByLegacyAndTopology(t *testing.T) {
	for _, missing := range []bool{false, true} {
		ctx := initTimeGraphQueryTestEnvironment()
		vm := &publicTimeGraphVM{responses: map[string]pl.Matrix{"node_info_relation": contractMatrix(map[string]string{"node_id": "n1", "zone": "east"}, 1700000000000), "node_to_middle_flow": contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, 1700000000000)}}
		model := &Model{schemaProvider: publicSourceExpandProvider(), timeGraphQueryReference: timeGraphTestQueryReference, timeGraphVMQuery: vm.query}
		model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) { return model, nil })
		if missing {
			model.timeGraphQueryReference = func(ctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
				r, err := timeGraphTestQueryReference(ctx, q)
				if q.QueryList[0].FieldName == "node_info_relation" {
					return metadata.QueryReference{}, err
				}
				return r, err
			}
			model.timeGraphVMQuery = func(ctx context.Context, q *structured.QueryTs, e string, i bool, s, en time.Time, st time.Duration) (pl.Matrix, error) {
				if q.QueryList[0].FieldName == "node_info_relation" {
					return nil, nil
				}
				return vm.query(ctx, q, e, i, s, en, st)
			}
		} else {
			model.timeGraphVMQueryWithPartial = func(ctx context.Context, q *structured.QueryTs, e string, i bool, s, en time.Time, st time.Duration) (pl.Matrix, bool, error) {
				matrix, err := vm.query(ctx, q, e, i, s, en, st)
				return matrix, q.QueryList[0].FieldName == "node_info_relation", err
			}
		}
		_, _, _, _, targets, err := model.QueryResourceMatcher(ctx, "", "space", "1700000000", "middle", "node", nil, cmdb.Matcher{"zone": "east"}, false, nil)
		require.NoError(t, err)
		rangeCtx := metadata.InitHashID(context.Background())
		_, _, _, _, _, rangeErr := model.QueryResourceMatcherRange(rangeCtx, "", "space", "60s", "1700000000", "1700000060", "middle", "node", nil, cmdb.Matcher{"zone": "east"}, false, nil)
		require.NoError(t, rangeErr)
		require.NotNil(t, metadata.GetStatus(rangeCtx))
		require.Equal(t, metadata.QueryTsPartial, metadata.GetStatus(rangeCtx).Code)
		status := metadata.GetStatus(ctx)
		require.NotNil(t, status)
		require.Equal(t, metadata.QueryTsPartial, status.Code)
		if missing {
			require.Empty(t, targets)
			require.Contains(t, status.Message, "source_info_no_route")
		} else {
			require.Len(t, targets, 1)
		}
		result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: 1700000000, SourceType: "node", SourceExpandInfo: cmdb.Matcher{"zone": "east"}, MaxHops: 1})
		require.NoError(t, err)
		require.True(t, result.Snapshots[0].Partial)
		if missing {
			require.Empty(t, result.Snapshots[0].Nodes)
			require.Equal(t, "source_info_no_route", result.Snapshots[0].PartialReason)
		}
	}
}

func TestSharedStorageBudgetsCountStoredVersions(t *testing.T) {
	ctx := context.Background()
	times := sharedGraphTestTimes(60)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	cfg := sharedGraphTestConfig()
	cfg.MaxEdges = 1
	cfg.MaxNodeInfos = 2
	g, err := newSharedTimeGraph(cfg, grid)
	require.NoError(t, err)
	relation := sharedGraphTestRelation("calls")
	info := cmdb.Matcher{"from_id": "a", "to_id": "b"}
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation, info, times...))
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation, info, times...))
	require.Equal(t, 60, g.edgeCount)
	require.Equal(t, 120, g.nodeInfoCount)
	require.Equal(t, 2, g.shared.nodeInfoVersionCount)
	other := relation
	other.MetricName = "other_flow"
	var limit *ResultLimitError
	require.ErrorAs(t, g.AddTimeRelationWithRelation(ctx, other, info, times...), &limit)
	require.Equal(t, "max_graph_edges", limit.Reason)
	require.ErrorAs(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a", "version": "v2"}, times[0]), &limit)
	require.Equal(t, "max_graph_node_infos", limit.Reason)
}

func TestCompactBudgetsRepresentationAndIntegerSafety(t *testing.T) {
	oldElements, oldBytes := MaxSharedTopologyOutputElements, MaxSharedTopologyOutputBytes
	t.Cleanup(func() { MaxSharedTopologyOutputElements, MaxSharedTopologyOutputBytes = oldElements, oldBytes })
	MaxSharedTopologyOutputElements = 3
	MaxSharedTopologyOutputBytes = 16000
	times := sharedGraphTestTimes(60)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	require.NoError(t, err)
	require.NoError(t, g.AddTimeRelationWithRelation(context.Background(), sharedGraphTestRelation("calls"), cmdb.Matcher{"from_id": "a", "to_id": "b"}, times...))
	query := SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 1}
	compact, err := g.FindCompactTopology(context.Background(), grid, query, nil)
	require.NoError(t, err)
	require.Equal(t, 120, compact.NodeOccurrences)
	require.Equal(t, 60, compact.EdgeOccurrences)
	bytes, err := json.Marshal(compact)
	require.NoError(t, err)
	require.Less(t, len(bytes), 16000)
	_, err = g.FindSharedTopology(context.Background(), grid, query)
	var limit *ResultLimitError
	require.ErrorAs(t, err, &limit)
	require.Equal(t, "max_topology_output_elements", limit.Reason)
	b := &topologyOutputBudget{maxBytes: math.MaxInt64}
	require.Error(t, b.addRepeated(math.MaxInt64, 2))
	require.Zero(t, b.bytes)
	require.Zero(t, b.elements)
	require.Error(t, b.addCharge(-1, 1))
	require.Error(t, b.addCharge(1, -1))
}

func TestSourceInfoMissingRouteRealLocalEngine(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	previous := querypromql.GlobalEngine
	querypromql.GlobalEngine = pl.NewEngine(pl.EngineOpts{Timeout: 10 * time.Second, MaxSamples: 1000, LookbackDelta: 5 * time.Minute})
	t.Cleanup(func() { querypromql.GlobalEngine = previous })
	model := &Model{schemaProvider: publicSourceExpandProvider()}
	var infoCtx context.Context
	model.timeGraphQueryReference = func(ctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
		infoCtx = ctx
		require.Equal(t, "node_info_relation", q.QueryList[0].FieldName)
		if err := q.ToTime(ctx); err != nil {
			return nil, err
		}
		ref := metadata.QueryReference{"a": {&metadata.QueryMetric{ReferenceName: "a", MetricName: "node_info_relation"}}}
		metadata.SetQueryReference(ctx, ref)
		return ref, nil
	}
	point := time.Unix(1700000040, 0)
	graph, err := model.buildTimeGraphFromRelations(ctx, "space", point, point, time.Minute, "node", cmdb.Matcher{"node_id": "n1"}, cmdb.Matcher{"zone": "east"}, nil, "10m")
	require.NoError(t, err)
	defer graph.Clean(ctx)
	require.Nil(t, metadata.GetExpand(infoCtx))
	require.False(t, metadata.GetQueryParams(infoCtx).IsDirectQuery())
	require.Equal(t, "source_info_no_route", metadata.GetStatus(infoCtx).Message)
	require.Empty(t, graph.findNodesByPartialMatcher("node", nil))
	require.Equal(t, "source_info_no_route", graph.partialTimesCopy()[point.UnixMilli()])
}

func TestExpandedPrimaryKeyIsUsedInTopology(t *testing.T) {
	for _, format := range []string{"snapshots", cmdb.CompactTopologyFormat} {
		vm := &publicTimeGraphVM{responses: map[string]pl.Matrix{
			"node_info_relation":  contractMatrix(map[string]string{"node_id": "n1"}, 1700000000000),
			"node_to_middle_flow": append(contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, 1700000000000), contractMatrix(map[string]string{"node_id": "n2", "middle_id": "m2"}, 1700000000000)...),
		}}
		model := &Model{schemaProvider: publicSourceExpandProvider(), timeGraphQueryReference: timeGraphTestQueryReference, timeGraphVMQuery: vm.query}
		result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: 1700000000, SourceType: "node", SourceExpandInfo: cmdb.Matcher{"node_id": "n1"}, MaxHops: 1, ResponseFormat: format})
		require.NoError(t, err)
		snapshots := result.Snapshots
		if result.Compact != nil {
			snapshots = decodeCompactForTest(t, result.Compact)
		}
		require.Len(t, snapshots[0].Nodes, 2)
		for _, node := range snapshots[0].Nodes {
			require.NotEqual(t, "n2", node.Dimensions["node_id"])
		}
		found := false
		for _, condition := range vm.calls[0].conditions.FieldList {
			if condition.DimensionName == "node_id" {
				require.Equal(t, []string{"n1"}, condition.Value)
				found = true
			}
		}
		require.True(t, found)
	}
}

func TestSharedEmptyEdgesAndVersionBudgetRelease(t *testing.T) {
	times := sharedGraphTestTimes(60)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	cfg := sharedGraphTestConfig()
	cfg.MaxEdges = 1
	cfg.MaxNodeInfos = 2
	g, err := newSharedTimeGraph(cfg, grid)
	require.NoError(t, err)
	for _, kind := range []string{"a", "b", "c"} {
		require.NoError(t, g.AddTimeRelationWithRelation(context.Background(), sharedGraphTestRelation(kind), cmdb.Matcher{"from_id": "a", "to_id": "b"}))
	}
	require.Empty(t, g.shared.edgeBits)
	require.NoError(t, g.AddTimeNode(context.Background(), "entity", cmdb.Matcher{"id": "a", "version": "v1"}, times...))
	require.NoError(t, g.AddTimeNode(context.Background(), "entity", cmdb.Matcher{"id": "a", "version": "v2"}, times[0]))
	require.Equal(t, 2, g.shared.nodeInfoVersionCount)
	var limit *ResultLimitError
	require.ErrorAs(t, g.AddTimeNode(context.Background(), "entity", cmdb.Matcher{"id": "a", "version": "v3"}, times[1]), &limit)
	require.Equal(t, 2, g.shared.nodeInfoVersionCount)
	require.NoError(t, g.AddTimeNode(context.Background(), "entity", cmdb.Matcher{"id": "a", "version": "v3"}, times...))
	require.Equal(t, 1, g.shared.nodeInfoVersionCount)
}

func TestOutputChargesDoNotMutateOnOverflowOrReservationFailure(t *testing.T) {
	cases := []*topologyOutputBudget{
		{bytes: math.MaxInt64, maxBytes: math.MaxInt64},
		{elements: math.MaxInt, maxBytes: math.MaxInt64},
	}
	for _, b := range cases {
		bytes, elements := b.bytes, b.elements
		require.Error(t, b.addCharge(512*1024*1024, 1))
		require.Equal(t, bytes, b.bytes)
		require.Equal(t, elements, b.elements)
	}
	ctx, release, err := AcquireSharedTopology(context.Background())
	require.NoError(t, err)
	b := &topologyOutputBudget{ctx: ctx, maxBytes: math.MaxInt64}
	require.Error(t, b.addCharge(math.MaxInt64/2, 1))
	require.Zero(t, b.bytes)
	require.Zero(t, b.elements)
	old := MaxSharedTopologyReservedBytes
	t.Cleanup(func() { MaxSharedTopologyReservedBytes = old; release() })
	MaxSharedTopologyReservedBytes = 1
	require.Error(t, b.addCharge(512*1024*1024, 1))
	require.Zero(t, b.bytes)
	require.Zero(t, b.elements)
	MaxSharedTopologyReservedBytes = old
	release()
	topologyAdmission.Lock()
	defer topologyAdmission.Unlock()
	require.Zero(t, topologyAdmission.active)
	require.Zero(t, topologyAdmission.reserved)
}

func TestPartialReasonsStableAndCompleteEmptyUnmarked(t *testing.T) {
	q := NewTimeGraphWithConfig(sharedGraphTestConfig())
	now := time.Unix(1700000000, 0)
	for _, reason := range []string{"source_info_no_route", "backend_partial", "source_info_no_route"} {
		q.markPartialRange(now, now, time.Minute, reason)
	}
	require.Equal(t, "backend_partial;source_info_no_route", q.partialTimesCopy()[now.UnixMilli()])
	ctx := initTimeGraphQueryTestEnvironment()
	model := sharedTopologyQueryModel(map[string]pl.Matrix{"source_middle_flow": nil, "middle_target_flow": nil})
	result, err := model.QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: 1700000000, SourceType: "source", MaxHops: 1})
	require.NoError(t, err)
	require.Empty(t, result.Snapshots[0].Nodes)
	require.False(t, result.Snapshots[0].Partial)
}

func TestPartialPathFallbackKeepsKnownQualityAndFailedPathsDoNotPublish(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	vm := &publicTimeGraphVM{responses: map[string]pl.Matrix{
		"node_info_relation":  nil,
		"node_to_middle_flow": contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, 1700000000000),
	}}
	model := &Model{schemaProvider: publicSourceExpandProvider(), timeGraphQueryReference: timeGraphTestQueryReference, timeGraphVMQuery: vm.query}
	model.timeGraphVMQueryWithPartial = func(ctx context.Context, q *structured.QueryTs, e string, i bool, s, en time.Time, st time.Duration) (pl.Matrix, bool, error) {
		matrix, err := vm.query(ctx, q, e, i, s, en, st)
		return matrix, q.QueryList[0].FieldName == "node_info_relation", err
	}
	paths := []resourcePath{{Steps: []resourcePathStep{{ResourceType: "node"}, {ResourceType: "middle", RelationType: "node_to_middle"}}}, {Steps: []resourcePathStep{{ResourceType: "node"}, {ResourceType: "middle", RelationType: "node_to_middle"}}}}
	call := 0
	results, err := queryFirstTimeGraphPath(ctx, paths, func(path resourcePath) ([]cmdb.PathResourcesResult, error) {
		call++
		expand := cmdb.Matcher{"zone": "east"}
		matcher := cmdb.Matcher{"node_id": "n1", "zone": "east"}
		if call == 2 {
			expand = nil
			matcher = cmdb.Matcher{"node_id": "n1"}
		}
		return model.queryRelationTimeGraph(ctx, "", "space", time.Unix(1700000000, 0), time.Unix(1700000000, 0), time.Minute, "node", []cmdb.Resource{"middle"}, resourcePathsToTimeGraphRelationPaths([]resourcePath{path}), matcher, expand)
	})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, 2, call)
	require.Equal(t, "backend_partial", metadata.GetStatus(ctx).Message)
}
