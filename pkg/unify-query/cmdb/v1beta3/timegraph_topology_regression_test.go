// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestSharedTopologyBackendGridCases(t *testing.T) {
	for _, tt := range []struct {
		name       string
		start, end int64
		step       string
		shift      bool
		wantErr    string
	}{
		{name: "非整步起点", start: 1700000001, end: 1700000121, step: "1m"},
		{name: "短步长与六十点", start: 1700000001, end: 1700000591, step: "10s"},
		{name: "整秒毫秒格式", start: 1700000001000, end: 1700000121000, step: "1m"},
		{name: "instant 毫秒格式", start: 1700000000000},
		{name: "instant 非整秒拒绝", start: 1700000000123, wantErr: "whole-second precision"},
		{name: "range 非整秒拒绝", start: 1700000000123, end: 1700000060123, step: "1m", wantErr: "whole-second precision"},
		{name: "纳秒 step 拒绝", start: 1700000000, end: 1700000060, step: "1ns", wantErr: "whole number of seconds"},
		{name: "微秒 step 拒绝", start: 1700000000, end: 1700000060, step: "500us", wantErr: "whole number of seconds"},
		{name: "毫秒 step 拒绝", start: 1700000000, end: 1700000060, step: "1ms", wantErr: "whole number of seconds"},
		{name: "非整秒 step 拒绝", start: 1700000000, end: 1700000060, step: "1500ms", wantErr: "whole number of seconds"},
		{name: "零 step 拒绝", start: 1700000000, end: 1700000060, step: "0s", wantErr: "step must be positive"},
		{name: "负 step 拒绝", start: 1700000000, end: 1700000060, step: "-1s", wantErr: "step must be positive"},
		{name: "偏移样本不能成为完整空图", start: 1700000001, end: 1700000121, step: "1m", shift: true, wantErr: "does not match topology time grid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := withTimeGraphTargetInfoShow(initTimeGraphQueryTestEnvironment(), true)
			model := &Model{schemaProvider: sharedTopologyQueryProvider(), timeGraphQueryReference: timeGraphTestQueryReference}
			request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, MaxHops: 2, Step: tt.step}
			if tt.end == 0 {
				request.Timestamp = tt.start
			} else {
				request.StartTime, request.EndTime = tt.start, tt.end
			}
			calls := make(map[string]bool)
			model.timeGraphVMQuery = func(queryCtx context.Context, q *structured.QueryTs, _ string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
				require.True(t, metadata.IsExactTimeGrid(queryCtx))
				require.True(t, q.NotTimeAlign)
				wantStart, err := parseTimestamp(q.Start)
				require.NoError(t, err)
				require.Equal(t, wantStart, start.UnixMilli())
				var timestamps []int64
				for ts := start; !ts.After(end); ts = ts.Add(step) {
					timestamps = append(timestamps, ts.UnixMilli())
				}
				if instant {
					require.Equal(t, start, end)
				}
				if tt.shift {
					timestamps[0]--
				}
				field := q.QueryList[0].FieldName
				calls[field] = true
				labels := map[string]map[string]string{
					"source_info_relation": {"source_id": "a"},
					"source_middle_flow":   {"source_id": "a", "middle_id": "b"},
					"middle_target_flow":   {"middle_id": "b", "target_id": "c"},
					"middle_info_relation": {"middle_id": "b"},
					"target_info_relation": {"target_id": "c"},
				}
				require.Contains(t, labels, field)
				return contractMatrix(labels[field], timestamps...), nil
			}
			var result cmdb.SharedTopologyResult
			var err error
			require.NotPanics(t, func() { result, err = model.QuerySharedTopology(ctx, request) })
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Empty(t, result.Snapshots)
				if !tt.shift {
					require.Empty(t, calls, "不支持的精度必须在取数前拒绝")
				}
				return
			}
			require.NoError(t, err)
			require.Len(t, calls, 5, "源信息、关系边、目标信息都使用相同网格")
			wantStart, err := normalizeTopologyTimestamp(tt.start)
			require.NoError(t, err)
			require.Equal(t, wantStart, result.StartTime)
			require.NotEmpty(t, result.Snapshots)
			for _, snapshot := range result.Snapshots {
				require.Len(t, snapshot.Nodes, 3)
				require.Len(t, snapshot.Edges, 2)
				require.False(t, snapshot.Partial)
			}
		})
	}
}

