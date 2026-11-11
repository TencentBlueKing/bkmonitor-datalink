// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/dominikbraun/graph"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// requireRelationTargets 比较目标列表；空结果不区分 nil 与空切片。
func requireRelationTargets(t *testing.T, want, got []RelationTarget, msgAndArgs ...any) {
	t.Helper()
	if len(want) == 0 {
		require.Empty(t, got, msgAndArgs...)
		return
	}
	require.Equal(t, want, got, msgAndArgs...)
}

// sortedRelationTargets 按 FindRelationTargets 的输出顺序排列：时刻、目标类型、
// 目标维度的 fmt.Sprint。
func sortedRelationTargets(targets []RelationTarget) []RelationTarget {
	sorted := append([]RelationTarget(nil), targets...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Timestamp != sorted[j].Timestamp {
			return sorted[i].Timestamp < sorted[j].Timestamp
		}
		if sorted[i].TargetType != sorted[j].TargetType {
			return sorted[i].TargetType < sorted[j].TargetType
		}
		return fmt.Sprint(sorted[i].Target.Dimensions) < fmt.Sprint(sorted[j].Target.Dimensions)
	})
	return sorted
}

// oracleRelationTargets 是旧实现（逐源实例枚举实例路径）的测试副本，去掉了
// max_graph_results 检查：每条完整实例路径只贡献终点，按 (时刻, 目标类型) 去重后
// 以新结果的顺序输出。pathCount 是旧实现会物化的实例路径条数。
func oracleRelationTargets(
	ctx context.Context,
	q *TimeGraph,
	sourceType cmdb.Resource,
	targetTypes []cmdb.Resource,
	sourceMatcher cmdb.Matcher,
	expectedPaths []cmdb.RelationPath,
) (targets []RelationTarget, pathCount int, err error) {
	if sourceType == "" || len(targetTypes) == 0 {
		return nil, 0, nil
	}
	targetTypeSet := make(map[cmdb.Resource]struct{}, len(targetTypes))
	for _, targetType := range targetTypes {
		targetTypeSet[targetType] = struct{}{}
	}

	q.lock.RLock()
	defer q.lock.RUnlock()
	timestamps := make([]int64, 0, len(q.timeGraph))
	for timestamp := range q.timeGraph {
		timestamps = append(timestamps, timestamp)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	sourceCandidates := q.findNodesByResourceType(sourceType)
	if len(sourceCandidates) == 0 {
		return nil, 0, nil
	}

	for _, timestamp := range timestamps {
		g := q.timeGraph[timestamp]
		if g == nil {
			continue
		}
		adjacency, adjacencyErr := g.AdjacencyMap()
		if adjacencyErr != nil {
			continue
		}
		sourceNodes := q.findNodesByPartialMatcherAt(timestamp, sourceType, sourceMatcher, sourceCandidates, adjacency)
		reached := make(map[cmdb.Resource]map[uint64]struct{})
		for _, sourceNode := range sourceNodes {
			for _, expectedPath := range expectedPaths {
				if len(expectedPath.Steps) == 0 || expectedPath.Steps[0].ResourceType != sourceType {
					continue
				}
				targetType := expectedPath.Steps[len(expectedPath.Steps)-1].ResourceType
				if _, ok := targetTypeSet[targetType]; !ok {
					continue
				}
				nodePaths := [][]uint64{{sourceNode}}
				if len(expectedPath.Steps) > 1 {
					nodePaths, err = oracleTypedRelationNodePaths(ctx, q, sourceNode, expectedPath.Steps, adjacency, q.edgeTypes[timestamp])
					if err != nil {
						return nil, 0, err
					}
				}
				pathCount += len(nodePaths)
				for _, path := range nodePaths {
					if reached[targetType] == nil {
						reached[targetType] = make(map[uint64]struct{})
					}
					reached[targetType][path[len(path)-1]] = struct{}{}
				}
			}
		}
		for targetType, nodes := range reached {
			for node := range nodes {
				resourceType, info := q.infoAt(timestamp, node)
				targets = append(targets, RelationTarget{
					Timestamp:  timestamp,
					TargetType: targetType,
					Target:     cmdb.PathNode{ResourceType: resourceType, Dimensions: info},
				})
			}
		}
	}
	return sortedRelationTargets(targets), pathCount, nil
}

// oracleTypedRelationNodePaths 是旧 findTypedRelationNodePaths 的测试副本：逐条
// 展开实例路径，实际自环占用一次显式跳数，回到同一路径上更早的不同实例属于回溯。
func oracleTypedRelationNodePaths(
	ctx context.Context,
	q *TimeGraph,
	sourceNode uint64,
	expectedPath []cmdb.RelationPathStep,
	adjacency map[uint64]map[uint64]graph.Edge[uint64],
	edgeTypes map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{},
) ([][]uint64, error) {
	frontier := [][]uint64{{sourceNode}}
	for index := 1; index < len(expectedPath); index++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var next [][]uint64
		for _, currentPath := range frontier {
			current := currentPath[len(currentPath)-1]
			for neighbor := range adjacency[current] {
				resourceType, _ := q.nodeBuilder.Info(neighbor)
				if resourceType != expectedPath[index].ResourceType {
					continue
				}
				revisited := false
				for _, node := range currentPath {
					revisited = revisited || node == neighbor
				}
				if neighbor != current && revisited {
					continue
				}
				if !relationEdgeMatches(edgeTypes, timeGraphEdgeKey{source: current, target: neighbor}, expectedPath[index]) {
					continue
				}
				next = append(next, append(append([]uint64(nil), currentPath...), neighbor))
			}
		}
		if len(next) == 0 {
			return nil, nil
		}
		frontier = next
	}
	return frontier, nil
}

