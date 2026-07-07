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
	RootID          string                   `json:"root_id,omitempty"`
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
	Target       *NodeLiveness
	ResourcePath []ResourceType
	NodePeriods  [][]*VisiblePeriod
	EdgePeriods  [][]*VisiblePeriod
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
	key := edge.RelationID
	if existing := g.Edges[key]; existing != nil && !sameEdgeLiveness(existing, edge) {
		key = directionalEdgeKey(edge)
	}
	_, exists := g.Edges[key]
	g.Edges[key] = edge
	if !exists {
		g.Adjacency[edge.FromID] = append(g.Adjacency[edge.FromID], key)
	}
}

func sameEdgeLiveness(left, right *EdgeLiveness) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.RelationID == right.RelationID &&
		left.Direction == right.Direction &&
		left.FromID == right.FromID &&
		left.ToID == right.ToID
}

func directionalEdgeKey(edge *EdgeLiveness) string {
	return edge.RelationID + "\x00" + string(edge.Direction) + "\x00" + edge.FromID + "\x00" + edge.ToID
}

func (g *LivenessGraph) GetNode(resourceID string) *NodeLiveness {
	return g.Nodes[resourceID]
}

func (g *LivenessGraph) GetEdge(relationID string) *EdgeLiveness {
	if edge := g.Edges[relationID]; edge != nil {
		return edge
	}
	for _, edge := range g.Edges {
		if edge.RelationID == relationID {
			return edge
		}
	}
	return nil
}

func (g *LivenessGraph) AddTraversalError(errMsg string) {
	g.TraversalErrors = append(g.TraversalErrors, errMsg)
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
	return g.targetPaths(targetType, pathResource, true, includeRootTargetOptions...)
}

func (g *LivenessGraph) TargetPathsForRange(
	targetType ResourceType,
	pathResource []ResourceType,
	includeRootTargetOptions ...bool,
) []*TargetPath {
	// range 查询要对齐旧 VM 的 step 窗口语义，候选路径不能先按全路径精确时间交集剪掉。
	return g.targetPaths(targetType, pathResource, false, includeRootTargetOptions...)
}

func (g *LivenessGraph) targetPaths(
	targetType ResourceType,
	pathResource []ResourceType,
	requireCommonOverlap bool,
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
		root := g.Nodes[rootID]
		if root == nil {
			continue
		}
		pathConstraint, directOnly := normalizePathResource(root.ResourceType, targetType, pathResource)
		visited := map[string]bool{rootID: true}
		g.collectTargetPaths(rootID, targetType, pathConstraint, 0, nil, nil, nil, visited, includeRootTarget, directOnly, requireCommonOverlap, &result)
	}
	return result
}

func (g *LivenessGraph) collectTargetPaths(
	nodeID string,
	targetType ResourceType,
	pathResource []ResourceType,
	pathIdx int,
	resourcePath []ResourceType,
	nodePeriods [][]*VisiblePeriod,
	edgePeriods [][]*VisiblePeriod,
	visited map[string]bool,
	includeRootTarget bool,
	directOnly bool,
	requireCommonOverlap bool,
	result *[]*TargetPath,
) {
	node := g.Nodes[nodeID]
	if node == nil {
		return
	}
	currentResourcePath := append(append([]ResourceType{}, resourcePath...), node.ResourceType)
	currentNodePeriods := append(append([][]*VisiblePeriod{}, nodePeriods...), node.RawPeriods)

	nextPathIdx := pathIdx
	if len(pathResource) > 0 && pathIdx < len(pathResource) && node.ResourceType == pathResource[pathIdx] {
		nextPathIdx++
	}

	if node.ResourceType == targetType &&
		nextPathIdx >= len(pathResource) &&
		(includeRootTarget || len(edgePeriods) > 0) &&
		(!directOnly || len(edgePeriods) <= 1) &&
		(!requireCommonOverlap || g.pathOverlapsQuery(currentNodePeriods, edgePeriods)) {
		*result = append(*result, &TargetPath{
			Target:       node,
			ResourcePath: currentResourcePath,
			NodePeriods:  currentNodePeriods,
			EdgePeriods:  edgePeriods,
		})
	}

	for _, edge := range g.outEdgesFromMap(nodeID) {
		allowSelfLoop := !includeRootTarget && edge.ToID == nodeID && len(edgePeriods) == 0 && node.ResourceType == targetType
		if visited[edge.ToID] && !allowSelfLoop {
			continue
		}
		target := g.Nodes[edge.ToID]
		if target == nil {
			continue
		}
		if !allowSelfLoop {
			visited[edge.ToID] = true
		}
		nextEdgePeriods := append(append([][]*VisiblePeriod{}, edgePeriods...), edge.RawPeriods)
		g.collectTargetPaths(
			edge.ToID,
			targetType,
			pathResource,
			nextPathIdx,
			currentResourcePath,
			currentNodePeriods,
			nextEdgePeriods,
			visited,
			includeRootTarget,
			directOnly,
			requireCommonOverlap,
			result,
		)
		if !allowSelfLoop {
			visited[edge.ToID] = false
		}
	}
}

func (g *LivenessGraph) rootNodeIDs() []string {
	if g.RootID != "" {
		if _, ok := g.Nodes[g.RootID]; ok {
			return []string{g.RootID}
		}
	}

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

func (g *LivenessGraph) pathOverlapsQuery(nodePeriods, edgePeriods [][]*VisiblePeriod) bool {
	periodGroups := make([][]*VisiblePeriod, 0, len(nodePeriods)+len(edgePeriods))
	periodGroups = append(periodGroups, nodePeriods...)
	periodGroups = append(periodGroups, edgePeriods...)
	if len(periodGroups) == 0 {
		return false
	}

	if g.QueryStart == 0 && g.QueryEnd == 0 {
		for _, periods := range periodGroups {
			if len(nonNilPeriods(periods)) == 0 {
				return false
			}
		}
		return true
	}

	candidates := []VisiblePeriod{{Start: g.QueryStart, End: g.QueryEnd}}
	for _, periods := range periodGroups {
		candidates = intersectVisiblePeriods(candidates, periods)
		if len(candidates) == 0 {
			return false
		}
	}
	return true
}

func nonNilPeriods(periods []*VisiblePeriod) []*VisiblePeriod {
	result := make([]*VisiblePeriod, 0, len(periods))
	for _, period := range periods {
		if period != nil {
			result = append(result, period)
		}
	}
	return result
}

func intersectVisiblePeriods(left []VisiblePeriod, right []*VisiblePeriod) []VisiblePeriod {
	result := make([]VisiblePeriod, 0)
	for _, l := range left {
		for _, r := range right {
			if r == nil {
				continue
			}
			start := l.Start
			if r.Start > start {
				start = r.Start
			}
			end := l.End
			if r.End < end {
				end = r.End
			}
			if start <= end {
				result = append(result, VisiblePeriod{Start: start, End: end})
			}
		}
	}
	return result
}
