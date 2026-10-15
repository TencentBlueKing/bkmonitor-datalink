// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"
	"time"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// TopologyGrid 是共享拓扑查询使用的规范化时间网格，时间点严格递增。
type TopologyGrid struct {
	Timestamps []int64
}

// NewTopologyGrid 校验时间网格并复制输入切片，避免调用方后续修改查询状态。
func NewTopologyGrid(timestamps []int64) (TopologyGrid, error) {
	maxPoints := effectiveMaxSharedTopologyPoints()
	if len(timestamps) == 0 {
		return TopologyGrid{}, fmt.Errorf("topology time grid must contain at least one point")
	}
	if maxPoints > 0 && len(timestamps) > maxPoints {
		return TopologyGrid{}, fmt.Errorf("topology time grid contains %d points, maximum is %d", len(timestamps), maxPoints)
	}
	for i := 1; i < len(timestamps); i++ {
		if timestamps[i] <= timestamps[i-1] {
			return TopologyGrid{}, fmt.Errorf("topology time grid must be strictly increasing")
		}
	}
	return TopologyGrid{Timestamps: append([]int64(nil), timestamps...)}, nil
}

// SharedTopologyQuery 控制共享拓扑的遍历和关系过滤。完整拓扑先遍历所有符合
// 条件的资源，目标资源类型过滤在输出阶段执行，不能改变遍历深度。
type SharedTopologyQuery struct {
	SourceType           cmdb.Resource
	SourceMatcher        cmdb.Matcher
	MaxHops              int
	AllowedCategories    []RelationCategory
	AllowedRelationTypes []string
	Direction            TraversalDirection
	PartialTimestamps    map[int64]string
	RootBits             map[uint64]timeBitmap // nil means no extra per-frame root constraint
}

// SharedTopologyNode 表示快照中的稳定实体身份。
type SharedTopologyNode struct {
	ID           uint64
	ResourceType cmdb.Resource
	Dimensions   cmdb.Matcher
}

// SharedTopologyEdge 表示快照中的一条关系身份。同一端点对可以承载多条关系。
type SharedTopologyEdge struct {
	Source       uint64
	Target       uint64
	RelationType string
	MetricName   string
	Category     string
	Direction    string
}

// SharedTopologySnapshot 表示一个评估时刻的 H 跳诱导子图。Partial 用于区分
// “完整查询得到的空图”和“数据不完整导致的空图”。
type SharedTopologySnapshot struct {
	Timestamp     int64
	Nodes         []SharedTopologyNode
	Edges         []SharedTopologyEdge
	Partial       bool
	PartialReason string
}

type timeGraphTopologyEdgeKey struct {
	source   uint64
	target   uint64
	relation timeGraphEdgeRelation
}

type sharedTopologyEdgeState struct {
	key        timeGraphTopologyEdgeKey
	activeBits timeBitmap
}

