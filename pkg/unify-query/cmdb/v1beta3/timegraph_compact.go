// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func compactTimeMask(bits timeBitmap) []string {
	encode := func(word uint64) string {
		const hex = "0123456789abcdef"
		var buffer [16]byte
		for index := len(buffer) - 1; index >= 0; index-- {
			buffer[index] = hex[word&15]
			word >>= 4
		}
		return string(buffer[:])
	}
	return []string{encode(bits.first)}
}

// FindCompactTopology 传播后直接导出自有的节点、关系版本及时间 mask，
// 避免按 K 个时间点分配完整快照。展开后的出现次数仍供调用方和负载观测使用。
func (q *TimeGraph) FindCompactTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, targets []cmdb.Resource) (result *cmdb.CompactTopology, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-compact-topology")
	defer finishTimeGraphStage(ctx, span, "topology-materialize", time.Now(), &err)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q.lock.RLock()
	defer q.lock.RUnlock()
	if q.shared == nil {
		return nil, fmt.Errorf("compact topology requires shared storage")
	}
	nodeBits, edges, err := q.sharedTopologyState(ctx, grid, query)
	if err != nil {
		return nil, err
	}
	reachable, seeds, err := q.propagateSharedTopology(ctx, grid, query, nodeBits, edges)
	if err != nil {
		return nil, err
	}
	span.Set("source-match-node-count", seeds)
	result = &cmdb.CompactTopology{
		Version: 1, Timestamps: append([]int64(nil), grid.timestamps...),
		Nodes: make([]cmdb.CompactTopologyNode, 0), Edges: make([]cmdb.CompactTopologyEdge, 0),
		Partial: make([]cmdb.CompactTopologyPartial, 0, len(query.PartialTimestamps)),
	}
	for i, ts := range grid.timestamps {
		if reason, ok := query.PartialTimestamps[ts]; ok {
			result.Partial = append(result.Partial, cmdb.CompactTopologyPartial{Index: i, Reason: reason})
		}
	}
	ids := make([]uint64, 0, len(reachable))
	for id := range reachable {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	kept := make(map[uint64]bool, len(ids))

	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resource := q.nodeBuilder.resourceName(id)
		keep := len(targets) == 0 || resource == query.SourceType || slices.Contains(targets, resource)
		kept[id] = keep
		remaining := reachable[id]
		appendVersion := func(bits timeBitmap, dimensions cmdb.Matcher) {
			count := bits.count()
			if count == 0 || !keep {
				return
			}
			result.Nodes = append(result.Nodes, cmdb.CompactTopologyNode{
				ID:           strconv.FormatUint(id, 10),
				ResourceType: resource,
				Dimensions:   cloneMatcher(dimensions),
				Mask:         compactTimeMask(bits),
			})
			result.NodeOccurrences += count
		}
		for _, version := range q.shared.nodeInfos[id] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			bits := remaining.intersect(version.activeBits)
			appendVersion(bits, version.dimensions)
			remaining = remaining.subtract(bits)
		}
		if !remaining.empty() {
			appendVersion(remaining, q.topologyBorrowedNodeInfo(id))
		}
	}
	sort.Slice(edges, func(i, j int) bool { return lessTopologyEdgeKey(edges[i].key, edges[j].key) })
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bits := edge.activeBits.intersect(reachable[edge.key.source]).intersect(reachable[edge.key.target])
		count := bits.count()
		if count == 0 {
			continue
		}
		if !kept[edge.key.source] || !kept[edge.key.target] {
			continue
		}
		key := edge.key
		result.Edges = append(result.Edges, cmdb.CompactTopologyEdge{
			Source:       strconv.FormatUint(key.source, 10),
			Target:       strconv.FormatUint(key.target, 10),
			RelationType: key.relation.relationType,
			MetricName:   key.relation.metricName,
			Category:     key.relation.category,
			Direction:    key.relation.direction,
			Mask:         compactTimeMask(bits),
		})
		result.EdgeOccurrences += count
	}
	span.Set("node-version-count", len(result.Nodes))
	span.Set("edge-version-count", len(result.Edges))
	span.Set("snapshot-node-count", result.NodeOccurrences)
	span.Set("snapshot-edge-count", result.EdgeOccurrences)
	return result, ctx.Err()
}

func lessTopologyEdgeKey(left, right timeGraphTopologyEdgeKey) bool {
	if left.source != right.source {
		return left.source < right.source
	}
	if left.target != right.target {
		return left.target < right.target
	}
	if left.relation.relationType != right.relation.relationType {
		return left.relation.relationType < right.relation.relationType
	}
	if left.relation.metricName != right.relation.metricName {
		return left.relation.metricName < right.relation.metricName
	}
	if left.relation.category != right.relation.category {
		return left.relation.category < right.relation.category
	}
	return left.relation.direction < right.relation.direction
}