func layerIDField(layer int) string {
	return fmt.Sprintf("r%d_id", layer)
}

// 分层随机图：各层资源类型互不相同，边带两种关系类型、只在部分时刻出现，
// 末层收窄制造汇合；源匹配器只选中部分根。逐层集合推进必须与旧的实例路径
// 枚举得到相同的终点集合与顺序。
func TestFindRelationTargetsMatchesPathEnumerationOracle(t *testing.T) {
	relationTypes := []string{"rel_a", "rel_b"}
	timestamps := []int64{1700000000000, 1700000060000, 1700000120000, 1700000180000}
	compared, nonEmpty, converged := 0, 0, 0
	for seed := int64(0); seed < 200; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			ctx := context.Background()
			rng := rand.New(rand.NewSource(seed))
			layers := 2 + rng.Intn(4)
			types := make([]cmdb.Resource, layers)
			widths := make([]int, layers)
			config := &TimeGraphConfig{}
			for layer := 0; layer < layers; layer++ {
				types[layer] = cmdb.Resource(fmt.Sprintf("r%d", layer))
				widths[layer] = 1 + rng.Intn(5)
				config.Resource = append(config.Resource, TimeGraphResourceConfig{
					Name: types[layer], Index: cmdb.Index{layerIDField(layer)}, Info: cmdb.Index{"group", "zone"},
				})
			}
			// 末层只留一两个实例，让大量实例路径汇合到少数目标。
			widths[layers-1] = 1 + rng.Intn(2)
			tg := NewTimeGraphWithConfig(config)
			t.Cleanup(func() { tg.Clean(ctx) })

			addEdge := func(fromLayer, from, toLayer, to int, relationType string, timestamp int64) {
				info := cmdb.Matcher{layerIDField(fromLayer): fmt.Sprint(from), layerIDField(toLayer): fmt.Sprint(to)}
				relation := cmdb.Relation{V: []cmdb.Resource{types[fromLayer], types[toLayer]}, RelationType: relationType}
				require.NoError(t, tg.AddTimeRelationWithRelation(ctx, relation, info, timestamp))
			}
			for _, timestamp := range timestamps {
				if rng.Intn(5) == 0 {
					continue // 整个时刻缺失。
				}
				for layer := 0; layer+1 < layers; layer++ {
					for from := 0; from < widths[layer]; from++ {
						for to := 0; to < widths[layer+1]; to++ {
							for _, relationType := range relationTypes {
								if rng.Intn(3) == 0 {
									addEdge(layer, from, layer+1, to, relationType, timestamp)
								}
							}
						}
					}
				}
				// 反向与跨层的噪声边，只有乱序候选路径会经过它们。
				for i := 0; i < 4; i++ {
					fromLayer, toLayer := rng.Intn(layers), rng.Intn(layers)
					if fromLayer == toLayer {
						continue
					}
					addEdge(fromLayer, rng.Intn(widths[fromLayer]), toLayer, rng.Intn(widths[toLayer]), relationTypes[rng.Intn(len(relationTypes))], timestamp)
				}
				// 根的 group 只在部分时刻存在；各实例的 zone 随时刻变化。
				for root := 0; root < widths[0]; root++ {
					if rng.Intn(4) != 0 {
						require.NoError(t, tg.AddTimeNode(ctx, types[0], cmdb.Matcher{layerIDField(0): fmt.Sprint(root), "group": fmt.Sprintf("g%d", root%2)}, timestamp))
					}
				}
				for layer := 0; layer < layers; layer++ {
					for node := 0; node < widths[layer]; node++ {
						if rng.Intn(3) == 0 {
							require.NoError(t, tg.AddTimeNode(ctx, types[layer], cmdb.Matcher{layerIDField(layer): fmt.Sprint(node), "zone": fmt.Sprintf("z%d", timestamp/60000%7)}, timestamp))
						}
					}
				}
			}

			randomStep := func(resource cmdb.Resource) cmdb.RelationPathStep {
				step := cmdb.RelationPathStep{ResourceType: resource}
				if choice := rng.Intn(3); choice > 0 {
					step.RelationType = relationTypes[choice-1]
				}
				return step
			}
			var paths []cmdb.RelationPath
			targetSet := make(map[cmdb.Resource]struct{})
			for count := 1 + rng.Intn(3); count > 0; count-- {
				length := 1 + rng.Intn(layers)
				order := make([]int, 0, length)
				if rng.Intn(2) == 0 {
					for layer := 1; layer < length; layer++ {
						order = append(order, layer)
					}
				} else {
					// 类型互不相同的乱序路径。
					for _, layer := range rng.Perm(layers - 1)[:length-1] {
						order = append(order, layer+1)
					}
				}
				path := cmdb.RelationPath{Steps: []cmdb.RelationPathStep{{ResourceType: types[0]}}}
				for _, layer := range order {
					path.Steps = append(path.Steps, randomStep(types[layer]))
				}
				paths = append(paths, path)
				targetSet[path.Steps[len(path.Steps)-1].ResourceType] = struct{}{}
			}
			if rng.Intn(4) == 0 {
				// 不从源类型出发的路径被跳过。
				paths = append(paths, cmdb.RelationPath{Steps: []cmdb.RelationPathStep{{ResourceType: types[1]}, randomStep(types[layers-1])}})
			}
			var targetTypes []cmdb.Resource
			for _, resource := range types {
				if _, ok := targetSet[resource]; ok {
					targetTypes = append(targetTypes, resource)
				}
			}
			if len(targetTypes) > 1 && rng.Intn(3) == 0 {
				targetTypes = targetTypes[1:] // 未请求的目标类型对应的路径被过滤。
			}

			for _, matcher := range []cmdb.Matcher{
				nil,
				{"group": "g0"},
				{layerIDField(0): "0"},
				{layerIDField(0): "1", "group": "g1"},
				{"group": "missing"},
			} {
				want, pathCount, err := oracleRelationTargets(ctx, tg, types[0], targetTypes, matcher, paths)
				require.NoError(t, err)
				got, err := tg.FindRelationTargets(ctx, types[0], targetTypes, matcher, paths)
				require.NoError(t, err)
				requireRelationTargets(t, want, got, "matcher=%v targetTypes=%v paths=%+v", matcher, targetTypes, paths)
				compared++
				if len(want) > 0 {
					nonEmpty++
				}
				if pathCount > len(want) {
					converged++
				}
			}
		})
	}
	// 生成器必须真的产生可达目标与汇合，否则等价只是空集合相等。
	require.Greater(t, nonEmpty, compared/4)
	require.Greater(t, converged, compared/10)
}

