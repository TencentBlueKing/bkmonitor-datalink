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

// findSharedTopologyDTO materializes the response directly from shared storage.
func (q *TimeGraph) findSharedTopologyDTO(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) (snapshots []cmdb.SharedTopologySnapshot, err error) {
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

	snapshots, err = q.materializeSharedTopologyDTO(ctx, grid, query, reachable, edges)
	if err != nil {
		return nil, err
	}
	for _, frame := range snapshots {
		counts.add(len(frame.Nodes), len(frame.Edges), frame.Partial)
	}
	span.Set("snapshot-node-count", counts.nodes)
	span.Set("snapshot-edge-count", counts.edges)
	span.Set("partial-snapshot-count", counts.partial)
	return snapshots, nil
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
			reachable[node] = matchedBits
			frontier[node] = matchedBits
		}
	}
	seedCount = len(frontier)
	span.Set("source-match-node-count", seedCount)
	if len(frontierSizes) < 64 {
		frontierSizes = append(frontierSizes, int64(len(frontier)))
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
		for _, edge := range edges {
			if err := ctx.Err(); err != nil {
				return nil, seedCount, err
			}
			discover(frontier[edge.key.source], edge)
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

func (q *TimeGraph) materializeSharedTopologyDTO(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState) ([]cmdb.SharedTopologySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]cmdb.SharedTopologySnapshot, len(grid.timestamps))
	for index, timestamp := range grid.timestamps {
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
		func(index int, node cmdb.SharedTopologyNode) {
			result[index].Nodes = append(result[index].Nodes, node)
		},
		func(index int, edge cmdb.SharedTopologyEdge) {
			result[index].Edges = append(result[index].Edges, edge)
		},
		func() error {
			nodes, edgeCounts, err := topologyFrameCounts(ctx, len(grid.timestamps), reachable, edges)
			if err != nil {
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
					return lessSharedTopologyEdge(result[index].Edges[i], result[index].Edges[j])
				})
			}
		})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// 响应收集器按已计数的逐帧容量预分配。
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

func (q *TimeGraph) emitSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState, emitNode func(int, cmdb.SharedTopologyNode), emitEdge func(int, cmdb.SharedTopologyEdge), prepare func() error, finish func()) (err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-materialize-topology")
	defer span.End(&err)
	started := time.Now()
	defer func() {
		metric.CMDBTimeGraphStageObserve(ctx, "topology-materialize", metric.CMDBTimeGraphErrorResult(err), time.Since(started))
	}()
	if prepare != nil {
		if err := prepare(); err != nil {
			return err
		}
	}
	// 持有图读锁时一次性定位存储网格，避免每帧重复查找。
	var infoFrameIndices []int
	if q.shared != nil {
		infoFrameIndices = make([]int, len(grid.timestamps))
		for index, timestamp := range grid.timestamps {
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

	for node, bits := range reachable {
		if err := q.emitSharedTopologyNode(ctx, grid, infoFrameIndices, node, bits, emitNode); err != nil {
			return err
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
		for index := range grid.timestamps {
			if index%256 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if !activeBits.has(index) {
				continue
			}
			emitEdge(index, cmdb.SharedTopologyEdge{
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
	finish()
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

func (q *TimeGraph) emitSharedTopologyNode(ctx context.Context, grid TopologyGrid, infoFrameIndices []int, node uint64, bits timeBitmap, emitNode func(int, cmdb.SharedTopologyNode)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resourceType := q.nodeBuilder.resourceName(node)
	var versions []sharedNodeInfo
	if q.shared != nil {
		versions = q.shared.nodeInfos[node]
	}
	for index := range grid.timestamps {
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
			info, exists = q.storedNodeInfoAt(grid.timestamps[index], node)
		}
		if !exists {
			info = q.topologyBorrowedNodeInfo(node)
		}
		emitNode(index, cmdb.SharedTopologyNode{
			ID:           node,
			ResourceType: resourceType,
			Dimensions:   cloneMatcher(info),
		})
	}
	return nil
}

// topologyBorrowedNodeInfo 只在图读锁阻止 Clean 时借用节点身份属性；输出的 DTO 仍复制 map。
func (q *TimeGraph) topologyBorrowedNodeInfo(id uint64) cmdb.Matcher {
	q.nodeBuilder.lock.RLock()
	defer q.nodeBuilder.lock.RUnlock()
	return q.nodeBuilder.info[id]
}
