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

// RefreshEdges Build tree
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

// longestPathUtil Get the longest path length of the tree
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

// NodeDepths Get the length of tree-layers
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

func (g *DiGraph) Length() int {
	return len(g.Nodes)
}

func (g *DiGraph) Empty() bool {
	return len(g.Nodes) == 0
}

func (g *DiGraph) StandardSpans() []*StandardSpan {
	var res []*StandardSpan
	for _, item := range g.Nodes {
		res = append(res, item.StandardSpan)
	}

	return res
}

// FindParentChildPairs Return all pairs of parent-child nodes
func (g *DiGraph) FindParentChildPairs() [][2]*Node {
	var res [][2]*Node

	var findPairs func(parent *Node, current *Node)

	findPairs = func(parent *Node, current *Node) {
		if parent != current {
			res = append(res, [2]*Node{parent, current})
		}

		for _, child := range g.Edges[current.SpanId] {
			findPairs(parent, child)
		}
	}

	for _, rootNode := range g.Nodes {
		findPairs(rootNode, rootNode)
	}

	return res
}

// FindChildPairsBasedFullTree Find out all parent-child
// node pairs in the filtered tree according to the fullTree
func FindChildPairsBasedFullTree(fullTree, filteredTree *DiGraph) [][2]*Node {
	var res [][2]*Node
	for _, parentNode := range filteredTree.Nodes {
		for _, possibleChildNode := range filteredTree.Nodes {
			if parentNode != possibleChildNode && CheckIfAncestor(fullTree, parentNode, possibleChildNode) {
				res = append(res, [2]*Node{parentNode, possibleChildNode})
			}
		}
	}
	return res
}

// CheckIfAncestor Check whether nodeA is the ancestor of nodeB which in the fullTree
func CheckIfAncestor(fullTree *DiGraph, a, b *Node) bool {
	visited := make(map[string]bool)

	var dfs func(*Node, string) bool
	dfs = func(node *Node, targetId string) bool {
		if node.SpanId == targetId {
			return true
		}

		if visited[node.SpanId] {
			return false
		}

		visited[node.SpanId] = true

		for _, child := range fullTree.Edges[node.SpanId] {
			if dfs(child, targetId) {
				return true
			}
		}
		return false
	}

	return dfs(a, b.SpanId)
}
