// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

type Node struct {
	*StandardSpan
}

type DiGraph struct {
	Nodes []*Node
	Edges map[string][]*Node
}

type NodeDegree struct {
	Node   *Node
	Degree int
}

func NewDiGraph() *DiGraph {
	return &DiGraph{Nodes: make([]*Node, 0), Edges: make(map[string][]*Node, 0)}
}

func (g *DiGraph) AddNode(n *Node) {
	g.Nodes = append(g.Nodes, n)
}

func (g *DiGraph) AddFrom(n []*Node) {
	g.Nodes = append(g.Nodes, n...)
}

func (g *DiGraph) AddEdge(from, to *Node) {
	if g.Edges == nil {
		g.Edges = make(map[string][]*Node)
	}

	g.Edges[from.SpanId] = append(g.Edges[from.SpanId], to)
}

func (g *DiGraph) RefreshEdges() {

	g.Edges = make(map[string][]*Node)

	nodeMapping := make(map[string]*Node)
	for _, node := range g.Nodes {
		nodeMapping[node.SpanId] = node
	}

	for _, node := range g.Nodes {
		if node.ParentSpanId != "" {
			parentNode, exists := nodeMapping[node.ParentSpanId]
			if exists {
				g.AddEdge(parentNode, node)
			}
		}
	}
}

func (g *DiGraph) longestPathUtil(n *Node, visited map[*Node]bool, dp map[*Node]int) int {
	if visited[n] {
		return dp[n]
	}

	visited[n] = true
	maxPath := 0

	for _, neighbor := range g.Edges[n.SpanId] {
		path := 1 + g.longestPathUtil(neighbor, visited, dp)
		if path > maxPath {
			maxPath = path
		}
	}

	dp[n] = maxPath
	return maxPath
}

func (g *DiGraph) LongestPath() int {
	visited := make(map[*Node]bool)
	dp := make(map[*Node]int)

	maxPath := 0
	for _, node := range g.Nodes {
		path := g.longestPathUtil(node, visited, dp)
		if path > maxPath {
			maxPath = path
		}
	}

	return maxPath
}

func (g *DiGraph) NodeDepths() []NodeDegree {
	var res []NodeDegree
	visited := make(map[*Node]bool)

	var traverse func(*Node, int)
	traverse = func(node *Node, depth int) {
		if visited[node] {
			return
		}
		visited[node] = true

		res = append(res, NodeDegree{Node: node, Degree: depth})
		for _, child := range g.Edges[node.SpanId] {
			traverse(child, depth+1)
		}
	}

	for _, node := range g.Nodes {
		traverse(node, 0)
	}

	return res
}
