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
	if len(timestamps) > maxPoints {
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
	activeBits uint64
}

func (q *TimeGraph) sharedTopologyState(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) (map[uint64]uint64, []sharedTopologyEdgeState, error) {
	if query.SourceType == "" {
		return nil, nil, fmt.Errorf("topology source type is required")
	}
	if query.MaxHops < 0 {
		return nil, nil, fmt.Errorf("topology max hops must not be negative")
	}
	if MaxAllowedHops > 0 && query.MaxHops > MaxAllowedHops {
		return nil, nil, fmt.Errorf("topology max hops %d exceeds maximum %d", query.MaxHops, MaxAllowedHops)
	}

	if q.shared != nil {
		return q.shared.state(ctx, grid, query)
	}

	timestampIndex := make(map[int64]uint8, len(grid.Timestamps))
	for i, timestamp := range grid.Timestamps {
		timestampIndex[timestamp] = uint8(i)
	}
	gridMask := (uint64(1) << len(grid.Timestamps)) - 1

	nodeBits := make(map[uint64]uint64, len(q.topologyNodes))
	for node, timestamps := range q.topologyNodes {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		for timestamp := range timestamps {
			if index, ok := timestampIndex[timestamp]; ok {
				nodeBits[node] |= uint64(1) << index
			}
		}
		nodeBits[node] &= gridMask
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
		var activeBits uint64
		for timestamp := range timestamps {
			if index, ok := timestampIndex[timestamp]; ok {
				activeBits |= uint64(1) << index
			}
		}
		if activeBits != 0 {
			edges = append(edges, sharedTopologyEdgeState{key: key, activeBits: activeBits & gridMask})
		}
	}
	return nodeBits, edges, nil
}

// FindSharedTopology 为网格中的每个时刻返回一个 H 跳诱导子图。节点、关系
// 身份和时间状态在查询内共享，避免为每个时刻复制完整图；旧路径接口继续
// 使用原有实现。
func (q *TimeGraph) FindSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) (snapshots []SharedTopologySnapshot, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-find-shared-topology")
	defer span.End(&err)
	started := time.Now()
	defer func() {
		outcome := metric.CMDBTimeGraphErrorResult(err)
		if err == nil {
			outcome = metric.CMDBRelationResultEmpty
			for _, snapshot := range snapshots {
				if snapshot.Partial {
					outcome = metric.CMDBRelationResultPartial
					break
				}
				if len(snapshot.Nodes) > 0 {
					outcome = metric.CMDBRelationResultSuccess
				}
			}
		}
		metric.CMDBTimeGraphStageObserve(ctx, "topology-traversal", outcome, time.Since(started))
	}()
	span.Set("source-type", query.SourceType)
	span.Set("max-hops", query.MaxHops)
	span.Set("grid-point-count", len(grid.Timestamps))
	span.Set("partial-point-count", len(query.PartialTimestamps))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalizedGrid, err := NewTopologyGrid(grid.Timestamps)
	if err != nil {
		return nil, err
	}
	grid = normalizedGrid

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

	snapshots, err = q.materializeSharedTopology(ctx, grid, query, reachable, edges)
	if err != nil {
		return nil, err
	}
	nodeCount, edgeCount, partialCount := 0, 0, 0
	for _, snapshot := range snapshots {
		nodeCount += len(snapshot.Nodes)
		edgeCount += len(snapshot.Edges)
		if snapshot.Partial {
			partialCount++
		}
	}
	span.Set("snapshot-node-count", nodeCount)
	span.Set("snapshot-edge-count", edgeCount)
	span.Set("partial-snapshot-count", partialCount)
	return snapshots, nil
}

