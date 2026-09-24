// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func newSharedTopologyFixture(t *testing.T) *TimeGraph {
	t.Helper()
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "source", Index: cmdb.Index{"source_id"}},
		{Name: "middle", Index: cmdb.Index{"middle_id"}, Info: cmdb.Index{"middle_name"}},
		{Name: "target", Index: cmdb.Index{"target_id"}},
	}})
	ctx := context.Background()
	require.NoError(t, tg.AddTimeNode(ctx, "source", cmdb.Matcher{"source_id": "a"}, 100, 200, 300))
	require.NoError(t, tg.AddTimeNode(ctx, "middle", cmdb.Matcher{"middle_id": "b"}, 100, 200, 300))
	require.NoError(t, tg.AddTimeNode(ctx, "target", cmdb.Matcher{"target_id": "c"}, 100, 200, 300))
	return tg
}

func addSharedTopologyRelation(t *testing.T, tg *TimeGraph, source, target cmdb.Resource, relationType string, timestamps ...int64) {
	addSharedTopologyRelationWithMetadata(t, tg, source, target, relationType, RelationCategoryStatic, DirectionOutbound, timestamps...)
}

func addSharedTopologyRelationWithMetadata(t *testing.T, tg *TimeGraph, source, target cmdb.Resource, relationType string, category RelationCategory, direction TraversalDirection, timestamps ...int64) {
	t.Helper()
	info := cmdb.Matcher{
		"source_id": "a",
		"middle_id": "b",
		"target_id": "c",
	}
	relation := cmdb.Relation{
		V:            []cmdb.Resource{source, target},
		RelationType: relationType,
		MetricName:   "metric_" + relationType,
		Category:     string(category),
		Direction:    string(direction),
	}
	require.NoError(t, tg.AddTimeRelationWithRelation(context.Background(), relation, info, timestamps...))
}

func hasSharedTopologyNode(snapshot SharedTopologySnapshot, resource cmdb.Resource, key, value string) bool {
	for _, node := range snapshot.Nodes {
		if node.ResourceType == resource && node.Dimensions[key] == value {
			return true
		}
	}
	return false
}

func hasSharedTopologyEdge(snapshot SharedTopologySnapshot, relationType string) bool {
	for _, edge := range snapshot.Edges {
		if edge.RelationType == relationType {
			return true
		}
	}
	return false
}

