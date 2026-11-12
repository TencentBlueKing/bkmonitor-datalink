// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// noRouteTopologyModel 让 missing 关系没有物理路由，其余关系按下推条件和请求网格
// 返回 data；calls 记录实际到达后端的指标。missing 为空时所有关系都有路由。
func noRouteTopologyModel(t *testing.T, provider SchemaProvider, data map[string]pl.Matrix, missing string, calls map[string]int) *Model {
	t.Helper()
	model := &Model{schemaProvider: provider}
	model.timeGraphQueryReference = func(ctx context.Context, ts *structured.QueryTs) (metadata.QueryReference, error) {
		if missing != "" && ts.QueryList[0].FieldName == missing {
			return metadata.QueryReference{}, nil
		}
		return selectorQueryReference(ctx, ts)
	}
	model.timeGraphVMQuery = func(_ context.Context, ts *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
		name := ts.QueryList[0].FieldName
		calls[name]++
		var matrix pl.Matrix
		for _, series := range filteredTimeGraphMatrix(t, data[name], ts.QueryList[0].Conditions) {
			kept := pl.Series{Metric: series.Metric}
			for _, point := range series.Points {
				if point.T >= start.UnixMilli() && point.T <= end.UnixMilli() && (point.T-start.UnixMilli())%step.Milliseconds() == 0 {
					kept.Points = append(kept.Points, point)
				}
			}
			if len(kept.Points) > 0 {
				matrix = append(matrix, kept)
			}
		}
		return matrix, nil
	}
	return model
}

// topologyObjectsForTest 去掉质量标记后比较节点和边：缺路由只能改变 Partial，
// 不能改变其他关系的结果。
func topologyObjectsForTest(t *testing.T, snapshots []cmdb.SharedTopologySnapshot) []string {
	t.Helper()
	stripped := make([]cmdb.SharedTopologySnapshot, len(snapshots))
	for i, snapshot := range snapshots {
		snapshot.Partial, snapshot.PartialReason = false, ""
		stripped[i] = snapshot
	}
	return canonicalTopologyForTest(t, stripped)
}

func noRouteSharedTopologyProvider() contractSchemaProvider {
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.resources = append(provider.resources, "peer")
	provider.primary["peer"] = []string{"peer_id"}
	provider.fields["peer"] = []string{"peer_id"}
	provider.schemas = append(provider.schemas, RelationSchema{
		RelationType: "source_to_peer", Category: RelationCategoryStatic,
		FromType: "source", ToType: "peer", IsDirectional: true, MetricName: "source_peer_flow",
	})
	return provider
}

func noRouteSharedTopologyData() map[string]pl.Matrix {
	return map[string]pl.Matrix{
		"source_middle_flow": contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000, 1700000100000),
		"middle_target_flow": contractMatrix(map[string]string{"middle_id": "b", "target_id": "c"}, 1700000100000, 1700000200000),
		"source_peer_flow":   contractMatrix(map[string]string{"source_id": "a", "peer_id": "p"}, 1700000000000, 1700000100000, 1700000200000),
	}
}

