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

// 逐层集合推进不枚举实例路径：路径条数按宽度指数增长时，结果仍只是各时刻
// 去重后的终点集合，不再有结果数或路径展开上限。
func TestTimeGraphRelationTargetFanOutCases(t *testing.T) {
	for _, tt := range []struct {
		name                         string
		width, depth, timestamps     int
		duplicate, canceled, deadEnd bool
		wantTargets                  []string
	}{
		{name: "两层全连接", width: 2, depth: 2, timestamps: 1, wantTargets: []string{"0", "1"}},
		{name: "汇合路径收敛为终点集合", width: 2, depth: 3, timestamps: 1, wantTargets: []string{"0", "1"}},
		{name: "重复计划不重复产出目标", width: 2, depth: 2, timestamps: 1, duplicate: true, wantTargets: []string{"0", "1"}},
		{name: "指数级路径只产出终点", width: 8, depth: 5, timestamps: 1, wantTargets: []string{"0", "1", "2", "3", "4", "5", "6", "7"}},
		{name: "中间层扩散后末层收窄", width: 4, depth: 2, timestamps: 1, deadEnd: true, wantTargets: []string{"0"}},
		{name: "逐时刻分别产出", width: 2, depth: 1, timestamps: 2, wantTargets: []string{"0", "1"}},
		{name: "提前取消", width: 2, depth: 2, timestamps: 1, canceled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			config := &TimeGraphConfig{}
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
			targetType := steps[len(steps)-1].ResourceType
			results, err := graph.FindRelationTargets(ctx, "r0", []cmdb.Resource{targetType}, cmdb.Matcher{"id0": "0"}, paths)
			if tt.canceled {
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			require.NoError(t, err)
			var want []RelationTarget
			for point := 0; point < tt.timestamps; point++ {
				for _, id := range tt.wantTargets {
					want = append(want, RelationTarget{
						Timestamp:  1700000000000 + int64(point)*60000,
						TargetType: targetType,
						Target:     cmdb.PathNode{ResourceType: targetType, Dimensions: cmdb.Matcher{fmt.Sprintf("id%d", tt.depth): id}},
					})
				}
			}
			require.Equal(t, want, results)
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
			var results []RelationTarget
			var err error
			if tt.typed {
				plans := cmdb.RelationPathsFromResourcePaths([][]cmdb.Resource{path})
				plans[0].Steps[1].RelationType = "source_to_middle"
				plans[0].Steps[1].Direction = string(DirectionInbound)
				results, err = model.QueryRelationTimeGraph(
					ctx, "5m", "space",
					time.Unix(1700000000, 0), time.Unix(1700000000, 0), 5*time.Minute,
					path[0], []cmdb.Resource{path[1]},
					plans,
					matcher, nil,
				)
			} else {
				results, err = model.QueryRelationTimeGraph(
					ctx, "5m", "space",
					time.Unix(1700000000, 0), time.Unix(1700000000, 0), 5*time.Minute,
					path[0], []cmdb.Resource{path[1]},
					cmdb.RelationPathsFromResourcePaths([][]cmdb.Resource{path}),
					matcher, nil,
				)
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