// 两步自关联 [T, T]：同类型实例之间的出向、入向边与真实自环混合，自环可以
// 回到根自身。逐层集合推进必须与旧的实例路径枚举一致。
func TestFindRelationTargetsSelfRelationMatchesPathEnumerationOracle(t *testing.T) {
	relationTypes := []string{"rel_a", "rel_b"}
	directions := []TraversalDirection{DirectionOutbound, DirectionInbound}
	stepDirections := []string{"", string(DirectionOutbound), string(DirectionInbound), string(DirectionBoth)}
	timestamps := []int64{1700000000000, 1700000060000, 1700000120000}
	compared, nonEmpty, rootReached := 0, 0, 0
	for seed := int64(0); seed < 200; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			ctx := context.Background()
			rng := rand.New(rand.NewSource(seed))
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
				{Name: "svc", Index: cmdb.Index{"svc_id"}, Info: cmdb.Index{"group"}},
			}})
			t.Cleanup(func() { tg.Clean(ctx) })
			width := 2 + rng.Intn(5)
			for _, timestamp := range timestamps {
				if rng.Intn(5) == 0 {
					continue
				}
				for from := 0; from < width; from++ {
					for to := 0; to < width; to++ {
						for _, relationType := range relationTypes {
							for _, direction := range directions {
								chance := 4
								if from == to {
									chance = 2 // 自环更常见。
								}
								if rng.Intn(chance) != 0 {
									continue
								}
								relation := cmdb.Relation{
									V: []cmdb.Resource{"svc", "svc"}, RelationType: relationType, MetricName: relationType + "_flow",
									Category: string(RelationCategoryDynamic), Direction: string(direction),
								}
								require.NoError(t, tg.AddTimeRelationWithRelation(ctx, relation, cmdb.Matcher{"from_svc_id": fmt.Sprint(from), "to_svc_id": fmt.Sprint(to)}, timestamp))
							}
						}
					}
				}
				for node := 0; node < width; node++ {
					if rng.Intn(4) != 0 {
						require.NoError(t, tg.AddTimeNode(ctx, "svc", cmdb.Matcher{"svc_id": fmt.Sprint(node), "group": fmt.Sprintf("g%d", node%2)}, timestamp))
					}
				}
			}

			var paths []cmdb.RelationPath
			selfOnly := true
			for count := 1 + rng.Intn(3); count > 0; count-- {
				if rng.Intn(4) == 0 {
					paths = append(paths, cmdb.RelationPath{Steps: []cmdb.RelationPathStep{{ResourceType: "svc"}}})
					selfOnly = false
					continue
				}
				step := cmdb.RelationPathStep{ResourceType: "svc", Direction: stepDirections[rng.Intn(len(stepDirections))]}
				if choice := rng.Intn(3); choice > 0 {
					step.RelationType = relationTypes[choice-1]
				}
				paths = append(paths, cmdb.RelationPath{Steps: []cmdb.RelationPathStep{{ResourceType: "svc"}, step}})
			}

			for _, matcher := range []cmdb.Matcher{nil, {"group": "g0"}, {"svc_id": "0"}, {"svc_id": "1", "group": "g1"}} {
				want, _, err := oracleRelationTargets(ctx, tg, "svc", []cmdb.Resource{"svc"}, matcher, paths)
				require.NoError(t, err)
				got, err := tg.FindRelationTargets(ctx, "svc", []cmdb.Resource{"svc"}, matcher, paths)
				require.NoError(t, err)
				requireRelationTargets(t, want, got, "matcher=%v paths=%+v", matcher, paths)
				compared++
				if len(want) > 0 {
					nonEmpty++
				}
				// 只看全是两步自关联的计划：此时根出现在结果里只能经由自环。
				if root, ok := matcher["svc_id"]; ok && selfOnly {
					for _, target := range want {
						if target.Target.Dimensions["svc_id"] == root {
							rootReached++
							break
						}
					}
				}
			}
		})
	}
	require.Greater(t, nonEmpty, compared/4)
	require.Positive(t, rootReached, "self loops must reach the root itself in some seeds")
}