// 范围内一条关系缺少物理路由（首跳或第二跳）：该关系按已读且无数据处理，其他关系的
// 节点和边与它有路由但无数据时一致；每帧带该关系的原因，紧凑格式携带同样的 partial，
// 且该关系的指标不访问后端。
func TestSharedTopologyRelationWithoutRouteKeepsOtherRelations(t *testing.T) {
	old := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = old })
	for _, missing := range []string{"source_middle_flow", "middle_target_flow"} {
		for _, planned := range []bool{false, true} {
			for _, mode := range []string{"instant", "range"} {
				t.Run(fmt.Sprintf("%s/planned=%v/%s", missing, planned, mode), func(t *testing.T) {
					SharedTopologyPlanCandidates = planned
					request := cmdb.SharedTopologyQuery{
						SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
						Timestamp: 1700000100, MaxHops: 2,
					}
					points := 1
					if mode == "range" {
						request.Timestamp, request.StartTime, request.EndTime, request.Step = 0, 1700000000, 1700000200, "100s"
						points = 3
					}
					data := noRouteSharedTopologyData()
					delete(data, missing)
					reason := "relation_no_physical_route:" + missing

					baselineCalls := map[string]int{}
					baseline, err := noRouteTopologyModel(t, noRouteSharedTopologyProvider(), data, "", baselineCalls).
						QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
					require.NoError(t, err)
					require.Positive(t, baselineCalls[missing], "the routed baseline reads the relation and finds nothing")

					calls := map[string]int{}
					model := noRouteTopologyModel(t, noRouteSharedTopologyProvider(), data, missing, calls)
					result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
					require.NoError(t, err)
					require.Zero(t, calls[missing], "an unrouted relation never reaches the backend")
					require.Positive(t, calls["source_peer_flow"])
					requireNoRouteKeepsBaselineReads(t, missing, baselineCalls, calls)
					require.Len(t, result.Snapshots, points)
					require.Equal(t, topologyObjectsForTest(t, baseline.Snapshots), topologyObjectsForTest(t, result.Snapshots))
					for i, snapshot := range result.Snapshots {
						require.False(t, baseline.Snapshots[i].Partial)
						require.NotEmpty(t, snapshot.Nodes, "the routed relation is still returned")
						require.True(t, snapshot.Partial)
						require.Equal(t, reason, snapshot.PartialReason)
					}

					request.ResponseFormat = cmdb.CompactTopologyFormat
					compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
					require.NoError(t, err)
					require.NotNil(t, compact.Compact)
					require.Len(t, compact.Compact.Partial, points)
					for i, partial := range compact.Compact.Partial {
						require.Equal(t, cmdb.CompactTopologyPartial{Index: i, Reason: reason}, partial)
					}
					require.Equal(t, canonicalTopologyForTest(t, result.Snapshots), canonicalTopologyForTest(t, decodeCompactForTest(t, compact.Compact)))
					require.Zero(t, calls[missing])
				})
			}
		}
	}
}

// 候选计划的每条取数路径（星形前沿、二部图第二阶段的身份分批与宽读取、有根批量、
// 无根批量）都把缺路由关系按已读且无数据处理：结果与该关系有路由但无数据时一致，
// 只多出 partial 原因；身份分批不会因缺路由拆分重试；与不使用候选计划的结果一致。
func TestTopologySelectorRelationWithoutRouteOnEveryFetchPath(t *testing.T) {
	old := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = old })
	star := selectorProvider()
	star.schemas = []RelationSchema{
		{RelationType: "ab", Category: RelationCategoryStatic, FromType: "a", ToType: "b", MetricName: "ab"},
		{RelationType: "ac", Category: RelationCategoryStatic, FromType: "a", ToType: "c", MetricName: "ac"},
	}
	fixtures := func(count int) map[string]pl.Matrix {
		data := map[string]pl.Matrix{}
		for i := 0; i < count; i++ {
			data["ab"] = append(data["ab"], contractMatrix(map[string]string{"a_id": "root", "b_id": fmt.Sprint(i)}, 1700000000000)...)
			data["ab"] = append(data["ab"], contractMatrix(map[string]string{"a_id": "other", "b_id": fmt.Sprint(i)}, 1700000000000)...)
		}
		return data
	}
	root := cmdb.Matcher{"a_id": "root"}
	for _, tc := range []struct {
		name     string
		provider contractSchemaProvider
		missing  string
		count    int
		hops     int
		root     cmdb.Matcher
	}{
		{name: "star", provider: star, missing: "ac", count: 1, hops: 1, root: root},
		{name: "bipartite-identity-batches", provider: selectorProvider(), missing: "bc", count: 2, hops: 2, root: root},
		{name: "bipartite-wide", provider: selectorProvider(), missing: "bc", count: timeGraphWideFrontierThreshold + 2, hops: 2, root: root},
		{name: "bulk", provider: selectorProvider(), missing: "bc", count: 2, hops: 6, root: root},
		{name: "bulk-without-root-filter", provider: selectorProvider(), missing: "bc", count: 2, hops: 2},
		// 首跳的另一条关系缺路由：二部图第二阶段仍按身份分批，不退回宽读取。
		{name: "bipartite-first-hop-unrouted", provider: selectorWithSecondFirstHop(), missing: "ad", count: 2, hops: 2, root: root},
		{name: "bipartite-first-hop-unrouted-wide", provider: selectorWithSecondFirstHop(), missing: "ad", count: timeGraphWideFrontierThreshold + 2, hops: 2, root: root},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: tc.root, Timestamp: 1700000000, MaxHops: tc.hops}
			SharedTopologyPlanCandidates = true
			baselineCalls := map[string]int{}
			baseline, err := noRouteTopologyModel(t, tc.provider, fixtures(tc.count), "", baselineCalls).
				QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.NoError(t, err)
			require.NotEmpty(t, baseline.Snapshots[0].Edges)
			require.False(t, baseline.Snapshots[0].Partial)
			require.Positive(t, baselineCalls[tc.missing], "the relation is in scope")

			var unplanned []string
			for _, planned := range []bool{false, true} {
				SharedTopologyPlanCandidates = planned
				calls := map[string]int{}
				result, err := noRouteTopologyModel(t, tc.provider, fixtures(tc.count), tc.missing, calls).
					QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
				require.NoError(t, err)
				require.Zero(t, calls[tc.missing], "an unrouted relation never reaches the backend")
				require.Positive(t, calls["ab"])
				if planned {
					requireNoRouteKeepsBaselineReads(t, tc.missing, baselineCalls, calls)
				}
				require.Equal(t, topologyObjectsForTest(t, baseline.Snapshots), topologyObjectsForTest(t, result.Snapshots))
				require.True(t, result.Snapshots[0].Partial)
				require.Equal(t, "relation_no_physical_route:"+tc.missing, result.Snapshots[0].PartialReason)
				if !planned {
					unplanned = canonicalTopologyForTest(t, result.Snapshots)
				} else {
					require.Equal(t, unplanned, canonicalTopologyForTest(t, result.Snapshots))
				}
			}
		})
	}
}