func TestFindSharedTopologyCases(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(*TimeGraph)
		grid          []int64
		query         SharedTopologyQuery
		wantTargetAt  map[int]bool
		wantEdgesAt   map[int][]string
		wantMiddleAt  map[int]string
		wantPartialAt map[int]string
	}{
		{
			name: "按时间位传播并保留诱导边",
			setup: func(tg *TimeGraph) {
				addSharedTopologyRelation(t, tg, "source", "middle", "source_middle", 100, 200)
				addSharedTopologyRelation(t, tg, "middle", "target", "middle_target", 200, 300)
				addSharedTopologyRelation(t, tg, "source", "target", "source_target", 200)
			},
			grid: []int64{100, 200, 300},
			query: SharedTopologyQuery{
				SourceType:    "source",
				SourceMatcher: cmdb.Matcher{"source_id": "a"},
				MaxHops:       2,
			},
			wantTargetAt: map[int]bool{0: false, 1: true, 2: false},
		},
		{
			name: "节点身份稳定但属性按时间点变化",
			setup: func(tg *TimeGraph) {
				addSharedTopologyRelation(t, tg, "source", "middle", "source_middle", 100, 200)
				require.NoError(t, tg.AddTimeNode(context.Background(), "middle", cmdb.Matcher{"middle_id": "b", "middle_name": "old"}, 100))
				require.NoError(t, tg.AddTimeNode(context.Background(), "middle", cmdb.Matcher{"middle_id": "b", "middle_name": "new"}, 200))
			},
			grid: []int64{100, 200},
			query: SharedTopologyQuery{
				SourceType:    "source",
				SourceMatcher: cmdb.Matcher{"source_id": "a"},
				MaxHops:       1,
			},
			wantMiddleAt: map[int]string{0: "old", 1: "new"},
		},
		{
			name: "一跳结果补齐已达节点之间的边",
			setup: func(tg *TimeGraph) {
				addSharedTopologyRelation(t, tg, "source", "middle", "source_middle", 100, 200)
				addSharedTopologyRelation(t, tg, "middle", "target", "middle_target", 200, 300)
				addSharedTopologyRelation(t, tg, "source", "target", "source_target", 200)
			},
			grid: []int64{100, 200, 300},
			query: SharedTopologyQuery{
				SourceType:    "source",
				SourceMatcher: cmdb.Matcher{"source_id": "a"},
				MaxHops:       1,
			},
			wantEdgesAt: map[int][]string{0: {"source_middle"}, 1: {"middle_target", "source_middle", "source_target"}},
		},
		{
			name: "关系身份过滤",
			setup: func(tg *TimeGraph) {
				addSharedTopologyRelation(t, tg, "source", "middle", "relation_x", 100)
				addSharedTopologyRelation(t, tg, "source", "middle", "relation_y", 100)
			},
			grid: []int64{100},
			query: SharedTopologyQuery{
				SourceType:           "source",
				SourceMatcher:        cmdb.Matcher{"source_id": "a"},
				MaxHops:              1,
				AllowedCategories:    []RelationCategory{RelationCategoryStatic},
				AllowedRelationTypes: []string{"relation_x"},
			},
			wantEdgesAt: map[int][]string{0: {"relation_x"}},
		},
		{
			name: "动态关系方向过滤",
			setup: func(tg *TimeGraph) {
				addSharedTopologyRelationWithMetadata(t, tg, "source", "middle", "dynamic_out", RelationCategoryDynamic, DirectionOutbound, 100)
				addSharedTopologyRelationWithMetadata(t, tg, "source", "middle", "dynamic_in", RelationCategoryDynamic, DirectionInbound, 100)
			},
			grid: []int64{100},
			query: SharedTopologyQuery{
				SourceType:    "source",
				SourceMatcher: cmdb.Matcher{"source_id": "a"},
				MaxHops:       1,
				Direction:     DirectionOutbound,
			},
			wantEdgesAt: map[int][]string{0: {"dynamic_out"}},
		},
		{
			name: "显式传递局部数据不完整状态",
			setup: func(tg *TimeGraph) {
				addSharedTopologyRelation(t, tg, "source", "middle", "source_middle", 100, 200, 300)
			},
			grid: []int64{100, 200, 300},
			query: SharedTopologyQuery{
				SourceType:        "source",
				SourceMatcher:     cmdb.Matcher{"source_id": "a"},
				MaxHops:           1,
				PartialTimestamps: map[int64]string{200: "range query partial"},
			},
			wantPartialAt: map[int]string{1: "range query partial"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tg := newSharedTopologyFixture(t)
			tt.setup(tg)
			grid, err := NewTopologyGrid(tt.grid)
			require.NoError(t, err)
			results, err := tg.FindSharedTopology(context.Background(), grid, tt.query)
			require.NoError(t, err)
			require.Len(t, results, len(tt.grid))

			for index, want := range tt.wantTargetAt {
				require.Equal(t, want, hasSharedTopologyNode(results[index], "target", "target_id", "c"), "目标节点时间点 %d", index)
			}
			for index, wantEdges := range tt.wantEdgesAt {
				for _, relationType := range wantEdges {
					require.True(t, hasSharedTopologyEdge(results[index], relationType), "时间点 %d 应包含关系 %s", index, relationType)
				}
			}
			for index, wantName := range tt.wantMiddleAt {
				found := false
				for _, node := range results[index].Nodes {
					if node.ResourceType != "middle" {
						continue
					}
					found = true
					require.Equal(t, wantName, node.Dimensions["middle_name"], "时间点 %d 的节点属性", index)
				}
				require.True(t, found, "时间点 %d 应包含 middle 节点", index)
			}
			for index, reason := range tt.wantPartialAt {
				require.True(t, results[index].Partial, "时间点 %d 应标记 partial", index)
				require.Equal(t, reason, results[index].PartialReason)
			}
		})
	}
}