// 生产形状：一个数据源经 2000 个 pod 汇合到 50 个 node。旧实现每个时刻物化
// 2000 条实例路径，10 个时刻超过 max_graph_results；逐层集合只返回各时刻的 50 个 node。
func TestFindRelationTargetsFanInReturnsTargetsWithoutPathLimit(t *testing.T) {
	ctx := context.Background()
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "datasource", Index: cmdb.Index{"bk_data_id"}},
		{Name: "pod", Index: cmdb.Index{"pod_id"}},
		{Name: "node", Index: cmdb.Index{"node_id"}},
	}})
	t.Cleanup(func() { tg.Clean(ctx) })
	timestamps := make([]int64, 10)
	for index := range timestamps {
		timestamps[index] = 1700000000000 + int64(index)*60000
	}
	for pod := 0; pod < 2000; pod++ {
		podID := fmt.Sprintf("pod-%04d", pod)
		require.NoError(t, tg.AddTimeRelation(ctx, "datasource", "pod", cmdb.Matcher{"bk_data_id": "1001", "pod_id": podID}, timestamps...))
		require.NoError(t, tg.AddTimeRelation(ctx, "pod", "node", cmdb.Matcher{"pod_id": podID, "node_id": fmt.Sprintf("node-%02d", pod%50)}, timestamps...))
	}

	results, err := tg.FindTargets(ctx, "datasource", []cmdb.Resource{"node"}, cmdb.Matcher{"bk_data_id": "1001"}, [][]cmdb.Resource{{"datasource", "pod", "node"}})
	require.NoError(t, err)
	var want []RelationTarget
	for _, timestamp := range timestamps {
		for node := 0; node < 50; node++ {
			want = append(want, RelationTarget{
				Timestamp:  timestamp,
				TargetType: "node",
				Target:     cmdb.PathNode{ResourceType: "node", Dimensions: cmdb.Matcher{"node_id": fmt.Sprintf("node-%02d", node)}},
			})
		}
	}
	require.Equal(t, sortedRelationTargets(want), results)
}

func legacyRangeGridPoints(start, end time.Time, step time.Duration) []time.Time {
	var points []time.Time
	for at := start; !at.After(end); at = at.Add(step) {
		points = append(points, at)
	}
	return points
}

// legacyGridSegments 按段长依次生成落在网格上的连续段。
func legacyGridSegments(start time.Time, step time.Duration, sizes ...int) []legacyRangeSegment {
	segments := make([]legacyRangeSegment, 0, len(sizes))
	for _, size := range sizes {
		segments = append(segments, legacyRangeSegment{start: start, end: start.Add(time.Duration(size-1) * step), points: size})
		start = start.Add(time.Duration(size) * step)
	}
	return segments
}

// runLegacyRangeSegmenter 逐段取出分段，并把 edgePoints 给出的边点数回报给分段器。
func runLegacyRangeSegmenter(start, end time.Time, step time.Duration, edgePoints func(legacyRangeSegment) int) []legacyRangeSegment {
	segmenter := newLegacyRangeSegmenter(start, end, step)
	var segments []legacyRangeSegment
	for segment, ok := segmenter.next(); ok; segment, ok = segmenter.next() {
		segments = append(segments, segment)
		segmenter.observe(segment, edgePoints(segment))
	}
	return segments
}

