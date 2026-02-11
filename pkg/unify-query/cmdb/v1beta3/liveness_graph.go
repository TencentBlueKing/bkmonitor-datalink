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

func (g *LivenessGraph) ExtractTargetMatchersWithID(targetType ResourceType) map[string]cmdb.Matcher {
	result := make(map[string]cmdb.Matcher)
	if g == nil || len(g.Nodes) == 0 {
		return result
	}

	for _, node := range g.Nodes {
		if node.ResourceType == targetType {
			if _, exists := result[node.ResourceID]; !exists {
				matcher := make(cmdb.Matcher, len(node.Labels))
				for k, v := range node.Labels {
					matcher[k] = v
				}
				result[node.ResourceID] = matcher
			}
		}
	}

	return result
}