// requireNoRouteKeepsBaselineReads 断言缺路由只去掉该关系自身的读取：其余每条关系的读取
// 次数与该关系有路由但无数据时相同，取数计划不因缺路由改变。
func requireNoRouteKeepsBaselineReads(t *testing.T, missing string, baseline, calls map[string]int) {
	t.Helper()
	for metric, count := range baseline {
		if metric != missing {
			require.Equal(t, count, calls[metric], "%s is read as often as with a routed but empty %s", metric, missing)
		}
	}
	for metric := range calls {
		require.Contains(t, baseline, metric)
	}
}

// selectorWithSecondFirstHop 在 a-b-c 二部图上再给根类型 a 加一条首跳关系 a-d。
func selectorWithSecondFirstHop() contractSchemaProvider {
	provider := selectorProvider()
	provider.resources = append(provider.resources, "d")
	provider.primary = map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id"}, "c": {"c_id"}, "d": {"d_id"}}
	provider.fields = map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id"}, "c": {"c_id"}, "d": {"d_id"}}
	provider.schemas = append(append([]RelationSchema(nil), provider.schemas...), RelationSchema{RelationType: "ad", Category: RelationCategoryStatic, FromType: "a", ToType: "d", MetricName: "ad"})
	return provider
}

// 根在整个窗口都没有观测、首跳另一条关系缺路由时，首跳读取后即返回空邻域（带缺路由原因），
// 与该关系有路由但无数据时一样不再读取后续关系；后端不完整时才继续读取。
func TestTopologyUnobservedRootWithUnroutedFirstHopReturnsEarly(t *testing.T) {
	old := SharedTopologyPlanCandidates
	t.Cleanup(func() { SharedTopologyPlanCandidates = old })
	SharedTopologyPlanCandidates = true
	data := map[string]pl.Matrix{
		"ab": contractMatrix(map[string]string{"a_id": "other", "b_id": "b0"}, 1700000000000),
		"bc": contractMatrix(map[string]string{"b_id": "b0", "c_id": "c0"}, 1700000000000),
	}
	for _, hops := range []int{2, 3} {
		t.Run(fmt.Sprintf("H%d", hops), func(t *testing.T) {
			request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "root"}, Timestamp: 1700000000, MaxHops: hops}
			baselineCalls := map[string]int{}
			baseline, err := noRouteTopologyModel(t, selectorWithSecondFirstHop(), data, "", baselineCalls).QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.NoError(t, err)
			require.Empty(t, baseline.Snapshots[0].Nodes)
			require.False(t, baseline.Snapshots[0].Partial)
			require.Zero(t, baselineCalls["bc"], "an unobserved root stops after the first hop")

			calls := map[string]int{}
			result, err := noRouteTopologyModel(t, selectorWithSecondFirstHop(), data, "ad", calls).QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.NoError(t, err)
			require.Empty(t, result.Snapshots[0].Nodes)
			require.True(t, result.Snapshots[0].Partial)
			require.Equal(t, "relation_no_physical_route:ad", result.Snapshots[0].PartialReason)
			requireNoRouteKeepsBaselineReads(t, "ad", baselineCalls, calls)
		})
	}
}

