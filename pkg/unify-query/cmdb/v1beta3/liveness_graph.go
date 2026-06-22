// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

type LivenessGraph struct {
	QueryStart      int64                    `json:"query_start"`
	QueryEnd        int64                    `json:"query_end"`
	Nodes           map[string]*NodeLiveness `json:"nodes"`
	Edges           map[string]*EdgeLiveness `json:"edges"`
	Adjacency       map[string][]string      `json:"adjacency"`
	TraversalErrors []string                 `json:"traversal_errors,omitempty"`
}

type NodeLiveness struct {
	ResourceID   string            `json:"resource_id"`
	ResourceType ResourceType      `json:"resource_type"`
	Labels       map[string]string `json:"labels,omitempty"`
	RawPeriods   []*VisiblePeriod  `json:"raw_periods"`
}

type EdgeLiveness struct {
	RelationID   string             `json:"relation_id"`
	RelationType RelationType       `json:"relation_type"`
	Category     RelationCategory   `json:"category"`
	Direction    TraversalDirection `json:"direction,omitempty"`
	FromID       string             `json:"from_id"`
	ToID         string             `json:"to_id"`
	RawPeriods   []*VisiblePeriod   `json:"raw_periods"`
}

type TargetPath struct {
	Target      *NodeLiveness
	NodePeriods [][]*VisiblePeriod
	EdgePeriods [][]*VisiblePeriod
}

func NewLivenessGraph(queryStart, queryEnd int64) *LivenessGraph {
	return &LivenessGraph{
		QueryStart: queryStart,
		QueryEnd:   queryEnd,
		Nodes:      make(map[string]*NodeLiveness),
		Edges:      make(map[string]*EdgeLiveness),
		Adjacency:  make(map[string][]string),
	}
}

func (g *LivenessGraph) AddNode(node *NodeLiveness) {
	g.Nodes[node.ResourceID] = node
	if _, exists := g.Adjacency[node.ResourceID]; !exists {
		g.Adjacency[node.ResourceID] = []string{}
	}
}

func (g *LivenessGraph) AddEdge(edge *EdgeLiveness) {
	_, exists := g.Edges[edge.RelationID]
	g.Edges[edge.RelationID] = edge
	if !exists {
		g.Adjacency[edge.FromID] = append(g.Adjacency[edge.FromID], edge.RelationID)
	}
}

func (g *LivenessGraph) GetNode(resourceID string) *NodeLiveness {
	return g.Nodes[resourceID]
}

func (g *LivenessGraph) GetEdge(relationID string) *EdgeLiveness {
	return g.Edges[relationID]
}

func (g *LivenessGraph) AddTraversalError(errMsg string) {
	g.TraversalErrors = append(g.TraversalErrors, errMsg)
}

func (g *LivenessGraph) HasErrors() bool {
	return len(g.TraversalErrors) > 0
}

func (g *LivenessGraph) IsComplete() bool {
	return len(g.TraversalErrors) == 0
}

func (g *LivenessGraph) GetOutEdges(resourceID string) []*EdgeLiveness {
	relationIDs := g.Adjacency[resourceID]
	edges := make([]*EdgeLiveness, 0, len(relationIDs))
	for _, rid := range relationIDs {
		if edge := g.Edges[rid]; edge != nil {
			edges = append(edges, edge)
		}
	}
	return edges
}

func (g *LivenessGraph) ExtractTargetMatchersWithID(
	targetType ResourceType,
	pathResource []ResourceType,
	includeRootTarget bool,
) map[string]cmdb.Matcher {
	result := make(map[string]cmdb.Matcher)
	if g == nil || len(g.Nodes) == 0 {
		return result
	}

	for _, path := range g.TargetPaths(targetType, pathResource, includeRootTarget) {
		node := path.Target
		if _, exists := result[node.ResourceID]; !exists {
			matcher := make(cmdb.Matcher, len(node.Labels))
			for k, v := range node.Labels {
				matcher[k] = v
			}
			result[node.ResourceID] = matcher
		}
	}

	return result
}