func (q *TimeGraph) sharedTopologyState(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) (map[uint64]timeBitmap, []sharedTopologyEdgeState, error) {
	if query.SourceType == "" {
		return nil, nil, fmt.Errorf("topology source type is required")
	}
	if query.MaxHops < 0 {
		return nil, nil, fmt.Errorf("topology max hops must not be negative")
	}
	if !yoloMode && MaxAllowedHops > 0 && query.MaxHops > MaxAllowedHops {
		return nil, nil, fmt.Errorf("topology max hops %d exceeds maximum %d", query.MaxHops, MaxAllowedHops)
	}

	if q.shared != nil {
		return q.shared.state(ctx, grid, query)
	}

	timestampIndex := make(map[int64]int, len(grid.Timestamps))
	for i, timestamp := range grid.Timestamps {
		timestampIndex[timestamp] = i
	}
	nodeBits := make(map[uint64]timeBitmap, len(q.topologyNodes))
	for node, timestamps := range q.topologyNodes {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		var active timeBitmap
		for timestamp := range timestamps {
			if index, ok := timestampIndex[timestamp]; ok {
				active.set(index)
			}
		}
		nodeBits[node] = active
	}

	categorySet := make(map[string]struct{}, len(query.AllowedCategories))
	for _, category := range query.AllowedCategories {
		categorySet[string(category)] = struct{}{}
	}
	relationTypeSet := make(map[string]struct{}, len(query.AllowedRelationTypes))
	for _, relationType := range query.AllowedRelationTypes {
		relationTypeSet[relationType] = struct{}{}
	}
	direction := query.Direction
	if direction == "" {
		direction = DirectionBoth
	}

	edges := make([]sharedTopologyEdgeState, 0, len(q.topologyEdges))
	for key, timestamps := range q.topologyEdges {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if len(categorySet) > 0 {
			if _, ok := categorySet[key.relation.category]; !ok {
				continue
			}
		}
		if len(relationTypeSet) > 0 {
			if _, ok := relationTypeSet[key.relation.relationType]; !ok {
				continue
			}
		}
		if key.relation.category == string(RelationCategoryDynamic) && direction != DirectionBoth && key.relation.direction != "" && key.relation.direction != string(direction) {
			continue
		}
		var activeBits timeBitmap
		for timestamp := range timestamps {
			if index, ok := timestampIndex[timestamp]; ok {
				activeBits.set(index)
			}
		}
		if !activeBits.empty() {
			edges = append(edges, sharedTopologyEdgeState{key: key, activeBits: activeBits})
		}
	}
	return nodeBits, edges, nil
}

// FindSharedTopology 为网格中的每个时刻返回一个 H 跳诱导子图。节点、关系
// 身份和时间状态在查询内共享，避免为每个时刻复制完整图；旧路径接口继续
// 使用原有实现。
func (q *TimeGraph) FindSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) (snapshots []SharedTopologySnapshot, err error) {
	_, err = q.findSharedTopology(ctx, grid, query, func(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState) (topologySnapshotCounts, error) {
		var e error
		snapshots, e = q.materializeSharedTopology(ctx, grid, query, reachable, edges)
		var counts topologySnapshotCounts
		for _, frame := range snapshots {
			counts.add(len(frame.Nodes), len(frame.Edges), frame.Partial)
		}
		return counts, e
	})
	return snapshots, err
}

type topologySnapshotCounts struct{ nodes, edges, partial int }

func (c *topologySnapshotCounts) add(nodes, edges int, partial bool) {
	c.nodes += nodes
	c.edges += edges
	if partial {
		c.partial++
	}
}

type topologyMaterializer func(context.Context, TopologyGrid, SharedTopologyQuery, map[uint64]timeBitmap, []sharedTopologyEdgeState) (topologySnapshotCounts, error)

// findSharedTopologyDTO 只物化模型需要的响应结构，避免先生成公开图快照再复制一次。
func (q *TimeGraph) findSharedTopologyDTO(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) (snapshots []cmdb.SharedTopologySnapshot, err error) {
	_, err = q.findSharedTopology(ctx, grid, query, func(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState) (topologySnapshotCounts, error) {
		var e error
		snapshots, e = q.materializeSharedTopologyDTO(ctx, grid, query, reachable, edges)
		var counts topologySnapshotCounts
		for _, frame := range snapshots {
			counts.add(len(frame.Nodes), len(frame.Edges), frame.Partial)
		}
		return counts, e
	})
	return snapshots, err
}

