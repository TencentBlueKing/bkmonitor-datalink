// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"maps"
	"math/bits"
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
	uniformStart         int64
	uniformEnd           int64
	uniformStep          int64
	nodeBits             map[uint64]timeBitmap
	relationNodeBits     map[uint64]timeBitmap
	sourceInfoBits       map[uint64]timeBitmap
	nodeInfoVersionCount int
	nodeInfos            map[uint64][]sharedNodeInfo
	edgeBits             map[timeGraphTopologyEdgeKey]timeBitmap
	edgePairBits         map[timeGraphEdgeKey]timeBitmap
	compactEdges         map[sharedCompactEdgeKey]timeBitmap
	wordEdges            map[sharedCompactEdgeKey]uint64
	wordPairs            map[timeGraphEdgeKey]uint64
	relationIDs          map[timeGraphEdgeRelation]uint64
	relations            []timeGraphEdgeRelation
	relationCache        [2]sharedRelationSlot
}

type (
	sharedCompactEdgeKey struct{ source, target, relation uint64 }
	sharedRelationSlot   struct {
		identity timeGraphEdgeRelation
		id       uint64
	}
)

func (g *sharedTimeGraph) edgeCount() int {
	if g.wordEdges != nil {
		return len(g.wordEdges)
	}
	if g.compactEdges != nil {
		return len(g.compactEdges)
	}
	return len(g.edgeBits)
}

func (g *sharedTimeGraph) compactRelationID(identity timeGraphEdgeRelation) uint64 {
	for _, slot := range g.relationCache {
		if slot.id != 0 && slot.identity == identity {
			return slot.id
		}
	}
	id, ok := g.relationIDs[identity]
	if !ok {
		g.relations = append(g.relations, identity)
		id = uint64(len(g.relations))
		g.relationIDs[identity] = id
	}
	g.relationCache[id&1] = sharedRelationSlot{identity, id}
	return id
}

type sharedNodeInfo struct {
	activeBits timeBitmap
	dimensions cmdb.Matcher
}

func newSharedTimeGraph(cfg *TimeGraphConfig, grid TopologyGrid) *TimeGraph {
	q := newTimeGraphWithConfig(cfg)
	q.shared = &sharedTimeGraph{
		grid:             grid,
		timestampIndex:   make(map[int64]int, len(grid.timestamps)),
		nodeBits:         make(map[uint64]timeBitmap),
		relationNodeBits: make(map[uint64]timeBitmap),
		nodeInfos:        make(map[uint64][]sharedNodeInfo),
		edgeBits:         make(map[timeGraphTopologyEdgeKey]timeBitmap),
		edgePairBits:     make(map[timeGraphEdgeKey]timeBitmap),
	}
	for index, timestamp := range grid.timestamps {
		q.shared.timestampIndex[timestamp] = index
		q.shared.fullGridBits.set(index)
	}
	if len(grid.timestamps) > 1 {
		first, last := grid.timestamps[0], grid.timestamps[len(grid.timestamps)-1]
		step, span := grid.timestamps[1]-first, last-first
		if step > 0 && span >= 0 && int64(len(grid.timestamps)-1) <= span/step {
			uniform := true
			for index, timestamp := range grid.timestamps {
				if timestamp != first+int64(index)*step {
					uniform = false
					break
				}
			}
			if uniform {
				q.shared.uniformStart, q.shared.uniformEnd, q.shared.uniformStep = first, last, step
			}
		}
	}
	return q
}

