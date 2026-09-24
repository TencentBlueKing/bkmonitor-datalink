// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func sharedGraphTestConfig() *TimeGraphConfig {
	return &TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "entity", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version", "group", "name"}}}}
}

func sharedGraphTestTimes(k int) []int64 {
	times := make([]int64, k)
	for i := range times {
		times[i] = 1700000000000 + int64(i)*1000
	}
	return times
}

func sharedGraphTestRelation(kind string) cmdb.Relation {
	return cmdb.Relation{V: []cmdb.Resource{"entity", "entity"}, RelationType: kind, MetricName: kind + "_flow", Category: string(RelationCategoryDynamic), Direction: string(DirectionOutbound)}
}

func TestDirectSharedGraphStorageAndOwnership(t *testing.T) {
	ctx := context.Background()
	times := sharedGraphTestTimes(60)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	require.NoError(t, err)
	info := cmdb.Matcher{"from_id": "a", "to_id": "b"}
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, sharedGraphTestRelation("calls"), info, times...))
	info["from_id"] = "changed"
	query := SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 1}
	result, err := g.FindSharedTopology(ctx, grid, query)
	require.NoError(t, err)
	require.Len(t, result, 60)
	for _, snapshot := range result {
		require.Len(t, snapshot.Nodes, 2)
		require.Len(t, snapshot.Edges, 1)
	}
	require.Nil(t, g.timeGraph)
	require.Nil(t, g.edgeTypes)
	require.Nil(t, g.nodeInfos)
	require.Nil(t, g.topologyNodes)
	require.Nil(t, g.topologyEdges)
	require.Len(t, g.shared.nodeBits, 2)
	require.Len(t, g.shared.edgeBits, 1)
	for _, versions := range g.shared.nodeInfos {
		require.Len(t, versions, 1)
	}
	before, err := json.Marshal(result)
	require.NoError(t, err)
	result[0].Nodes[0].Dimensions["id"] = "caller-change"
	require.Equal(t, "a", result[1].Nodes[0].Dimensions["id"])
	again, err := g.FindSharedTopology(ctx, grid, query)
	require.NoError(t, err)
	g.Clean(ctx)
	after, err := json.Marshal(again)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Empty(t, g.shared.nodeBits)
	require.Empty(t, g.shared.nodeInfos)
	require.Empty(t, g.shared.edgeBits)
	require.Empty(t, g.shared.edgePairBits)
}

func TestDirectSharedGraphDifferential(t *testing.T) {
	// 同一输入顺序保持 ID 一致，比较完整结果（包括历史属性、partial 和边身份）。
	for _, k := range []int{1, 3, 30, 60} {
		t.Run(fmt.Sprintf("K%d", k), func(t *testing.T) {
			ctx := context.Background()
			times := sharedGraphTestTimes(k)
			grid, err := NewTopologyGrid(times)
			require.NoError(t, err)
			legacy := NewTimeGraphWithConfig(sharedGraphTestConfig())
			direct, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
			require.NoError(t, err)
			graphs := []*TimeGraph{legacy, direct}
			rng := rand.New(rand.NewSource(1499))
			for node := 0; node < 40; node++ {
				info := cmdb.Matcher{"id": fmt.Sprint(node), "group": fmt.Sprint(node % 4), "version": "old"}
				for _, g := range graphs {
					require.NoError(t, g.AddTimeNode(ctx, "entity", info, times...))
				}
				for change := 0; change < 4; change++ {
					selected := []int64{}
					for _, ts := range times {
						if rng.Intn(2) == 0 {
							selected = append(selected, ts)
						}
					}
					update := cmdb.Matcher{"id": fmt.Sprint(node), "version": fmt.Sprint(change % 2), "name": fmt.Sprint(change)}
					for _, g := range graphs {
						require.NoError(t, g.AddTimeNode(ctx, "entity", update, selected...))
					}
				}
			}
			for edge := 0; edge < 100; edge++ {
				info := cmdb.Matcher{"from_id": fmt.Sprint(rng.Intn(38)), "to_id": fmt.Sprint(rng.Intn(38))}
				selected := []int64{}
				for _, ts := range times {
					if rng.Intn(2) == 0 {
						selected = append(selected, ts)
					}
				}
				relation := sharedGraphTestRelation(fmt.Sprintf("calls%d", edge%3))
				if edge%4 == 0 {
					relation.Direction = string(DirectionInbound)
				}
				for _, g := range graphs {
					require.NoError(t, g.AddTimeRelationWithRelation(ctx, relation, info, selected...))
				}
			}
			for _, hops := range []int{0, 1, 2, 5} {
				for _, matcher := range []cmdb.Matcher{{"id": "0"}, {"group": "1"}, {"version": "old"}, {"id": "39"}} {
					for _, direction := range []TraversalDirection{DirectionBoth, DirectionInbound, DirectionOutbound} {
						query := SharedTopologyQuery{SourceType: "entity", SourceMatcher: matcher, MaxHops: hops, Direction: direction, PartialTimestamps: map[int64]string{times[k-1]: "backend_partial"}}
						want, err := legacy.FindSharedTopology(ctx, grid, query)
						require.NoError(t, err)
						got, err := direct.FindSharedTopology(ctx, grid, query)
						require.NoError(t, err)
						require.Equal(t, want, got)
					}
				}
			}
			require.Equal(t, legacy.edgeCount, direct.edgeCount)
			require.Equal(t, legacy.nodeInfoCount, direct.nodeInfoCount)
		})
	}
}

func TestDirectSharedGraphBudgetsAndGrid(t *testing.T) {
	for _, test := range []struct {
		name                string
		nodes, edges, infos int
		want                string
	}{
		{name: "节点预算", nodes: 1, want: "max_graph_nodes"},
		{name: "时间边预算", edges: 1, want: "max_graph_edges"},
		{name: "节点时间预算", infos: 1, want: "max_graph_node_infos"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := sharedGraphTestConfig()
			cfg.MaxNodes = test.nodes
			cfg.MaxEdges = test.edges
			cfg.MaxNodeInfos = test.infos
			grid, err := NewTopologyGrid(sharedGraphTestTimes(3))
			require.NoError(t, err)
			g, err := newSharedTimeGraph(cfg, grid)
			require.NoError(t, err)
			err = g.AddTimeRelationWithRelation(context.Background(), sharedGraphTestRelation("calls"), cmdb.Matcher{"from_id": "a", "to_id": "b"}, grid.Timestamps...)
			var limit *ResultLimitError
			require.ErrorAs(t, err, &limit)
			require.Equal(t, test.want, limit.Reason)
		})
	}
	grid, err := NewTopologyGrid(sharedGraphTestTimes(1))
	require.NoError(t, err)
	g, err := newSharedTimeGraph(sharedGraphTestConfig(), grid)
	require.NoError(t, err)
	require.ErrorContains(t, g.AddTimeNode(context.Background(), "entity", cmdb.Matcher{"id": "a"}, 0), "does not match")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a"}, grid.Timestamps...), context.Canceled)
}
