// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func defaultTimeGraphConfig() *TimeGraphConfig {
	return buildTimeGraphConfig(GetSchemaProvider(), "")
}

func ResourcesIndex(resources ...cmdb.Resource) cmdb.Index {
	config := defaultTimeGraphConfig()
	byResource := make(map[cmdb.Resource]TimeGraphResourceConfig, len(config.Resource))
	for _, resource := range config.Resource {
		byResource[resource.Name] = resource
	}
	var result cmdb.Index
	for _, resource := range resources {
		result = append(result, byResource[resource].Index...)
	}
	return result
}

func ResourcesInfo(resource cmdb.Resource) cmdb.Index {
	config := defaultTimeGraphConfig()
	for _, item := range config.Resource {
		if item.Name == resource {
			return append(cmdb.Index(nil), item.Info...)
		}
	}
	return nil
}

func NewTimeGraph() *TimeGraph {
	return NewTimeGraphWithConfig(defaultTimeGraphConfig())
}

func (q *TimeGraph) GetNodesByResourceType(resourceType cmdb.Resource) []cmdb.Matcher {
	return q.nodeBuilder.ResourceNodeInfo(resourceType)
}

// FindTargets 用资源类型序列描述候选路径，返回各时刻沿路径可达的目标。
func (q *TimeGraph) FindTargets(
	ctx context.Context,
	sourceType cmdb.Resource,
	targetTypes []cmdb.Resource,
	sourceMatcher cmdb.Matcher,
	expectedPaths [][]cmdb.Resource,
) ([]RelationTarget, error) {
	relationPaths := make([]cmdb.RelationPath, 0, len(expectedPaths))
	for _, expectedPath := range expectedPaths {
		steps := make([]cmdb.RelationPathStep, 0, len(expectedPath))
		for _, resourceType := range expectedPath {
			steps = append(steps, cmdb.RelationPathStep{ResourceType: resourceType})
		}
		relationPaths = append(relationPaths, cmdb.RelationPath{Steps: steps})
	}
	return q.FindRelationTargets(ctx, sourceType, targetTypes, sourceMatcher, relationPaths)
}

type (
	SharedTopologyNode     = cmdb.SharedTopologyNode
	SharedTopologyEdge     = cmdb.SharedTopologyEdge
	SharedTopologySnapshot = cmdb.SharedTopologySnapshot
)

func (q *TimeGraph) FindSharedTopology(ctx context.Context, grid TopologyGrid, query SharedTopologyQuery) ([]SharedTopologySnapshot, error) {
	graph := q
	if q.shared == nil {
		graph = &TimeGraph{nodeBuilder: q.nodeBuilder, shared: &sharedTimeGraph{
			grid:           grid,
			timestampIndex: make(map[int64]int, len(grid.timestamps)),
			nodeBits:       make(map[uint64]timeBitmap),
			nodeInfos:      make(map[uint64][]sharedNodeInfo),
			edgeBits:       make(map[timeGraphTopologyEdgeKey]timeBitmap),
		}}
		for index, timestamp := range grid.timestamps {
			graph.shared.timestampIndex[timestamp] = index
			active := timeBitmap{}
			active.set(index)
			for node, info := range q.nodeInfos[timestamp] {
				graph.shared.nodeBits[node] = graph.shared.nodeBits[node].union(active)
				graph.shared.nodeInfos[node] = appendSharedNodeInfo(graph.shared.nodeInfos[node], active, info)
			}
			for pair, relations := range q.edgeTypes[timestamp] {
				for relation := range relations {
					key := timeGraphTopologyEdgeKey{source: pair.source, target: pair.target, relation: relation}
					graph.shared.edgeBits[key] = graph.shared.edgeBits[key].union(active)
				}
			}
		}
	}
	snapshots, err := graph.findSharedTopologyDTO(ctx, grid, query)
	for index := range snapshots {
		if len(snapshots[index].Nodes) == 0 {
			snapshots[index].Nodes = nil
		}
		if len(snapshots[index].Edges) == 0 {
			snapshots[index].Edges = nil
		}
	}
	return snapshots, err
}