func TestLegacyRangeSegmenter(t *testing.T) {
	start := time.Unix(1700000040, 0)
	minute := func(steps int) time.Time { return start.Add(time.Duration(steps) * time.Minute) }
	const half = 30 * time.Second
	perPoint := func(density int) func(legacyRangeSegment) int {
		return func(segment legacyRangeSegment) int { return segment.points * density }
	}
	offGrid := func(segments []legacyRangeSegment, end time.Time) []legacyRangeSegment {
		segments[len(segments)-1].end = end
		return segments
	}
	for _, tc := range []struct {
		name       string
		end        time.Time
		step       time.Duration
		edgePoints func(legacyRangeSegment) int
		want       []legacyRangeSegment
	}{
		{name: "single_point", end: start, step: time.Minute, want: []legacyRangeSegment{{start: start, end: start, points: 1}}},
		{name: "ten_points", end: minute(9), step: time.Minute, want: legacyGridSegments(start, time.Minute, 10)},
		{name: "64_points", end: minute(63), step: time.Minute, want: legacyGridSegments(start, time.Minute, 64)},
		// 余下恰好一个点时并入当前段，不产生单点段。
		{name: "65_points_absorb_last", end: minute(64), step: time.Minute, want: legacyGridSegments(start, time.Minute, 65)},
		{name: "66_points", end: minute(65), step: time.Minute, want: legacyGridSegments(start, time.Minute, 64, 2)},
		// 没有边点时逐段翻倍：64 之后至多 128，余下 66 点一次取完。
		{name: "130_points_double", end: minute(129), step: time.Minute, want: legacyGridSegments(start, time.Minute, 64, 66)},
		{name: "1000_points_double", end: minute(999), step: time.Minute, want: legacyGridSegments(start, time.Minute, 64, 128, 256, 512, 40)},
		// 每点 10000 条边：下一段 = max(2, min(128, 262144/10000)) = 26。
		{name: "dense_points_follow_budget", end: minute(129), step: time.Minute, edgePoints: perPoint(10000), want: legacyGridSegments(start, time.Minute, 64, 26, 26, 14)},
		// 每点边数向上取整：(64*4096+1)/64 取整为 4097，262144/4097 = 63。
		{name: "per_point_rounds_up", end: minute(199), step: time.Minute, edgePoints: func(segment legacyRangeSegment) int { return segment.points*4096 + 1 }, want: legacyGridSegments(start, time.Minute, 64, 63, 63, 10)},
		// 单点就超过预算时退到最小段 2；末尾余 1 点时并入，得到 3。
		{name: "per_point_over_budget", end: minute(68), step: time.Minute, edgePoints: perPoint(legacyRangeSegmentGraphPoints + 1), want: legacyGridSegments(start, time.Minute, 64, 2, 3)},
		{name: "off_grid_end_single_segment", end: minute(10).Add(half), step: time.Minute, want: offGrid(legacyGridSegments(start, time.Minute, 11), minute(10).Add(half))},
		{name: "off_grid_end_kept_by_last_segment", end: minute(129).Add(half), step: time.Minute, want: offGrid(legacyGridSegments(start, time.Minute, 64, 66), minute(129).Add(half))},
		{name: "off_grid_end_below_one_step", end: start.Add(half), step: time.Minute, want: []legacyRangeSegment{{start: start, end: start.Add(half), points: 1}}},
		{name: "zero_step", end: minute(200), step: 0, want: []legacyRangeSegment{{start: start, end: minute(200), points: 1}}},
		{name: "negative_step", end: minute(200), step: -time.Minute, want: []legacyRangeSegment{{start: start, end: minute(200), points: 1}}},
		{name: "end_before_start", end: minute(-1), step: time.Minute, want: []legacyRangeSegment{{start: start, end: minute(-1), points: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edgePoints := tc.edgePoints
			if edgePoints == nil {
				edgePoints = perPoint(0)
			}
			segments := runLegacyRangeSegmenter(start, tc.end, tc.step, edgePoints)
			require.Equal(t, tc.want, segments)
			if tc.step <= 0 || !tc.end.After(start) {
				return
			}
			grid := legacyRangeGridPoints(start, tc.end, tc.step)
			var covered []time.Time
			for index, segment := range segments {
				points := legacyRangeGridPoints(segment.start, segment.end, tc.step)
				require.Len(t, points, segment.points)
				if len(grid) > 1 {
					require.GreaterOrEqual(t, segment.points, 2, "a range segment must not degrade into an instant query")
				}
				require.Zero(t, segment.start.Sub(start)%tc.step, "segment starts stay on the grid")
				if index > 0 {
					require.Equal(t, segments[index-1].end.Add(tc.step), segment.start, "segments are contiguous on the grid")
				}
				covered = append(covered, points...)
			}
			require.Equal(t, start, segments[0].start)
			require.Equal(t, tc.end, segments[len(segments)-1].end)
			require.Equal(t, grid, covered)
		})
	}
}

// legacySegmentMiddles 是分段 fixture：每个网格点都关联 every，偶数点另关联 even，
// 奇数点另关联 odd，任何一段错位都会改变分桶答案。
func legacySegmentMiddles(start, at time.Time) []string {
	if int(at.Sub(start)/time.Minute)%2 == 0 {
		return []string{"even", "every"}
	}
	return []string{"every", "odd"}
}

// legacySegmentVM 按请求网格返回 fixture，并记录每次后端查询覆盖的网格段。
func legacySegmentVM(t *testing.T, start time.Time, calls *[]legacyRangeSegment) func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
	return func(_ context.Context, query *structured.QueryTs, _ string, instant bool, queryStart, queryEnd time.Time, step time.Duration) (pl.Matrix, error) {
		require.Equal(t, "source_middle_flow", query.QueryList[0].FieldName)
		require.False(t, instant)
		require.Equal(t, time.Minute, step)
		grid := legacyRangeGridPoints(queryStart, queryEnd, step)
		*calls = append(*calls, legacyRangeSegment{start: queryStart, end: queryEnd, points: len(grid)})
		samples := make(map[string][]int64)
		for _, at := range grid {
			for _, middle := range legacySegmentMiddles(start, at) {
				samples[middle] = append(samples[middle], at.UnixMilli())
			}
		}
		var matrix pl.Matrix
		for _, middle := range []string{"even", "every", "odd"} {
			if len(samples[middle]) > 0 {
				matrix = append(matrix, contractMatrix(map[string]string{"source_id": "a", "middle_id": middle}, samples[middle]...)...)
			}
		}
		return filterTopologyRegressionMatrix(query, matrix), nil
	}
}