func TestNewTopologyGridBoundaryCases(t *testing.T) {
	oldMaxPoints := MaxSharedTopologyPoints
	MaxSharedTopologyPoints = 60
	t.Cleanup(func() { MaxSharedTopologyPoints = oldMaxPoints })

	tests := []struct {
		name       string
		maxPoints  int
		pointCount int
		timestamps []int64
		wantErr    bool
	}{
		{name: "配置值允许 60 点", maxPoints: 60, pointCount: 60},
		{name: "超过配置上限", maxPoints: 60, pointCount: 61, wantErr: true},
		{name: "重复时间点", maxPoints: 60, timestamps: []int64{100, 100}, wantErr: true},
		{name: "配置值限制为 3 点", maxPoints: 3, pointCount: 3},
		{name: "超过自定义配置值", maxPoints: 3, pointCount: 4, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			MaxSharedTopologyPoints = tt.maxPoints
			if tt.timestamps == nil {
				tt.timestamps = make([]int64, tt.pointCount)
			}
			for index := range tt.timestamps {
				if tt.name != "重复时间点" {
					tt.timestamps[index] = int64(index + 1)
				}
			}
			_, err := NewTopologyGrid(tt.timestamps)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestFindSharedTopologyValidationCases(t *testing.T) {
	tg := newSharedTopologyFixture(t)
	grid, err := NewTopologyGrid([]int64{100, 200, 300})
	require.NoError(t, err)

	tests := []struct {
		name  string
		query SharedTopologyQuery
	}{
		{name: "超过最大跳数", query: SharedTopologyQuery{SourceType: "source", MaxHops: MaxAllowedHops + 1}},
		{name: "缺少源资源类型", query: SharedTopologyQuery{MaxHops: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tg.FindSharedTopology(context.Background(), grid, tt.query)
			require.Error(t, err)
		})
	}
}

func TestFindSharedTopologyBoundaryCases(t *testing.T) {
	oldMaxPoints := MaxSharedTopologyPoints
	MaxSharedTopologyPoints = 60
	t.Cleanup(func() { MaxSharedTopologyPoints = oldMaxPoints })
	timestamps := make([]int64, 60)
	for index := range timestamps {
		timestamps[index] = int64(index + 1)
	}
	tests := []struct {
		name      string
		cancelled bool
		wantErr   bool
	}{
		{name: "第 60 个时间位可用"},
		{name: "调用前取消", cancelled: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
				{Name: "source", Index: cmdb.Index{"source_id"}},
				{Name: "middle", Index: cmdb.Index{"middle_id"}},
			}})
			require.NoError(t, tg.AddTimeNode(context.Background(), "source", cmdb.Matcher{"source_id": "a"}, timestamps...))
			require.NoError(t, tg.AddTimeNode(context.Background(), "middle", cmdb.Matcher{"middle_id": "b"}, timestamps...))
			addSharedTopologyRelation(t, tg, "source", "middle", "source_middle", timestamps...)
			grid, err := NewTopologyGrid(timestamps)
			require.NoError(t, err)
			results, err := tg.FindSharedTopology(ctx, grid, SharedTopologyQuery{
				SourceType:    "source",
				SourceMatcher: cmdb.Matcher{"source_id": "a"},
				MaxHops:       1,
			})
			if tt.wantErr {
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			require.NoError(t, err)
			require.Len(t, results, 60)
			require.True(t, hasSharedTopologyNode(results[59], "middle", "middle_id", "b"))
		})
	}
}

// BenchmarkFindSharedTopologyGridSizes 用固定规模的星形关系比较不同 K
// 对共享拓扑计算阶段的分配和耗时；VM 取数不在此基准范围内。
func BenchmarkFindSharedTopologyGridSizes(b *testing.B) {
	for _, testCase := range []struct {
		name string
		K    int
	}{
		{name: "K1", K: 1},
		{name: "K30", K: 30},
		{name: "K60", K: 60},
	} {
		b.Run(testCase.name, func(b *testing.B) {
			oldMaxPoints := MaxSharedTopologyPoints
			MaxSharedTopologyPoints = 60
			b.Cleanup(func() { MaxSharedTopologyPoints = oldMaxPoints })
			timestamps := make([]int64, testCase.K)
			for index := range timestamps {
				timestamps[index] = int64(index + 1)
			}
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
				{Name: "source", Index: cmdb.Index{"source_id"}},
				{Name: "middle", Index: cmdb.Index{"middle_id"}},
			}})
			ctx := context.Background()
			require.NoError(b, tg.AddTimeNode(ctx, "source", cmdb.Matcher{"source_id": "a"}, timestamps...))
			for index := 0; index < 200; index++ {
				middleID := fmt.Sprintf("middle-%d", index)
				require.NoError(b, tg.AddTimeRelationWithRelation(ctx, cmdb.Relation{
					V:            []cmdb.Resource{"source", "middle"},
					RelationType: "source_to_middle",
					MetricName:   "source_to_middle_flow",
					Category:     string(RelationCategoryStatic),
					Direction:    string(DirectionOutbound),
				}, cmdb.Matcher{"source_id": "a", "middle_id": middleID}, timestamps...))
			}
			grid, err := NewTopologyGrid(timestamps)
			require.NoError(b, err)
			query := SharedTopologyQuery{
				SourceType:    "source",
				SourceMatcher: cmdb.Matcher{"source_id": "a"},
				MaxHops:       1,
			}
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				_, err = tg.FindSharedTopology(ctx, grid, query)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