func (q *TimeGraph) findSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, materialize topologyMaterializer) (counts topologySnapshotCounts, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-find-shared-topology")
	defer span.End(&err)
	started := time.Now()
	defer func() {
		outcome := metric.CMDBTimeGraphErrorResult(err)
		if err == nil {
			outcome = metric.CMDBRelationResultEmpty
			if counts.partial > 0 {
				outcome = metric.CMDBRelationResultPartial
			} else if counts.nodes > 0 {
				outcome = metric.CMDBRelationResultSuccess
			}
		}
		metric.CMDBTimeGraphStageObserve(ctx, "topology-traversal", outcome, time.Since(started))
	}()
	span.Set("source-type", query.SourceType)
	span.Set("max-hops", query.MaxHops)
	span.Set("grid-point-count", len(grid.Timestamps))
	span.Set("partial-point-count", len(query.PartialTimestamps))
	if err := ctx.Err(); err != nil {
		return topologySnapshotCounts{}, err
	}
	normalizedGrid, err := NewTopologyGrid(grid.Timestamps)
	if err != nil {
		return topologySnapshotCounts{}, err
	}
	grid = normalizedGrid

	q.lock.RLock()
	defer q.lock.RUnlock()

	ctx, releaseTraversal, err := q.withTraversalReservation(ctx)
	if err != nil {
		return topologySnapshotCounts{}, err
	}
	defer releaseTraversal()

	_, stateSpan := trace.NewSpan(ctx, "timegraph-build-shared-topology-state")
	var stateErr error
	stateSpan.Set("source-type", query.SourceType)
	stateSpan.Set("source-matcher-count", len(query.SourceMatcher))
	stateSpan.Set("allowed-category-count", len(query.AllowedCategories))
	stateSpan.Set("allowed-relation-type-count", len(query.AllowedRelationTypes))
	stateSpan.Set("direction", query.Direction)
	stateStarted := time.Now()
	nodeBits, edges, stateErr := q.sharedTopologyState(ctx, grid, query)
	metric.CMDBTimeGraphStageObserve(ctx, "topology-state", metric.CMDBTimeGraphErrorResult(stateErr), time.Since(stateStarted))
	if stateErr == nil {
		stateSpan.Set("graph-node-count", len(nodeBits))
		stateSpan.Set("graph-edge-count", len(edges))
	}
	stateSpan.End(&stateErr)
	if stateErr != nil {
		return topologySnapshotCounts{}, stateErr
	}
	span.Set("graph-node-count", len(nodeBits))
	span.Set("graph-edge-count", len(edges))

	reachable, seedCount, err := q.propagateSharedTopology(ctx, grid, query, nodeBits, edges)
	span.Set("source-match-node-count", seedCount)
	if err != nil {
		return topologySnapshotCounts{}, err
	}
	span.Set("reachable-node-count", len(reachable))
	span.Set("traversal-level-count", query.MaxHops)

	counts, err = materialize(ctx, grid, query, reachable, edges)
	if err != nil {
		return topologySnapshotCounts{}, err
	}
	span.Set("snapshot-node-count", counts.nodes)
	span.Set("snapshot-edge-count", counts.edges)
	span.Set("partial-snapshot-count", counts.partial)
	return counts, nil
}