func legacySegmentBuckets(start time.Time, grid []time.Time) []cmdb.MatchersWithTimestamp {
	buckets := make([]cmdb.MatchersWithTimestamp, 0, len(grid))
	for _, at := range grid {
		var matchers cmdb.Matchers
		for _, middle := range legacySegmentMiddles(start, at) {
			matchers = append(matchers, cmdb.Matcher{"middle_id": middle})
		}
		sort.SliceStable(matchers, func(i, j int) bool { return fmt.Sprint(matchers[i]) < fmt.Sprint(matchers[j]) })
		buckets = append(buckets, cmdb.MatchersWithTimestamp{Timestamp: at.UnixMilli(), Matchers: matchers})
	}
	return buckets
}

func legacySegmentModel() *Model {
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.schemas = provider.schemas[:1]
	return legacyRegressionModel(provider)
}

// 区间查询按自适应分段向后端取数：每段是同一网格上的连续区间，轻量图逐段翻倍，
// 拼接后的分桶答案与按 fixture 逐点计算的答案相同。
func TestLegacyRangeQuerySegmentsKeepBucketAnswer(t *testing.T) {
	start := time.Unix(1700000040, 0)
	for _, tc := range []struct {
		points    int
		wantSizes []int
	}{
		{points: 65, wantSizes: []int{65}},
		{points: 130, wantSizes: []int{64, 66}},
		{points: 200, wantSizes: []int{64, 128, 8}},
	} {
		t.Run(fmt.Sprintf("%d_points", tc.points), func(t *testing.T) {
			end := start.Add(time.Duration(tc.points-1) * time.Minute)
			model := legacySegmentModel()
			var calls []legacyRangeSegment
			model.timeGraphVMQuery = legacySegmentVM(t, start, &calls)

			_, _, path, _, buckets, err := model.QueryResourceMatcherRange(
				initTimeGraphQueryTestEnvironment(), "", "space", "1m",
				strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10),
				"middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil,
			)
			require.NoError(t, err)
			require.Equal(t, []string{"source", "middle"}, path)
			grid := legacyRangeGridPoints(start, end, time.Minute)
			require.Equal(t, legacySegmentBuckets(start, grid), buckets)

			require.Equal(t, legacyGridSegments(start, time.Minute, tc.wantSizes...), calls)
			var queried []time.Time
			for _, call := range calls {
				queried = append(queried, legacyRangeGridPoints(call.start, call.end, time.Minute)...)
			}
			require.Equal(t, grid, queried, "segments cover the request grid exactly once")
		})
	}
}

// 单次后端响应过大时把该段按时间对半重查：答案与不拆分时相同，每次被接受的读取都在
// 后端接受的点数内，拆分后的段至少两点；两点的段无法再拆分时返回原错误。
func TestLegacyRangeHalvesTooLargeSegments(t *testing.T) {
	start := time.Unix(1700000040, 0)
	query := func(model *Model, points int) ([]string, []cmdb.MatchersWithTimestamp, error) {
		end := start.Add(time.Duration(points-1) * time.Minute)
		_, _, path, _, buckets, err := model.QueryResourceMatcherRange(
			initTimeGraphQueryTestEnvironment(), "", "space", "1m",
			strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10),
			"middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil,
		)
		return path, buckets, err
	}
	// 后端拒收超过 maxPoints 点的读取，其余读取按 fixture 返回。
	limitedModel := func(maxPoints int, accepted, rejected *[]legacyRangeSegment) *Model {
		model := legacySegmentModel()
		backend := legacySegmentVM(t, start, accepted)
		model.timeGraphVMQuery = func(ctx context.Context, q *structured.QueryTs, expr string, instant bool, queryStart, queryEnd time.Time, step time.Duration) (pl.Matrix, error) {
			if points := len(legacyRangeGridPoints(queryStart, queryEnd, step)); points > maxPoints {
				*rejected = append(*rejected, legacyRangeSegment{start: queryStart, end: queryEnd, points: points})
				return nil, &metadata.BackendResponseTooLargeError{Message: "response body size exceeds the configured limit"}
			}
			return backend(ctx, q, expr, instant, queryStart, queryEnd, step)
		}
		return model
	}
	for _, tc := range []struct{ points, maxPoints int }{
		{points: 65, maxPoints: 20},
		{points: 200, maxPoints: 33},
		{points: 130, maxPoints: 2},
	} {
		t.Run(fmt.Sprintf("%d_points_max_%d", tc.points, tc.maxPoints), func(t *testing.T) {
			var unsplit []legacyRangeSegment
			reference := legacySegmentModel()
			reference.timeGraphVMQuery = legacySegmentVM(t, start, &unsplit)
			_, want, err := query(reference, tc.points)
			require.NoError(t, err)

			var accepted, rejected []legacyRangeSegment
			path, buckets, err := query(limitedModel(tc.maxPoints, &accepted, &rejected), tc.points)
			require.NoError(t, err)
			require.Equal(t, []string{"source", "middle"}, path)
			require.Equal(t, want, buckets)
			require.Equal(t, legacySegmentBuckets(start, legacyRangeGridPoints(start, start.Add(time.Duration(tc.points-1)*time.Minute), time.Minute)), buckets)
			require.NotEmpty(t, rejected, "the fixture must exercise the split")
			covered := make(map[int64]struct{})
			for _, call := range accepted {
				require.LessOrEqual(t, call.points, tc.maxPoints, "every accepted read stays within the backend size")
				require.GreaterOrEqual(t, call.points, 2, "a split segment must not degrade into an instant query")
				for _, at := range legacyRangeGridPoints(call.start, call.end, time.Minute) {
					covered[at.UnixMilli()] = struct{}{}
				}
			}
			require.Len(t, covered, tc.points, "accepted reads cover the whole grid")
		})
	}
	t.Run("two_point_segment_returns_error", func(t *testing.T) {
		var accepted, rejected []legacyRangeSegment
		_, buckets, err := query(limitedModel(1, &accepted, &rejected), 2)
		require.ErrorIs(t, err, metadata.ErrBackendResponseTooLarge)
		require.Empty(t, buckets)
		require.Empty(t, accepted)
		require.Equal(t, legacyGridSegments(start, time.Minute, 2), rejected)
	})
}