// topology 子查询按原因计入质量指标：缺路由计为 relation_no_physical_route，后端不完整计为
// backend_partial，两者分开；完整查询不计数。
func TestTopologyQualityMetricSeparatesNoRouteFromBackendPartial(t *testing.T) {
	quality := func(reason string) float64 {
		families, err := prometheus.DefaultGatherer.Gather()
		require.NoError(t, err)
		for _, family := range families {
			if family.GetName() != "unify_query_cmdb_relation_quality_total" {
				continue
			}
			for _, item := range family.GetMetric() {
				labels := map[string]string{}
				for _, label := range item.GetLabel() {
					labels[label.GetName()] = label.GetValue()
				}
				if labels["entry"] == "topology" && labels["query_mode"] == "instant" && labels["quality"] == "partial" && labels["reason"] == reason {
					return item.GetCounter().GetValue()
				}
			}
		}
		return 0
	}
	request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, Timestamp: 1700000100, MaxHops: 2}
	for _, tc := range []struct {
		name, missing string
		partial       bool
		want          map[string]float64
	}{
		{name: "complete", want: map[string]float64{"relation_no_physical_route": 0, "backend_partial": 0}},
		{name: "no_route", missing: "middle_target_flow", want: map[string]float64{"relation_no_physical_route": 1, "backend_partial": 0}},
		{name: "backend_partial", partial: true, want: map[string]float64{"relation_no_physical_route": 0, "backend_partial": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := map[string]float64{}
			for reason := range tc.want {
				before[reason] = quality(reason)
			}
			model := noRouteTopologyModel(t, noRouteSharedTopologyProvider(), noRouteSharedTopologyData(), tc.missing, map[string]int{})
			if tc.partial {
				vm := model.timeGraphVMQuery
				model.timeGraphVMQuery = nil
				model.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
					matrix, err := vm(ctx, ts, expr, instant, start, end, step)
					return matrix, ts.QueryList[0].FieldName == "source_peer_flow", err
				}
			}
			_, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.NoError(t, err)
			for reason, delta := range tc.want {
				require.Equal(t, delta, quality(reason)-before[reason], reason)
			}
		})
	}
}

// 缺路由只由共享拓扑的取数器吸收；兼容入口的取数器仍返回错误，交给候选循环和
// 分段循环降级。两者都不访问后端。
func TestRelationNoRouteAbsorbedOnlyBySharedTopologyLoader(t *testing.T) {
	const stamp int64 = 1700000000000
	relations := []cmdb.Relation{
		{V: []cmdb.Resource{"source", "middle"}, RelationType: "source_to_middle", MetricName: "source_middle_flow", Category: "static", Direction: "outbound"},
		{V: []cmdb.Resource{"middle", "target"}, RelationType: "middle_to_target", MetricName: "middle_target_flow", Category: "static", Direction: "outbound"},
	}
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			model := sharedTopologyQueryModel(nil)
			model.timeGraphQueryReference = func(context.Context, *structured.QueryTs) (metadata.QueryReference, error) {
				return metadata.QueryReference{}, nil
			}
			var fields []string
			model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
				fields = append(fields, q.QueryList[0].FieldName)
				return nil, nil
			}
			var grid *TopologyGrid
			if shared {
				g, err := NewTopologyGrid([]int64{stamp})
				require.NoError(t, err)
				grid = &g
			}
			start := time.UnixMilli(stamp)
			graph, err := model.buildTimeGraph(ctx, "space", start, start, time.Second, "source", cmdb.Matcher{"source_id": "a"}, nil, nil, relations, "1m", nil, grid)
			require.Empty(t, fields, "an unrouted relation never reaches the backend")
			if !shared {
				var routeErr *relationNoPhysicalRouteError
				require.ErrorAs(t, err, &routeErr)
				require.Equal(t, "source_middle_flow", routeErr.metric)
				require.Nil(t, graph)
				return
			}
			require.NoError(t, err)
			defer graph.Clean(ctx)
			require.Equal(t, map[int64]string{
				stamp: "relation_no_physical_route:middle_target_flow;relation_no_physical_route:source_middle_flow",
			}, graph.partialTimesCopy())
			require.Zero(t, graph.nodeBuilder.Length())
		})
	}
}
