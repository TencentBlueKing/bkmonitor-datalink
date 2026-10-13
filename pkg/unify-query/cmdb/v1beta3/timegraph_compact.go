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

// FindCompactTopology exports owned dictionaries directly after propagation;
// it never allocates K complete snapshots. Output budgets charge stored versions,
// while occurrence counts remain available for caller and workload observability.
func (q *TimeGraph) FindCompactTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery, targets []cmdb.Resource) (result *cmdb.CompactTopology, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-compact-topology")
	defer finishTimeGraphStage(ctx, span, "topology-materialize", time.Now(), &err)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	grid, err = NewTopologyGrid(grid.Timestamps)
	if err != nil {
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
	budget := newTopologyOutputBudget(grid, query.PartialTimestamps)
	budget.ctx = ctx
	defer func() {
		span.Set("expanded-budget-elements", budget.elements)
		span.Set("expanded-budget-bytes", budget.bytes)
	}()
	if err := budget.checkBytes(0); err != nil {
		return nil, err
	}
	result = &cmdb.CompactTopology{
		Version: 1, Timestamps: append([]int64(nil), grid.Timestamps...),
		Nodes: make([]cmdb.CompactTopologyNode, 0), Edges: make([]cmdb.CompactTopologyEdge, 0),
		Partial: make([]cmdb.CompactTopologyPartial, 0),
	}
	for i, ts := range grid.Timestamps {
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
	if yoloMode {
		// Reserve actual output, rather than the potentially much wider candidates.
		// Additional attribute versions still grow the node slice normally.
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
			// Charge each materialized version, including ID and bitmap overhead.
			// Input/graph budgets continue to bound traversal before target filtering.
			if err := budget.addCharge(topologyNodeByteBound(resource, dimensions)+64+int64(20*(1+len(bits.rest))), 1); err != nil {
				return err
			}
			if keep {
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
			_, fallback := q.nodeBuilder.Info(id)
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
		if err := budget.addCharge(topologyEdgeByteBound(edge.key)+64+int64(20*(1+len(bits.rest))), 1); err != nil {
			return nil, err
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
