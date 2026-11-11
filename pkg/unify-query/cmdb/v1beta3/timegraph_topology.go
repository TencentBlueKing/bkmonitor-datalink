// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// TopologyGrid 由 NewTopologyGrid 创建并持有不可变的递增时间点，内部直接复用。
type TopologyGrid struct {
	timestamps []int64
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
	return TopologyGrid{timestamps: append([]int64(nil), timestamps...)}, nil
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
	if MaxAllowedHops > 0 && query.MaxHops > MaxAllowedHops {
		return nil, nil, fmt.Errorf("topology max hops %d exceeds maximum %d", query.MaxHops, MaxAllowedHops)
	}

	return q.shared.state(ctx, grid, query)
}

type topologySnapshotCounts struct{ nodes, edges, partial int }

func (c *topologySnapshotCounts) add(nodes, edges int, partial bool) {
	c.nodes += nodes
	c.edges += edges
	if partial {
		c.partial++
	}
}

// TopologyFrames 按网格顺序逐帧交出快照，共 PointCount 帧。每帧在交出时构造，yield 返回后
// 不再被保留；只在 QuerySharedTopologyFrames 的回调返回前有效。
type TopologyFrames func(yield func(cmdb.SharedTopologySnapshot) error) error

// findSharedTopologyDTO 遍历一次后把全部帧收集为快照，供直接模型调用和测试使用。
func (q *TimeGraph) findSharedTopologyDTO(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) ([]cmdb.SharedTopologySnapshot, error) {
	frames, err := q.prepareSharedTopologyFrames(ctx, grid, query, nil)
	if err != nil {
		return nil, err
	}
	snapshots := make([]cmdb.SharedTopologySnapshot, 0, len(grid.timestamps))
	err = frames.each(ctx, func(snapshot cmdb.SharedTopologySnapshot) error {
		snapshots = append(snapshots, snapshot)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

type sharedTopologyFrameNode struct {
	id       uint64
	resource cmdb.Resource
	bits     timeBitmap
	versions []sharedNodeInfo
}

type sharedTopologyFrameEdge struct {
	edge cmdb.SharedTopologyEdge
	bits timeBitmap
}

// sharedTopologyFrames 保存一次遍历的结果：可达节点按 ID、诱导边按输出顺序预先排好，
// 每帧只筛出该帧可达的节点和两端都可达的边，各帧顺序与整体排序后逐帧拆分相同，
// 不同时物化 K 帧的 DTO。allowed 非空时按 target_types 在帧内过滤节点及其关联边。
type sharedTopologyFrames struct {
	q       *TimeGraph
	grid    TopologyGrid
	partial map[int64]string
	allowed map[cmdb.Resource]struct{}
	nodes   []sharedTopologyFrameNode
	edges   []sharedTopologyFrameEdge
	// infoFrames 把请求网格下标映射到存储网格下标，-1 表示存储网格没有该帧。
	infoFrames []int
	// counts 是过滤前的出现次数，与逐帧物化后的统计相同。
	counts topologySnapshotCounts
}

// prepareSharedTopologyFrames 求状态并传播可达，按输出顺序整理节点和边；不物化任何帧。
func (q *TimeGraph) prepareSharedTopologyFrames(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, allowed map[cmdb.Resource]struct{}) (frames *sharedTopologyFrames, err error) {
	var counts topologySnapshotCounts
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
	span.Set("grid-point-count", len(grid.timestamps))
	span.Set("partial-point-count", len(query.PartialTimestamps))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q.lock.RLock()
	defer q.lock.RUnlock()

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
		return nil, stateErr
	}
	span.Set("graph-node-count", len(nodeBits))
	span.Set("graph-edge-count", len(edges))

	reachable, seedCount, err := q.propagateSharedTopology(ctx, grid, query, nodeBits, edges)
	span.Set("source-match-node-count", seedCount)
	if err != nil {
		return nil, err
	}
	span.Set("reachable-node-count", len(reachable))
	span.Set("traversal-level-count", query.MaxHops)

	frames = &sharedTopologyFrames{q: q, grid: grid, partial: query.PartialTimestamps, allowed: allowed, infoFrames: make([]int, len(grid.timestamps))}
	for index, timestamp := range grid.timestamps {
		frames.infoFrames[index] = -1
		if stored, ok := q.shared.timestampIndex[timestamp]; ok {
			frames.infoFrames[index] = stored
		}
		if _, ok := query.PartialTimestamps[timestamp]; ok {
			counts.partial++
		}
	}
	frames.nodes = make([]sharedTopologyFrameNode, 0, len(reachable))
	for node, bits := range reachable {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frames.nodes = append(frames.nodes, sharedTopologyFrameNode{id: node, resource: q.nodeBuilder.resourceName(node), bits: bits, versions: q.shared.nodeInfos[node]})
		counts.nodes += bits.count()
	}
	sort.Slice(frames.nodes, func(i, j int) bool { return frames.nodes[i].id < frames.nodes[j].id })
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		active := edge.activeBits.intersect(reachable[edge.key.source]).intersect(reachable[edge.key.target])
		if active.empty() {
			continue
		}
		frames.edges = append(frames.edges, sharedTopologyFrameEdge{bits: active, edge: cmdb.SharedTopologyEdge{
			Source:       edge.key.source,
			Target:       edge.key.target,
			RelationType: edge.key.relation.relationType,
			MetricName:   edge.key.relation.metricName,
			Category:     edge.key.relation.category,
			Direction:    edge.key.relation.direction,
		}})
		counts.edges += active.count()
	}
	sort.Slice(frames.edges, func(i, j int) bool { return lessSharedTopologyEdge(frames.edges[i].edge, frames.edges[j].edge) })
	frames.counts = counts
	span.Set("snapshot-node-count", counts.nodes)
	span.Set("snapshot-edge-count", counts.edges)
	span.Set("partial-snapshot-count", counts.partial)
	return frames, nil
}

// each 按网格顺序构造并交出每一帧；整个过程持有图读锁，借用的属性在交出前复制。
func (f *sharedTopologyFrames) each(ctx context.Context, yield func(cmdb.SharedTopologySnapshot) error) (err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-materialize-topology")
	defer span.End(&err)
	started := time.Now()
	defer func() {
		metric.CMDBTimeGraphStageObserve(ctx, "topology-materialize", metric.CMDBTimeGraphErrorResult(err), time.Since(started))
	}()
	span.Set("frame-count", len(f.grid.timestamps))
	span.Set("frame-node-count", len(f.nodes))
	span.Set("frame-edge-count", len(f.edges))
	f.q.lock.RLock()
	defer f.q.lock.RUnlock()
	var kept map[uint64]struct{}
	if f.allowed != nil {
		kept = make(map[uint64]struct{})
	}
	for index, timestamp := range f.grid.timestamps {
		if err := ctx.Err(); err != nil {
			return err
		}
		snapshot := cmdb.SharedTopologySnapshot{Timestamp: timestamp, Nodes: make([]cmdb.SharedTopologyNode, 0), Edges: make([]cmdb.SharedTopologyEdge, 0)}
		if reason, ok := f.partial[timestamp]; ok {
			snapshot.Partial, snapshot.PartialReason = true, reason
		}
		clear(kept)
		for position, node := range f.nodes {
			if position%256 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if !node.bits.has(index) {
				continue
			}
			if kept != nil {
				if _, ok := f.allowed[node.resource]; !ok {
					continue
				}
				kept[node.id] = struct{}{}
			}
			snapshot.Nodes = append(snapshot.Nodes, cmdb.SharedTopologyNode{ID: node.id, ResourceType: node.resource, Dimensions: cloneMatcher(f.nodeInfo(node, index))})
		}
		for position, edge := range f.edges {
			if position%256 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if !edge.bits.has(index) {
				continue
			}
			if kept != nil {
				if _, ok := kept[edge.edge.Source]; !ok {
					continue
				}
				if _, ok := kept[edge.edge.Target]; !ok {
					continue
				}
			}
			snapshot.Edges = append(snapshot.Edges, edge.edge)
		}
		if err := yield(snapshot); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// nodeInfo 返回节点在该帧的属性版本；没有版本时回退到稳定身份信息。调用方持有图读锁。
func (f *sharedTopologyFrames) nodeInfo(node sharedTopologyFrameNode, index int) cmdb.Matcher {
	if stored := f.infoFrames[index]; stored >= 0 {
		for _, version := range node.versions {
			if version.activeBits.has(stored) {
				return version.dimensions
			}
		}
	}
	return f.q.topologyBorrowedNodeInfo(node.id)
}

func (q *TimeGraph) propagateSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, nodeBits map[uint64]timeBitmap, edges []sharedTopologyEdgeState) (result map[uint64]timeBitmap, seedCount int, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-propagate-topology")
	defer finishTimeGraphStage(ctx, span, "topology-propagation", time.Now(), &err)
	span.Set("max-hops", query.MaxHops)
	var reachable map[uint64]timeBitmap
	frontierSizes := make([]int64, 0, 8)
	completed := 0
	defer func() {
		span.Set("frontier-nodes-by-level", frontierSizes)
		span.Set("completed-levels", completed)
		span.Set("frontier-levels-truncated", completed >= 64)
		span.Set("reachable-node-count", len(reachable))
	}()
	reachable, err = q.sharedTopologySeeds(ctx, grid, query, nodeBits)
	if err != nil {
		return nil, seedCount, err
	}
	frontier := maps.Clone(reachable)
	seedCount = len(frontier)
	span.Set("source-match-node-count", seedCount)
	if len(frontierSizes) < 64 {
		frontierSizes = append(frontierSizes, int64(len(frontier)))
	}

	for level := 0; level < query.MaxHops && len(frontier) > 0; level++ {
		frontier, err = sharedTopologyStep(ctx, frontier, reachable, nodeBits, edges)
		if err != nil {
			return nil, seedCount, err
		}
		completed++
		if len(frontierSizes) < 64 {
			frontierSizes = append(frontierSizes, int64(len(frontier)))
		}
	}
	span.Set("reachable-node-count", len(reachable))
	span.Set("traversal-level-count", query.MaxHops)

	return reachable, seedCount, nil
}

// sharedTopologySeeds 返回各帧匹配根条件的源类型节点及其时间位。取数阶段与最终遍历
// 共用这一判定，逐跳取数据此决定从哪些根展开。
func (q *TimeGraph) sharedTopologySeeds(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, nodeBits map[uint64]timeBitmap) (map[uint64]timeBitmap, error) {
	seeds := make(map[uint64]timeBitmap)
	for node, bits := range nodeBits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resourceType := q.nodeBuilder.resourceName(node)
		if resourceType != query.SourceType {
			continue
		}
		if query.RootBits != nil {
			bits = bits.intersect(query.RootBits[node])
		}
		matchedBits := bits
		if len(query.SourceMatcher) != 0 {
			_, info := q.nodeBuilder.Info(node)
			matchedBits = timeBitmap{}
			for index, timestamp := range grid.timestamps {
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
			seeds[node] = matchedBits
		}
	}
	return seeds, nil
}

// sharedTopologyStep 推进一层并返回新前沿，reachable 原地累积。每跳只传播源点、关系和
// 目标点共同活跃的帧；一个节点可在不同帧首次到达。
func sharedTopologyStep(ctx context.Context, frontier, reachable, nodeBits map[uint64]timeBitmap, edges []sharedTopologyEdgeState) (map[uint64]timeBitmap, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nextFrontier := make(map[uint64]timeBitmap)
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// frontier is fixed for this level. Accumulate only previously unseen
		// target frames; cycles and parallel edges need not enter a second map.
		target := edge.key.target
		candidate := frontier[edge.key.source].intersect(edge.activeBits).subtract(reachable[target])
		if candidate.empty() {
			continue
		}
		candidate = candidate.intersect(nodeBits[target])
		if candidate.empty() {
			continue
		}
		reachable[target] = reachable[target].union(candidate)
		nextFrontier[target] = nextFrontier[target].union(candidate)
	}
	return nextFrontier, nil
}

func lessSharedTopologyEdge(left, right cmdb.SharedTopologyEdge) bool {
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

// topologyBorrowedNodeInfo 只在图读锁阻止 Clean 时借用节点身份属性；输出的 DTO 仍复制 map。
func (q *TimeGraph) topologyBorrowedNodeInfo(id uint64) cmdb.Matcher {
	q.nodeBuilder.lock.RLock()
	defer q.nodeBuilder.lock.RUnlock()
	return q.nodeBuilder.info[id]
}
