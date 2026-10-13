// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// sharedTimeGraph 只保存一份实体和关系。相同属性跨时间复用，属性变化用
// 不相交的时间位分组表示，不为每个评估点创建图或 timestamp map。
// 所有读写由所属 TimeGraph 的锁保护，生命周期仅限单次查询。
type sharedTimeGraph struct {
	grid                 TopologyGrid
	fullGridBits         timeBitmap
	timestampIndex       map[int64]int
	nodeBits             map[uint64]timeBitmap
	relationNodeBits     map[uint64]timeBitmap
	sourceInfoBits       map[uint64]timeBitmap
	nodeInfoVersionCount int
	nodeInfos            map[uint64][]sharedNodeInfo
	edgeBits             map[timeGraphTopologyEdgeKey]timeBitmap
	edgePairBits         map[timeGraphEdgeKey]timeBitmap
}

type sharedNodeInfo struct {
	activeBits timeBitmap
	dimensions cmdb.Matcher
}

func newSharedTimeGraph(cfg *TimeGraphConfig, grid TopologyGrid) (*TimeGraph, error) {
	grid, err := NewTopologyGrid(grid.Timestamps)
	if err != nil {
		return nil, err
	}
	q := newTimeGraphWithConfig(cfg)
	q.shared = &sharedTimeGraph{
		grid:             grid,
		timestampIndex:   make(map[int64]int, len(grid.Timestamps)),
		nodeBits:         make(map[uint64]timeBitmap),
		relationNodeBits: make(map[uint64]timeBitmap),
		nodeInfos:        make(map[uint64][]sharedNodeInfo),
		edgeBits:         make(map[timeGraphTopologyEdgeKey]timeBitmap),
		edgePairBits:     make(map[timeGraphEdgeKey]timeBitmap),
	}
	for index, timestamp := range grid.Timestamps {
		q.shared.timestampIndex[timestamp] = index
		q.shared.fullGridBits.set(index)
	}
	return q, nil
}

func (g *sharedTimeGraph) clear() {
	clear(g.nodeBits)
	clear(g.relationNodeBits)
	clear(g.sourceInfoBits)
	g.nodeInfoVersionCount = 0
	clear(g.nodeInfos)
	clear(g.edgeBits)
	clear(g.edgePairBits)
}

func (q *TimeGraph) timepointCount() int {
	if q.shared == nil {
		return len(q.timeGraph)
	}
	var active timeBitmap
	for _, nodeBits := range q.shared.nodeBits {
		active = active.union(nodeBits)
	}
	return active.count()
}

func (g *sharedTimeGraph) timeBits(timestamps []int64) (timeBitmap, error) {
	// The full grid is common; its immutable words need no per-point lookup.
	if slices.Equal(timestamps, g.grid.Timestamps) {
		return g.fullGridBits, nil
	}
	var active timeBitmap
	for _, timestamp := range timestamps {
		index, ok := g.timestampIndex[timestamp]
		if !ok {
			return timeBitmap{}, fmt.Errorf("sample timestamp %d does not match topology time grid", timestamp)
		}
		active.set(index)
	}
	return active, nil
}

func appendSharedNodeInfo(versions []sharedNodeInfo, active timeBitmap, info cmdb.Matcher) []sharedNodeInfo {
	if active.empty() {
		return versions
	}
	for index := range versions {
		if maps.Equal(versions[index].dimensions, info) {
			versions[index].activeBits = versions[index].activeBits.union(active)
			return versions
		}
	}
	return append(versions, sharedNodeInfo{activeBits: active, dimensions: info})
}

