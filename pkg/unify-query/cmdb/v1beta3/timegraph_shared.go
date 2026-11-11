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
}

func (g *sharedTimeGraph) edgeCount() int { return len(g.edgeBits) }

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

func (q *TimeGraph) setSharedNodeInfo(node uint64, info cmdb.Matcher, active timeBitmap) {
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
			return
		}
	}
	newPoints := active.subtract(g.nodeBits[node]).count()
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
	g.nodeInfoVersionCount += len(versions) - len(previous)
	g.nodeInfos[node] = versions
	g.nodeBits[node] = g.nodeBits[node].union(active)
	q.nodeInfoCount += newPoints
}

func (q *TimeGraph) addSharedRelation(ctx context.Context, relation cmdb.Relation, direction string, source, target uint64, sourceInfo, targetInfo cmdb.Matcher, timestamps []int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	active, err := q.shared.timeBits(timestamps)
	if err != nil {
		return err
	}
	g := q.shared
	if active.empty() {
		return nil
	}
	pair := timeGraphEdgeKey{source: source, target: target}
	// 关系按完整身份存一份，出现次数按节点对的时间位累计。
	newEdges := active.subtract(g.edgePairBits[pair]).count()
	key := timeGraphTopologyEdgeKey{source: source, target: target, relation: timeGraphEdgeRelation{
		relationType: relation.RelationType, metricName: relation.MetricName,
		category: relation.Category, direction: direction,
	}}
	q.setSharedNodeInfo(source, sourceInfo, active)
	q.setSharedNodeInfo(target, targetInfo, active)
	g.relationNodeBits[source] = g.relationNodeBits[source].union(active)
	g.relationNodeBits[target] = g.relationNodeBits[target].union(active)
	g.edgeBits[key] = g.edgeBits[key].union(active)
	g.edgePairBits[pair] = g.edgePairBits[pair].union(active)
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
	for key, active := range g.edgeBits {
		if err := appendEdge(key, active); err != nil {
			return nil, nil, err
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
