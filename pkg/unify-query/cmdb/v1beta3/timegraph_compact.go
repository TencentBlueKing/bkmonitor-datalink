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
	"unsafe"

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
	words := make([]string, 1, len(bits.rest)+1)
	words[0] = encode(bits.first)
	for _, word := range bits.rest {
		words = append(words, encode(word))
	}
	for len(words) > 1 && words[len(words)-1] == "0000000000000000" {
		words = words[:len(words)-1]
	}
	return words
}

// FindCompactTopology 传播后直接导出自有的节点、关系版本及时间 mask，
// 避免按 K 个时间点分配完整快照。输出预算按实际存储版本收费，
// 展开后的出现次数仍供调用方和负载观测使用。
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
	ctx, releaseTraversal, err := q.withTraversalReservation(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseTraversal()
	nodeBits, edges, err := q.sharedTopologyState(ctx, grid, query)
	if err != nil {
		return nil, err
	}
	reachable, seeds, err := q.propagateSharedTopology(ctx, grid, query, nodeBits, edges)
	if err != nil {
		return nil, err
	}
	span.Set("source-match-node-count", seeds)
	budget := newTopologyOutputBudget(grid, query.PartialTimestamps)
	budget.ctx = ctx
	defer func() {
		span.Set("expanded-budget-elements", budget.elements)
		span.Set("expanded-budget-bytes", budget.bytes)
	}()
	if err := budget.addCharge(int64(unsafe.Sizeof(cmdb.CompactTopology{}))+int64(len(grid.timestamps))*8+int64(len(query.PartialTimestamps))*int64(unsafe.Sizeof(cmdb.CompactTopologyPartial{})), 0); err != nil {
		return nil, err
	}
	if err := budget.checkBytes(0); err != nil {
		return nil, err
	}
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
	if err := budget.addCharge(int64(len(reachable))*(8+48)+128, 0); err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(reachable))
	for id := range reachable {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	kept := make(map[uint64]bool, len(ids))
	if yoloMode {
		// 按实际输出预分配，而非按更宽的候选集合；额外属性版本按需扩容。
		keptNodes := 0
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			resource := q.nodeBuilder.resourceName(id)
			keep := len(targets) == 0 || resource == query.SourceType || slices.Contains(targets, resource)
			kept[id] = keep
			if keep {
				keptNodes++
			}
		}
		keptEdges := 0
		for _, edge := range edges {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if kept[edge.key.source] && kept[edge.key.target] && edge.activeBits.intersectsBoth(reachable[edge.key.source], reachable[edge.key.target]) {
				keptEdges++
			}
		}
		result.Nodes = make([]cmdb.CompactTopologyNode, 0, keptNodes)
		result.Edges = make([]cmdb.CompactTopologyEdge, 0, keptEdges)
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resource := q.nodeBuilder.resourceName(id)
		keep := len(targets) == 0 || resource == query.SourceType || slices.Contains(targets, resource)
		kept[id] = keep
		remaining := reachable[id]
		appendVersion := func(bits timeBitmap, dimensions cmdb.Matcher) error {
			count := bits.count()
			if count == 0 {
				return nil
			}
			if keep {
				if err := budget.retainMatcher(resource, dimensions); err != nil {
					return err
				}
				charge := 3*int64(unsafe.Sizeof(cmdb.CompactTopologyNode{})) + topologyMatcherMemory(len(dimensions)) + 20 + int64(1+len(bits.rest))*(16+int64(unsafe.Sizeof("")))
				if err := budget.addCharge(charge, 1); err != nil {
					return err
				}
				result.Nodes = append(result.Nodes, cmdb.CompactTopologyNode{
					ID:           strconv.FormatUint(id, 10),
					ResourceType: resource,
					Dimensions:   cloneMatcher(dimensions),
					Mask:         compactTimeMask(bits),
				})
				result.NodeOccurrences += count
			}
			return nil
		}
		for _, version := range q.shared.nodeInfos[id] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			bits := remaining.intersect(version.activeBits)
			if err := appendVersion(bits, version.dimensions); err != nil {
				return nil, err
			}
			remaining = remaining.subtract(bits)
		}
		if !remaining.empty() {
			fallback := q.topologyBorrowedNodeInfo(id)
			if err := appendVersion(remaining, fallback); err != nil {
				return nil, err
			}
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
		if err := budget.retainEdge(edge.key); err != nil {
			return nil, err
		}
		charge := 3*int64(unsafe.Sizeof(cmdb.CompactTopologyEdge{})) + 40 + int64(1+len(bits.rest))*(16+int64(unsafe.Sizeof("")))
		if err := budget.addCharge(charge, 1); err != nil {
			return nil, err
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
	if err := commitTopologyOutput(ctx, func() int64 { return topologyCompactOwnedLowerBound(result) }); err != nil {
		return nil, err
	}
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