func (q *TimeGraph) propagateSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, nodeBits map[uint64]timeBitmap, edges []sharedTopologyEdgeState) (result map[uint64]timeBitmap, seedCount int, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-propagate-topology")
	defer finishTimeGraphStage(ctx, span, "topology-propagation", time.Now(), &err)
	span.Set("max-hops", query.MaxHops)
	reachable := make(map[uint64]timeBitmap)
	frontier := make(map[uint64]timeBitmap)
	frontierSizes := make([]int64, 0, 8)
	completed := 0
	defer func() {
		span.Set("frontier-nodes-by-level", frontierSizes)
		span.Set("completed-levels", completed)
		span.Set("frontier-levels-truncated", completed >= 64)
		span.Set("reachable-node-count", len(reachable))
	}()
	for node, bits := range nodeBits {
		if err := ctx.Err(); err != nil {
			return nil, seedCount, err
		}
		resourceType := q.nodeBuilder.resourceName(node)
		if resourceType != query.SourceType {
			continue
		}
		if query.RootBits != nil {
			bits = bits.intersect(query.RootBits[node])
		}
		matchedBits := bits.trimTrailingZeroWords()
		if len(query.SourceMatcher) != 0 {
			_, info := q.nodeBuilder.Info(node)
			matchedBits = timeBitmap{}
			for index, timestamp := range grid.Timestamps {
				if !bits.has(index) {
					continue
				}
				_, timestampInfo := q.nodeInfoAt(timestamp, node, info)
				if q.matchesPartial(timestampInfo, query.SourceMatcher) {
					matchedBits.set(index)
				}
			}
		}
		if !matchedBits.empty() {
			reachable[node] = matchedBits
			frontier[node] = matchedBits
		}
	}
	seedCount = len(frontier)
	span.Set("source-match-node-count", seedCount)
	if len(frontierSizes) < 64 {
		frontierSizes = append(frontierSizes, int64(len(frontier)))
	}

	// YOLO 深跳查询为已过滤的有向边建立一次索引；普通或浅层查询沿用原扫描方式。
	var outgoing map[uint64][]sharedTopologyEdgeState
	if yoloMode && query.MaxHops > 1 && len(frontier) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, seedCount, err
		}
		// 输出也要使用相同边序；直接分组本请求持有的边，避免为每个源点扩容索引数组。
		sort.Slice(edges, func(i, j int) bool { return lessTopologyEdgeKey(edges[i].key, edges[j].key) })
		if err := ctx.Err(); err != nil {
			return nil, seedCount, err
		}
		outgoing = make(map[uint64][]sharedTopologyEdgeState)
		for start := 0; start < len(edges); {
			if err := ctx.Err(); err != nil {
				return nil, seedCount, err
			}
			end := start + 1
			for end < len(edges) && edges[end].key.source == edges[start].key.source {
				if err := ctx.Err(); err != nil {
					return nil, seedCount, err
				}
				end++
			}
			outgoing[edges[start].key.source] = edges[start:end]
			start = end
		}
	}

	for level := 0; level < query.MaxHops && len(frontier) > 0; level++ {
		// 每跳只传播源点、关系和目标点共同活跃的帧；一个节点可在不同帧首次到达。
		if err := ctx.Err(); err != nil {
			return nil, seedCount, err
		}
		nextFrontier := make(map[uint64]timeBitmap)
		discover := func(sourceBits timeBitmap, edge sharedTopologyEdgeState) {
			// frontier is fixed for this level. Accumulate only previously unseen
			// target frames; cycles and parallel edges need not enter a second map.
			target := edge.key.target
			candidate := sourceBits.intersect(edge.activeBits).subtract(reachable[target])
			if candidate.empty() {
				return
			}
			candidate = candidate.intersect(nodeBits[target])
			if candidate.empty() {
				return
			}
			reachable[target] = reachable[target].union(candidate)
			nextFrontier[target] = nextFrontier[target].union(candidate)
		}
		if outgoing != nil {
			for source, sourceBits := range frontier {
				if err := ctx.Err(); err != nil {
					return nil, seedCount, err
				}
				for _, edge := range outgoing[source] {
					if err := ctx.Err(); err != nil {
						return nil, seedCount, err
					}
					discover(sourceBits, edge)
				}
			}
		} else {
			for _, edge := range edges {
				if err := ctx.Err(); err != nil {
					return nil, seedCount, err
				}
				discover(frontier[edge.key.source], edge)
			}
		}
		frontier = nextFrontier
		completed++
		if len(frontierSizes) < 64 {
			frontierSizes = append(frontierSizes, int64(len(frontier)))
		}
	}
	span.Set("reachable-node-count", len(reachable))
	span.Set("traversal-level-count", query.MaxHops)

	return reachable, seedCount, nil
}

