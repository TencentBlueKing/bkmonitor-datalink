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
		ids := make([]uint64, 0, len(reachable))
		for id := range reachable {
			if err := ctx.Err(); err != nil {
				return topologySnapshotCounts{}, err
			}
			ids = append(ids, id)
		}
		slices.Sort(ids)
		kept := make(map[uint64]bool, len(ids))
		nodeCount := 0
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return topologySnapshotCounts{}, err
			}
			resource := q.nodeBuilder.resourceName(id)
			keep := len(targets) == 0 || resource == query.SourceType || slices.Contains(targets, resource)
			kept[id] = keep
			if keep {
				nodeCount++
			}
		}
		edgeCount := 0
		for _, edge := range edges {
			if err := ctx.Err(); err != nil {
				return topologySnapshotCounts{}, err
			}
			if kept[edge.key.source] && kept[edge.key.target] && edge.activeBits.intersectsBoth(reachable[edge.key.source], reachable[edge.key.target]) {
				edgeCount++
			}
		}
		plan.Nodes = make([]cmdb.SharedTopologySnapshotPlanNode, 0, nodeCount)
		plan.Edges = make([]cmdb.SharedTopologySnapshotPlanEdge, 0, edgeCount)
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return topologySnapshotCounts{}, err
			}
			if !kept[id] {
				continue
			}
			resource := q.nodeBuilder.resourceName(id)
			remaining := reachable[id]
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
			if !kept[key.source] || !kept[key.target] {
				continue
			}
			active := edge.activeBits.intersect(reachable[key.source]).intersect(reachable[key.target])
			if active.empty() {
				continue
			}
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