func TestSharedTopologyRepeatedResourceCases(t *testing.T) {
	for _, tt := range []struct {
		name      string
		direction string
		hops      int
		edges     [][2]string
		wantEdges int
	}{
		{"同类型多跳出向", "outbound", 2, [][2]string{{"a", "b"}, {"b", "c"}}, 2},
		{"同类型多跳入向", "inbound", 2, [][2]string{{"b", "a"}, {"c", "b"}}, 2},
		{"一跳边界诱导边", "outbound", 1, [][2]string{{"a", "b"}, {"a", "c"}, {"b", "c"}}, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := &Model{schemaProvider: publicDynamicSelfProvider(), timeGraphQueryReference: timeGraphTestQueryReference}
			model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, end time.Time, _ time.Duration) (pl.Matrix, error) {
				if q.QueryList[0].FieldName == "service_info_relation" {
					return contractMatrix(map[string]string{"id": "a"}, end.UnixMilli()), nil
				}
				var matrix pl.Matrix
				for _, edge := range tt.edges {
					matrix = append(matrix, contractMatrix(map[string]string{"from_id": edge[0], "to_id": edge[1]}, end.UnixMilli())...)
				}
				return filterTopologyRegressionMatrix(q, matrix), nil
			}
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: 1700000000, SourceType: "service", SourceInfo: cmdb.Matcher{"id": "a"}, MaxHops: tt.hops, DynamicRelationDirection: tt.direction})
			require.NoError(t, err)
			require.Len(t, result.Snapshots, 1)
			var ids []string
			for _, node := range result.Snapshots[0].Nodes {
				ids = append(ids, node.Dimensions["id"])
			}
			require.ElementsMatch(t, []string{"a", "b", "c"}, ids)
			require.Len(t, result.Snapshots[0].Edges, tt.wantEdges)
		})
	}
}

func TestSharedTopologyReturnsToSourceTypeCases(t *testing.T) {
	for _, tt := range []struct {
		name      string
		hops      int
		wantNodes int
		wantEdges int
	}{
		{"一跳保持种子过滤", 1, 2, 1},
		{"回到起点类型并补齐诱导边", 2, 3, 3},
		{"回到起点类型后继续展开", 3, 4, 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider := sharedTopologyQueryProvider().(contractSchemaProvider)
			provider.schemas = []RelationSchema{
				{RelationType: "forward", Category: RelationCategoryStatic, FromType: "source", ToType: "middle", IsDirectional: true, MetricName: "forward_flow"},
				{RelationType: "back", Category: RelationCategoryStatic, FromType: "middle", ToType: "source", IsDirectional: true, MetricName: "back_flow"},
			}
			model := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
			model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, end time.Time, _ time.Duration) (pl.Matrix, error) {
				var matrix pl.Matrix
				switch q.QueryList[0].FieldName {
				case "source_info_relation":
					matrix = contractMatrix(map[string]string{"source_id": "a"}, end.UnixMilli())
				case "forward_flow":
					for _, edge := range [][2]string{{"a", "b"}, {"c", "b"}, {"c", "d"}} {
						matrix = append(matrix, contractMatrix(map[string]string{"source_id": edge[0], "middle_id": edge[1]}, end.UnixMilli())...)
					}
				case "back_flow":
					matrix = contractMatrix(map[string]string{"middle_id": "b", "source_id": "c"}, end.UnixMilli())
				}
				return filterTopologyRegressionMatrix(q, matrix), nil
			}
			result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: 1700000000, SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, MaxHops: tt.hops})
			require.NoError(t, err)
			require.Len(t, result.Snapshots[0].Nodes, tt.wantNodes)
			require.Len(t, result.Snapshots[0].Edges, tt.wantEdges)
		})
	}
}