// materializeSharedTopology 在预算内生成独立快照；调用方持有图的读锁。
func (q *TimeGraph) materializeSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState) ([]SharedTopologySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ReserveTopologyStage(ctx, "output", int64(len(grid.Timestamps))*int64(unsafe.Sizeof(SharedTopologySnapshot{}))); err != nil {
		return nil, err
	}
	result := make([]SharedTopologySnapshot, len(grid.Timestamps))
	for index, timestamp := range grid.Timestamps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result[index].Timestamp = timestamp
		if reason, ok := query.PartialTimestamps[timestamp]; ok {
			result[index].Partial = true
			result[index].PartialReason = reason
		}
	}
	err := q.emitSharedTopology(ctx, grid, query, reachable, edges,
		func(index int, node SharedTopologyNode) { result[index].Nodes = append(result[index].Nodes, node) },
		func(index int, edge SharedTopologyEdge) { result[index].Edges = append(result[index].Edges, edge) },
		func(_ bool, budget *topologyOutputBudget) error {
			nodes, counts, e := topologyFrameCounts(ctx, len(grid.Timestamps), reachable, edges)
			if e != nil {
				return e
			}
			if e = budget.addCharge(topologySnapshotContainers(nodes, counts), 0); e != nil {
				return e
			}
			for i := range result {
				if nodes[i] > 0 {
					result[i].Nodes = make([]SharedTopologyNode, 0, nodes[i])
				}
				if counts[i] > 0 {
					result[i].Edges = make([]SharedTopologyEdge, 0, counts[i])
				}
			}
			return nil
		},
		func() {
			for index := range result {
				sort.Slice(result[index].Nodes, func(i, j int) bool { return result[index].Nodes[i].ID < result[index].Nodes[j].ID })
				sort.Slice(result[index].Edges, func(i, j int) bool { return lessSharedTopologyEdge(result[index].Edges[i], result[index].Edges[j]) })
			}
		})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (q *TimeGraph) materializeSharedTopologyDTO(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState) ([]cmdb.SharedTopologySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ReserveTopologyStage(ctx, "output", int64(len(grid.Timestamps))*int64(unsafe.Sizeof(cmdb.SharedTopologySnapshot{}))); err != nil {
		return nil, err
	}
	result := make([]cmdb.SharedTopologySnapshot, len(grid.Timestamps))
	for index, timestamp := range grid.Timestamps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result[index].Timestamp = timestamp
		result[index].Nodes = make([]cmdb.SharedTopologyNode, 0)
		result[index].Edges = make([]cmdb.SharedTopologyEdge, 0)
		if reason, ok := query.PartialTimestamps[timestamp]; ok {
			result[index].Partial = true
			result[index].PartialReason = reason
		}
	}
	err := q.emitSharedTopology(ctx, grid, query, reachable, edges,
		func(index int, node SharedTopologyNode) {
			result[index].Nodes = append(result[index].Nodes, cmdb.SharedTopologyNode(node))
		},
		func(index int, edge SharedTopologyEdge) {
			result[index].Edges = append(result[index].Edges, cmdb.SharedTopologyEdge(edge))
		},
		func(ordered bool, budget *topologyOutputBudget) error {
			nodes, edgeCounts, err := topologyFrameCounts(ctx, len(grid.Timestamps), reachable, edges)
			if err != nil {
				return err
			}
			if err := budget.addCharge(topologySnapshotContainers(nodes, edgeCounts), 0); err != nil {
				return err
			}
			for index := range result {
				if err := ctx.Err(); err != nil {
					return err
				}
				result[index].Nodes = make([]cmdb.SharedTopologyNode, 0, nodes[index])
				result[index].Edges = make([]cmdb.SharedTopologyEdge, 0, edgeCounts[index])
			}
			return nil
		},
		func() {
			for index := range result {
				sort.Slice(result[index].Nodes, func(i, j int) bool { return result[index].Nodes[i].ID < result[index].Nodes[j].ID })
				sort.Slice(result[index].Edges, func(i, j int) bool {
					return lessSharedTopologyEdge(SharedTopologyEdge(result[index].Edges[i]), SharedTopologyEdge(result[index].Edges[j]))
				})
			}
		})
	if err != nil {
		return nil, err
	}
	if err := commitTopologyOutput(ctx, func() int64 { return topologySnapshotOwnedLowerBound(result) }); err != nil {
		return nil, err
	}
	return result, nil
}