func (q *TimeGraph) propagateSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, nodeBits map[uint64]uint64, edges []sharedTopologyEdgeState) (result map[uint64]uint64, seedCount int, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-propagate-topology")
	defer finishTimeGraphStage(ctx, span, "topology-propagation", time.Now(), &err)
	span.Set("max-hops", query.MaxHops)
	reachable := make(map[uint64]uint64)
	frontier := make(map[uint64]uint64)
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
		resourceType, info := q.nodeBuilder.Info(node)
		if resourceType != query.SourceType {
			continue
		}
		matchedBits := uint64(0)
		for index, timestamp := range grid.Timestamps {
			if bits&(uint64(1)<<index) == 0 {
				continue
			}
			_, timestampInfo := q.nodeInfoAt(timestamp, node, info)
			if q.matchesPartial(timestampInfo, query.SourceMatcher) {
				matchedBits |= uint64(1) << index
			}
		}
		if matchedBits != 0 {
			reachable[node] = matchedBits
			frontier[node] = matchedBits
		}
	}
	seedCount = len(frontier)
	span.Set("source-match-node-count", seedCount)
	if len(frontierSizes) < 64 {
		frontierSizes = append(frontierSizes, int64(len(frontier)))
	}

	for level := 0; level < query.MaxHops; level++ {
		if err := ctx.Err(); err != nil {
			return nil, seedCount, err
		}
		discovered := make(map[uint64]uint64)
		for _, edge := range edges {
			if err := ctx.Err(); err != nil {
				return nil, seedCount, err
			}
			candidate := frontier[edge.key.source] & edge.activeBits & nodeBits[edge.key.target]
			if candidate != 0 {
				discovered[edge.key.target] |= candidate
			}
		}
		nextFrontier := make(map[uint64]uint64)
		for node, candidate := range discovered {
			newBits := candidate &^ reachable[node]
			if newBits != 0 {
				reachable[node] |= newBits
				nextFrontier[node] = newBits
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
func (q *TimeGraph) materializeSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, reachable map[uint64]uint64, edges []sharedTopologyEdgeState) (snapshots []SharedTopologySnapshot, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-materialize-topology")
	defer span.End(&err)
	started := time.Now()
	budget := newTopologyOutputBudget(grid, query.PartialTimestamps)
	defer func() {
		span.Set("output-budget-elements-used", budget.elements)
		span.Set("output-budget-elements-limit", budget.maxElements)
		span.Set("output-budget-bytes-used", budget.bytes)
		span.Set("output-budget-bytes-limit", budget.maxBytes)
		SetTimeGraphLimitTrace(span, err)
		metric.CMDBTimeGraphStageObserve(ctx, "topology-materialize", metric.CMDBTimeGraphErrorResult(err), time.Since(started))
	}()
	if err := budget.checkBytes(0); err != nil {
		return nil, err
	}
	result := make([]SharedTopologySnapshot, len(grid.Timestamps))
	for index, timestamp := range grid.Timestamps {
		result[index].Timestamp = timestamp
		if reason, ok := query.PartialTimestamps[timestamp]; ok {
			result[index].Partial = true
			result[index].PartialReason = reason
		}
	}
	for node, bits := range reachable {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for index := range grid.Timestamps {
			if bits&(uint64(1)<<index) == 0 {
				continue
			}
			_, info := q.nodeBuilder.Info(node)
			resourceType, info := q.nodeInfoAt(grid.Timestamps[index], node, info)
			if err := budget.add(topologyNodeByteBound(resourceType, info)); err != nil {
				return nil, err
			}
			result[index].Nodes = append(result[index].Nodes, SharedTopologyNode{
				ID:           node,
				ResourceType: resourceType,
				Dimensions:   info,
			})
		}
	}
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		activeBits := edge.activeBits & reachable[edge.key.source] & reachable[edge.key.target]
		if activeBits == 0 {
			continue
		}
		for index := range grid.Timestamps {
			if activeBits&(uint64(1)<<index) == 0 {
				continue
			}
			if err := budget.add(topologyEdgeByteBound(edge.key)); err != nil {
				return nil, err
			}
			result[index].Edges = append(result[index].Edges, SharedTopologyEdge{
				Source:       edge.key.source,
				Target:       edge.key.target,
				RelationType: edge.key.relation.relationType,
				MetricName:   edge.key.relation.metricName,
				Category:     edge.key.relation.category,
				Direction:    edge.key.relation.direction,
			})
		}
	}
	for index := range result {
		sort.Slice(result[index].Nodes, func(i, j int) bool {
			return result[index].Nodes[i].ID < result[index].Nodes[j].ID
		})
		sort.Slice(result[index].Edges, func(i, j int) bool {
			left, right := result[index].Edges[i], result[index].Edges[j]
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
		})
	}
	return result, nil
}

// nodeInfoAt 返回节点在指定时间点的属性；没有时间点属性时回退到稳定身份
// 信息，保证关系指标只提供主键的场景仍能正常输出。
func (q *TimeGraph) nodeInfoAt(timestamp int64, node uint64, fallback cmdb.Matcher) (cmdb.Resource, cmdb.Matcher) {
	resourceType, _ := q.nodeBuilder.Info(node)
	if q.shared != nil {
		bit := q.shared.timestampBits[timestamp]
		for _, version := range q.shared.nodeInfos[node] {
			if version.activeBits&bit != 0 {
				return resourceType, cloneMatcher(version.dimensions)
			}
		}
		return resourceType, fallback
	}
	if timestampInfo, ok := q.nodeInfos[timestamp][node]; ok {
		return resourceType, cloneMatcher(timestampInfo)
	}
	return resourceType, fallback
}
