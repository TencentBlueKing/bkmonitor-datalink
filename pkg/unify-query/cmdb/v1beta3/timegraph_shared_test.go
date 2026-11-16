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

func sharedTopologyTestConfig() *TimeGraphConfig {
	return &TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "entity", Index: cmdb.Index{"id"}, Info: cmdb.Index{"version", "group", "name"}}}}
}

func sharedTopologyTestTimes(k int) []int64 {
	times := make([]int64, k)
	for i := range times {
		times[i] = 1700000000000 + int64(i)*1000
	}
	return times
}

func sharedTopologyTestRelation(kind string) cmdb.Relation {
	return cmdb.Relation{V: []cmdb.Resource{"entity", "entity"}, RelationType: kind, MetricName: kind + "_flow", Category: string(RelationCategoryDynamic), Direction: string(DirectionOutbound)}
}

func TestDirectSharedGraphStorageAndOwnership(t *testing.T) {
	ctx := context.Background()
	times := sharedTopologyTestTimes(60)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	g := newSharedTimeGraph(sharedTopologyTestConfig(), grid)
	info := cmdb.Matcher{"from_id": "a", "to_id": "b"}
	require.NoError(t, g.AddTimeRelationWithRelation(ctx, sharedTopologyTestRelation("calls"), info, times...))
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
			times := sharedTopologyTestTimes(k)
			grid, err := NewTopologyGrid(times)
			require.NoError(t, err)
			legacy := NewTimeGraphWithConfig(sharedTopologyTestConfig())
			direct := newSharedTimeGraph(sharedTopologyTestConfig(), grid)
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
				relation := sharedTopologyTestRelation(fmt.Sprintf("calls%d", edge%3))
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
						compact, err := direct.FindCompactTopology(ctx, grid, query, nil)
						require.NoError(t, err)
						require.Equal(t, convertSharedTopologySnapshots(want), decodeCompactForTest(t, compact))
					}
				}
			}
			require.Equal(t, legacy.edgeCount, direct.edgeCount)
			require.Equal(t, legacy.nodeInfoCount, direct.nodeInfoCount)
		})
	}
}

// 图规模不设上限：节点、关系身份和属性版本超过旧的计数上限时照常构图并给出答案。
func TestDirectSharedTopologyGrowsWithoutCountCapsAndValidatesGrid(t *testing.T) {
	for _, points := range []int{1, 3, 60} {
		t.Run(fmt.Sprintf("%d_points", points), func(t *testing.T) {
			grid, err := NewTopologyGrid(sharedTopologyTestTimes(points))
			require.NoError(t, err)
			g := newSharedTimeGraph(sharedTopologyTestConfig(), grid)
			info := cmdb.Matcher{"from_id": "a", "to_id": "b"}
			require.NoError(t, g.AddTimeRelationWithRelation(context.Background(), sharedTopologyTestRelation("calls"), info, grid.timestamps...))
			require.NoError(t, g.AddTimeRelationWithRelation(context.Background(), sharedTopologyTestRelation("calls"), info, grid.timestamps...))
			require.Equal(t, 1, g.shared.edgeCount(), "duplicate observations do not store another relation")
			require.NoError(t, g.AddTimeRelationWithRelation(context.Background(), sharedTopologyTestRelation("other"), info, grid.timestamps...))
			require.Equal(t, 2, g.nodeBuilder.Length())
			require.Equal(t, 2, g.shared.edgeCount())
			require.Equal(t, 2, g.shared.nodeInfoVersionCount)
			frames, err := g.FindSharedTopology(context.Background(), grid, SharedTopologyQuery{SourceType: "entity", SourceMatcher: cmdb.Matcher{"id": "a"}, MaxHops: 1})
			require.NoError(t, err)
			require.Len(t, frames, points)
			for _, frame := range frames {
				require.Len(t, frame.Nodes, 2)
				require.Len(t, frame.Edges, 2)
			}
		})
	}
	grid, err := NewTopologyGrid(sharedTopologyTestTimes(1))
	require.NoError(t, err)
	g := newSharedTimeGraph(sharedTopologyTestConfig(), grid)
	require.ErrorContains(t, g.AddTimeNode(context.Background(), "entity", cmdb.Matcher{"id": "a"}, 0), "does not match")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, g.AddTimeNode(ctx, "entity", cmdb.Matcher{"id": "a"}, grid.timestamps...), context.Canceled)
}