func (q *TimeGraph) setSharedNodeInfo(node uint64, info cmdb.Matcher, active timeBitmap) error {
	g := q.shared
	newPoints := active.subtract(g.nodeBits[node]).count()

	previous := g.nodeInfos[node]
	if len(previous) == 1 && active.isSubsetOf(previous[0].activeBits) {
		unchanged := true
		for key, value := range info {
			current, present := previous[0].dimensions[key]
			if !present || value > current {
				unchanged = false
				break
			}
		}
		if unchanged {
			if q.maxNodeInfos > 0 && g.nodeInfoVersionCount > q.maxNodeInfos {
				return &ResultLimitError{Reason: "max_graph_node_infos", Count: g.nodeInfoVersionCount, Limit: q.maxNodeInfos}
			}
			return nil
		}
	}
	versions := make([]sharedNodeInfo, 0, len(previous)+1)
	remaining := active
	for _, version := range previous {
		overlap := version.activeBits.intersect(active)
		versions = appendSharedNodeInfo(versions, version.activeBits.subtract(active), version.dimensions)
		if !overlap.empty() {
			versions = appendSharedNodeInfo(versions, overlap, mergeMatcher(version.dimensions, info))
			remaining = remaining.subtract(overlap)
		}
	}
	if !remaining.empty() {
		var owned cmdb.Matcher
		borrowed := false
		if len(previous) == 0 {
			owned, borrowed = q.nodeBuilder.matchingInfoView(node, info)
		}
		if !borrowed {
			owned = cloneMatcher(info)
		}
		versions = appendSharedNodeInfo(versions, remaining, owned)
	}
	versionCount := g.nodeInfoVersionCount + len(versions) - len(previous)
	if q.maxNodeInfos > 0 && versionCount > q.maxNodeInfos {
		return &ResultLimitError{Reason: "max_graph_node_infos", Count: versionCount, Limit: q.maxNodeInfos}
	}
	g.nodeInfoVersionCount = versionCount
	g.nodeInfos[node] = versions
	g.nodeBits[node] = g.nodeBits[node].union(active)
	q.nodeInfoCount += newPoints
	return nil
}

func (q *TimeGraph) addSharedRelation(ctx context.Context, relation cmdb.Relation, direction string, source, target uint64, sourceInfo, targetInfo cmdb.Matcher, timestamps []int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g := q.shared
	active, err := g.timeBits(timestamps)
	if err != nil {
		return err
	}
	if active.empty() {
		return nil
	}
	pair := timeGraphEdgeKey{source: source, target: target}
	// Storage limits count full relation identities; occurrence metrics and
	// input-point, memory, time and traversal limits remain independent.
	newEdges := active.subtract(g.edgePairBits[pair]).count()
	key := timeGraphTopologyEdgeKey{source: source, target: target, relation: timeGraphEdgeRelation{
		relationType: relation.RelationType, metricName: relation.MetricName,
		category: relation.Category, direction: direction,
	}}
	if _, exists := g.edgeBits[key]; !exists && !active.empty() && q.maxEdges > 0 && len(g.edgeBits) >= q.maxEdges {
		return &ResultLimitError{Reason: "max_graph_edges", Count: len(g.edgeBits) + 1, Limit: q.maxEdges}
	}
	if err := q.setSharedNodeInfo(source, sourceInfo, active); err != nil {
		return err
	}
	if err := q.setSharedNodeInfo(target, targetInfo, active); err != nil {
		return err
	}

	g.relationNodeBits[source] = g.relationNodeBits[source].union(active)
	g.relationNodeBits[target] = g.relationNodeBits[target].union(active)
	g.edgeBits[key] = g.edgeBits[key].union(active)
	g.edgePairBits[pair] = g.edgePairBits[pair].union(active)
	q.edgeCount += newEdges
	return nil
}

func (g *sharedTimeGraph) state(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) (map[uint64]timeBitmap, []sharedTopologyEdgeState, error) {
	if !slices.Equal(grid.Timestamps, g.grid.Timestamps) {
		return nil, nil, fmt.Errorf("topology query grid does not match shared graph grid")
	}
	edges := make([]sharedTopologyEdgeState, 0, len(g.edgeBits))
	for key, active := range g.edgeBits {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if len(query.AllowedCategories) > 0 && !slices.Contains(query.AllowedCategories, RelationCategory(key.relation.category)) {
			continue
		}
		if len(query.AllowedRelationTypes) > 0 && !slices.Contains(query.AllowedRelationTypes, key.relation.relationType) {
			continue
		}
		if key.relation.category == string(RelationCategoryDynamic) && query.Direction != "" && query.Direction != DirectionBoth && key.relation.direction != "" && key.relation.direction != string(query.Direction) {
			continue
		}
		edges = append(edges, sharedTopologyEdgeState{key: key, activeBits: active})
	}
	return g.nodeBits, edges, nil
}

// sharedTopologyRootBits keeps info-only nodes out of the topology root set.
// When attributes were requested, only the info query's per-frame matches qualify.
func (q *TimeGraph) sharedTopologyRootBits() map[uint64]timeBitmap {
	roots := make(map[uint64]timeBitmap, len(q.shared.relationNodeBits))
	for id, bits := range q.shared.relationNodeBits {
		if q.shared.sourceInfoBits != nil {
			bits = bits.intersect(q.shared.sourceInfoBits[id])
		}
		if !bits.empty() {
			roots[id] = bits
		}
	}
	return roots
}