// 响应收集器按已计数的逐帧容量预分配，并在分配前预约 DTO 容器内存。
func topologyFrameCounts(ctx context.Context, points int, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState) ([]int, []int, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	nodes, edgeCounts := make([]int, points), make([]int, points)
	for _, active := range reachable {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		for index := 0; index < points; index++ {
			if index%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
			}
			if active.has(index) {
				nodes[index]++
			}
		}
	}
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		active := edge.activeBits.intersect(reachable[edge.key.source]).intersect(reachable[edge.key.target])
		for index := 0; index < points; index++ {
			if index%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
			}
			if active.has(index) {
				edgeCounts[index]++
			}
		}
	}
	return nodes, edgeCounts, nil
}

func lessSharedTopologyEdge(left, right SharedTopologyEdge) bool {
	if left.Source != right.Source {
		return left.Source < right.Source
	}
	if left.Target != right.Target {
		return left.Target < right.Target
	}
	if left.RelationType != right.RelationType {
		return left.RelationType < right.RelationType
	}
	if left.MetricName != right.MetricName {
		return left.MetricName < right.MetricName
	}
	if left.Category != right.Category {
		return left.Category < right.Category
	}
	return left.Direction < right.Direction
}

func (q *TimeGraph) emitSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState, emitNode func(int, SharedTopologyNode), emitEdge func(int, SharedTopologyEdge), prepare func(bool, *topologyOutputBudget) error, finish func()) (err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-materialize-topology")
	defer span.End(&err)
	started := time.Now()
	budget := newTopologyOutputBudget(grid, query.PartialTimestamps)
	budget.ctx = ctx
	defer func() {
		span.Set("output-budget-elements-used", budget.elements)
		span.Set("output-budget-bytes-used", budget.bytes)
		SetTimeGraphLimitTrace(span, err)
		metric.CMDBTimeGraphStageObserve(ctx, "topology-materialize", metric.CMDBTimeGraphErrorResult(err), time.Since(started))
	}()
	if err := budget.checkBytes(0); err != nil {
		return err
	}
	ordered := yoloMode
	if prepare != nil {
		if err := prepare(ordered, budget); err != nil {
			return err
		}
	}
	// 持有图读锁时一次性定位存储网格，避免每帧重复查找。
	var infoFrameIndices []int
	if q.shared != nil {
		infoFrameIndices = make([]int, len(grid.Timestamps))
		for index, timestamp := range grid.Timestamps {
			if err := ctx.Err(); err != nil {
				return err
			}
			if stored, ok := q.shared.timestampIndex[timestamp]; ok {
				infoFrameIndices[index] = stored
			} else {
				infoFrameIndices[index] = -1
			}
		}
	}

	// YOLO 按已排序候选输出；普通模式仍按帧排序，输出契约相同。
	if ordered {
		nodes := make([]uint64, 0, len(reachable))
		for node := range reachable {
			if err := ctx.Err(); err != nil {
				return err
			}
			nodes = append(nodes, node)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, node := range nodes {
			if err := q.emitSharedTopologyNode(ctx, grid, infoFrameIndices, budget, node, reachable[node], emitNode); err != nil {
				return err
			}
		}
		// 传播已结束，邻接索引不再引用此切片，可以复用其容量。
		sort.Slice(edges, func(i, j int) bool { return lessTopologyEdgeKey(edges[i].key, edges[j].key) })
		if err := ctx.Err(); err != nil {
			return err
		}
	} else {
		for node, bits := range reachable {
			if err := q.emitSharedTopologyNode(ctx, grid, infoFrameIndices, budget, node, bits, emitNode); err != nil {
				return err
			}
		}
	}
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		activeBits := edge.activeBits.intersect(reachable[edge.key.source]).intersect(reachable[edge.key.target])
		if activeBits.empty() {
			continue
		}
		if err := budget.retainEdge(edge.key); err != nil {
			return err
		}
		for index := range grid.Timestamps {
			if index%256 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if !activeBits.has(index) {
				continue
			}
			if err := budget.add(0); err != nil {
				return err
			}
			emitEdge(index, SharedTopologyEdge{
				Source:       edge.key.source,
				Target:       edge.key.target,
				RelationType: edge.key.relation.relationType,
				MetricName:   edge.key.relation.metricName,
				Category:     edge.key.relation.category,
				Direction:    edge.key.relation.direction,
			})
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ordered {
		finish()
	}
	return ctx.Err()
}

// nodeInfoAt 返回节点在指定时间点的属性；没有时间点属性时回退到稳定身份
// 信息，保证关系指标只提供主键的场景仍能正常输出。
func (q *TimeGraph) nodeInfoAt(timestamp int64, node uint64, fallback cmdb.Matcher) (cmdb.Resource, cmdb.Matcher) {
	resourceType := q.nodeBuilder.resourceName(node)
	if info, exists := q.storedNodeInfoAt(timestamp, node); exists {
		return resourceType, cloneMatcher(info)
	}
	return resourceType, fallback
}

// storedNodeInfoAt 仅在调用方持有图锁时借用属性。
// bool 用于区分“存储的 nil map”和“该时间点没有属性版本”。
func (q *TimeGraph) storedNodeInfoAt(timestamp int64, node uint64) (cmdb.Matcher, bool) {
	if q.shared != nil {
		index, ok := q.shared.timestampIndex[timestamp]
		if !ok {
			return nil, false
		}
		for _, version := range q.shared.nodeInfos[node] {
			if version.activeBits.has(index) {
				return version.dimensions, true
			}
		}
		return nil, false
	}
	info, exists := q.nodeInfos[timestamp][node]
	return info, exists
}

func (q *TimeGraph) emitSharedTopologyNode(ctx context.Context, grid TopologyGrid, infoFrameIndices []int, budget *topologyOutputBudget, node uint64, bits timeBitmap, emitNode func(int, SharedTopologyNode)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resourceType := q.nodeBuilder.resourceName(node)
	var versions []sharedNodeInfo
	if q.shared != nil {
		versions = q.shared.nodeInfos[node]
		// Borrowed strings are kept once per version, not once per frame.
		for _, version := range versions {
			if err := budget.retainMatcher(resourceType, version.dimensions); err != nil {
				return err
			}
		}
	}
	fallbackRetained := false
	for index := range grid.Timestamps {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if !bits.has(index) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var info cmdb.Matcher
		var exists bool
		if q.shared != nil {
			storedIndex := infoFrameIndices[index]
			if storedIndex >= 0 {
				for _, version := range versions {
					if version.activeBits.has(storedIndex) {
						info, exists = version.dimensions, true
						break
					}
				}
			}
		} else {
			info, exists = q.storedNodeInfoAt(grid.Timestamps[index], node)
		}
		if !exists {
			info = q.topologyBorrowedNodeInfo(node)
		}
		entries := len(info)
		if q.shared == nil || (!exists && !fallbackRetained) {
			if err := budget.retainMatcher(resourceType, info); err != nil {
				return err
			}
			if !exists {
				fallbackRetained = true
			}
		}
		if err := budget.add(topologyMatcherMemory(entries)); err != nil {
			return err
		}
		info = cloneMatcher(info)
		emitNode(index, SharedTopologyNode{
			ID:           node,
			ResourceType: resourceType,
			Dimensions:   info,
		})
	}
	return nil
}