// 路由按时段生效：没有物理路由的段不读数并标记不完整，其它段照常返回；
// 所有段都没有路由时，该候选按无路由处理，由候选循环记录原因并换下一条。
func TestLegacyRangeNoRouteSegments(t *testing.T) {
	start := time.Unix(1700000040, 0)
	minute := func(steps int) time.Time { return start.Add(time.Duration(steps) * time.Minute) }
	for _, tc := range []struct {
		name                         string
		points                       int
		unroutedFrom, unroutedTo     time.Time // 起点落在此闭区间内的段没有路由。
		wantCalls                    []legacyRangeSegment
		wantRoutedFrom, wantRoutedTo []int // 有结果的网格下标闭区间。
	}{
		{
			name: "first_segment_unrouted", points: 130, unroutedFrom: minute(0), unroutedTo: minute(63),
			// 无路由段按零边点回报，下一段翻倍到 128，余下 66 点一次取完。
			wantCalls:      legacyGridSegments(minute(64), time.Minute, 66),
			wantRoutedFrom: []int{64}, wantRoutedTo: []int{129},
		},
		{
			name: "middle_segment_unrouted", points: 200, unroutedFrom: minute(64), unroutedTo: minute(191),
			wantCalls:      []legacyRangeSegment{legacyGridSegments(start, time.Minute, 64)[0], legacyGridSegments(minute(192), time.Minute, 8)[0]},
			wantRoutedFrom: []int{0, 192}, wantRoutedTo: []int{63, 199},
		},
		{name: "all_segments_unrouted", points: 130, unroutedFrom: minute(0), unroutedTo: minute(129)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			end := start.Add(time.Duration(tc.points-1) * time.Minute)
			model := legacySegmentModel()
			model.timeGraphQueryReference = func(ctx context.Context, query *structured.QueryTs) (metadata.QueryReference, error) {
				queryStart, err := strconv.ParseInt(query.Start, 10, 64)
				require.NoError(t, err)
				if at := time.Unix(queryStart, 0); !at.Before(tc.unroutedFrom) && !at.After(tc.unroutedTo) {
					return nil, nil
				}
				return timeGraphTestQueryReference(ctx, query)
			}
			var calls []legacyRangeSegment
			model.timeGraphVMQuery = legacySegmentVM(t, start, &calls)

			_, _, path, _, buckets, err := model.QueryResourceMatcherRange(
				ctx, "", "space", "1m",
				strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10),
				"middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil,
			)
			require.NoError(t, err)
			require.Equal(t, []string{"source", "middle"}, path)
			for _, call := range calls {
				require.True(t, call.start.Before(tc.unroutedFrom) || call.start.After(tc.unroutedTo), "unrouted segments must never reach VM: %+v", call)
			}
			require.Equal(t, tc.wantCalls, calls)

			grid := legacyRangeGridPoints(start, end, time.Minute)
			var routed []time.Time
			for index := range tc.wantRoutedFrom {
				routed = append(routed, grid[tc.wantRoutedFrom[index]:tc.wantRoutedTo[index]+1]...)
			}
			if len(routed) == 0 {
				require.Empty(t, buckets)
			} else {
				require.Equal(t, legacySegmentBuckets(start, routed), buckets)
			}
			status := metadata.GetStatus(ctx)
			require.NotNil(t, status)
			require.Equal(t, metadata.QueryTsPartial, status.Code)
			require.Contains(t, status.Message, "relation_no_physical_route:source_middle_flow")
		})
	}
}

