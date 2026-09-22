// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestTimeGraphPathExpansionBudgetCases(t *testing.T) {
	for _, tt := range []struct {
		name                            string
		width, depth, limit, timestamps int
		duplicate, canceled, deadEnd    bool
		wantCount                       int
		wantReason                      string
	}{
		{name: "恰好预算", width: 2, depth: 2, limit: 4, timestamps: 1, wantCount: 4},
		{name: "汇合路径全部保留", width: 2, depth: 3, limit: 8, timestamps: 1, wantCount: 8},
		{name: "重复计划不重复消耗结果预算", width: 2, depth: 2, limit: 4, timestamps: 1, duplicate: true, wantCount: 4},
		{name: "展开前拒绝指数级路径", width: 8, depth: 5, limit: 100, timestamps: 1, wantReason: "max_graph_results"},
		{name: "中间层也受预算约束", width: 4, depth: 2, limit: 3, timestamps: 1, deadEnd: true, wantReason: "max_graph_results"},
		{name: "跨时间累计结果预算", width: 2, depth: 1, limit: 3, timestamps: 2, wantReason: "max_graph_results"},
		{name: "提前取消", width: 2, depth: 2, limit: 4, timestamps: 1, canceled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			config := &TimeGraphConfig{MaxResults: tt.limit}
			steps := make([]cmdb.RelationPathStep, 0, tt.depth+1)
			for depth := 0; depth <= tt.depth; depth++ {
				resource := cmdb.Resource(fmt.Sprintf("r%d", depth))
				config.Resource = append(config.Resource, TimeGraphResourceConfig{Name: resource, Index: cmdb.Index{fmt.Sprintf("id%d", depth)}})
				steps = append(steps, cmdb.RelationPathStep{ResourceType: resource})
			}
			graph := NewTimeGraphWithConfig(config)
			defer graph.Clean(ctx)
			for point := 0; point < tt.timestamps; point++ {
				for depth := 1; depth <= tt.depth; depth++ {
					previousWidth := tt.width
					if depth == 1 {
						previousWidth = 1
					}
					for previous := 0; previous < previousWidth; previous++ {
						for next := 0; next < tt.width; next++ {
							if tt.deadEnd && depth == tt.depth && (previous != 0 || next != 0) {
								continue
							}
							info := cmdb.Matcher{fmt.Sprintf("id%d", depth-1): fmt.Sprint(previous), fmt.Sprintf("id%d", depth): fmt.Sprint(next)}
							err := graph.AddTimeRelation(ctx, steps[depth-1].ResourceType, steps[depth].ResourceType, info, 1700000000000+int64(point)*60000)
							require.NoError(t, err)
						}
					}
				}
			}
			if tt.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			paths := []cmdb.RelationPath{{Steps: steps}}
			if tt.duplicate {
				paths = append(paths, paths[0])
			}
			results, err := graph.FindRelationPathResources(ctx, "r0", []cmdb.Resource{steps[len(steps)-1].ResourceType}, cmdb.Matcher{"id0": "0"}, paths)
			if tt.canceled {
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			if tt.wantReason != "" {
				var limitErr *ResultLimitError
				require.ErrorAs(t, err, &limitErr)
				require.Equal(t, tt.wantReason, limitErr.Reason)
				require.Equal(t, tt.limit+1, limitErr.Count)
				require.Equal(t, tt.limit, limitErr.Limit)
				if tt.timestamps == 1 {
					require.Equal(t, "path expansion", limitErr.Path)
				}
				require.Empty(t, results)
				return
			}
			require.NoError(t, err)
			require.Len(t, results, tt.wantCount)
		})
	}
}

func TestTimeGraphExplicitPathDirectionCases(t *testing.T) {
	for _, tt := range []struct {
		name        string
		category    RelationCategory
		directional bool
		reverse     bool
		typed       bool
		wantErr     bool
	}{
		{name: "单向静态正向", category: RelationCategoryStatic, directional: true},
		{name: "单向静态反向拒绝", category: RelationCategoryStatic, directional: true, reverse: true, wantErr: true},
		{name: "关系计划反向同样拒绝", category: RelationCategoryStatic, directional: true, reverse: true, typed: true, wantErr: true},
		{name: "无向静态允许反向", category: RelationCategoryStatic, reverse: true},
		{name: "动态允许入向", category: RelationCategoryDynamic, directional: true, reverse: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider := sharedTopologyQueryProvider().(contractSchemaProvider)
			provider.schemas = provider.schemas[:1]
			provider.schemas[0].Category = tt.category
			provider.schemas[0].IsDirectional = tt.directional
			model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
			calls := 0
			model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, end time.Time, _ time.Duration) (pl.Matrix, error) {
				calls++
				require.Equal(t, "source_middle_flow", q.QueryList[0].FieldName)
				labels := map[string]string{"source_id": "a", "middle_id": "b"}
				if tt.category == RelationCategoryDynamic {
					labels = map[string]string{"from_source_id": "a", "to_middle_id": "b"}
				}
				return filterTopologyRegressionMatrix(q, contractMatrix(labels, end.UnixMilli())), nil
			}
			path := []cmdb.Resource{"source", "middle"}
			matcher := cmdb.Matcher{"source_id": "a"}
			if tt.reverse {
				path = []cmdb.Resource{"middle", "source"}
				matcher = cmdb.Matcher{"middle_id": "b"}
			}
			ctx := initTimeGraphQueryTestEnvironment()
			var results []cmdb.PathResourcesResult
			var err error
			if tt.typed {
				plans := cmdb.RelationPathsFromResourcePaths([][]cmdb.Resource{path})
				plans[0].Steps[1].RelationType = "source_to_middle"
				plans[0].Steps[1].Direction = string(DirectionInbound)
				results, err = model.QueryRelationPathResources(ctx, "5m", "space", "1700000000", path[0], []cmdb.Resource{path[1]}, plans, matcher)
			} else {
				results, err = model.QueryPathResources(ctx, "5m", "space", "1700000000", path[0], []cmdb.Resource{path[1]}, [][]cmdb.Resource{path}, matcher)
			}
			if tt.wantErr {
				require.ErrorContains(t, err, "is not allowed by schema")
				require.Empty(t, results)
				require.Zero(t, calls, "禁止通过默认指标 fallback 绕过方向校验")
				return
			}
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Equal(t, path[1], results[0].TargetType)
		})
	}
}
