// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
package v1beta3

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func ownedTopologyTimeMask(bits timeBitmap) cmdb.SharedTopologyTimeMask {
	return cmdb.SharedTopologyTimeMask{First: bits.first, Rest: append([]uint64(nil), bits.rest...)}
}

// findSharedTopologySnapshotPlan 在图锁内把可达结果转为自有的属性版本和时间 mask。
// 调用方可在 graph.Clean 后逐帧编码，无须持有图存储或复制整张快照。
func (q *TimeGraph) findSharedTopologySnapshotPlan(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, targets []cmdb.Resource) (*cmdb.SharedTopologySnapshotPlan, error) {
	var plan *cmdb.SharedTopologySnapshotPlan
	_, err := q.findSharedTopology(ctx, grid, query, func(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState) (topologySnapshotCounts, error) {
		if !yoloMode || q.shared == nil {
			return topologySnapshotCounts{}, fmt.Errorf("snapshot plan requires YOLO shared storage")
		}
		plan = &cmdb.SharedTopologySnapshotPlan{Timestamps: append([]int64(nil), grid.Timestamps...)}
		for i, ts := range grid.Timestamps {
			if reason, ok := query.PartialTimestamps[ts]; ok {
				plan.Partial = append(plan.Partial, cmdb.SharedTopologySnapshotPlanPartial{Index: i, Reason: reason})
			}
		}
		// Output filtering applies after traversal. Keep the original reachability
		// map when all resource types are requested; no second membership map is needed.
		selected := reachable
		if len(targets) > 0 {
			selected = make(map[uint64]timeBitmap, len(reachable))
		}
		ids := make([]uint64, 0, len(reachable))
		for id, active := range reachable {
			if err := ctx.Err(); err != nil {
				return topologySnapshotCounts{}, err
			}
			if len(targets) > 0 {
				resource := q.nodeBuilder.resourceName(id)
				if resource != query.SourceType && !slices.Contains(targets, resource) {
					continue
				}
				selected[id] = active
			}
			ids = append(ids, id)
		}
		slices.Sort(ids)
		// The state slice is local to this traversal. Compute the final induced
		// edge masks once, before allocating the exported DTOs or sorting rows.
		edges, err := snapshotPlanReachableEdges(ctx, selected, edges)
		if err != nil {
			return topologySnapshotCounts{}, err
		}
		plan.Nodes = make([]cmdb.SharedTopologySnapshotPlanNode, 0, len(ids))
		plan.Edges = make([]cmdb.SharedTopologySnapshotPlanEdge, 0, len(edges))
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return topologySnapshotCounts{}, err
			}
			resource := q.nodeBuilder.resourceName(id)
			remaining := reachable[id]
			// 各属性版本从剩余可达时间位中取交集，保证同一节点每帧只输出一个版本。
			appendVersion := func(bits timeBitmap, dimensions cmdb.Matcher) {
				if bits.empty() {
					return
				}
				plan.Nodes = append(plan.Nodes, cmdb.SharedTopologySnapshotPlanNode{Node: cmdb.SharedTopologyNode{ID: id, ResourceType: resource, Dimensions: cloneMatcher(dimensions)}, Mask: ownedTopologyTimeMask(bits)})
				plan.NodeOccurrences += bits.count()
			}
			for _, version := range q.shared.nodeInfos[id] {
				if err := ctx.Err(); err != nil {
					return topologySnapshotCounts{}, err
				}
				active := remaining.intersect(version.activeBits)
				appendVersion(active, version.dimensions)
				remaining = remaining.subtract(active)
			}
			if !remaining.empty() {
				_, fallback := q.nodeBuilder.Info(id)
				appendVersion(remaining, fallback)
			}
		}
		sort.Slice(edges, func(i, j int) bool { return lessTopologyEdgeKey(edges[i].key, edges[j].key) })
		for _, edge := range edges {
			if err := ctx.Err(); err != nil {
				return topologySnapshotCounts{}, err
			}
			key := edge.key
			active := edge.activeBits
			plan.Edges = append(plan.Edges, cmdb.SharedTopologySnapshotPlanEdge{Edge: cmdb.SharedTopologyEdge{Source: key.source, Target: key.target, RelationType: key.relation.relationType, MetricName: key.relation.metricName, Category: key.relation.category, Direction: key.relation.direction}, Mask: ownedTopologyTimeMask(active)})
			plan.EdgeOccurrences += active.count()
		}
		return topologySnapshotCounts{nodes: plan.NodeOccurrences, edges: plan.EdgeOccurrences, partial: len(plan.Partial)}, ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// snapshotPlanReachableEdges compacts a traversal-owned state slice. Bitmap
// intersections allocate their own words and never mutate the graph's masks.
func snapshotPlanReachableEdges(ctx context.Context, reachable map[uint64]timeBitmap, edges []sharedTopologyEdgeState) ([]sharedTopologyEdgeState, error) {
	kept := edges[:0]
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		active := edge.activeBits.intersect(reachable[edge.key.source]).intersect(reachable[edge.key.target])
		if active.empty() {
			continue
		}
		edge.activeBits = active
		kept = append(kept, edge)
	}
	clear(edges[len(kept):])
	return kept, nil
}