func (g *sharedTimeGraph) clear() {
	clear(g.nodeBits)
	clear(g.relationNodeBits)
	clear(g.sourceInfoBits)
	g.nodeInfoVersionCount = 0
	clear(g.nodeInfos)
	clear(g.edgeBits)
	clear(g.edgePairBits)
	clear(g.compactEdges)
	clear(g.wordEdges)
	clear(g.wordPairs)
	clear(g.relationIDs)
	g.relations = nil
	g.relationCache = [2]sharedRelationSlot{}
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
	// 完整网格可共用不变的位图字，无须逐点查索引。
	if slices.Equal(timestamps, g.grid.timestamps) {
		return g.fullGridBits, nil
	}
	var active timeBitmap
	if g.uniformStep > 0 {
		for _, timestamp := range timestamps {
			if timestamp < g.uniformStart || timestamp > g.uniformEnd {
				return timeBitmap{}, fmt.Errorf("sample timestamp %d does not match topology time grid", timestamp)
			}
			offset := timestamp - g.uniformStart
			index := offset / g.uniformStep
			if offset-index*g.uniformStep != 0 {
				return timeBitmap{}, fmt.Errorf("sample timestamp %d does not match topology time grid", timestamp)
			}
			active.set(int(index))
		}
		return active, nil
	}
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

func (q *TimeGraph) setSharedNodeInfo(node uint64, info cmdb.Matcher, active timeBitmap) (err error) {
	g := q.shared
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
	newPoints := active.subtract(g.nodeBits[node]).count()
	if q.residency != nil {
		reservation, reserveErr := q.reserveInfoChange(node, info, active)
		if reserveErr != nil {
			return reserveErr
		}
		defer func() {
			if settleErr := reservation.settle(g.nodeInfos[node], err == nil); err == nil {
				err = settleErr
			}
		}()
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
	active, err := q.shared.timeBits(timestamps)
	if err != nil {
		return err
	}
	return q.addSharedRelationWithBits(ctx, relation, direction, source, target, sourceInfo, targetInfo, active, true)
}

func (q *TimeGraph) addSharedRelationWithBits(ctx context.Context, relation cmdb.Relation, direction string, source, target uint64, sourceInfo, targetInfo cmdb.Matcher, active timeBitmap, updateNodes bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g := q.shared
	if active.empty() {
		return nil
	}
	pair := timeGraphEdgeKey{source: source, target: target}
	// 存储上限按完整关系身份计数；出现次数、输入点、内存、时间及遍历上限独立计量。
	var newEdges int
	if g.wordPairs != nil {
		newEdges = bits.OnesCount64(active.first &^ g.wordPairs[pair])
	} else {
		newEdges = active.subtract(g.edgePairBits[pair]).count()
	}
	key := timeGraphTopologyEdgeKey{source: source, target: target, relation: timeGraphEdgeRelation{
		relationType: relation.RelationType, metricName: relation.MetricName,
		category: relation.Category, direction: direction,
	}}
	if err := q.reserveSharedEdge(key, pair); err != nil {
		return err
	}
	var compact sharedCompactEdgeKey
	if g.wordEdges != nil || g.compactEdges != nil {
		compact = sharedCompactEdgeKey{source, target, g.compactRelationID(key.relation)}
	}
	// A membership lookup is only needed at the configured edge limit. Below
	// it (or with no limit), inserting or updating a relation is always allowed.
	if q.residency == nil && q.maxEdges > 0 && g.edgeCount() >= q.maxEdges {
		var exists bool
		switch {
		case g.wordEdges != nil:
			_, exists = g.wordEdges[compact]
		case g.compactEdges != nil:
			_, exists = g.compactEdges[compact]
		default:
			_, exists = g.edgeBits[key]
		}
		if !exists {
			return &ResultLimitError{Reason: "max_graph_edges", Count: g.edgeCount() + 1, Limit: q.maxEdges}
		}
	}
	if updateNodes {
		if err := q.setSharedNodeInfo(source, sourceInfo, active); err != nil {
			return err
		}
		if err := q.setSharedNodeInfo(target, targetInfo, active); err != nil {
			return err
		}
		g.relationNodeBits[source] = g.relationNodeBits[source].union(active)
		g.relationNodeBits[target] = g.relationNodeBits[target].union(active)
	}
	switch {
	case g.wordEdges != nil:
		g.wordEdges[compact] |= active.first
	case g.compactEdges != nil:
		g.compactEdges[compact] = g.compactEdges[compact].union(active)
	default:
		g.edgeBits[key] = g.edgeBits[key].union(active)
	}
	if g.wordPairs != nil {
		g.wordPairs[pair] |= active.first
	} else {
		g.edgePairBits[pair] = g.edgePairBits[pair].union(active)
	}
	q.edgeCount += newEdges
	return nil
}

func (g *sharedTimeGraph) state(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) (map[uint64]timeBitmap, []sharedTopologyEdgeState, error) {
	if !slices.Equal(grid.timestamps, g.grid.timestamps) {
		return nil, nil, fmt.Errorf("topology query grid does not match shared graph grid")
	}
	edges := make([]sharedTopologyEdgeState, 0, g.edgeCount())
	appendEdge := func(key timeGraphTopologyEdgeKey, active timeBitmap) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(query.AllowedCategories) > 0 && !slices.Contains(query.AllowedCategories, RelationCategory(key.relation.category)) {
			return nil
		}
		if len(query.AllowedRelationTypes) > 0 && !slices.Contains(query.AllowedRelationTypes, key.relation.relationType) {
			return nil
		}
		if key.relation.category == string(RelationCategoryDynamic) && query.Direction != "" && query.Direction != DirectionBoth && key.relation.direction != "" && key.relation.direction != string(query.Direction) {
			return nil
		}
		edges = append(edges, sharedTopologyEdgeState{key: key, activeBits: active})
		return nil
	}
	switch {
	case g.wordEdges != nil:
		for key, active := range g.wordEdges {
			full := timeGraphTopologyEdgeKey{source: key.source, target: key.target, relation: g.relations[key.relation-1]}
			if err := appendEdge(full, timeBitmap{first: active}); err != nil {
				return nil, nil, err
			}
		}
	case g.compactEdges != nil:
		for key, active := range g.compactEdges {
			full := timeGraphTopologyEdgeKey{source: key.source, target: key.target, relation: g.relations[key.relation-1]}
			if err := appendEdge(full, active); err != nil {
				return nil, nil, err
			}
		}
	default:
		for key, active := range g.edgeBits {
			if err := appendEdge(key, active); err != nil {
				return nil, nil, err
			}
		}
	}

	return g.nodeBits, edges, nil
}

// sharedTopologyRootBits 排除只由 info 查询产生的孤立节点。
// 请求属性时，只有 info 查询在该帧匹配的关系节点才可作为根。
func (q *TimeGraph) sharedTopologyRootBits() map[uint64]timeBitmap {
	// The private query only reads this map before graph cleanup. Without an
	// info condition, the relation endpoint masks are already the exact roots.
	if q.shared.sourceInfoBits == nil {
		return q.shared.relationNodeBits
	}
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