func (g *LivenessGraph) TargetPaths(
	targetType ResourceType,
	pathResource []ResourceType,
	includeRootTargetOptions ...bool,
) []*TargetPath {
	if g == nil || len(g.Nodes) == 0 {
		return nil
	}

	includeRootTarget := true
	if len(includeRootTargetOptions) > 0 {
		includeRootTarget = includeRootTargetOptions[0]
	}

	var result []*TargetPath
	for _, rootID := range g.rootNodeIDs() {
		visited := map[string]bool{rootID: true}
		g.collectTargetPaths(rootID, targetType, pathResource, 0, nil, nil, visited, includeRootTarget, &result)
	}
	return result
}

func (g *LivenessGraph) collectTargetPaths(
	nodeID string,
	targetType ResourceType,
	pathResource []ResourceType,
	pathIdx int,
	nodePeriods [][]*VisiblePeriod,
	edgePeriods [][]*VisiblePeriod,
	visited map[string]bool,
	includeRootTarget bool,
	result *[]*TargetPath,
) {
	node := g.Nodes[nodeID]
	if node == nil {
		return
	}
	currentNodePeriods := append(append([][]*VisiblePeriod{}, nodePeriods...), node.RawPeriods)

	nextPathIdx := pathIdx
	if len(pathResource) > 0 && pathIdx < len(pathResource) && node.ResourceType == pathResource[pathIdx] {
		nextPathIdx++
	}

	if node.ResourceType == targetType &&
		nextPathIdx >= len(pathResource) &&
		(includeRootTarget || len(edgePeriods) > 0) &&
		g.nodeOverlapsQuery(node) {
		*result = append(*result, &TargetPath{Target: node, NodePeriods: currentNodePeriods, EdgePeriods: edgePeriods})
	}

	for _, edge := range g.outEdgesFromMap(nodeID) {
		if visited[edge.ToID] {
			continue
		}
		target := g.Nodes[edge.ToID]
		if target == nil {
			continue
		}
		if len(pathResource) > 0 && nextPathIdx < len(pathResource) &&
			target.ResourceType != pathResource[nextPathIdx] && target.ResourceType != targetType {
			continue
		}
		visited[edge.ToID] = true
		nextEdgePeriods := append(append([][]*VisiblePeriod{}, edgePeriods...), edge.RawPeriods)
		g.collectTargetPaths(
			edge.ToID,
			targetType,
			pathResource,
			nextPathIdx,
			currentNodePeriods,
			nextEdgePeriods,
			visited,
			includeRootTarget,
			result,
		)
		visited[edge.ToID] = false
	}
}

func (g *LivenessGraph) rootNodeIDs() []string {
	incoming := make(map[string]bool, len(g.Edges))
	for _, edge := range g.Edges {
		incoming[edge.ToID] = true
	}
	roots := make([]string, 0, len(g.Nodes))
	for id := range g.Nodes {
		if !incoming[id] {
			roots = append(roots, id)
		}
	}
	if len(roots) > 0 {
		return roots
	}
	for id := range g.Nodes {
		roots = append(roots, id)
	}
	return roots
}

func (g *LivenessGraph) outEdgesFromMap(resourceID string) []*EdgeLiveness {
	edges := make([]*EdgeLiveness, 0, len(g.Edges))
	for _, edge := range g.Edges {
		if edge.FromID == resourceID {
			edges = append(edges, edge)
		}
	}
	return edges
}

func (g *LivenessGraph) nodeOverlapsQuery(node *NodeLiveness) bool {
	if node == nil || len(node.RawPeriods) == 0 {
		return false
	}
	if g.QueryStart == 0 && g.QueryEnd == 0 {
		return true
	}
	for _, period := range node.RawPeriods {
		if period != nil && period.End >= g.QueryStart && period.Start <= g.QueryEnd {
			return true
		}
	}
	return false
}