// 首个候选在所有段都没有路由时，候选循环记录原因并换到下一条候选；全部候选都
// 没有路由时与单段查询一样返回空结果和最后一条候选路径。
func TestLegacyRangeNoRouteCandidateFallsBack(t *testing.T) {
	start := time.Unix(1700000040, 0)
	const points = 130
	end := start.Add(time.Duration(points-1) * time.Minute)
	grid := legacyRangeGridPoints(start, end, time.Minute)
	for _, tc := range []struct {
		name    string
		missing map[string]bool
		want    []cmdb.MatchersWithTimestamp
	}{
		{name: "direct_candidate_unrouted", missing: map[string]bool{"source_target_flow": true}, want: func() []cmdb.MatchersWithTimestamp {
			buckets := make([]cmdb.MatchersWithTimestamp, 0, len(grid))
			for _, at := range grid {
				buckets = append(buckets, cmdb.MatchersWithTimestamp{Timestamp: at.UnixMilli(), Matchers: cmdb.Matchers{{"target_id": "indirect"}}})
			}
			return buckets
		}()},
		{name: "all_candidates_unrouted", missing: map[string]bool{"source_target_flow": true, "source_middle_flow": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			model := legacyRegressionModel(legacyAlternativeProvider(true))
			model.timeGraphQueryReference = func(ctx context.Context, query *structured.QueryTs) (metadata.QueryReference, error) {
				if tc.missing[query.QueryList[0].FieldName] {
					return nil, nil
				}
				return timeGraphTestQueryReference(ctx, query)
			}
			calls := make(map[string][]legacyRangeSegment)
			model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, instant bool, queryStart, queryEnd time.Time, step time.Duration) (pl.Matrix, error) {
				name := query.QueryList[0].FieldName
				require.False(t, tc.missing[name], "unrouted relations must never reach VM")
				require.False(t, instant)
				var timestamps []int64
				for _, at := range legacyRangeGridPoints(queryStart, queryEnd, step) {
					timestamps = append(timestamps, at.UnixMilli())
				}
				calls[name] = append(calls[name], legacyRangeSegment{start: queryStart, end: queryEnd, points: len(timestamps)})
				return filterTopologyRegressionMatrix(query, legacyAlternativeMatrix(name, timestamps...)), nil
			}

			_, _, path, _, buckets, err := model.QueryResourceMatcherRange(
				ctx, "", "space", "1m",
				strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10),
				"target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil,
			)
			require.NoError(t, err)
			require.Equal(t, []string{"source", "middle", "target"}, path)
			if len(tc.want) == 0 {
				require.Empty(t, buckets)
				require.Empty(t, calls)
			} else {
				require.Equal(t, tc.want, buckets)
				wantSegments := legacyGridSegments(start, time.Minute, 64, 66)
				require.Equal(t, map[string][]legacyRangeSegment{"source_middle_flow": wantSegments, "middle_target_flow": wantSegments}, calls)
			}
			status := metadata.GetStatus(ctx)
			require.NotNil(t, status)
			require.Equal(t, metadata.QueryTsPartial, status.Code)
			for name := range tc.missing {
				require.Contains(t, status.Message, "relation_no_physical_route:"+name)
			}
		})
	}
}

func TestValidateRelationTargetPath(t *testing.T) {
	steps := func(resources ...cmdb.Resource) []cmdb.RelationPathStep {
		result := make([]cmdb.RelationPathStep, 0, len(resources))
		for _, resource := range resources {
			result = append(result, cmdb.RelationPathStep{ResourceType: resource})
		}
		return result
	}
	for _, tc := range []struct {
		name    string
		path    []cmdb.Resource
		wantErr string
	}{
		{name: "single_resource", path: []cmdb.Resource{"a"}},
		{name: "distinct_chain", path: []cmdb.Resource{"a", "b", "c"}},
		{name: "self_relation", path: []cmdb.Resource{"t", "t"}},
		{name: "revisit_source", path: []cmdb.Resource{"a", "b", "a"}, wantErr: "relation path revisits resource type a"},
		{name: "revisit_previous", path: []cmdb.Resource{"a", "b", "b"}, wantErr: "relation path revisits resource type b"},
		{name: "repeated_self_relation", path: []cmdb.Resource{"t", "t", "t"}, wantErr: "relation path revisits resource type t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRelationTargetPath(steps(tc.path...))
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.wantErr)
		})
	}

	// FindRelationTargets 在读图前拒绝回访类型的路径，即使图中存在可达边。
	ctx := context.Background()
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{
		{Name: "a", Index: cmdb.Index{"a_id"}},
		{Name: "b", Index: cmdb.Index{"b_id"}},
	}})
	t.Cleanup(func() { tg.Clean(ctx) })
	require.NoError(t, tg.AddTimeRelation(ctx, "a", "b", cmdb.Matcher{"a_id": "1", "b_id": "2"}, 100))
	require.NoError(t, tg.AddTimeRelation(ctx, "b", "a", cmdb.Matcher{"a_id": "3", "b_id": "2"}, 100))
	results, err := tg.FindTargets(ctx, "a", []cmdb.Resource{"a"}, nil, [][]cmdb.Resource{{"a", "b", "a"}})
	require.EqualError(t, err, "relation path revisits resource type a")
	require.Empty(t, results)
}