// filterTopologyRegressionMatrix 模拟后端实际执行下推条件，避免 fixture 掩盖漏边。
func filterTopologyRegressionMatrix(q *structured.QueryTs, matrix pl.Matrix) pl.Matrix {
	var result pl.Matrix
	for _, series := range matrix {
		matched := true
		for _, field := range q.QueryList[0].Conditions.FieldList {
			equal := series.Metric.Get(field.DimensionName) == field.Value[0]
			if field.Operator == structured.ConditionEqual {
				matched = matched && equal
			} else if field.Operator == structured.ConditionNotEqual {
				matched = matched && !equal
			}
		}
		if matched {
			result = append(result, series)
		}
	}
	return result
}

func TestSharedTopologyInstantPartialCases(t *testing.T) {
	for _, tt := range []struct {
		name, partialMetric string
		empty               bool
	}{
		{name: "完整响应"},
		{name: "源节点 partial", partialMetric: "source_info_relation"},
		{name: "关系 partial", partialMetric: "source_middle_flow"},
		{name: "目标信息 partial", partialMetric: "middle_info_relation"},
		{name: "空且 partial", partialMetric: "source_info_relation", empty: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := withTimeGraphTargetInfoShow(initTimeGraphQueryTestEnvironment(), true)
			model := &Model{schemaProvider: sharedTopologyQueryProvider(), timeGraphQueryReference: timeGraphTestQueryReference}
			model.timeGraphVMQueryWithPartial = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, end time.Time, _ time.Duration) (pl.Matrix, bool, error) {
				field := q.QueryList[0].FieldName
				partial := field == tt.partialMetric
				if tt.empty {
					return nil, partial, nil
				}
				labels := map[string]map[string]string{
					"source_info_relation": {"source_id": "a"},
					"source_middle_flow":   {"source_id": "a", "middle_id": "b"},
					"middle_info_relation": {"middle_id": "b"},
				}
				if value, ok := labels[field]; ok {
					return contractMatrix(value, end.UnixMilli()), partial, nil
				}
				return nil, partial, nil
			}
			outcome := "success"
			if tt.partialMetric != "" {
				outcome = "partial"
			}
			labels := map[string]string{"scope": "query", "query_mode": "instant", "result": outcome}
			before := readTimeGraphMetric(t, "cmdb_topology_operations_total", labels, "counter")
			result, err := model.QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: 1700000000, SourceType: "source", MaxHops: 1})
			require.NoError(t, err)
			require.Equal(t, before+1, readTimeGraphMetric(t, "cmdb_topology_operations_total", labels, "counter"))
			require.Equal(t, tt.partialMetric != "", result.Snapshots[0].Partial)
			if tt.partialMetric != "" {
				require.Equal(t, "backend_partial", result.Snapshots[0].PartialReason)
			}
		})
	}
}

type topologyLegacyInstantBackend struct {
	tsdb.Instance
}

func (topologyLegacyInstantBackend) DirectQuery(context.Context, string, time.Time) (pl.Vector, error) {
	return pl.Vector{}, nil
}

type topologyPartialInstantBackend struct {
	topologyLegacyInstantBackend
}

func (topologyPartialInstantBackend) DirectQueryWithPartial(context.Context, string, time.Time) (pl.Vector, bool, error) {
	return pl.Vector{}, true, nil
}

func TestTimeGraphInstantBackendCompletenessCases(t *testing.T) {
	for _, tt := range []struct {
		name        string
		instance    tsdb.Instance
		exact       bool
		wantPartial bool
		wantError   bool
	}{
		{"保留后端 partial", topologyPartialInstantBackend{}, true, true, false},
		{"拓扑拒绝未知完整性", topologyLegacyInstantBackend{}, true, false, true},
		{"旧路径保留兼容", topologyLegacyInstantBackend{}, false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.exact {
				ctx = metadata.WithExactTimeGrid(ctx)
			}
			_, partial, err := queryTimeGraphInstant(ctx, tt.instance, "up", time.Unix(1700000000, 0))
			require.Equal(t, tt.wantPartial, partial)
			if tt.wantError {
				require.ErrorContains(t, err, "does not report result completeness")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
